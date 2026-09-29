package loop_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

type recoveryEvents struct {
	mu     sync.Mutex
	events []runtime.Event
	fail   func(runtime.Event) error
}

func (e *recoveryEvents) publish(ctx context.Context, event runtime.Event) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if e.fail != nil {
		if err := e.fail(event); err != nil {
			return 0, err
		}
	}
	event.Seq = int64(len(e.events) + 1)
	e.events = append(e.events, event)
	return event.Seq, nil
}

func recoveryRun(t *testing.T, h *harness, ctx context.Context, events *recoveryEvents) (*message.History, error) {
	t.Helper()
	history := message.NewHistory()
	ctx = runtime.WithRunContext(ctx, runtime.RunContext{RunID: "r1", ThreadID: "t1", Publish: events.publish})
	_, err := h.runner.Run(ctx, loop.Request{RunID: "r1", ThreadID: "t1", History: history, Prompt: "run tools"})
	return history, err
}

func recoveredResults(t *testing.T, history *message.History, count int) []message.Message {
	t.Helper()
	var calls []message.ToolCall
	var results []message.Message
	for _, m := range history.All() {
		calls = append(calls, m.ToolCalls...)
		if m.Role == message.RoleTool {
			results = append(results, m)
		}
	}
	if len(calls) != count || len(results) != count {
		t.Fatalf("history has %d calls and %d results; want %d paired calls: %+v", len(calls), len(results), count, history.All())
	}
	for i, call := range calls {
		if results[i].ToolCallID != call.ID {
			t.Fatalf("result order differs from assistant order: %+v", results)
		}
	}
	return results
}

func assertRecoveryCode(t *testing.T, result message.Message, code string) {
	t.Helper()
	if !result.IsError || result.AdditionalKwargs["tool_recovery_code"] != code {
		t.Fatalf("recovery result=%+v, want %s", result, code)
	}
}

type failAfterModel struct{ err error }

func (failAfterModel) Name() string                                         { return "failAfterModel" }
func (f failAfterModel) AfterModel(context.Context, *lifecycle.State) error { return f.err }

func TestToolRecoveryBeforeDispatch(t *testing.T) {
	governanceErr := errors.New("governance unavailable")
	for _, tc := range []struct {
		name     string
		handlers []lifecycle.Handler
		limits   loop.Limits
		tools    []tool.Definition
		want     error
	}{
		{name: "after_model", handlers: []lifecycle.Handler{failAfterModel{governanceErr}}, tools: []tool.Definition{echoTool("a"), echoTool("b")}, want: governanceErr},
		{name: "unavailable_tool"},
		{name: "budget", limits: loop.Limits{MaxToolCalls: 1}, tools: []tool.Definition{echoTool("a"), echoTool("b")}, want: loop.ErrBudgetExhausted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.ToolCalls(faux.Call{Name: "a", Args: `{}`}, faux.Call{Name: "b", Args: `{}`})}, tools: tc.tools, auxiliary: tc.handlers, limits: tc.limits})
			events := &recoveryEvents{}
			history, err := recoveryRun(t, h, context.Background(), events)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
			for _, result := range recoveredResults(t, history, 2) {
				assertRecoveryCode(t, result, "TOOL_NOT_STARTED")
			}
			var results int
			for _, event := range events.events {
				if event.Type == runtime.EventToolStart {
					t.Fatal("tool started before rejection")
				}
				if event.Type == runtime.EventToolResult {
					results++
				}
			}
			if results != 2 {
				t.Fatalf("durable recovery results=%d", results)
			}
		})
	}
}

