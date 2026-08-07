package loop_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// --- 脚手架 ---

type harness struct {
	t        *testing.T
	fx       *faux.Model
	registry *tool.Registry
	runner   *loop.Runner
}

type harnessOpts struct {
	turns      []faux.Turn
	tools      []tool.Definition
	middleware []middleware.Middleware
	limits     loop.Limits
	stopGate   loop.StopGate
	compactor  loop.Compactor
	trimmer    loop.Trimmer
	publisher  loop.Publisher
	resolver   loop.ToolSetResolver
}

func newHarness(t *testing.T, opts harnessOpts) *harness {
	t.Helper()

	fx := faux.New(opts.turns...)
	registry := tool.NewRegistry()
	for _, d := range opts.tools {
		if err := registry.Register(d); err != nil {
			t.Fatalf("Register(%q) error = %v", d.Name, err)
		}
	}

	chain, err := middleware.NewChain(opts.middleware, middleware.ChainOptions{})
	if err != nil {
		t.Fatalf("NewChain() error = %v", err)
	}

	limits := opts.limits
	if limits.MaxIterations == 0 {
		limits.MaxIterations = 10 // 防止测试里的失控循环挂住 CI
	}

	r, err := loop.NewRunner(loop.Config{
		Sampler:   loop.NewDirectSampler(fx),
		Registry:  registry,
		Executor:  tool.NewExecutor(registry, tool.ExecutorOptions{}),
		Chain:     chain,
		Limits:    limits,
		StopGate:  opts.stopGate,
		Compactor: opts.compactor,
		Trimmer:   opts.trimmer,
		Publisher: opts.publisher,
		ToolSet:   opts.resolver,
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	return &harness{t: t, fx: fx, registry: registry, runner: r}
}

func (h *harness) run(ctx context.Context, prompt string) (*loop.Result, error) {
	h.t.Helper()
	hist := message.NewHistory()
	return h.runner.Run(ctx, loop.Request{
		ThreadID: "t1",
		RunID:    "r1",
		History:  hist,
		Prompt:   prompt,
	})
}

func echoTool(name string) tool.Definition {
	return tool.Definition{
		Name:        name,
		Group:       "test",
		Description: name,
		Parameters:  json.RawMessage(`{"type":"object"}`),
		Metadata:    tool.Metadata{IsReadOnly: true, IsConcurrencySafe: true},
		Handler: func(_ context.Context, c tool.Call) (*tool.Result, error) {
			return &tool.Result{Content: name + " ran"}, nil
		},
	}
}

// --- 步 12：正常终止 ---

func TestLoop_NoToolCallsEndsTurn(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.Text("final answer")}})

	res, err := h.run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if res.Output != "final answer" {
		t.Fatalf("Output = %q", res.Output)
	}
	if res.Iterations != 1 {
		t.Fatalf("Iterations = %d, want 1", res.Iterations)
	}
	if h.fx.CallCount() != 1 {
		t.Fatalf("model called %d times, want 1", h.fx.CallCount())
	}
}

// --- 步 10：有工具调用则继续 ---

func TestLoop_ToolCallsContinueUntilPlainAnswer(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOpts{
		turns: []faux.Turn{
			faux.ToolCall("echo", `{}`),
			faux.Text("done"),
		},
		tools: []tool.Definition{echoTool("echo")},
	})

	res, err := h.run(context.Background(), "run echo")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if res.Output != "done" {
		t.Fatalf("Output = %q, want %q", res.Output, "done")
	}
	if res.Iterations != 2 {
		t.Fatalf("Iterations = %d, want 2", res.Iterations)
	}
}

