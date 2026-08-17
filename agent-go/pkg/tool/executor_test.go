package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func call(id, name string) tool.Call {
	return tool.Call{ID: id, Name: name, Args: json.RawMessage(`{}`)}
}

// --- 分段算法 ---

func TestPartition_OnlyReadOnlyConcurrencySafeToolsGoConcurrent(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("read1", readOnly))
	mustRegister(t, r, def("read2", readOnly))
	mustRegister(t, r, def("write1"))
	mustRegister(t, r, def("read3", readOnly))

	segs := tool.Partition(r, []tool.Call{
		call("1", "read1"),
		call("2", "read2"),
		call("3", "write1"),
		call("4", "read3"),
	})

	if len(segs) != 3 {
		t.Fatalf("Partition() = %d segments (%s), want 3", len(segs), describeSegments(segs))
	}
	if !segs[0].Concurrent || len(segs[0].Calls) != 2 {
		t.Errorf("segment 0 = %s, want a concurrent pair", describeSegments(segs[:1]))
	}
	if segs[1].Concurrent || len(segs[1].Calls) != 1 {
		t.Errorf("segment 1 = %s, want an exclusive write", describeSegments(segs[1:2]))
	}
	if !segs[2].Concurrent || len(segs[2].Calls) != 1 {
		t.Errorf("segment 2 = %s, want a concurrent single", describeSegments(segs[2:]))
	}
}

// 只读但非并发安全的工具不得并行（例如共享一个非线程安全的客户端）。
func TestPartition_ReadOnlyButNotConcurrencySafeStaysExclusive(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("a", func(d *tool.Definition) { d.Metadata.IsReadOnly = true }))
	mustRegister(t, r, def("b", func(d *tool.Definition) { d.Metadata.IsReadOnly = true }))

	segs := tool.Partition(r, []tool.Call{call("1", "a"), call("2", "b")})

	if len(segs) != 2 {
		t.Fatalf("Partition() = %s, want two exclusive segments", describeSegments(segs))
	}
}

// 未注册的工具不能被当成"可并发"—— 未知即保守。
func TestPartition_UnknownToolIsExclusive(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	segs := tool.Partition(r, []tool.Call{call("1", "ghost")})

	if len(segs) != 1 || segs[0].Concurrent {
		t.Fatalf("Partition() = %s, want one exclusive segment", describeSegments(segs))
	}
}

func TestPartition_PreservesOriginalIndex(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("w", nil2))
	mustRegister(t, r, def("r", readOnly))

	segs := tool.Partition(r, []tool.Call{call("1", "w"), call("2", "r")})

	if segs[0].Calls[0].Index != 0 {
		t.Errorf("segment 0 call index = %d, want 0", segs[0].Calls[0].Index)
	}
	if segs[1].Calls[0].Index != 1 {
		t.Errorf("segment 1 call index = %d, want 1", segs[1].Calls[0].Index)
	}
}

func TestPartition_Empty(t *testing.T) {
	t.Parallel()

	if got := tool.Partition(tool.NewRegistry(), nil); got != nil {
		t.Fatalf("Partition(nil) = %s, want nil", describeSegments(got))
	}
}

// --- 执行器 ---

