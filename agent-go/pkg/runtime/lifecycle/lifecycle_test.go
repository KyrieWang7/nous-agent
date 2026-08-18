package lifecycle_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// --- State ---

func TestState_TakeResetsDirective(t *testing.T) {
	t.Parallel()

	st := lifecycle.NewState(lifecycle.StateInit{History: message.NewHistory()})
	st.Directive = lifecycle.DirectiveStop

	if got := st.Take(); got != lifecycle.DirectiveStop {
		t.Fatalf("Take() = %v, want Stop", got)
	}
	if got := st.Take(); got != lifecycle.DirectiveProceed {
		t.Fatalf("second Take() = %v, want Proceed; Take must reset", got)
	}
}

func TestState_CloneForToolIsIndependentButSharesHistory(t *testing.T) {
	t.Parallel()

	h := message.NewHistory()
	st := lifecycle.NewState(lifecycle.StateInit{History: h, ThreadID: "t1"})
	st.ToolCall = &tool.Call{ID: "a", Name: "ls"}

	dup := st.CloneForTool(tool.Call{ID: "b", Name: "bash"})

	if dup.ToolCall.Name != "bash" {
		t.Fatalf("clone ToolCall = %+v", dup.ToolCall)
	}
	if st.ToolCall.Name != "ls" {
		t.Fatal("CloneForTool mutated the parent state")
	}
	if dup.ThreadID != "t1" {
		t.Fatalf("clone lost ThreadID: %q", dup.ThreadID)
	}
	if dup.History != h {
		t.Fatal("CloneForTool must share the History pointer; the transcript is single-instance state")
	}
}

func TestState_ValuesIsLazilyInitialised(t *testing.T) {
	t.Parallel()

	st := lifecycle.NewState(lifecycle.StateInit{History: message.NewHistory()})
	st.SetValue("k", 1)

	if got, ok := st.Value("k"); !ok || got != 1 {
		t.Fatalf("Value() = %v, %v", got, ok)
	}
}

// --- Dispatcher: 阶段与顺序 ---