// 转录是唯一状态：user / assistant+toolcall / tool / assistant 四条齐全，
// 且顺序正确 —— 否则下一次模型请求非法。
func TestLoop_HistoryIsTheOnlyStateAndIsWellFormed(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOpts{
		turns: []faux.Turn{faux.ToolCall("echo", `{}`), faux.Text("done")},
		tools: []tool.Definition{echoTool("echo")},
	})

	hist := message.NewHistory()
	if _, err := h.runner.Run(context.Background(), loop.Request{
		ThreadID: "t1", RunID: "r1", History: hist, Prompt: "go",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	msgs := hist.All()
	if len(msgs) != 4 {
		t.Fatalf("history = %s, want 4 messages", describe(msgs))
	}
	want := []message.Role{message.RoleUser, message.RoleAssistant, message.RoleTool, message.RoleAssistant}
	for i, w := range want {
		if msgs[i].Role != w {
			t.Fatalf("history = %s, want roles %v", describe(msgs), want)
		}
	}
	if msgs[2].ToolCallID != msgs[1].ToolCalls[0].ID {
		t.Fatalf("tool result is not paired with its call: %s", describe(msgs))
	}
}

func TestLoop_ToolResultsKeepModelOrder(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOpts{
		turns: []faux.Turn{
			faux.ToolCalls(
				faux.Call{ID: "c1", Name: "alpha", Args: `{}`},
				faux.Call{ID: "c2", Name: "beta", Args: `{}`},
			),
			faux.Text("done"),
		},
		tools: []tool.Definition{echoTool("alpha"), echoTool("beta")},
	})

	hist := message.NewHistory()
	if _, err := h.runner.Run(context.Background(), loop.Request{
		ThreadID: "t1", RunID: "r1", History: hist, Prompt: "go",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	msgs := hist.All()
	var toolIDs []string
	for _, m := range msgs {
		if m.Role == message.RoleTool {
			toolIDs = append(toolIDs, m.ToolCallID)
		}
	}
	if len(toolIDs) != 2 || toolIDs[0] != "c1" || toolIDs[1] != "c2" {
		t.Fatalf("tool results = %v, want [c1 c2] in the model's order", toolIDs)
	}
}

// --- 步 1、2：取消与上限 ---

func TestLoop_ContextCancelledBeforeFirstIteration(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.Text("never")}})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := h.run(ctx, "hi")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if h.fx.CallCount() != 0 {
		t.Fatalf("model called %d times after cancellation, want 0", h.fx.CallCount())
	}
}

func TestLoop_MaxIterations(t *testing.T) {
	t.Parallel()

	// 每轮都请求工具，永不给纯文本答案
	turns := make([]faux.Turn, 8)
	for i := range turns {
		turns[i] = faux.ToolCall("echo", `{}`)
	}

	h := newHarness(t, harnessOpts{
		turns:  turns,
		tools:  []tool.Definition{echoTool("echo")},
		limits: loop.Limits{MaxIterations: 3},
	})

	_, err := h.run(context.Background(), "loop forever")
	if !errors.Is(err, loop.ErrMaxIterations) {
		t.Fatalf("Run() error = %v, want ErrMaxIterations", err)
	}
	if h.fx.CallCount() != 3 {
		t.Fatalf("model called %d times, want 3", h.fx.CallCount())
	}
}

// --- 步 3：采样前压缩 ---

func TestLoop_CompactionRunsBeforeEachSampling(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	c := compactorFunc(func(context.Context, *message.History) (bool, error) {
		calls.Add(1)
		return false, nil
	})

	h := newHarness(t, harnessOpts{
		turns:     []faux.Turn{faux.ToolCall("echo", `{}`), faux.Text("done")},
		tools:     []tool.Definition{echoTool("echo")},
		compactor: c,
	})

	if _, err := h.run(context.Background(), "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("compactor called %d times, want 2 (once per iteration)", got)
	}
}

// Result.Compacted 是持久化层选择"整体重写"还是"追加"的唯一判据，
// 不能靠"消息数变少了"这种推断（设计文档 §13.2）。
func TestLoop_CompactedFlagIsStickyAcrossIterations(t *testing.T) {
	t.Parallel()

	var n atomic.Int64
	c := compactorFunc(func(context.Context, *message.History) (bool, error) {
		// 只在第一轮压缩
		return n.Add(1) == 1, nil
	})

	h := newHarness(t, harnessOpts{
		turns:     []faux.Turn{faux.ToolCall("echo", `{}`), faux.Text("done")},
		tools:     []tool.Definition{echoTool("echo")},
		compactor: c,
	})

	res, err := h.run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !res.Compacted {
		t.Fatal("Result.Compacted = false; a compaction happened in iteration 1 and the flag must survive later iterations")
	}
}

func TestLoop_CompactorErrorAbortsTurn(t *testing.T) {
	t.Parallel()

	c := compactorFunc(func(context.Context, *message.History) (bool, error) {
		return false, errors.New("summariser exploded")
	})

	h := newHarness(t, harnessOpts{
		turns:     []faux.Turn{faux.Text("never")},
		compactor: c,
	})

	_, err := h.run(context.Background(), "go")
	if err == nil || !strings.Contains(err.Error(), "summariser exploded") {
		t.Fatalf("Run() error = %v, want the compactor error", err)
	}
}