func TestExecutor_ResultsInOriginalOrderDespiteConcurrency(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	// 故意让第一个慢、最后一个快，若按完成顺序回写就会错序
	mustRegister(t, r, slowTool("slow", readOnly, 60*time.Millisecond))
	mustRegister(t, r, slowTool("mid", readOnly, 30*time.Millisecond))
	mustRegister(t, r, slowTool("fast", readOnly, 0))

	ex := tool.NewExecutor(r, tool.ExecutorOptions{Concurrency: 4})
	got, err := ex.Run(context.Background(), []tool.Call{
		call("1", "slow"), call("2", "mid"), call("3", "fast"),
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []string{"slow", "mid", "fast"}
	for i, w := range want {
		if got[i].Call.Name != w {
			t.Fatalf("results out of order: got %v, want %v", resultNames(got), want)
		}
	}
}

func TestExecutor_ConcurrentSegmentActuallyRunsInParallel(t *testing.T) {
	t.Parallel()

	var inFlight, peak int64
	probe := func(d *tool.Definition) {
		readOnly(d)
		d.Handler = func(context.Context, tool.Call) (*tool.Result, error) {
			n := atomic.AddInt64(&inFlight, 1)
			for {
				old := atomic.LoadInt64(&peak)
				if n <= old || atomic.CompareAndSwapInt64(&peak, old, n) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			atomic.AddInt64(&inFlight, -1)
			return &tool.Result{Content: "ok"}, nil
		}
	}

	r := tool.NewRegistry()
	mustRegister(t, r, def("a", probe))
	mustRegister(t, r, def("b", probe))
	mustRegister(t, r, def("c", probe))

	ex := tool.NewExecutor(r, tool.ExecutorOptions{Concurrency: 3})
	if _, err := ex.Run(context.Background(), []tool.Call{
		call("1", "a"), call("2", "b"), call("3", "c"),
	}, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if atomic.LoadInt64(&peak) < 2 {
		t.Fatalf("peak concurrency = %d, want >= 2; the segment did not run in parallel", peak)
	}
}

func TestExecutor_RespectsConcurrencyLimit(t *testing.T) {
	t.Parallel()

	var inFlight, peak int64
	probe := func(d *tool.Definition) {
		readOnly(d)
		d.Handler = func(context.Context, tool.Call) (*tool.Result, error) {
			n := atomic.AddInt64(&inFlight, 1)
			for {
				old := atomic.LoadInt64(&peak)
				if n <= old || atomic.CompareAndSwapInt64(&peak, old, n) {
					break
				}
			}
			time.Sleep(25 * time.Millisecond)
			atomic.AddInt64(&inFlight, -1)
			return &tool.Result{Content: "ok"}, nil
		}
	}

	r := tool.NewRegistry()
	var calls []tool.Call
	for i := range 6 {
		name := fmt.Sprintf("t%d", i)
		mustRegister(t, r, def(name, probe))
		calls = append(calls, call(fmt.Sprint(i), name))
	}

	ex := tool.NewExecutor(r, tool.ExecutorOptions{Concurrency: 2})
	if _, err := ex.Run(context.Background(), calls, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got := atomic.LoadInt64(&peak); got > 2 {
		t.Fatalf("peak concurrency = %d, want <= 2", got)
	}
}

func TestExecutor_ExclusiveSegmentsNeverOverlap(t *testing.T) {
	t.Parallel()

	var inFlight int64
	var violated atomic.Bool
	writeProbe := func(d *tool.Definition) {
		d.Handler = func(context.Context, tool.Call) (*tool.Result, error) {
			if atomic.AddInt64(&inFlight, 1) > 1 {
				violated.Store(true)
			}
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt64(&inFlight, -1)
			return &tool.Result{Content: "ok"}, nil
		}
	}

	r := tool.NewRegistry()
	mustRegister(t, r, def("w1", writeProbe))
	mustRegister(t, r, def("w2", writeProbe))

	ex := tool.NewExecutor(r, tool.ExecutorOptions{Concurrency: 4})
	if _, err := ex.Run(context.Background(), []tool.Call{
		call("1", "w1"), call("2", "w2"),
	}, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if violated.Load() {
		t.Fatal("two write tools executed concurrently; exclusive segments must not overlap")
	}
}

// 工具的框架层错误转为 error 结果回灌，不杀回合（设计文档 §17.2）。
func TestExecutor_HandlerErrorBecomesErrorResult(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("boom", func(d *tool.Definition) {
		d.Handler = func(context.Context, tool.Call) (*tool.Result, error) {
			return nil, errors.New("sandbox unavailable")
		}
	}))

	ex := tool.NewExecutor(r, tool.ExecutorOptions{})
	got, err := ex.Run(context.Background(), []tool.Call{call("1", "boom")}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v; handler errors must not abort the batch", err)
	}
	if len(got) != 1 || !got[0].Result.IsError {
		t.Fatalf("result = %+v, want an error result", got)
	}
	if !strings.Contains(got[0].Result.Content, "sandbox unavailable") {
		t.Errorf("result content = %q, want it to carry the cause", got[0].Result.Content)
	}
}

func TestExecutor_RecoversPanic(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("panics", func(d *tool.Definition) {
		d.Handler = func(context.Context, tool.Call) (*tool.Result, error) {
			panic("tool blew up")
		}
	}))
	mustRegister(t, r, def("after", readOnly))

	ex := tool.NewExecutor(r, tool.ExecutorOptions{})
	got, err := ex.Run(context.Background(), []tool.Call{
		call("1", "panics"), call("2", "after"),
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v; a panicking tool must not abort the batch", err)
	}
	if len(got) != 2 {
		t.Fatalf("results = %d, want 2; the batch must continue after a panic", len(got))
	}
	if !got[0].Result.IsError || !strings.Contains(got[0].Result.Content, "panic") {
		t.Errorf("panicking tool result = %+v, want an error result mentioning panic", got[0].Result)
	}
}

func TestExecutor_UnknownToolBecomesErrorResult(t *testing.T) {
	t.Parallel()

	ex := tool.NewExecutor(tool.NewRegistry(), tool.ExecutorOptions{})
	got, err := ex.Run(context.Background(), []tool.Call{call("1", "ghost")}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(got) != 1 || !got[0].Result.IsError {
		t.Fatalf("result = %+v, want an error result for an unknown tool", got)
	}
}

type recordingTransactionObserver struct {
	starts int
	ends   int
	denies int
}

type recordingTransaction struct{ observer *recordingTransactionObserver }

func (o *recordingTransactionObserver) Start(context.Context, tool.Call) (tool.Transaction, error) {
	o.starts++
	return &recordingTransaction{observer: o}, nil
}

func (t *recordingTransaction) Finish(*tool.Result, error) error {
	t.observer.ends++
	return nil
}

func (t *recordingTransaction) Deny(string) error {
	t.observer.denies++
	return nil
}

func (t *recordingTransaction) Cancel(error) error { return nil }

func TestExecutor_TransactionObserverWrapsExecutionAndDeny(t *testing.T) {
	r := tool.NewRegistry()
	mustRegister(t, r, def("echo", nil2))
	ex := tool.NewExecutor(r, tool.ExecutorOptions{})
	observer := &recordingTransactionObserver{}
	ctx := tool.WithTransactionObserver(context.Background(), observer)
	if _, err := ex.Run(ctx, []tool.Call{call("1", "echo")}, nil); err != nil {
		t.Fatal(err)
	}
	if observer.starts != 1 || observer.ends != 1 {
		t.Fatalf("observer counts after execution = starts %d ends %d", observer.starts, observer.ends)
	}
	deny := &fakeInterceptor{before: func(context.Context, tool.Call) (tool.Decision, error) {
		return tool.Decision{Deny: true, Reason: "approval required"}, nil
	}}
	if _, err := ex.Run(ctx, []tool.Call{call("2", "echo")}, deny); err != nil {
		t.Fatal(err)
	}
	if observer.starts != 2 || observer.denies != 1 || observer.ends != 1 {
		t.Fatalf("observer counts after deny = starts %d ends %d denies %d", observer.starts, observer.ends, observer.denies)
	}
}

func TestExecutor_ContextCancellationStopsRemainingSegments(t *testing.T) {
	t.Parallel()

	var executed atomic.Int64
	counting := func(d *tool.Definition) {
		d.Handler = func(ctx context.Context, _ tool.Call) (*tool.Result, error) {
			executed.Add(1)
			return &tool.Result{Content: "ok"}, nil
		}
	}

	r := tool.NewRegistry()
	mustRegister(t, r, def("w1", counting))
	mustRegister(t, r, def("w2", counting))
	mustRegister(t, r, def("w3", counting))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立刻取消

	ex := tool.NewExecutor(r, tool.ExecutorOptions{})
	_, err := ex.Run(ctx, []tool.Call{call("1", "w1"), call("2", "w2"), call("3", "w3")}, nil)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if got := executed.Load(); got != 0 {
		t.Fatalf("executed %d tools after cancellation, want 0", got)
	}
}

func TestExecutor_CascadeCancelWithinConcurrentSegment(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 8)
	var completed atomic.Int64

	r := tool.NewRegistry()
	mustRegister(t, r, def("canceller", func(d *tool.Definition) {
		readOnly(d)
		d.Handler = func(context.Context, tool.Call) (*tool.Result, error) {
			started <- struct{}{}
			return nil, context.Canceled
		}
	}))
	mustRegister(t, r, def("slowpoke", func(d *tool.Definition) {
		readOnly(d)
		d.Handler = func(ctx context.Context, _ tool.Call) (*tool.Result, error) {
			started <- struct{}{}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
				completed.Add(1)
				return &tool.Result{Content: "too late"}, nil
			}
		}
	}))

	ex := tool.NewExecutor(r, tool.ExecutorOptions{Concurrency: 4})
	start := time.Now()
	_, err := ex.Run(context.Background(), []tool.Call{
		call("1", "canceller"), call("2", "slowpoke"),
	}, nil)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Run() took %v; the cancellation did not cascade to siblings", elapsed)
	}
	if completed.Load() != 0 {
		t.Fatal("a sibling call completed despite cascade cancellation")
	}
}

// --- Interceptor ---

func TestExecutor_InterceptorDenySkipsExecution(t *testing.T) {
	t.Parallel()

	var executed atomic.Bool
	r := tool.NewRegistry()
	mustRegister(t, r, def("guarded", func(d *tool.Definition) {
		d.Handler = func(context.Context, tool.Call) (*tool.Result, error) {
			executed.Store(true)
			return &tool.Result{Content: "should not happen"}, nil
		}
	}))

	ic := &fakeInterceptor{
		before: func(_ context.Context, c tool.Call) (tool.Decision, error) {
			return tool.Decision{Deny: true, Reason: "permission denied"}, nil
		},
	}

	ex := tool.NewExecutor(r, tool.ExecutorOptions{})
	got, err := ex.Run(context.Background(), []tool.Call{call("1", "guarded")}, ic)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if executed.Load() {
		t.Fatal("handler ran despite a Deny decision")
	}
	if !got[0].Result.IsError || !strings.Contains(got[0].Result.Content, "permission denied") {
		t.Fatalf("result = %+v, want an error result carrying the deny reason", got[0].Result)
	}
}

func TestExecutor_InterceptorRewritesArgs(t *testing.T) {
	t.Parallel()

	var seen json.RawMessage
	r := tool.NewRegistry()
	mustRegister(t, r, def("echo", func(d *tool.Definition) {
		d.Handler = func(_ context.Context, c tool.Call) (*tool.Result, error) {
			seen = c.Args
			return &tool.Result{Content: "ok"}, nil
		}
	}))

	ic := &fakeInterceptor{
		before: func(context.Context, tool.Call) (tool.Decision, error) {
			return tool.Decision{Args: json.RawMessage(`{"rewritten":true}`)}, nil
		},
	}

	ex := tool.NewExecutor(r, tool.ExecutorOptions{})
	if _, err := ex.Run(context.Background(), []tool.Call{call("1", "echo")}, ic); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if string(seen) != `{"rewritten":true}` {
		t.Fatalf("handler received args %q, want the rewritten ones", seen)
	}
}

func TestExecutor_InterceptorEndTurnStopsBatch(t *testing.T) {
	t.Parallel()

	var executed atomic.Int64
	counting := func(d *tool.Definition) {
		d.Handler = func(context.Context, tool.Call) (*tool.Result, error) {
			executed.Add(1)
			return &tool.Result{Content: "ok"}, nil
		}
	}

	r := tool.NewRegistry()
	mustRegister(t, r, def("ask_clarification", counting))
	mustRegister(t, r, def("later", counting))

	ic := &fakeInterceptor{
		before: func(_ context.Context, c tool.Call) (tool.Decision, error) {
			if c.Name == "ask_clarification" {
				return tool.Decision{EndTurn: true, Reason: "need input"}, nil
			}
			return tool.Decision{}, nil
		},
	}

	ex := tool.NewExecutor(r, tool.ExecutorOptions{})
	got, err := ex.Run(context.Background(), []tool.Call{
		call("1", "ask_clarification"), call("2", "later"),
	}, ic)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !ex.EndTurnRequested() {
		t.Error("EndTurnRequested() = false, want true")
	}
	if executed.Load() != 0 {
		t.Errorf("executed %d tools, want 0 after EndTurn", executed.Load())
	}
	if len(got) != 1 {
		t.Errorf("results = %d, want 1; the batch must stop at EndTurn", len(got))
	}
}

func TestExecutor_InterceptorAfterToolRewritesResult(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("big", func(d *tool.Definition) {
		d.Handler = func(context.Context, tool.Call) (*tool.Result, error) {
			return &tool.Result{Content: strings.Repeat("x", 100)}, nil
		}
	}))

	ic := &fakeInterceptor{
		after: func(_ context.Context, _ tool.Call, res *tool.Result, _ error) (*tool.Result, error) {
			return &tool.Result{Content: "truncated"}, nil
		},
	}

	ex := tool.NewExecutor(r, tool.ExecutorOptions{})
	got, err := ex.Run(context.Background(), []tool.Call{call("1", "big")}, ic)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got[0].Result.Content != "truncated" {
		t.Fatalf("result = %q, want the rewritten result", got[0].Result.Content)
	}
}

// AfterTool 必须看到 Handler 的原始错误，才能把它转成 error ToolMessage。
func TestExecutor_InterceptorAfterToolSeesExecError(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("boom", func(d *tool.Definition) {
		d.Handler = func(context.Context, tool.Call) (*tool.Result, error) {
			return nil, errors.New("disk full")
		}
	}))

	var seenErr error
	ic := &fakeInterceptor{
		after: func(_ context.Context, _ tool.Call, res *tool.Result, execErr error) (*tool.Result, error) {
			seenErr = execErr
			return res, nil
		},
	}

	ex := tool.NewExecutor(r, tool.ExecutorOptions{})
	if _, err := ex.Run(context.Background(), []tool.Call{call("1", "boom")}, ic); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if seenErr == nil || !strings.Contains(seenErr.Error(), "disk full") {
		t.Fatalf("AfterTool saw execErr = %v, want the handler error", seenErr)
	}
}

// 中断时已执行的结果必须回传：它们要进转录，否则对应的 tool_calls 会悬空，
// 下一次模型请求非法。
func TestExecutor_ReturnsPartialResultsOnAbort(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("first"))  // 独占段，会执行
	mustRegister(t, r, def("second")) // 独占段，拦截器在此报错

	ic := &fakeInterceptor{
		before: func(_ context.Context, c tool.Call) (tool.Decision, error) {
			if c.Name == "second" {
				return tool.Decision{}, errors.New("governance failure")
			}
			return tool.Decision{}, nil
		},
	}

	ex := tool.NewExecutor(r, tool.ExecutorOptions{})
	got, err := ex.Run(context.Background(), []tool.Call{
		call("1", "first"), call("2", "second"),
	}, ic)

	if err == nil {
		t.Fatal("Run() error = nil, want the governance error")
	}
	if len(got) != 1 {
		t.Fatalf("partial results = %d (%v), want 1", len(got), resultNames(got))
	}
	if got[0].Call.Name != "first" || got[0].Result == nil {
		t.Fatalf("partial result = %+v, want the completed first call", got[0])
	}
}

