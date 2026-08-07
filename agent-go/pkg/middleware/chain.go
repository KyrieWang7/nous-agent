package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// ChainOptions 配置链的执行。
type ChainOptions struct {
	// Timeout 是单个中间件的执行上限。<= 0 表示不限。
	Timeout time.Duration

	// Logger 用于记录 GradeListener 中间件被吞掉的错误。
	// 为 nil 时用 slog.Default()。
	Logger *slog.Logger
}

// stageEntry 是一个中间件在某阶段的可执行条目。
//
// 名字与级别在装配期就固定下来，运行期不再回查中间件对象。
// 这一点是必须的：中间件常带函数字段（Prompter、回调），
// 带 func 字段的 struct 在 Go 里不可哈希，用 map[Middleware]X 会直接 panic。
type stageEntry struct {
	name  string
	grade Grade
	run   func(context.Context, *State) error
}

// toolEntry 是一个中间件在工具调用两侧的可执行条目。
type toolEntry struct {
	name   string
	grade  Grade
	before func(context.Context, *State) (tool.Decision, error)
	after  func(context.Context, *State) error
}

// Chain 是装配好的中间件链。
//
// 每阶段的执行序列在 NewChain 时按类型断言分桶并固定顺序（After* 已反转），
// 运行期只是遍历。每轮迭代对 24 个中间件做 6 次断言是无谓开销。
type Chain struct {
	names  []string
	stages [4][]stageEntry

	beforeTool []toolEntry
	afterTool  []toolEntry

	opts   ChainOptions
	logger *slog.Logger
}

// NewChain 装配中间件链。
//
// 重名与 nil 都是装配期错误：锚点排序按名字引用中间件，
// 重名会让"插到 X 之后"变成不确定的行为。
func NewChain(mws []Middleware, opts ChainOptions) (*Chain, error) {
	c := &Chain{opts: opts, logger: opts.Logger}
	if c.logger == nil {
		c.logger = slog.Default()
	}

	seen := make(map[string]struct{}, len(mws))
	for i, m := range mws {
		if m == nil {
			return nil, fmt.Errorf("middleware: entry %d is nil", i)
		}
		name := m.Name()
		if name == "" {
			return nil, fmt.Errorf("middleware: entry %d has an empty name", i)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("middleware: duplicate name %q; names identify middlewares for anchoring", name)
		}
		seen[name] = struct{}{}
		c.names = append(c.names, name)

		grade := gradeOf(m)

		if v, ok := m.(BeforeAgent); ok {
			c.stages[StageBeforeAgent] = append(c.stages[StageBeforeAgent],
				stageEntry{name: name, grade: grade, run: v.BeforeAgent})
		}
		if v, ok := m.(BeforeModel); ok {
			c.stages[StageBeforeModel] = append(c.stages[StageBeforeModel],
				stageEntry{name: name, grade: grade, run: v.BeforeModel})
		}
		if v, ok := m.(AfterModel); ok {
			c.stages[StageAfterModel] = append(c.stages[StageAfterModel],
				stageEntry{name: name, grade: grade, run: v.AfterModel})
		}
		if v, ok := m.(AfterAgent); ok {
			c.stages[StageAfterAgent] = append(c.stages[StageAfterAgent],
				stageEntry{name: name, grade: grade, run: v.AfterAgent})
		}

		if v, ok := m.(BeforeTool); ok {
			c.beforeTool = append(c.beforeTool, toolEntry{name: name, grade: grade, before: v.BeforeTool})
		}
		if v, ok := m.(AfterTool); ok {
			c.afterTool = append(c.afterTool, toolEntry{name: name, grade: grade, after: v.AfterTool})
		}
	}

	// After* 阶段逆注册顺序执行（设计文档 §4.3）。在装配期反转一次，
	// 而不是每轮迭代反转 —— 也让顺序快照测试有一个稳定的观察点。
	for _, stage := range []Stage{StageAfterModel, StageAfterAgent} {
		s := c.stages[stage]
		for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
			s[i], s[j] = s[j], s[i]
		}
	}

	return c, nil
}

// Names 返回链内中间件的名字，按注册顺序。
func (c *Chain) Names() []string {
	out := make([]string, len(c.names))
	copy(out, c.names)
	return out
}

// StageOrder 返回某阶段的实际执行顺序，供顺序快照测试断言。
func (c *Chain) StageOrder(stage Stage) []string {
	entries := c.stages[stage]
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.name
	}
	return out
}