func TestChain_BeforeStagesRunInRegistrationOrder(t *testing.T) {
	t.Parallel()

	var order []string
	c := mustChain(t,
		recorder{name: "first", log: &order},
		recorder{name: "second", log: &order},
		recorder{name: "third", log: &order},
	)

	if err := c.Execute(context.Background(), lifecycle.StageBeforeModel, newState()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	want := "first:BeforeModel,second:BeforeModel,third:BeforeModel"
	if got := strings.Join(order, ","); got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
}

// AfterModel/AfterAgent 逆注册顺序执行（设计文档 §4.3）。
// nous-agent 依赖这个反序让 SafetyFinishReason 先于 LoopDetection 观察原始输出。
func TestChain_AfterStagesRunInReverseRegistrationOrder(t *testing.T) {
	t.Parallel()

	for _, stage := range []lifecycle.Stage{lifecycle.StageAfterModel, lifecycle.StageAfterAgent} {
		t.Run(stage.String(), func(t *testing.T) {
			t.Parallel()

			var order []string
			c := mustChain(t,
				recorder{name: "loopDetection", log: &order},
				recorder{name: "safetyFinishReason", log: &order},
			)

			if err := c.Execute(context.Background(), stage, newState()); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if len(order) != 2 {
				t.Fatalf("order = %v, want 2 entries", order)
			}
			if !strings.HasPrefix(order[0], "safetyFinishReason") {
				t.Fatalf("order = %v; the later-registered lifecycle must run first", order)
			}
		})
	}
}

func TestChain_OnlyCallsImplementedStages(t *testing.T) {
	t.Parallel()

	only := &beforeModelOnly{}
	c := mustChain(t, only)

	for _, stage := range []lifecycle.Stage{
		lifecycle.StageBeforeAgent,
		lifecycle.StageBeforeModel,
		lifecycle.StageAfterModel,
		lifecycle.StageAfterAgent,
	} {
		if err := c.Execute(context.Background(), stage, newState()); err != nil {
			t.Fatalf("Execute(%v) error = %v", stage, err)
		}
	}

	if only.calls != 1 {
		t.Fatalf("lifecycle invoked %d times, want 1; unimplemented stages must be skipped", only.calls)
	}
}

// --- Dispatcher: 错误分级（设计文档 §17.1）---

func TestChain_AbortGradeShortCircuits(t *testing.T) {
	t.Parallel()

	var order []string
	c := mustChain(t,
		failing{name: "governance", grade: lifecycle.GradeAbort, err: errors.New("denied")},
		recorder{name: "downstream", log: &order},
	)

	err := c.Execute(context.Background(), lifecycle.StageBeforeModel, newState())
	if err == nil {
		t.Fatal("Execute() error = nil, want the abort error")
	}
	if !strings.Contains(err.Error(), "governance") || !strings.Contains(err.Error(), "denied") {
		t.Errorf("error = %q, want it to name the lifecycle and the cause", err)
	}
	if len(order) != 0 {
		t.Errorf("downstream lifecycle ran after an abort: %v", order)
	}
}

// 监听类生命周期处理器失败不得判死一个已经成功的回合。
// Title 生成失败让整轮回答丢失是纯负收益。
func TestChain_ListenerGradeIsLoggedNotPropagated(t *testing.T) {
	t.Parallel()

	var order []string
	c := mustChain(t,
		failing{name: "title", grade: lifecycle.GradeListener, err: errors.New("summariser down")},
		recorder{name: "downstream", log: &order},
	)

	if err := c.Execute(context.Background(), lifecycle.StageAfterAgent, newState()); err != nil {
		t.Fatalf("Execute() error = %v, want nil; listener failures must not abort the turn", err)
	}
	if len(order) != 1 {
		t.Fatalf("downstream ran %d times, want 1; the chain must continue past a listener failure", len(order))
	}
}

func TestChain_DefaultGradeAborts(t *testing.T) {
	t.Parallel()

	// 未声明 Grade 的生命周期处理器按 Abort 处理：默认保守
	c := mustChain(t, plainFailing{})

	if err := c.Execute(context.Background(), lifecycle.StageBeforeModel, newState()); err == nil {
		t.Fatal("Execute() error = nil; handlers without an explicit grade must abort")
	}
}

func TestChain_PerHandlerTimeout(t *testing.T) {
	t.Parallel()

	c := mustChainOpts(t, lifecycle.DispatcherOptions{Timeout: 20 * time.Millisecond}, sleeper{d: time.Second})

	start := time.Now()
	err := c.Execute(context.Background(), lifecycle.StageBeforeModel, newState())

	if err == nil {
		t.Fatal("Execute() error = nil, want a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Execute() took %v; the per-lifecycle timeout did not fire", elapsed)
	}
}

func TestChain_RespectsContextCancellation(t *testing.T) {
	t.Parallel()

	var order []string
	c := mustChain(t, recorder{name: "a", log: &order}, recorder{name: "b", log: &order})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.Execute(ctx, lifecycle.StageBeforeModel, newState()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context.Canceled", err)
	}
	if len(order) != 0 {
		t.Errorf("handlers ran despite a cancelled context: %v", order)
	}
}

func TestChain_DirectiveStopHaltsRemainingHandlers(t *testing.T) {
	t.Parallel()

	var order []string
	// AfterModel 逆序执行，所以后注册的 stopper 先跑，downstream 应被跳过。
	c := mustChain(t,
		recorder{name: "downstream", log: &order},
		stopper{name: "guardrail"},
	)

	st := newState()
	if err := c.Execute(context.Background(), lifecycle.StageAfterModel, st); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if st.Directive != lifecycle.DirectiveStop {
		t.Fatalf("Directive = %v, want Stop", st.Directive)
	}
	if len(order) != 0 {
		t.Errorf("handlers ran after DirectiveStop: %v", order)
	}
}

func TestChain_NilHandlersAreRejected(t *testing.T) {
	t.Parallel()

	if _, err := lifecycle.NewDispatcher([]lifecycle.Handler{nil}, lifecycle.DispatcherOptions{}); err == nil {
		t.Fatal("NewDispatcher() accepted a nil lifecycle")
	}
}

func TestChain_DuplicateNamesAreRejected(t *testing.T) {
	t.Parallel()

	_, err := lifecycle.NewDispatcher([]lifecycle.Handler{
		recorder{name: "dup"}, recorder{name: "dup"},
	}, lifecycle.DispatcherOptions{})

	if err == nil {
		t.Fatal("NewDispatcher() accepted duplicate names; anchors reference handlers by name")
	}
}

// 生命周期处理器带函数字段是常态（Prompter、回调、Handler）。带 func 字段的 struct
// 在 Go 里不可哈希，所以链的内部结构不能用 map[Handler]X 做索引。
func TestChain_AcceptsHandlersWithFuncFields(t *testing.T) {
	t.Parallel()

	var called bool
	c := mustChain(t, inspector{
		name:    "hasFuncField",
		onAfter: func(*lifecycle.State) { called = true },
	})

	ic := c.ToolInterceptor(newState())
	if _, err := ic.AfterTool(context.Background(), tool.Call{Name: "x"}, &tool.Result{}, nil); err != nil {
		t.Fatalf("AfterTool() error = %v", err)
	}
	if !called {
		t.Fatal("lifecycle with a func field was not invoked")
	}
}

func TestChain_StageOrderReportsActualExecutionOrder(t *testing.T) {
	t.Parallel()

	c := mustChain(t, recorder{name: "a"}, recorder{name: "b"}, recorder{name: "c"})

	if got := c.StageOrder(lifecycle.StageBeforeModel); strings.Join(got, ",") != "a,b,c" {
		t.Errorf("BeforeModel order = %v, want [a b c]", got)
	}
	if got := c.StageOrder(lifecycle.StageAfterModel); strings.Join(got, ",") != "c,b,a" {
		t.Errorf("AfterModel order = %v, want [c b a] (reversed)", got)
	}
}

func TestChain_Names(t *testing.T) {
	t.Parallel()

	c := mustChain(t, recorder{name: "a"}, recorder{name: "b"})
	got := c.Names()

	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("Names() = %v, want [a b] in registration order", got)
	}
}