func TestExecutor_InterceptorBeforeErrorAbortsBatch(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	mustRegister(t, r, def("x", readOnly))

	ic := &fakeInterceptor{
		before: func(context.Context, tool.Call) (tool.Decision, error) {
			return tool.Decision{}, errors.New("governance failure")
		},
	}

	ex := tool.NewExecutor(r, tool.ExecutorOptions{})
	_, err := ex.Run(context.Background(), []tool.Call{call("1", "x")}, ic)
	if err == nil || !strings.Contains(err.Error(), "governance failure") {
		t.Fatalf("Run() error = %v, want the governance error to abort the batch", err)
	}
}

func TestExecutor_ConcurrentInterceptorUseIsSafe(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var order []string

	r := tool.NewRegistry()
	var calls []tool.Call
	for i := range 8 {
		name := fmt.Sprintf("r%d", i)
		mustRegister(t, r, def(name, readOnly))
		calls = append(calls, call(fmt.Sprint(i), name))
	}

	ic := &fakeInterceptor{
		before: func(_ context.Context, c tool.Call) (tool.Decision, error) {
			mu.Lock()
			order = append(order, c.Name)
			mu.Unlock()
			return tool.Decision{}, nil
		},
	}

	ex := tool.NewExecutor(r, tool.ExecutorOptions{Concurrency: 4})
	if _, err := ex.Run(context.Background(), calls, ic); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 8 {
		t.Fatalf("interceptor saw %d calls, want 8", len(order))
	}
}

