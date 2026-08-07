package tool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// Outcome 是一次工具调用的最终结果，含调用本身与执行期错误。
type Outcome struct {
	Call   Call
	Result *Result

	// ExecErr 是 Handler 返回的框架层错误。Result 已经承载了给模型看的说明，
	// ExecErr 保留原始错误供日志与遥测使用。
	ExecErr error
}

// ExecutorOptions 配置执行器。
type ExecutorOptions struct {
	// Concurrency 是并发段内的最大并行度。<= 0 表示不限。
	Concurrency int
}

// Executor 按段执行一批工具调用。
//
// 它不是中间件：并发分段、信号量、级联取消是执行机制而非横切关注点，
// 换实现就换掉整个 Executor（设计文档 §5 注入接口）。
// 中间件通过 Interceptor 介入每次调用。
type Executor struct {
	registry *Registry
	opts     ExecutorOptions

	endTurn atomic.Bool
}

// NewExecutor 返回按 opts 执行的执行器。
func NewExecutor(r *Registry, opts ExecutorOptions) *Executor {
	return &Executor{registry: r, opts: opts}
}

// EndTurnRequested 报告本批执行中是否有拦截器要求结束回合（澄清拦截）。
func (e *Executor) EndTurnRequested() bool {
	return e.endTurn.Load()
}

// Run 执行 calls，按原始顺序返回结果。
//
// 失败语义：
//   - 工具自身的错误（Handler 返回 error 或 panic）转为 error Result 回灌，
//     不中断本批，也不杀回合。
//   - ctx 取消与拦截器返回的 error 会中断本批并返回该 error —— 前者是外部意志，
//     后者是治理失败，都不该被吞掉。
//   - 并发段内任一调用被取消时，级联取消同段其余调用，不等它们跑完。
func (e *Executor) Run(ctx context.Context, calls []Call, ic Interceptor) ([]Outcome, error) {
	e.endTurn.Store(false)

	if len(calls) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	outcomes := make([]Outcome, len(calls))

	// bound 是已写入结果的下标上界（不含）。中断时按它截断，让调用方拿到
	// 已经真实执行过的那部分结果 —— 它们必须进转录，否则 tool_calls 会悬空。
	//
	// 用下标上界而不是计数：out 按绝对下标写入，计数只在"分段连续升序"这个
	// 前提下才与上界相等，改动 Partition 就会静默错位。
	bound := 0

	for _, seg := range Partition(e.registry, calls) {
		if err := ctx.Err(); err != nil {
			return outcomes[:bound], err
		}

		var (
			segBound int
			err      error
		)
		if seg.Concurrent && len(seg.Calls) > 1 {
			segBound, err = e.runConcurrent(ctx, seg, ic, outcomes)
		} else {
			segBound, err = e.runSerial(ctx, seg, ic, outcomes)
		}
		bound = max(bound, segBound)

		if err != nil {
			return outcomes[:bound], err
		}
		if e.endTurn.Load() {
			return outcomes[:bound], nil
		}
	}

	return outcomes, nil
}

// runSerial 顺序执行段内调用，返回已写入结果的下标上界（不含）。
func (e *Executor) runSerial(ctx context.Context, seg Segment, ic Interceptor, out []Outcome) (int, error) {
	bound := 0
	for _, item := range seg.Calls {
		if err := ctx.Err(); err != nil {
			return bound, err
		}

		outcome, stop, err := e.invoke(ctx, item.Call, ic)
		if err != nil {
			return bound, err
		}
		out[item.Index] = outcome
		bound = item.Index + 1

		if stop {
			e.endTurn.Store(true)
			return bound, nil
		}
	}
	return bound, nil
}

func (e *Executor) runConcurrent(ctx context.Context, seg Segment, ic Interceptor, out []Outcome) (int, error) {
	// 段内任一调用被取消或治理失败时，级联取消同段其余调用。
	// 不这样做的话，一个已经注定失败的批次仍会等最慢的调用跑完。
	groupCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var sem chan struct{}
	if e.opts.Concurrency > 0 {
		sem = make(chan struct{}, e.opts.Concurrency)
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		bound    int
		stopped  atomic.Bool
	)

	recordErr := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
		cancel()
	}

	for _, item := range seg.Calls {
		wg.Add(1)
		go func(item IndexedCall) {
			defer wg.Done()

			if sem != nil {
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-groupCtx.Done():
					return
				}
			}
			if groupCtx.Err() != nil {
				return
			}

			outcome, stop, err := e.invoke(groupCtx, item.Call, ic)
			if err != nil {
				recordErr(err)
				return
			}

			mu.Lock()
			out[item.Index] = outcome
			bound = max(bound, item.Index+1)
			mu.Unlock()

			if stop {
				stopped.Store(true)
				cancel()
			}
		}(item)
	}
	wg.Wait()

	if stopped.Load() {
		e.endTurn.Store(true)
	}

	mu.Lock()
	err, writtenBound := firstErr, bound
	mu.Unlock()

	return writtenBound, err
}

// invoke 执行单次调用，穿过 Interceptor 的前后两侧。
//
// 第二个返回值 stop 表示拦截器要求结束回合。
func (e *Executor) invoke(ctx context.Context, c Call, ic Interceptor) (outcome Outcome, stop bool, err error) {
	if ic != nil {
		decision, decErr := ic.BeforeTool(ctx, c)
		if decErr != nil {
			return Outcome{}, false, decErr
		}
		if decision.EndTurn {
			return Outcome{
				Call:   c,
				Result: &Result{Content: reasonOr(decision.Reason, "turn ended by interceptor")},
			}, true, nil
		}
		if decision.Deny {
			return Outcome{
				Call: c,
				Result: &Result{
					Content: reasonOr(decision.Reason, "tool call denied"),
					IsError: true,
				},
			}, false, nil
		}
		if decision.Args != nil {
			c.Args = decision.Args
		}
	}

	res, execErr := e.callHandler(ctx, c)

	if ic != nil {
		rewritten, afterErr := ic.AfterTool(ctx, c, res, execErr)
		if afterErr != nil {
			return Outcome{}, false, afterErr
		}
		if rewritten != nil {
			res = rewritten
		}
	}

	if res == nil {
		res = errorResult(execErr)
	}

	// 取消不是工具的失败，是外部意志：往上抛，不转成 error 结果。
	if execErr != nil && (errors.Is(execErr, context.Canceled) || errors.Is(execErr, context.DeadlineExceeded)) {
		return Outcome{}, false, execErr
	}

	return Outcome{Call: c, Result: res, ExecErr: execErr}, false, nil
}

// callHandler 查表并执行，把 panic 收成 error。
//
// 一个工具 panic 不该带走整个 run：它变成一条 error 结果回灌给模型
// （设计文档 §17.2）。
func (e *Executor) callHandler(ctx context.Context, c Call) (res *Result, err error) {
	d, lookupErr := e.registry.Get(c.Name)
	if lookupErr != nil {
		return nil, lookupErr
	}

	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = fmt.Errorf("tool %q panicked: %v", c.Name, r)
		}
	}()

	return d.Handler(ctx, c)
}

func errorResult(err error) *Result {
	msg := "tool returned no result"
	if err != nil {
		msg = err.Error()
	}
	return &Result{Content: msg, IsError: true}
}

func reasonOr(reason, fallback string) string {
	if reason != "" {
		return reason
	}
	return fallback
}