// --- Dispatcher: 工具拦截适配器 ---

func TestChain_ToolInterceptorRunsBeforeAndAfterTool(t *testing.T) {
	t.Parallel()

	tw := &toolWatcher{name: "watcher"}
	c := mustChain(t, tw)

	st := newState()
	ic := c.ToolInterceptor(st)

	call := tool.Call{ID: "1", Name: "ls", Args: json.RawMessage(`{}`)}
	if _, err := ic.BeforeTool(context.Background(), call); err != nil {
		t.Fatalf("BeforeTool() error = %v", err)
	}
	res := &tool.Result{Content: "ok"}
	if _, err := ic.AfterTool(context.Background(), call, res, nil); err != nil {
		t.Fatalf("AfterTool() error = %v", err)
	}

	if tw.before != 1 || tw.after != 1 {
		t.Fatalf("before = %d, after = %d, want 1 each", tw.before, tw.after)
	}
	if tw.sawCall != "ls" {
		t.Errorf("BeforeTool saw call %q", tw.sawCall)
	}
}

func TestChain_ToolInterceptorFirstDenyWins(t *testing.T) {
	t.Parallel()

	c := mustChain(t,
		denier{name: "permission", reason: "read_only mode"},
		denier{name: "hook", reason: "hook says no"},
	)

	ic := c.ToolInterceptor(newState())
	decision, err := ic.BeforeTool(context.Background(), tool.Call{ID: "1", Name: "bash"})
	if err != nil {
		t.Fatalf("BeforeTool() error = %v", err)
	}
	if !decision.Deny {
		t.Fatal("decision.Deny = false, want true")
	}
	if decision.Reason != "read_only mode" {
		t.Fatalf("reason = %q, want the first denier's reason", decision.Reason)
	}
}

// Hook 改写入参后，后续 BeforeTool 生命周期处理器必须看到改写后的值。
func TestChain_ToolInterceptorPropagatesRewrittenArgs(t *testing.T) {
	t.Parallel()

	var seenBySecond string
	c := mustChain(t,
		rewriter{name: "hook", args: `{"rewritten":true}`},
		inspector{name: "audit", onBefore: func(c tool.Call) { seenBySecond = string(c.Args) }},
	)

	ic := c.ToolInterceptor(newState())
	decision, err := ic.BeforeTool(context.Background(), tool.Call{ID: "1", Name: "bash", Args: json.RawMessage(`{"orig":true}`)})
	if err != nil {
		t.Fatalf("BeforeTool() error = %v", err)
	}
	if string(decision.Args) != `{"rewritten":true}` {
		t.Fatalf("decision.Args = %q", decision.Args)
	}
	if seenBySecond != `{"rewritten":true}` {
		t.Fatalf("second lifecycle saw args %q, want the rewritten value", seenBySecond)
	}
}