// --- 步 4、5：裁剪与每轮工具集 ---

func TestLoop_TrimmerShapesTheRequestNotTheHistory(t *testing.T) {
	t.Parallel()

	tr := trimmerFunc(func(msgs []message.Message) []message.Message {
		return msgs[:1] // 只投递第一条
	})

	h := newHarness(t, harnessOpts{
		turns:   []faux.Turn{faux.Text("done")},
		trimmer: tr,
	})

	hist := message.NewHistory()
	if _, err := h.runner.Run(context.Background(), loop.Request{
		ThreadID: "t1", RunID: "r1", History: hist, Prompt: "go",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	req, _ := h.fx.LastRequest()
	if len(req.Messages) != 1 {
		t.Fatalf("delivered %d messages, want 1 after trimming", len(req.Messages))
	}
	// 转录本身不受裁剪影响
	if hist.Len() != 2 {
		t.Fatalf("history has %d messages, want 2; trimming must not mutate the transcript", hist.Len())
	}
}

func TestLoop_ToolSetIsRecomputedEachIteration(t *testing.T) {
	t.Parallel()

	var iterations atomic.Int64
	resolver := resolverFunc(func(st *middleware.State) ([]string, []string) {
		iterations.Add(1)
		// 第一轮给 echo，第二轮收走
		if st.Iteration == 0 {
			return []string{"echo"}, nil
		}
		return nil, nil
	})

	h := newHarness(t, harnessOpts{
		turns:    []faux.Turn{faux.ToolCall("echo", `{}`), faux.Text("done")},
		tools:    []tool.Definition{echoTool("echo")},
		resolver: resolver,
	})

	if _, err := h.run(context.Background(), "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := iterations.Load(); got != 2 {
		t.Fatalf("resolver called %d times, want 2 (每轮重算)", got)
	}

	reqs := h.fx.Requests()
	if len(reqs[0].Tools) != 1 {
		t.Fatalf("iteration 0 delivered %d tools, want 1", len(reqs[0].Tools))
	}
	if len(reqs[1].Tools) != 0 {
		t.Fatalf("iteration 1 delivered %d tools, want 0; the narrowed set must take effect", len(reqs[1].Tools))
	}
}

func TestLoop_DefaultToolSetIsEverythingRegistered(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOpts{
		turns: []faux.Turn{faux.Text("done")},
		tools: []tool.Definition{echoTool("alpha"), echoTool("beta")},
	})

	if _, err := h.run(context.Background(), "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	req, _ := h.fx.LastRequest()
	if len(req.Tools) != 2 {
		t.Fatalf("delivered %d tools, want 2", len(req.Tools))
	}
}

// --- 步 6、9：切面 ---

func TestLoop_StagesRunAtTheRightTimes(t *testing.T) {
	t.Parallel()

	var log []string
	rec := &stageRecorder{log: &log}

	h := newHarness(t, harnessOpts{
		turns:      []faux.Turn{faux.ToolCall("echo", `{}`), faux.Text("done")},
		tools:      []tool.Definition{echoTool("echo")},
		middleware: []middleware.Middleware{rec},
	})

	if _, err := h.run(context.Background(), "go"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got := strings.Join(log, ",")
	// BeforeAgent/AfterAgent 各一次；BeforeModel/AfterModel 每轮各一次
	want := "BeforeAgent,BeforeModel,AfterModel,BeforeModel,AfterModel,AfterAgent"
	if got != want {
		t.Fatalf("stage order = %s, want %s", got, want)
	}
}

func TestLoop_AfterAgentRunsEvenWhenTheTurnFails(t *testing.T) {
	t.Parallel()

	var log []string
	rec := &stageRecorder{log: &log}

	h := newHarness(t, harnessOpts{
		turns:      []faux.Turn{faux.Fail(model.ErrProviderUnavailable)},
		middleware: []middleware.Middleware{rec},
	})

	if _, err := h.run(context.Background(), "go"); err == nil {
		t.Fatal("Run() error = nil, want the provider error")
	}

	if !strings.Contains(strings.Join(log, ","), "AfterAgent") {
		t.Fatalf("stage log = %v; AfterAgent must run even on failure (cleanup, telemetry)", log)
	}
}

// --- 步 9b：发布延后到切面之后 ---

// 护栏在 AfterModel 改写回复后，发布出去的必须是改写后的内容。
// 反过来（先发布再跑切面）等于把被拦截的文本留在用户屏幕上。
func TestLoop_PublishesAfterAfterModelStage(t *testing.T) {
	t.Parallel()

	pub := &recordingPublisher{}

	h := newHarness(t, harnessOpts{
		turns:      []faux.Turn{faux.Text("unsafe content")},
		middleware: []middleware.Middleware{rewriteReply{to: "safe fallback"}},
		publisher:  pub,
	})

	res, err := h.run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(pub.published) != 1 {
		t.Fatalf("published %d replies, want 1", len(pub.published))
	}
	if pub.published[0] != "safe fallback" {
		t.Fatalf("published %q, want the rewritten reply", pub.published[0])
	}
	if res.Output != "safe fallback" {
		t.Fatalf("Output = %q, want the rewritten reply", res.Output)
	}
}

// --- Directive ---

func TestLoop_DirectiveStopEndsTurnImmediately(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOpts{
		turns:      []faux.Turn{faux.ToolCall("echo", `{}`), faux.Text("never reached")},
		tools:      []tool.Definition{echoTool("echo")},
		middleware: []middleware.Middleware{stopAfterModel{}},
	})

	res, err := h.run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if res.Iterations != 1 {
		t.Fatalf("Iterations = %d, want 1; DirectiveStop must end the turn", res.Iterations)
	}
	if h.fx.CallCount() != 1 {
		t.Fatalf("model called %d times, want 1", h.fx.CallCount())
	}
}

func TestLoop_DirectiveContinueForcesAnotherIteration(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOpts{
		turns: []faux.Turn{faux.Text("first"), faux.Text("second")},
		middleware: []middleware.Middleware{
			&continueOnce{},
		},
	})

	res, err := h.run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if res.Iterations != 2 {
		t.Fatalf("Iterations = %d, want 2; DirectiveContinue must re-iterate", res.Iterations)
	}
	if res.Output != "second" {
		t.Fatalf("Output = %q, want the re-answered content", res.Output)
	}
}

// 澄清拦截：BeforeTool 返回 EndTurn，回合结束，工具未执行。
func TestLoop_ToolInterceptorEndTurnStopsTheTurn(t *testing.T) {
	t.Parallel()

	var ran atomic.Bool
	clarify := tool.Definition{
		Name: "ask_clarification", Group: "test",
		Parameters: json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, tool.Call) (*tool.Result, error) {
			ran.Store(true)
			return &tool.Result{Content: "should not run"}, nil
		},
	}

	h := newHarness(t, harnessOpts{
		turns:      []faux.Turn{faux.ToolCall("ask_clarification", `{}`), faux.Text("never")},
		tools:      []tool.Definition{clarify},
		middleware: []middleware.Middleware{clarificationInterceptor{}},
	})

	res, err := h.run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if ran.Load() {
		t.Fatal("the clarification tool executed; the interceptor must prevent it")
	}
	if res.Iterations != 1 {
		t.Fatalf("Iterations = %d, want 1", res.Iterations)
	}
}