func TestToolRecoveryAfterResultCommitFailure(t *testing.T) {
	storeErr := errors.New("result append failed")
	var executed []string
	var defs []tool.Definition
	for _, name := range []string{"a", "b"} {
		d := echoTool(name)
		d.Metadata = tool.Metadata{}
		d.Handler = func(_ context.Context, c tool.Call) (*tool.Result, error) {
			executed = append(executed, c.Name)
			return &tool.Result{Content: "side effect happened"}, nil
		}
		defs = append(defs, d)
	}
	h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.ToolCalls(faux.Call{Name: "a", Args: `{}`}, faux.Call{Name: "b", Args: `{}`})}, tools: defs})
	failed := false
	events := &recoveryEvents{fail: func(e runtime.Event) error {
		if e.Type == runtime.EventToolResult && !failed {
			failed = true
			return storeErr
		}
		return nil
	}}
	history, err := recoveryRun(t, h, context.Background(), events)
	if !errors.Is(err, storeErr) {
		t.Fatalf("error=%v", err)
	}
	results := recoveredResults(t, history, 2)
	assertRecoveryCode(t, results[0], "TOOL_OUTCOME_UNKNOWN")
	assertRecoveryCode(t, results[1], "TOOL_NOT_STARTED")
	if !strings.Contains(results[0].Content, "Do not retry blindly") {
		t.Fatalf("missing side-effect warning: %q", results[0].Content)
	}
	if !reflect.DeepEqual(executed, []string{"a"}) {
		t.Fatalf("executed=%v", executed)
	}
}

type failAfterTool struct{ err error }

func (failAfterTool) Name() string                                        { return "failAfterTool" }
func (f failAfterTool) AfterTool(context.Context, *lifecycle.State) error { return f.err }

func TestToolRecoveryPreservesCommittedResultAfterGovernanceFailure(t *testing.T) {
	cause := errors.New("after-tool governance failed")
	d := echoTool("a")
	d.Metadata = tool.Metadata{}
	d.Handler = func(context.Context, tool.Call) (*tool.Result, error) {
		return &tool.Result{Content: "already completed", ContentBlocks: []message.ContentBlock{{Type: "text", Text: "detail"}}, AdditionalKwargs: map[string]any{"custom": "value"}}, nil
	}
	h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.ToolCalls(faux.Call{Name: "a", Args: `{}`}, faux.Call{Name: "b", Args: `{}`})}, tools: []tool.Definition{d, echoTool("b")}, auxiliary: []lifecycle.Handler{failAfterTool{cause}}})
	events := &recoveryEvents{}
	history, err := recoveryRun(t, h, context.Background(), events)
	if !errors.Is(err, cause) {
		t.Fatalf("error=%v", err)
	}
	results := recoveredResults(t, history, 2)
	if results[0].Content != "already completed" || len(results[0].ContentBlocks) != 1 || results[0].AdditionalKwargs["custom"] != "value" {
		t.Fatalf("committed result lost: %+v", results[0])
	}
	assertRecoveryCode(t, results[1], "TOOL_NOT_STARTED")
	var count int
	for _, e := range events.events {
		if e.Type == runtime.EventToolResult {
			var p runtime.ToolResult
			if err := json.Unmarshal(e.Data, &p); err != nil {
				t.Fatal(err)
			}
			if p.ToolCallID == results[0].ToolCallID {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatalf("first result committed %d times", count)
	}
}

func TestToolRecoveryRetainsOriginalAndRecoveryFailure(t *testing.T) {
	cause := errors.New("governance failed")
	storeErr := errors.New("recovery append failed")
	h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.ToolCall("a", `{}`)}, tools: []tool.Definition{echoTool("a")}, auxiliary: []lifecycle.Handler{failAfterModel{cause}}})
	events := &recoveryEvents{fail: func(e runtime.Event) error {
		if e.Type == runtime.EventToolResult {
			return storeErr
		}
		return nil
	}}
	history, err := recoveryRun(t, h, context.Background(), events)
	if !errors.Is(err, cause) || !errors.Is(err, storeErr) {
		t.Fatalf("error lost original/recovery failure: %v", err)
	}
	for _, m := range history.All() {
		if m.Role == message.RoleTool {
			t.Fatal("uncommitted result entered history")
		}
	}
}