// --- helpers ---

func nil2(*tool.Definition) {}

func slowTool(name string, opt func(*tool.Definition), d time.Duration) tool.Definition {
	return def(name, func(td *tool.Definition) {
		if opt != nil {
			opt(td)
		}
		td.Handler = func(context.Context, tool.Call) (*tool.Result, error) {
			if d > 0 {
				time.Sleep(d)
			}
			return &tool.Result{Content: name}, nil
		}
	})
}

func resultNames(rs []tool.Outcome) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Call.Name
	}
	return out
}

func describeSegments(segs []tool.Segment) string {
	parts := make([]string, len(segs))
	for i, s := range segs {
		names := make([]string, len(s.Calls))
		for j, c := range s.Calls {
			names[j] = c.Call.Name
		}
		kind := "exclusive"
		if s.Concurrent {
			kind = "concurrent"
		}
		parts[i] = fmt.Sprintf("%s[%s]", kind, strings.Join(names, ","))
	}
	return strings.Join(parts, " ")
}

type fakeInterceptor struct {
	before func(context.Context, tool.Call) (tool.Decision, error)
	after  func(context.Context, tool.Call, *tool.Result, error) (*tool.Result, error)
}

func (f *fakeInterceptor) BeforeTool(ctx context.Context, c tool.Call) (tool.Decision, error) {
	if f.before == nil {
		return tool.Decision{}, nil
	}
	return f.before(ctx, c)
}

func (f *fakeInterceptor) AfterTool(ctx context.Context, c tool.Call, res *tool.Result, execErr error) (*tool.Result, error) {
	if f.after == nil {
		return res, nil
	}
	return f.after(ctx, c, res, execErr)
}