// --- 步 11：停止门 ---

func TestLoop_StopGateReinjectsAndBoundsRetries(t *testing.T) {
	t.Parallel()

	turns := make([]faux.Turn, 6)
	for i := range turns {
		turns[i] = faux.Text(fmt.Sprintf("answer %d", i))
	}

	h := newHarness(t, harnessOpts{
		turns: turns,
		stopGate: stopGateFunc(func(context.Context, string, *middleware.State) (string, error) {
			return "tests still failing", nil // 永远拦截
		}),
		limits: loop.Limits{MaxIterations: 20, StopReinjectionLimit: 2},
	})

	_, err := h.run(context.Background(), "go")
	if err == nil || !strings.Contains(err.Error(), "tests still failing") {
		t.Fatalf("Run() error = %v, want the blocked-stop error", err)
	}
	// 首轮 + 2 次续跑 = 3 次采样
	if got := h.fx.CallCount(); got != 3 {
		t.Fatalf("model called %d times, want 3 (initial + StopReinjectionLimit)", got)
	}
}

func TestLoop_StopGatePassThrough(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOpts{
		turns: []faux.Turn{faux.Text("done")},
		stopGate: stopGateFunc(func(context.Context, string, *middleware.State) (string, error) {
			return "", nil // 放行
		}),
	})

	res, err := h.run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if res.Output != "done" {
		t.Fatalf("Output = %q", res.Output)
	}
}