func TestToolRecoveryOnCancellationUsesCleanupContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := echoTool("a")
	d.Metadata = tool.Metadata{}
	d.Handler = func(context.Context, tool.Call) (*tool.Result, error) { cancel(); return nil, context.Canceled }
	h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.ToolCalls(faux.Call{Name: "a", Args: `{}`}, faux.Call{Name: "b", Args: `{}`})}, tools: []tool.Definition{d, echoTool("b")}})
	history, err := recoveryRun(t, h, ctx, &recoveryEvents{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	results := recoveredResults(t, history, 2)
	assertRecoveryCode(t, results[0], "TOOL_OUTCOME_UNKNOWN")
	assertRecoveryCode(t, results[1], "TOOL_NOT_STARTED")
}

type failBeforeTool struct{ err error }

func (failBeforeTool) Name() string { return "failBeforeTool" }
func (f failBeforeTool) BeforeTool(context.Context, *lifecycle.State) (tool.Decision, error) {
	return tool.Decision{}, f.err
}

func TestToolRecoveryDoesNotHideOriginalResultWriteFailure(t *testing.T) {
	cause := errors.New("governance failure")
	storeErr := errors.New("first result write failed")
	for _, handler := range []lifecycle.Handler{failBeforeTool{cause}, failAfterTool{cause}} {
		t.Run(handler.Name(), func(t *testing.T) {
			h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.ToolCall("a", `{}`)}, tools: []tool.Definition{echoTool("a")}, auxiliary: []lifecycle.Handler{handler}})
			failed := false
			events := &recoveryEvents{fail: func(e runtime.Event) error {
				if e.Type == runtime.EventToolResult && !failed {
					failed = true
					return storeErr
				}
				return nil
			}}
			history, err := recoveryRun(t, h, context.Background(), events)
			if !errors.Is(err, cause) || !errors.Is(err, storeErr) {
				t.Fatalf("original result commit failure was hidden: %v", err)
			}
			assertRecoveryCode(t, recoveredResults(t, history, 1)[0], "TOOL_OUTCOME_UNKNOWN")
		})
	}
}

func TestToolRecoveryWaitsForConcurrentDispatches(t *testing.T) {
	started := make(chan struct{})
	blocked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var drained atomic.Bool
	a, b, c := echoTool("a"), echoTool("b"), echoTool("c")
	a.Handler = func(ctx context.Context, _ tool.Call) (*tool.Result, error) {
		select {
		case <-started:
			return nil, context.Canceled
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	b.Handler = func(ctx context.Context, _ tool.Call) (*tool.Result, error) {
		close(started)
		<-ctx.Done()
		close(blocked)
		<-release
		drained.Store(true)
		return &tool.Result{Content: "late known result"}, nil
	}
	c.Metadata = tool.Metadata{} // A separate serial segment must never be dispatched.
	c.Handler = func(context.Context, tool.Call) (*tool.Result, error) {
		t.Error("started a later segment after failure")
		return nil, nil
	}
	h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.ToolCalls(faux.Call{Name: "a", Args: `{}`}, faux.Call{Name: "b", Args: `{}`}, faux.Call{Name: "c", Args: `{}`})}, tools: []tool.Definition{a, b, c}})
	events := &recoveryEvents{fail: func(e runtime.Event) error {
		if e.Type == runtime.EventToolResult && strings.Contains(string(e.Data), "TOOL_NOT_STARTED") && !drained.Load() {
			t.Error("recovery ran while a dispatch was still active")
		}
		return nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var history *message.History
	var runErr error
	done := make(chan struct{})
	go func() { history, runErr = recoveryRun(t, h, ctx, events); close(done) }()
	select {
	case <-blocked:
	case <-ctx.Done():
		t.Fatal("sibling dispatch was not cancelled")
	}
	select {
	case <-done:
		t.Fatal("loop returned while a dispatch was still active")
	default:
	}
	unblock()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("loop did not drain")
	}
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("error=%v", runErr)
	}
	results := recoveredResults(t, history, 3)
	assertRecoveryCode(t, results[1], "TOOL_OUTCOME_UNKNOWN")
	assertRecoveryCode(t, results[2], "TOOL_NOT_STARTED")
}