func TestChain_ToolInterceptorPropagatesSandboxMode(t *testing.T) {
	t.Parallel()
	c := mustChain(t, sandboxSelector{name: "permission", mode: "workspace-write"})
	decision, err := c.ToolInterceptor(newState()).BeforeTool(context.Background(), tool.Call{ID: "1", Name: "write_file"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.SandboxMode != "workspace-write" {
		t.Fatalf("sandbox mode = %q, want workspace-write", decision.SandboxMode)
	}
}

func TestChain_ToolInterceptorAfterToolSeesResultAndExecErr(t *testing.T) {
	t.Parallel()

	var gotContent string
	var gotErr error
	c := mustChain(t, inspector{
		name: "errhandler",
		onAfter: func(st *lifecycle.State) {
			if st.ToolResult != nil {
				gotContent = st.ToolResult.Content
			}
			gotErr = st.ToolExecErr
		},
	})

	ic := c.ToolInterceptor(newState())
	execErr := errors.New("disk full")
	if _, err := ic.AfterTool(context.Background(), tool.Call{Name: "write"}, &tool.Result{Content: "partial"}, execErr); err != nil {
		t.Fatalf("AfterTool() error = %v", err)
	}

	if gotContent != "partial" {
		t.Errorf("AfterTool saw content %q", gotContent)
	}
	if !errors.Is(gotErr, execErr) {
		t.Errorf("AfterTool saw execErr = %v, want %v", gotErr, execErr)
	}
}

func TestChain_ToolInterceptorAfterToolCanRewriteResult(t *testing.T) {
	t.Parallel()

	c := mustChain(t, resultRewriter{name: "budget", content: "externalised"})

	ic := c.ToolInterceptor(newState())
	got, err := ic.AfterTool(context.Background(), tool.Call{Name: "read"}, &tool.Result{Content: strings.Repeat("x", 50)}, nil)
	if err != nil {
		t.Fatalf("AfterTool() error = %v", err)
	}
	if got.Content != "externalised" {
		t.Fatalf("result = %q, want the rewritten content", got.Content)
	}
}

func TestChain_ToolInterceptorEndTurnPropagates(t *testing.T) {
	t.Parallel()

	c := mustChain(t, endTurner{name: "clarification"})

	ic := c.ToolInterceptor(newState())
	decision, err := ic.BeforeTool(context.Background(), tool.Call{Name: "ask_clarification"})
	if err != nil {
		t.Fatalf("BeforeTool() error = %v", err)
	}
	if !decision.EndTurn {
		t.Fatal("decision.EndTurn = false, want true")
	}
}

// --- 锚点排序（设计文档 §4.3）---

func TestBuild_AnchorAfter(t *testing.T) {
	t.Parallel()

	got := mustBuild(t,
		[]lifecycle.Handler{recorder{name: "a"}, recorder{name: "b"}, terminal{}},
		[]lifecycle.Handler{anchored{name: "x", after: "a"}},
	)

	assertOrder(t, got, "a", "x", "b", "clarification")
}

func TestBuild_AnchorBefore(t *testing.T) {
	t.Parallel()

	got := mustBuild(t,
		[]lifecycle.Handler{recorder{name: "a"}, recorder{name: "b"}, terminal{}},
		[]lifecycle.Handler{anchored{name: "x", before: "b"}},
	)

	assertOrder(t, got, "a", "x", "b", "clarification")
}

func TestBuild_UnanchoredGoesBeforeTerminal(t *testing.T) {
	t.Parallel()

	got := mustBuild(t,
		[]lifecycle.Handler{recorder{name: "a"}, terminal{}},
		[]lifecycle.Handler{recorder{name: "x"}},
	)

	assertOrder(t, got, "a", "x", "clarification")
}

func TestBuild_UnanchoredAppendsWhenNoTerminal(t *testing.T) {
	t.Parallel()

	got := mustBuild(t,
		[]lifecycle.Handler{recorder{name: "a"}},
		[]lifecycle.Handler{recorder{name: "x"}},
	)

	assertOrder(t, got, "a", "x")
}

func TestBuild_ExtraCanAnchorToAnotherExtra(t *testing.T) {
	t.Parallel()

	got := mustBuild(t,
		[]lifecycle.Handler{recorder{name: "a"}, terminal{}},
		[]lifecycle.Handler{
			anchored{name: "y", after: "x"}, // 依赖另一个 extra，需要迭代插入
			anchored{name: "x", after: "a"},
		},
	)

	assertOrder(t, got, "a", "x", "y", "clarification")
}

func TestBuild_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		base  []lifecycle.Handler
		extra []lifecycle.Handler
		want  string
	}{
		{
			name:  "both anchors declared",
			base:  []lifecycle.Handler{recorder{name: "a"}},
			extra: []lifecycle.Handler{anchored{name: "x", after: "a", before: "a"}},
			want:  "both",
		},
		{
			name:  "anchor target missing",
			base:  []lifecycle.Handler{recorder{name: "a"}},
			extra: []lifecycle.Handler{anchored{name: "x", after: "ghost"}},
			want:  "ghost",
		},
		{
			name: "circular anchors",
			base: []lifecycle.Handler{recorder{name: "a"}},
			extra: []lifecycle.Handler{
				anchored{name: "x", after: "y"},
				anchored{name: "y", after: "x"},
			},
			want: "circular",
		},
		{
			name:  "duplicate name against base",
			base:  []lifecycle.Handler{recorder{name: "a"}},
			extra: []lifecycle.Handler{recorder{name: "a"}},
			want:  "duplicate",
		},
		{
			name: "two extras claim the same anchor slot",
			base: []lifecycle.Handler{recorder{name: "a"}},
			extra: []lifecycle.Handler{
				anchored{name: "x", after: "a"},
				anchored{name: "y", after: "a"},
			},
			want: "conflict",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := lifecycle.Compose(tc.base, tc.extra)
			if err == nil {
				t.Fatalf("Build() succeeded, want error mentioning %q", tc.want)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestBuild_NoExtrasReturnsBaseUnchanged(t *testing.T) {
	t.Parallel()

	got := mustBuild(t, []lifecycle.Handler{recorder{name: "a"}, terminal{}}, nil)
	assertOrder(t, got, "a", "clarification")
}

// 配置声明启用的生命周期处理器必须真的出现在链里。
// nous-agent 的 RuntimeFeatures 与 extra_handler 都是静默失效的死缝。
func TestVerifyPresent(t *testing.T) {
	t.Parallel()

	chain := []lifecycle.Handler{recorder{name: "a"}, recorder{name: "b"}}

	if err := lifecycle.VerifyPresent(chain, []string{"a", "b"}); err != nil {
		t.Fatalf("VerifyPresent() error = %v", err)
	}

	err := lifecycle.VerifyPresent(chain, []string{"a", "missing"})
	if err == nil {
		t.Fatal("VerifyPresent() error = nil for a declared-but-absent lifecycle")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error = %q, want it to name the absent lifecycle", err)
	}
}

// --- helpers ---

func newState() *lifecycle.State {
	return lifecycle.NewState(lifecycle.StateInit{History: message.NewHistory()})
}

func mustChain(t *testing.T, mws ...lifecycle.Handler) *lifecycle.Dispatcher {
	t.Helper()
	return mustChainOpts(t, lifecycle.DispatcherOptions{}, mws...)
}

func mustChainOpts(t *testing.T, opts lifecycle.DispatcherOptions, mws ...lifecycle.Handler) *lifecycle.Dispatcher {
	t.Helper()
	c, err := lifecycle.NewDispatcher(mws, opts)
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}
	return c
}

func mustBuild(t *testing.T, base, extra []lifecycle.Handler) []lifecycle.Handler {
	t.Helper()
	got, err := lifecycle.Compose(base, extra)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return got
}

func assertOrder(t *testing.T, mws []lifecycle.Handler, want ...string) {
	t.Helper()
	got := make([]string, len(mws))
	for i, m := range mws {
		got[i] = m.Name()
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// recorder 记录被调用的阶段，四个阶段全实现。
type recorder struct {
	name string
	log  *[]string
}

func (r recorder) Name() string { return r.name }
func (r recorder) record(stage string) error {
	if r.log != nil {
		*r.log = append(*r.log, r.name+":"+stage)
	}
	return nil
}
func (r recorder) BeforeAgent(context.Context, *lifecycle.State) error {
	return r.record("BeforeAgent")
}
func (r recorder) BeforeModel(context.Context, *lifecycle.State) error {
	return r.record("BeforeModel")
}
func (r recorder) AfterModel(context.Context, *lifecycle.State) error { return r.record("AfterModel") }
func (r recorder) AfterAgent(context.Context, *lifecycle.State) error { return r.record("AfterAgent") }

type beforeModelOnly struct{ calls int }

func (b *beforeModelOnly) Name() string { return "beforeModelOnly" }
func (b *beforeModelOnly) BeforeModel(context.Context, *lifecycle.State) error {
	b.calls++
	return nil
}

type failing struct {
	name  string
	grade lifecycle.Grade
	err   error
}

func (f failing) Name() string                                        { return f.name }
func (f failing) Grade() lifecycle.Grade                              { return f.grade }
func (f failing) BeforeModel(context.Context, *lifecycle.State) error { return f.err }
func (f failing) AfterAgent(context.Context, *lifecycle.State) error  { return f.err }

type plainFailing struct{}

func (plainFailing) Name() string { return "plainFailing" }
func (plainFailing) BeforeModel(context.Context, *lifecycle.State) error {
	return errors.New("boom")
}

type sleeper struct{ d time.Duration }

func (sleeper) Name() string { return "sleeper" }
func (s sleeper) BeforeModel(ctx context.Context, _ *lifecycle.State) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.d):
		return nil
	}
}

type stopper struct{ name string }

func (s stopper) Name() string { return s.name }
func (s stopper) AfterModel(_ context.Context, st *lifecycle.State) error {
	st.Directive = lifecycle.DirectiveStop
	return nil
}

type terminal struct{}

func (terminal) Name() string { return lifecycle.TerminalName }
func (terminal) AfterModel(context.Context, *lifecycle.State) error {
	return nil
}

type anchored struct {
	name          string
	after, before string
}

func (a anchored) Name() string { return a.name }
func (a anchored) Anchor() lifecycle.Anchor {
	return lifecycle.Anchor{After: a.after, Before: a.before}
}
func (a anchored) BeforeModel(context.Context, *lifecycle.State) error { return nil }

type toolWatcher struct {
	name    string
	before  int
	after   int
	sawCall string
}

func (t *toolWatcher) Name() string { return t.name }
func (t *toolWatcher) BeforeTool(_ context.Context, st *lifecycle.State) (tool.Decision, error) {
	t.before++
	if st.ToolCall != nil {
		t.sawCall = st.ToolCall.Name
	}
	return tool.Decision{}, nil
}
func (t *toolWatcher) AfterTool(context.Context, *lifecycle.State) error {
	t.after++
	return nil
}

type denier struct {
	name   string
	reason string
}

func (d denier) Name() string { return d.name }
func (d denier) BeforeTool(context.Context, *lifecycle.State) (tool.Decision, error) {
	return tool.Decision{Deny: true, Reason: d.reason}, nil
}

type rewriter struct {
	name string
	args string
}

type sandboxSelector struct {
	name string
	mode string
}

func (s sandboxSelector) Name() string { return s.name }
func (s sandboxSelector) BeforeTool(context.Context, *lifecycle.State) (tool.Decision, error) {
	return tool.Decision{SandboxMode: s.mode}, nil
}

func (r rewriter) Name() string { return r.name }
func (r rewriter) BeforeTool(context.Context, *lifecycle.State) (tool.Decision, error) {
	return tool.Decision{Args: json.RawMessage(r.args)}, nil
}

type endTurner struct{ name string }

func (e endTurner) Name() string { return e.name }
func (e endTurner) BeforeTool(context.Context, *lifecycle.State) (tool.Decision, error) {
	return tool.Decision{EndTurn: true, Reason: "need input"}, nil
}

type inspector struct {
	name     string
	onBefore func(tool.Call)
	onAfter  func(*lifecycle.State)
}

func (i inspector) Name() string { return i.name }
func (i inspector) BeforeTool(_ context.Context, st *lifecycle.State) (tool.Decision, error) {
	if i.onBefore != nil && st.ToolCall != nil {
		i.onBefore(*st.ToolCall)
	}
	return tool.Decision{}, nil
}
func (i inspector) AfterTool(_ context.Context, st *lifecycle.State) error {
	if i.onAfter != nil {
		i.onAfter(st)
	}
	return nil
}

type resultRewriter struct {
	name    string
	content string
}

func (r resultRewriter) Name() string { return r.name }
func (r resultRewriter) AfterTool(_ context.Context, st *lifecycle.State) error {
	st.ToolResult = &tool.Result{Content: r.content}
	return nil
}