func TestLoop_StopGateReinjectionAppearsInHistory(t *testing.T) {
	t.Parallel()

	var n atomic.Int64
	h := newHarness(t, harnessOpts{
		turns: []faux.Turn{faux.Text("first"), faux.Text("second")},
		stopGate: stopGateFunc(func(context.Context, string, *middleware.State) (string, error) {
			if n.Add(1) == 1 {
				return "you forgot the tests", nil
			}
			return "", nil
		}),
		limits: loop.Limits{MaxIterations: 10, StopReinjectionLimit: 3},
	})

	hist := message.NewHistory()
	if _, err := h.runner.Run(context.Background(), loop.Request{
		ThreadID: "t1", RunID: "r1", History: hist, Prompt: "go",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var found bool
	for _, m := range hist.All() {
		if m.Role == message.RoleUser && strings.Contains(m.Content, "you forgot the tests") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the blocked-stop reason was not reinjected into the transcript: %s", describe(hist.All()))
	}
}

// --- 采样错误 ---

func TestLoop_SamplerErrorAbortsTurn(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.Fail(model.ErrRateLimited)}})

	_, err := h.run(context.Background(), "go")
	if !model.IsRateLimited(err) {
		t.Fatalf("Run() error = %v, want the classified provider error to surface", err)
	}
}

// 工具的失败不杀回合：它变成 error 结果回灌，模型继续。
func TestLoop_ToolFailureDoesNotAbortTurn(t *testing.T) {
	t.Parallel()

	failing := tool.Definition{
		Name: "broken", Group: "test",
		Parameters: json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, tool.Call) (*tool.Result, error) {
			return nil, errors.New("disk full")
		},
	}

	h := newHarness(t, harnessOpts{
		turns: []faux.Turn{faux.ToolCall("broken", `{}`), faux.Text("recovered")},
		tools: []tool.Definition{failing},
	})

	res, err := h.run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run() error = %v; a tool failure must not abort the turn", err)
	}
	if res.Output != "recovered" {
		t.Fatalf("Output = %q", res.Output)
	}
}

// --- 用量 ---

func TestLoop_AccumulatesUsageAcrossIterations(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOpts{
		turns: []faux.Turn{
			faux.ToolCall("echo", `{}`).WithUsage(model.Usage{InputTokens: 10, OutputTokens: 3}),
			faux.Text("done").WithUsage(model.Usage{InputTokens: 5, OutputTokens: 2}),
		},
		tools: []tool.Definition{echoTool("echo")},
	})

	res, err := h.run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if res.Usage.InputTokens != 15 || res.Usage.OutputTokens != 5 {
		t.Fatalf("Usage = %+v, want input 15 / output 5", res.Usage)
	}
}

// --- 配置校验 ---