func TestToolRecoveryBalancesEarlySuccessfulStop(t *testing.T) {
	h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.ToolCalls(faux.Call{Name: "a", Args: `{}`}, faux.Call{Name: "b", Args: `{}`})}, tools: []tool.Definition{echoTool("a"), echoTool("b")}, auxiliary: []lifecycle.Handler{stopAfterModel{}}})
	history, err := recoveryRun(t, h, context.Background(), &recoveryEvents{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range recoveredResults(t, history, 2) {
		assertRecoveryCode(t, m, "TOOL_NOT_STARTED")
	}
}

func TestToolRecoveryBalancesContinueBeforeNextModelInput(t *testing.T) {
	h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.ToolCall("a", `{}`), faux.Text("continued")}, tools: []tool.Definition{echoTool("a")}, auxiliary: []lifecycle.Handler{&continueOnce{}}})
	history, err := recoveryRun(t, h, context.Background(), &recoveryEvents{})
	if err != nil {
		t.Fatal(err)
	}
	assertRecoveryCode(t, recoveredResults(t, history, 1)[0], "TOOL_NOT_STARTED")
	requests := h.fx.Requests()
	if len(requests) != 2 || len(requests[1].Messages) != 3 || requests[1].Messages[2].Role != message.RoleTool {
		t.Fatalf("next model input is unbalanced: %+v", requests)
	}
}

type removeToolCalls struct{}

func (removeToolCalls) Name() string { return "removeToolCalls" }
func (removeToolCalls) AfterModel(_ context.Context, st *lifecycle.State) error {
	st.ModelOutput.Message.ToolCalls = nil
	st.ModelOutput.Message.Content = "blocked"
	st.History.ReplaceLastAssistant(st.ModelOutput.Message)
	st.Directive = lifecycle.DirectiveStop
	return nil
}

func TestToolRecoveryDoesNotRestoreCallsRemovedByGovernance(t *testing.T) {
	h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.ToolCall("a", `{}`)}, tools: []tool.Definition{echoTool("a")}, auxiliary: []lifecycle.Handler{removeToolCalls{}}})
	events := &recoveryEvents{}
	history, err := recoveryRun(t, h, context.Background(), events)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range history.All() {
		if m.Role == message.RoleTool || len(m.ToolCalls) > 0 {
			t.Fatalf("removed call resurrected: %+v", history.All())
		}
	}
	for _, event := range events.events {
		if event.Type == runtime.EventToolResult {
			t.Fatal("produced an orphan recovery result")
		}
	}
}

type untrackedExecutor struct{ err error }

func (e untrackedExecutor) Run(context.Context, []tool.Call, tool.Interceptor) ([]tool.Outcome, error) {
	return nil, e.err
}
func (untrackedExecutor) EndTurnRequested() bool { return false }

func TestToolRecoveryTreatsUntrackedCustomDispatchAsUnknown(t *testing.T) {
	cause := errors.New("custom dispatch failed")
	h := newHarness(t, harnessOpts{turns: []faux.Turn{faux.ToolCall("a", `{}`)}, tools: []tool.Definition{echoTool("a")}})
	chain, err := lifecycle.NewDispatcher(nil, lifecycle.DispatcherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h.runner, err = loop.NewRunner(loop.Config{Sampler: loop.NewDirectSampler(h.fx), Registry: h.registry, Executor: untrackedExecutor{cause}, Lifecycle: chain})
	if err != nil {
		t.Fatal(err)
	}
	history, err := recoveryRun(t, h, context.Background(), &recoveryEvents{})
	if !errors.Is(err, cause) {
		t.Fatalf("error=%v", err)
	}
	assertRecoveryCode(t, recoveredResults(t, history, 1)[0], "TOOL_OUTCOME_UNKNOWN")
}