// Execute 运行某个阶段的全部中间件。
//
// 语义：
//   - Before* 正序，After* 逆序（设计文档 §4.3）。
//   - 中间件置 DirectiveStop 后停止本阶段剩余中间件 —— 澄清拦截之后再跑
//     别的切面既无意义，也可能覆盖它的决定。
//   - GradeAbort 失败短路并返回包装后的错误；GradeListener 失败只记日志。
func (c *Chain) Execute(ctx context.Context, stage Stage, st *State) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	for _, e := range c.stages[stage] {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := c.withTimeout(ctx, func(callCtx context.Context) error {
			return e.run(callCtx, st)
		})
		if err != nil {
			if handled := c.handle(ctx, e.name, e.grade, stage.String(), err); handled != nil {
				return handled
			}
			continue
		}

		if st.Directive == DirectiveStop {
			return nil
		}
	}
	return nil
}

// handle 按级别处理中间件错误。返回 nil 表示已吞掉（监听类），继续执行链。
func (c *Chain) handle(ctx context.Context, name string, grade Grade, stage string, err error) error {
	if grade == GradeListener {
		c.logger.WarnContext(ctx, "listener middleware failed",
			"middleware", name, "stage", stage, "error", err)
		return nil
	}
	return fmt.Errorf("middleware %s failed at %s: %w", name, stage, err)
}

func (c *Chain) withTimeout(ctx context.Context, fn func(context.Context) error) error {
	if c.opts.Timeout <= 0 {
		return fn(ctx)
	}
	callCtx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
	defer cancel()
	return fn(callCtx)
}

// ToolInterceptor 返回绑定到 st 的 tool.Interceptor 适配器。
//
// pkg/tool 不 import pkg/middleware（State 持有 tool.Result，反向依赖会成环），
// 所以由这里提供适配器把 BeforeTool / AfterTool 接到执行器上。
func (c *Chain) ToolInterceptor(st *State) tool.Interceptor {
	return &chainInterceptor{chain: c, parent: st}
}

type chainInterceptor struct {
	chain  *Chain
	parent *State
}

// BeforeTool 依次询问链内的 BeforeTool 中间件。
//
// 第一个 Deny 或 EndTurn 生效并短路后续中间件：既然已经拒绝，再问下去只会让
// "谁的理由被展示"变成注册顺序的偶然产物。改写入参会累积传递给后续中间件 ——
// 否则审计类中间件审的是被改写前的旧值。
func (ci *chainInterceptor) BeforeTool(ctx context.Context, call tool.Call) (tool.Decision, error) {
	st := ci.parent.CloneForTool(call)
	var out tool.Decision

	for _, e := range ci.chain.beforeTool {
		if err := ctx.Err(); err != nil {
			return tool.Decision{}, err
		}

		var decision tool.Decision
		err := ci.chain.withTimeout(ctx, func(callCtx context.Context) error {
			var err error
			decision, err = e.before(callCtx, st)
			return err
		})
		if err != nil {
			if handled := ci.chain.handle(ctx, e.name, e.grade, "BeforeTool", err); handled != nil {
				return tool.Decision{}, handled
			}
			continue
		}

		if decision.Args != nil {
			st.ToolCall.Args = decision.Args
			out.Args = decision.Args
		}
		if decision.EndTurn {
			out.EndTurn = true
			out.Reason = firstNonEmpty(out.Reason, decision.Reason)
			ci.parent.Directive = DirectiveStop
			return out, nil
		}
		if decision.Deny {
			out.Deny = true
			out.Reason = firstNonEmpty(decision.Reason, out.Reason)
			return out, nil
		}
	}

	return out, nil
}

// AfterTool 依次运行链内的 AfterTool 中间件，返回可能被改写的结果。
func (ci *chainInterceptor) AfterTool(
	ctx context.Context, call tool.Call, res *tool.Result, execErr error,
) (*tool.Result, error) {
	st := ci.parent.CloneForTool(call)
	st.ToolResult = res
	st.ToolExecErr = execErr

	for _, e := range ci.chain.afterTool {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		err := ci.chain.withTimeout(ctx, func(callCtx context.Context) error {
			return e.after(callCtx, st)
		})
		if err != nil {
			if handled := ci.chain.handle(ctx, e.name, e.grade, "AfterTool", err); handled != nil {
				return nil, handled
			}
			continue
		}
	}

	if st.Directive == DirectiveStop {
		ci.parent.Directive = DirectiveStop
	}
	return st.ToolResult, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