func TestNewRunner_RequiresSamplerAndExecutor(t *testing.T) {
	t.Parallel()

	chain, _ := middleware.NewChain(nil, middleware.ChainOptions{})
	registry := tool.NewRegistry()

	tests := []struct {
		name string
		cfg  loop.Config
		want string
	}{
		{
			name: "no sampler",
			cfg:  loop.Config{Registry: registry, Executor: tool.NewExecutor(registry, tool.ExecutorOptions{}), Chain: chain},
			want: "sampler",
		},
		{
			name: "no executor",
			cfg:  loop.Config{Sampler: loop.NewDirectSampler(faux.New()), Registry: registry, Chain: chain},
			want: "executor",
		},
		{
			name: "no registry",
			cfg:  loop.Config{Sampler: loop.NewDirectSampler(faux.New()), Executor: tool.NewExecutor(registry, tool.ExecutorOptions{}), Chain: chain},
			want: "registry",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := loop.NewRunner(tc.cfg)
			if err == nil {
				t.Fatalf("NewRunner() succeeded, want error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestRunner_RequiresHistory(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.Text("x")}})

	_, err := h.runner.Run(context.Background(), loop.Request{ThreadID: "t", RunID: "r", Prompt: "hi"})
	if err == nil || !strings.Contains(err.Error(), "history") {
		t.Fatalf("Run() error = %v, want it to require a history", err)
	}
}

func TestRunner_RequiresPromptOrExistingHistory(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.Text("x")}})

	_, err := h.runner.Run(context.Background(), loop.Request{
		ThreadID: "t", RunID: "r", History: message.NewHistory(),
	})
	if err == nil {
		t.Fatal("Run() with neither prompt nor history succeeded, want an error")
	}
}

// 续跑：转录里已有内容时无需 prompt。
func TestRunner_ResumesFromExistingHistory(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.Text("resumed")}})

	hist := message.NewHistory()
	hist.Load([]message.Message{{Role: message.RoleUser, Content: "earlier question"}})

	res, err := h.runner.Run(context.Background(), loop.Request{
		ThreadID: "t", RunID: "r", History: hist,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if res.Output != "resumed" {
		t.Fatalf("Output = %q", res.Output)
	}
}

// --- helpers ---

func describe(msgs []message.Message) string {
	parts := make([]string, len(msgs))
	for i, m := range msgs {
		switch {
		case len(m.ToolCalls) > 0:
			parts[i] = string(m.Role) + "+tools"
		default:
			parts[i] = string(m.Role)
		}
	}
	return "[" + strings.Join(parts, " ") + "]"
}

type compactorFunc func(context.Context, *message.History) (bool, error)

func (f compactorFunc) MaybeCompact(ctx context.Context, h *message.History) (bool, error) {
	return f(ctx, h)
}

type trimmerFunc func([]message.Message) []message.Message

func (f trimmerFunc) Trim(msgs []message.Message) []message.Message { return f(msgs) }

type resolverFunc func(*middleware.State) ([]string, []string)

func (f resolverFunc) Resolve(st *middleware.State) ([]string, []string) { return f(st) }

type stopGateFunc func(context.Context, string, *middleware.State) (string, error)

func (f stopGateFunc) Evaluate(ctx context.Context, stopReason string, st *middleware.State) (string, error) {
	return f(ctx, stopReason, st)
}

type recordingPublisher struct{ published []string }

func (p *recordingPublisher) PublishReply(_ context.Context, st *middleware.State, _ bool) {
	if st.ModelOutput != nil {
		p.published = append(p.published, st.ModelOutput.Message.Content)
	}
}

type stageRecorder struct{ log *[]string }

func (s *stageRecorder) Name() string { return "stageRecorder" }
func (s *stageRecorder) BeforeAgent(context.Context, *middleware.State) error {
	*s.log = append(*s.log, "BeforeAgent")
	return nil
}
func (s *stageRecorder) BeforeModel(context.Context, *middleware.State) error {
	*s.log = append(*s.log, "BeforeModel")
	return nil
}
func (s *stageRecorder) AfterModel(context.Context, *middleware.State) error {
	*s.log = append(*s.log, "AfterModel")
	return nil
}
func (s *stageRecorder) AfterAgent(context.Context, *middleware.State) error {
	*s.log = append(*s.log, "AfterAgent")
	return nil
}

// rewriteReply 模拟护栏：在 AfterModel 改写回复并同步转录。
type rewriteReply struct{ to string }

func (rewriteReply) Name() string { return "guardrailOutput" }
func (r rewriteReply) AfterModel(_ context.Context, st *middleware.State) error {
	st.ModelOutput.Message.Content = r.to
	st.History.ReplaceLastAssistant(st.ModelOutput.Message)
	return nil
}

type stopAfterModel struct{}

func (stopAfterModel) Name() string { return "stopper" }
func (stopAfterModel) AfterModel(_ context.Context, st *middleware.State) error {
	st.Directive = middleware.DirectiveStop
	return nil
}

type continueOnce struct{ fired bool }

func (c *continueOnce) Name() string { return "schemaValidate" }
func (c *continueOnce) AfterModel(_ context.Context, st *middleware.State) error {
	if !c.fired {
		c.fired = true
		st.Directive = middleware.DirectiveContinue
	}
	return nil
}

type clarificationInterceptor struct{}

func (clarificationInterceptor) Name() string { return middleware.TerminalName }
func (clarificationInterceptor) BeforeTool(_ context.Context, st *middleware.State) (tool.Decision, error) {
	if st.ToolCall != nil && st.ToolCall.Name == "ask_clarification" {
		return tool.Decision{EndTurn: true, Reason: "need input"}, nil
	}
	return tool.Decision{}, nil
}
