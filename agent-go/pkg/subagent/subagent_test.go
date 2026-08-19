package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/harness"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/hooks"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func testRunContext(ctx context.Context, run runtime.RunContext) context.Context {
	if run.Budget == nil {
		run.Budget = runtime.NewBudgetLedger(runtime.BudgetAmount{})
	}
	if run.StateMachine == nil {
		state, err := runtime.NewRunStateMachine(run.RunID, run.ThreadID)
		if err != nil {
			panic(err)
		}
		if err := state.Start(); err != nil {
			panic(err)
		}
		run.StateMachine = state
	}
	if !run.Capabilities.Initialized() {
		registry := capability.NewRegistry()
		for _, name := range []string{"explore", "verification", "worker"} {
			definition := Definition{Name: name, Description: name}
			if err := registry.Register(capability.Entry{
				Definition: capability.Definition{Name: "agent." + name, Kind: capability.KindAgent, Scope: capability.ScopeRun},
				Resolver:   func(context.Context, capability.ResolveRequest) (any, error) { return definition, nil },
			}); err != nil {
				panic(err)
			}
		}
		view, err := capability.NewView(registry.Snapshot(), nil)
		if err != nil {
			panic(err)
		}
		run.Capabilities = view
	}
	return runtime.WithRunContext(ctx, run)
}

// Keep this test package independent from lifecycle handlers. The production
// swarm package imports subagent, so importing handlers here would create a
// test-only cycle once swarm lifecycle integration is enabled.
type testTokenUsage struct{}

func (testTokenUsage) Name() string { return "testTokenUsage" }
func (testTokenUsage) AfterModel(ctx context.Context, st *lifecycle.State) error {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Journal == nil || st.ModelOutput == nil {
		return nil
	}
	run.Journal.Observe(runtime.Entry{Bucket: runtime.BucketLead, Source: "lead", CallID: st.ModelOutput.CallID, Usage: st.ModelOutput.Usage})
	return nil
}

type failingAfterModel struct{}

func (failingAfterModel) Name() string { return "failingAfterModel" }
func (failingAfterModel) AfterModel(context.Context, *lifecycle.State) error {
	return errors.New("after-model failed")
}

type captureTrustedRunID struct {
	runID       *string
	parentRunID *string
	eventRunID  *string
}

func (m captureTrustedRunID) Name() string { return "captureTrustedRunID" }
func (m captureTrustedRunID) BeforeAgent(ctx context.Context, _ *lifecycle.State) error {
	run, _ := runtime.RunContextFrom(ctx)
	*m.runID = run.RunID
	*m.parentRunID = run.ParentRunID
	*m.eventRunID = run.EventStreamRunID()
	return nil
}

func TestDispatchUsesIndependentHistoryAndRestrictsTools(t *testing.T) {
	var gotAllowed []string
	factory := func(_ Definition, allowed []string) (*loop.Runner, error) {
		gotAllowed = append([]string(nil), allowed...)
		m := faux.New(faux.Text("child done"))
		h, err := harness.New(harness.Options{Model: m})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}
	m := NewManager(factory, nil, 0)
	_ = m.Register(Definition{Name: "explore", Description: "explore", AllowedTools: []string{"read", "write"}})
	ctx := testRunContext(context.Background(), runtime.RunContext{RunID: "r", ThreadID: "t", AllowedTools: []string{"read"}})
	res, err := m.Dispatch(ctx, DispatchRequest{SubagentType: "explore", Prompt: "inspect", RestrictTools: []string{"read"}, ToolCallID: "tc"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "child done" || len(gotAllowed) != 1 || gotAllowed[0] != "read" {
		t.Fatalf("result=%#v allowed=%v", res, gotAllowed)
	}
	if _, err := m.Dispatch(ctx, DispatchRequest{SubagentType: "explore", Prompt: "x", RestrictTools: []string{"write"}}); err == nil {
		t.Fatal("widening restriction accepted")
	}
}

func TestDispatchReleasesPreparedRunnerOnceAfterSuccess(t *testing.T) {
	var releases atomic.Int32
	manager := NewManagerWithPreparedFactory(func(_ Definition, _ []string) (PreparedRunner, error) {
		h, err := harness.New(harness.Options{Model: faux.New(faux.Text("done"))})
		if err != nil {
			return PreparedRunner{}, err
		}
		return PreparedRunner{
			Runner: h.Runner(),
			Release: func() error {
				releases.Add(1)
				return h.Close()
			},
		}, nil
	}, nil, 0)
	if err := manager.Register(Definition{Name: "worker", Description: "worker"}); err != nil {
		t.Fatal(err)
	}
	ctx := testRunContext(context.Background(), runtime.RunContext{RunID: "run", ThreadID: "thread"})

	result, err := manager.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "work", ToolCallID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != message.SubagentCompleted {
		t.Fatalf("Dispatch() status = %q", result.Status)
	}
	if got := releases.Load(); got != 1 {
		t.Fatalf("release calls = %d, want 1", got)
	}
}

func TestDispatchReleasesPreparedRunnerWhenStartHookDenies(t *testing.T) {
	var releases atomic.Int32
	manager := NewManagerWithPreparedFactory(func(_ Definition, _ []string) (PreparedRunner, error) {
		h, err := harness.New(harness.Options{Model: faux.New(faux.Text("must not run"))})
		if err != nil {
			return PreparedRunner{}, err
		}
		return PreparedRunner{Runner: h.Runner(), Release: func() error {
			releases.Add(1)
			return h.Close()
		}}, nil
	}, nil, 0)
	runner, err := hooks.NewRunner([]hooks.Hook{&hooks.FuncHook{
		HookName: "policy",
		OnEvents: []hooks.Event{hooks.EventSubagentStart},
		Fn: func(context.Context, hooks.Payload) (hooks.Outcome, error) {
			return hooks.Outcome{Deny: true, Message: "delegation disabled"}, nil
		},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetHookRunner(runner)
	if err := manager.Register(Definition{Name: "worker", Description: "worker"}); err != nil {
		t.Fatal(err)
	}
	ctx := testRunContext(context.Background(), runtime.RunContext{RunID: "run", ThreadID: "thread"})

	_, err = manager.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "work", ToolCallID: "task-1"})
	if err == nil || !strings.Contains(err.Error(), "delegation disabled") {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if got := releases.Load(); got != 1 {
		t.Fatalf("release calls = %d, want 1", got)
	}
}

func TestDispatchReportsPreparedRunnerReleaseAsCoordinationError(t *testing.T) {
	manager := NewManagerWithPreparedFactory(func(_ Definition, _ []string) (PreparedRunner, error) {
		h, err := harness.New(harness.Options{Model: faux.New(faux.Text("done"))})
		if err != nil {
			return PreparedRunner{}, err
		}
		return PreparedRunner{Runner: h.Runner(), Release: func() error {
			_ = h.Close()
			return errors.New("close failed")
		}}, nil
	}, nil, 0)
	if err := manager.Register(Definition{Name: "worker", Description: "worker"}); err != nil {
		t.Fatal(err)
	}
	ctx := testRunContext(context.Background(), runtime.RunContext{RunID: "run", ThreadID: "thread"})

	result, err := manager.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "work", ToolCallID: "task-1"})
	if err == nil || !strings.Contains(err.Error(), "close failed") {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if result.Status != message.SubagentCompleted {
		t.Fatalf("Dispatch() status = %q, want completed", result.Status)
	}
	if !strings.Contains(result.CoordinationError, "close failed") {
		t.Fatalf("Dispatch() coordination error = %q", result.CoordinationError)
	}
}

func TestDispatchReleasesPreparedRunnerBeforePersistingEarlyTerminalFailure(t *testing.T) {
	store := &flakyTaskStore{failAt: map[int]error{1: errors.New("running write failed")}}
	manager := NewManagerWithPreparedFactory(func(_ Definition, _ []string) (PreparedRunner, error) {
		h, err := harness.New(harness.Options{Model: faux.New(faux.Text("must not run"))})
		if err != nil {
			return PreparedRunner{}, err
		}
		return PreparedRunner{Runner: h.Runner(), Release: func() error {
			_ = h.Close()
			return errors.New("close failed")
		}}, nil
	}, store, 0)
	if err := manager.Register(Definition{Name: "worker", Description: "worker"}); err != nil {
		t.Fatal(err)
	}
	ctx := testRunContext(context.Background(), runtime.RunContext{RunID: "run", ThreadID: "thread"})

	result, err := manager.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "work", ToolCallID: "task-1"})
	if err == nil {
		t.Fatal("Dispatch() returned nil error")
	}
	for _, want := range []string{"running write failed", "close failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Dispatch() error = %v, want %q", err, want)
		}
	}
	if !strings.Contains(result.CoordinationError, "close failed") {
		t.Fatalf("Dispatch() coordination error = %q", result.CoordinationError)
	}
	if len(store.puts) < 2 || !strings.Contains(store.puts[1].CoordinationError, "close failed") {
		t.Fatalf("stored terminal results = %#v", store.puts)
	}
}

func TestDispatchRejectsRunContextWithoutBudget(t *testing.T) {
	m := NewManager(nil, nil, 0)
	if err := m.Register(Definition{Name: "worker", Description: "worker"}); err != nil {
		t.Fatal(err)
	}
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "run", ThreadID: "thread"})
	_, err := m.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "work"})
	if err == nil || !strings.Contains(err.Error(), "requires a run context with budget") {
		t.Fatalf("Dispatch() error = %v", err)
	}
}

func TestDispatchRejectsAgentExcludedFromCapabilityView(t *testing.T) {
	manager := NewManager(nil, nil, 0)
	if err := manager.Register(Definition{Name: "worker", Description: "worker"}); err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	if err := registry.Register(capability.Entry{
		Definition: capability.Definition{Name: "agent.other", Kind: capability.KindAgent, Scope: capability.ScopeRun},
		Resolver: func(context.Context, capability.ResolveRequest) (any, error) {
			return Definition{Name: "other", Description: "other"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	view, err := capability.NewView(registry.Snapshot(), []string{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := testRunContext(context.Background(), runtime.RunContext{RunID: "run", ThreadID: "thread", Capabilities: view})
	_, err = manager.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "work"})
	if err == nil || !strings.Contains(err.Error(), `"agent.worker" is not allowed`) {
		t.Fatalf("Dispatch() error = %v", err)
	}
}

func TestDispatchRejectsChildBeyondTrustedRecursionDepth(t *testing.T) {
	var factoryCalls atomic.Int32
	manager := NewManager(func(Definition, []string) (*loop.Runner, error) {
		factoryCalls.Add(1)
		return nil, errors.New("factory must not be called")
	}, nil, 0)
	if err := manager.Register(Definition{Name: "worker", Description: "worker"}); err != nil {
		t.Fatal(err)
	}
	ctx := testRunContext(context.Background(), runtime.RunContext{
		RunID:             "child",
		ThreadID:          "thread",
		AgentDepth:        1,
		MaxRecursionDepth: 1,
	})

	_, err := manager.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "recurse"})
	if err == nil || !strings.Contains(err.Error(), "maximum recursion depth 1 reached") {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if got := factoryCalls.Load(); got != 0 {
		t.Fatalf("runner factory called %d times", got)
	}
}

func TestCloneRunContextStartsChildOutsidePlanMode(t *testing.T) {
	parent := runtime.RunContext{
		Values: map[string]any{"is_plan_mode": true},
		Budget: runtime.NewBudgetLedger(runtime.BudgetAmount{}),
	}
	child, err := cloneRunContext(parent)
	if err != nil {
		t.Fatal(err)
	}
	if active, _ := child.Values["is_plan_mode"].(bool); active {
		t.Fatal("child inherited parent plan mode")
	}
	if active, _ := parent.Values["is_plan_mode"].(bool); !active {
		t.Fatal("child mutation changed parent values")
	}
}

func TestDispatchUsesOneTrustedChildRunIDAcrossContextAndLoop(t *testing.T) {
	var contextRunID, parentRunID, eventRunID string
	factory := func(_ Definition, _ []string) (*loop.Runner, error) {
		h, err := harness.New(harness.Options{
			Model:             faux.New(faux.Text("done")),
			LifecycleHandlers: []lifecycle.Handler{captureTrustedRunID{runID: &contextRunID, parentRunID: &parentRunID, eventRunID: &eventRunID}},
		})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}
	manager := NewManager(factory, nil, 0)
	if err := manager.Register(Definition{Name: "worker", Description: "worker"}); err != nil {
		t.Fatal(err)
	}
	ctx := testRunContext(context.Background(), runtime.RunContext{RunID: "parent:coordinator", ParentRunID: "parent", EventRunID: "parent", ThreadID: "thread"})
	if _, err := manager.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "work", ToolCallID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	if contextRunID != "parent:coordinator:task-1" {
		t.Fatalf("trusted child RunContext.RunID = %q, want parent:coordinator:task-1", contextRunID)
	}
	if parentRunID != "parent:coordinator" {
		t.Fatalf("trusted child RunContext.ParentRunID = %q, want parent:coordinator", parentRunID)
	}
	if eventRunID != "parent" {
		t.Fatalf("trusted child RunContext.EventStreamRunID() = %q, want parent", eventRunID)
	}
}

func TestPublishRoutesNestedSubagentEventsToRootRun(t *testing.T) {
	t.Parallel()

	var got runtime.Event
	run := runtime.RunContext{
		RunID:      "root:coordinator",
		EventRunID: "root",
		ThreadID:   "thread",
		Publish: func(_ context.Context, event runtime.Event) (int64, error) {
			got = event
			return 1, nil
		},
	}
	publish(context.Background(), run, runtime.EventSubagentStart, map[string]string{"task_id": "task-1"})
	if got.RunID != "root" || got.ThreadID != "thread" || got.Type != runtime.EventSubagentStart {
		t.Fatalf("event = %#v", got)
	}
}

func TestDispatchAttributesUsageOnlyToSubagentBucket(t *testing.T) {
	factory := func(_ Definition, _ []string) (*loop.Runner, error) {
		m := faux.New(faux.Text("done").WithUsage(model.Usage{InputTokens: 3, OutputTokens: 2}))
		h, err := harness.New(harness.Options{Model: m, LifecycleHandlers: []lifecycle.Handler{testTokenUsage{}}})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}
	manager := NewManager(factory, nil, 0)
	_ = manager.Register(Definition{Name: "worker", Description: "worker"})
	journal := runtime.NewJournal(nil)
	ctx := testRunContext(context.Background(), runtime.RunContext{RunID: "parent", ThreadID: "thread", Journal: journal})
	if _, err := manager.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "work"}); err != nil {
		t.Fatal(err)
	}
	totals := journal.Totals()
	if totals.SubagentTokens != 5 || totals.LeadTokens != 0 || totals.InputTokens != 3 || totals.OutputTokens != 2 {
		t.Fatalf("totals = %#v", totals)
	}
}

func TestDispatchDeduplicatesReplayedTaskUsage(t *testing.T) {
	factory := func(_ Definition, _ []string) (*loop.Runner, error) {
		m := faux.New(faux.Text("done").WithUsage(model.Usage{InputTokens: 3, OutputTokens: 2}))
		h, err := harness.New(harness.Options{Model: m, LifecycleHandlers: []lifecycle.Handler{testTokenUsage{}}})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}
	manager := NewManager(factory, nil, 0)
	if err := manager.Register(Definition{Name: "worker", Description: "worker"}); err != nil {
		t.Fatal(err)
	}
	journal := runtime.NewJournal(nil)
	ctx := testRunContext(context.Background(), runtime.RunContext{RunID: "parent", ThreadID: "thread", Journal: journal})
	request := DispatchRequest{SubagentType: "worker", Prompt: "work", ToolCallID: "task-1"}

	for range 2 {
		if _, err := manager.Dispatch(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	if totals := journal.Totals(); totals.SubagentTokens != 5 || totals.LLMCalls != 1 {
		t.Fatalf("replayed task totals = %#v, want one child charge", totals)
	}

	request.ToolCallID = "task-2"
	if _, err := manager.Dispatch(ctx, request); err != nil {
		t.Fatal(err)
	}
	if totals := journal.Totals(); totals.SubagentTokens != 10 || totals.LLMCalls != 2 {
		t.Fatalf("distinct task totals = %#v, want two child charges", totals)
	}
}

func TestDispatchMergesUsageWhenChildFailsAfterSampling(t *testing.T) {
	factory := func(_ Definition, _ []string) (*loop.Runner, error) {
		m := faux.New(faux.Text("partial").WithUsage(model.Usage{InputTokens: 4, OutputTokens: 1}))
		h, err := harness.New(harness.Options{Model: m, LifecycleHandlers: []lifecycle.Handler{failingAfterModel{}, testTokenUsage{}}})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}
	manager := NewManager(factory, nil, 0)
	if err := manager.Register(Definition{Name: "worker", Description: "worker"}); err != nil {
		t.Fatal(err)
	}
	journal := runtime.NewJournal(nil)
	ctx := testRunContext(context.Background(), runtime.RunContext{RunID: "parent", ThreadID: "thread", Journal: journal})
	result, err := manager.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "work"})
	if err == nil || result.Status != message.SubagentFailed {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	if totals := journal.Totals(); totals.SubagentTokens != 5 || totals.LLMCalls != 1 {
		t.Fatalf("parent totals = %#v", totals)
	}
}

func TestManagerLimitsConcurrentDispatch(t *testing.T) {
	release := make(chan struct{})
	var factories atomic.Int32
	factory := func(_ Definition, _ []string) (*loop.Runner, error) {
		factories.Add(1)
		<-release
		h, err := harness.New(harness.Options{Model: faux.New(faux.Text("done"))})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}
	m := NewManager(factory, nil, 0, 1)
	if err := m.Register(Definition{Name: "worker", Description: "worker"}); err != nil {
		t.Fatal(err)
	}
	ctx := testRunContext(context.Background(), runtime.RunContext{RunID: "r", ThreadID: "t"})
	done := make(chan error, 2)
	go func() {
		_, err := m.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "one"})
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for factories.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	go func() {
		_, err := m.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "two"})
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if got := factories.Load(); got != 1 {
		t.Fatalf("factories started concurrently = %d", got)
	}
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

type blockingModel struct{}

func (blockingModel) Info() model.Info { return model.Info{Name: "blocking", SupportsTools: true} }
func (blockingModel) Complete(ctx context.Context, _ model.Request) (*model.Response, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (blockingModel) Stream(context.Context, model.Request) (model.StreamReader, error) {
	return nil, nil
}

type panickingModel struct{}

func (panickingModel) Info() model.Info { return model.Info{Name: "panicking", SupportsTools: true} }
func (panickingModel) Complete(context.Context, model.Request) (*model.Response, error) {
	panic("model adapter panic")
}
func (panickingModel) Stream(context.Context, model.Request) (model.StreamReader, error) {
	return nil, nil
}

func TestDispatchAppliesTrustedTimeoutPolicy(t *testing.T) {
	var releases atomic.Int32
	factory := func(_ Definition, _ []string) (PreparedRunner, error) {
		h, err := harness.New(harness.Options{Model: blockingModel{}})
		if err != nil {
			return PreparedRunner{}, err
		}
		return PreparedRunner{Runner: h.Runner(), Release: func() error {
			releases.Add(1)
			return h.Close()
		}}, nil
	}
	manager := NewManagerWithPreparedFactory(factory, nil, 0)
	manager.SetTimeoutResolver(func(parent runtime.RunContext, _ DispatchRequest) time.Duration {
		if parent.ThreadID != "thread" {
			t.Fatalf("parent = %#v", parent)
		}
		return 10 * time.Millisecond
	})
	if err := manager.Register(Definition{Name: "worker", Description: "worker"}); err != nil {
		t.Fatal(err)
	}
	ctx := testRunContext(context.Background(), runtime.RunContext{RunID: "parent", ThreadID: "thread"})
	result, err := manager.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "wait"})
	if err == nil || result.Status != message.SubagentTimedOut {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	if got := releases.Load(); got != 1 {
		t.Fatalf("release calls = %d, want 1", got)
	}
}

func TestBuiltinsAndTaskTool(t *testing.T) {
	definitions := Builtins()
	if len(definitions) != 5 {
		t.Fatalf("builtins=%d", len(definitions))
	}
	wantTurns := map[string]int{
		"general-purpose": 50,
		"explore":         30,
		"plan":            20,
		"bash":            30,
		"verification":    40,
	}
	for _, definition := range definitions {
		if definition.MaxTurns != wantTurns[definition.Name] {
			t.Errorf("%s max turns = %d, want %d", definition.Name, definition.MaxTurns, wantTurns[definition.Name])
		}
		if strings.TrimSpace(definition.SystemPrompt) == "" {
			t.Errorf("%s has no profile prompt", definition.Name)
		}
	}
	verification := definitions[4]
	if verification.Name != "verification" || len(verification.AllowedTools) != 0 {
		t.Fatalf("verification profile = %#v, want trusted parent tool inheritance", verification)
	}
	for _, index := range []int{1, 2} {
		for _, toolName := range []string{"glob", "grep", "ls", "read_file"} {
			if !containsString(definitions[index].AllowedTools, toolName) {
				t.Errorf("%s tools = %v, missing %q", definitions[index].Name, definitions[index].AllowedTools, toolName)
			}
		}
	}
	bash := definitions[3]
	for _, toolName := range []string{"bash", "write_file", "str_replace"} {
		if !containsString(bash.AllowedTools, toolName) {
			t.Errorf("bash tools = %v, missing %q", bash.AllowedTools, toolName)
		}
	}
	m := NewManager(nil, nil, 0)
	if d := m.TaskTool(); d.Name != "task" {
		t.Fatalf("tool=%s", d.Name)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestTaskToolAcceptsCanonicalContractAndStampsSuccess(t *testing.T) {
	var got Definition
	factory := func(def Definition, _ []string) (*loop.Runner, error) {
		got = def
		h, err := harness.New(harness.Options{Model: faux.New(faux.Text("child output"))})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}
	m := NewManager(factory, nil, 0)
	if err := m.Register(Definition{Name: "explore", Description: "read only", MaxTurns: 15}); err != nil {
		t.Fatal(err)
	}
	result, err := m.TaskTool().Handler(workerContext(), tool.Call{
		ID:   "call-1",
		Name: "task",
		Args: json.RawMessage(`{"description":"Inspect repository","prompt":"find the issue","subagent_type":"explore","max_turns":7}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !strings.HasPrefix(result.Content, "Task Succeeded. Result:") {
		t.Fatalf("result = %#v", result)
	}
	if got.Name != "explore" || got.MaxTurns != 7 {
		t.Fatalf("definition override = %#v", got)
	}
	if status := result.AdditionalKwargs[message.SubagentStatusKey]; status != message.SubagentCompleted {
		t.Fatalf("status = %v, want %q", status, message.SubagentCompleted)
	}
}

func TestTaskToolCannotRaiseTrustedProfileTurnLimit(t *testing.T) {
	var factoryCalls int
	m := NewManager(func(_ Definition, _ []string) (*loop.Runner, error) {
		factoryCalls++
		return nil, errors.New("factory must not run")
	}, nil, 0)
	if err := m.Register(Definition{Name: "explore", Description: "read only", MaxTurns: 15}); err != nil {
		t.Fatal(err)
	}
	result, err := m.TaskTool().Handler(context.Background(), tool.Call{
		ID:   "call-over-limit",
		Name: "task",
		Args: json.RawMessage(`{"description":"Inspect repository","prompt":"find the issue","subagent_type":"explore","max_turns":16}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content, "exceeds the trusted limit 15") {
		t.Fatalf("result = %#v", result)
	}
	if factoryCalls != 0 {
		t.Fatalf("factory calls = %d", factoryCalls)
	}
}

func TestTaskToolRejectsRemovedAgentAlias(t *testing.T) {
	m := NewManager(nil, nil, 0)
	result, err := m.TaskTool().Handler(context.Background(), tool.Call{
		ID:   "call-removed-alias",
		Name: "task",
		Args: json.RawMessage(`{"agent":"removed","description":"old caller","prompt":"old caller"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content, "unknown field") {
		t.Fatalf("removed alias result = %#v", result)
	}
}

func TestTaskToolFailureCarriesStructuredError(t *testing.T) {
	m := NewManager(nil, nil, 0)
	events := make(chan runtime.Event, 2)
	ctx := testRunContext(context.Background(), runtime.RunContext{
		RunID: "parent", ThreadID: "thread",
		Publish: func(_ context.Context, event runtime.Event) (int64, error) {
			events <- event
			return 1, nil
		},
	})
	result, err := m.TaskTool().Handler(ctx, tool.Call{
		ID:   "call-fail",
		Name: "task",
		Args: json.RawMessage(`{"description":"Unknown","prompt":"work","subagent_type":"missing"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.HasPrefix(result.Content, "Task failed. Error:") {
		t.Fatalf("failure result = %#v", result)
	}
	if result.AdditionalKwargs[message.SubagentStatusKey] != message.SubagentFailed {
		t.Fatalf("failure status = %#v", result.AdditionalKwargs)
	}
	if !strings.Contains(result.AdditionalKwargs[message.SubagentErrorKey].(string), "missing") {
		t.Fatalf("failure error = %#v", result.AdditionalKwargs)
	}
	for _, wantType := range []runtime.EventType{runtime.EventSubagentStart, runtime.EventSubagentResult} {
		event := <-events
		if event.Type != wantType || !strings.Contains(string(event.Data), `"task_id":"call-fail"`) {
			t.Fatalf("event = %#v, want type=%q and tool-call task id", event, wantType)
		}
	}
}

type flakyTaskStore struct {
	mu     sync.Mutex
	puts   []Result
	failAt map[int]error
	onPut  func(Result)
}

func (s *flakyTaskStore) Put(_ context.Context, result Result, _ time.Duration) error {
	s.mu.Lock()
	s.puts = append(s.puts, result)
	attempt := len(s.puts)
	err := s.failAt[attempt]
	s.mu.Unlock()
	if s.onPut != nil {
		s.onPut(result)
	}
	return err
}
func (s *flakyTaskStore) Get(_ context.Context, id string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.puts) - 1; i >= 0; i-- {
		if s.puts[i].TaskID != id {
			continue
		}
		if err := s.failAt[i+1]; err != nil {
			continue
		}
		return s.puts[i], nil
	}
	return Result{}, errors.New("task not found")
}

type recordingLifecycle struct {
	finished  []Result
	finishErr error
	onFinish  func(Result)
	starts    int
}

func (l *recordingLifecycle) Start(_ context.Context, parent runtime.RunContext, _ string, _ DispatchRequest) (runtime.RunContext, error) {
	l.starts++
	return parent, nil
}
func (l *recordingLifecycle) Finish(_ context.Context, _, _ runtime.RunContext, _ DispatchRequest, result Result) error {
	l.finished = append(l.finished, result)
	if l.onFinish != nil {
		l.onFinish(result)
	}
	return l.finishErr
}

func successfulRunnerFactory(output string) RunnerFactory {
	return func(_ Definition, _ []string) (*loop.Runner, error) {
		h, err := harness.New(harness.Options{Model: faux.New(faux.Text(output))})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}
}

func registerWorker(t *testing.T, manager *Manager) {
	t.Helper()
	if err := manager.Register(Definition{Name: "worker", Description: "worker"}); err != nil {
		t.Fatal(err)
	}
}

func workerContext() context.Context {
	return testRunContext(context.Background(), runtime.RunContext{RunID: "parent", ThreadID: "thread"})
}

func TestChildPanicStillFinalizesAndPersistsTheTask(t *testing.T) {
	var releases atomic.Int32
	factory := func(_ Definition, _ []string) (PreparedRunner, error) {
		h, err := harness.New(harness.Options{Model: panickingModel{}})
		if err != nil {
			return PreparedRunner{}, err
		}
		return PreparedRunner{Runner: h.Runner(), Release: func() error {
			releases.Add(1)
			return h.Close()
		}}, nil
	}
	store := NewMemoryTaskStore()
	lifecycle := &recordingLifecycle{}
	manager := NewManagerWithPreparedFactory(factory, store, time.Hour)
	manager.SetLifecycle(lifecycle)
	registerWorker(t, manager)

	result, err := manager.Dispatch(workerContext(), DispatchRequest{
		SubagentType: "worker", Prompt: "work", ToolCallID: "panic-task",
	})
	if err == nil || result.Status != message.SubagentFailed || !strings.Contains(result.Error, "panicked") {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	if got := releases.Load(); got != 1 {
		t.Fatalf("release calls = %d, want 1", got)
	}
	if len(lifecycle.finished) != 1 || lifecycle.finished[0].Status != message.SubagentFailed {
		t.Fatalf("lifecycle results = %#v", lifecycle.finished)
	}
	persisted, err := store.Get(context.Background(), "panic-task")
	if err != nil || persisted.Status != message.SubagentFailed {
		t.Fatalf("persisted = %#v, error = %v", persisted, err)
	}
}

func TestLifecycleFailurePreservesCompletedExecutionState(t *testing.T) {
	var order []string
	store := &flakyTaskStore{failAt: map[int]error{}, onPut: func(result Result) {
		order = append(order, "store:"+result.Status)
	}}
	lifecycle := &recordingLifecycle{
		finishErr: errors.New("mailbox finalization failed"),
		onFinish:  func(result Result) { order = append(order, "lifecycle:"+result.Status) },
	}
	manager := NewManager(successfulRunnerFactory("done"), store, time.Hour)
	manager.SetLifecycle(lifecycle)
	registerWorker(t, manager)

	result, err := manager.Dispatch(workerContext(), DispatchRequest{SubagentType: "worker", Prompt: "work", ToolCallID: "call-1"})
	if err == nil || result.Status != message.SubagentCompleted || result.Error != "" || !strings.Contains(result.CoordinationError, "mailbox finalization failed") {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	if len(lifecycle.finished) != 1 || lifecycle.finished[0].Status != message.SubagentCompleted {
		t.Fatalf("lifecycle results = %#v", lifecycle.finished)
	}
	if len(store.puts) != 2 || store.puts[0].Status != "running" || store.puts[1].Status != message.SubagentCompleted || store.puts[1].CoordinationError == "" {
		t.Fatalf("stored states = %#v", store.puts)
	}
	wantOrder := []string{"store:running", "lifecycle:completed", "store:completed"}
	if strings.Join(order, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("finalization order = %v, want %v", order, wantOrder)
	}
}

func TestTaskToolKeepsCompletedStatusWhenCoordinationFails(t *testing.T) {
	lifecycle := &recordingLifecycle{finishErr: errors.New("announcement unavailable")}
	manager := NewManager(successfulRunnerFactory("done"), nil, time.Hour)
	manager.SetLifecycle(lifecycle)
	registerWorker(t, manager)

	result, err := manager.TaskTool().Handler(workerContext(), tool.Call{
		ID:   "call-coordination",
		Name: "task",
		Args: json.RawMessage(`{"description":"work","prompt":"work","subagent_type":"worker"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.HasPrefix(result.Content, "Task Succeeded. Result: done") {
		t.Fatalf("tool result = %#v", result)
	}
	if result.AdditionalKwargs[message.SubagentStatusKey] != message.SubagentCompleted ||
		!strings.Contains(result.AdditionalKwargs[message.SubagentErrorKey].(string), "announcement unavailable") {
		t.Fatalf("structured result = %#v", result.AdditionalKwargs)
	}
}

func TestTerminalStoreFailureRetriesFailedAfterLifecycle(t *testing.T) {
	var order []string
	store := &flakyTaskStore{
		failAt: map[int]error{2: errors.New("redis unavailable")},
		onPut:  func(result Result) { order = append(order, "store:"+result.Status) },
	}
	lifecycle := &recordingLifecycle{onFinish: func(result Result) {
		order = append(order, "lifecycle:"+result.Status)
	}}
	manager := NewManager(successfulRunnerFactory("done"), store, time.Hour)
	manager.SetLifecycle(lifecycle)
	registerWorker(t, manager)

	result, err := manager.Dispatch(workerContext(), DispatchRequest{SubagentType: "worker", Prompt: "work", ToolCallID: "call-1"})
	if err == nil || result.Status != message.SubagentFailed || !strings.Contains(result.Error, "redis unavailable") {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	if len(lifecycle.finished) != 1 || lifecycle.finished[0].Status != message.SubagentCompleted {
		t.Fatalf("lifecycle results = %#v", lifecycle.finished)
	}
	wantOrder := []string{"store:running", "lifecycle:completed", "store:completed", "store:failed"}
	if strings.Join(order, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("finalization order = %v, want %v", order, wantOrder)
	}
}

func TestFinalTerminalStoreRetryFailureIsReturned(t *testing.T) {
	store := &flakyTaskStore{failAt: map[int]error{
		2: errors.New("terminal write unavailable"),
		3: errors.New("retry write unavailable"),
	}}
	manager := NewManager(successfulRunnerFactory("done"), store, time.Hour)
	registerWorker(t, manager)

	result, err := manager.Dispatch(workerContext(), DispatchRequest{SubagentType: "worker", Prompt: "work", ToolCallID: "call-1"})
	if err == nil || result.Status != message.SubagentFailed {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	for _, want := range []string{"terminal write unavailable", "retry write unavailable"} {
		if !strings.Contains(result.Error, want) || !strings.Contains(err.Error(), want) {
			t.Fatalf("result/error = %#v / %v, want %q", result, err, want)
		}
	}
	if len(store.puts) != 3 || store.puts[1].Status != message.SubagentCompleted || store.puts[2].Status != message.SubagentFailed {
		t.Fatalf("stored attempts = %#v", store.puts)
	}
}

func TestSubagentStartHookDenialFailsClosed(t *testing.T) {
	lifecycle := &recordingLifecycle{}
	runner, err := hooks.NewRunner([]hooks.Hook{&hooks.FuncHook{
		HookName: "policy",
		OnEvents: []hooks.Event{hooks.EventSubagentStart},
		Fn: func(context.Context, hooks.Payload) (hooks.Outcome, error) {
			return hooks.Outcome{Deny: true, Message: "delegation disabled"}, nil
		},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(successfulRunnerFactory("must not run"), nil, time.Hour)
	manager.SetHookRunner(runner)
	manager.SetLifecycle(lifecycle)
	registerWorker(t, manager)

	result, err := manager.Dispatch(workerContext(), DispatchRequest{SubagentType: "worker", Prompt: "work", ToolCallID: "call-1"})
	if err == nil || result.Status != message.SubagentFailed || !strings.Contains(result.Error, "delegation disabled") {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	if lifecycle.starts != 0 || len(lifecycle.finished) != 0 {
		t.Fatalf("denied dispatch reached lifecycle: starts=%d finished=%#v", lifecycle.starts, lifecycle.finished)
	}
}

func TestSubagentLifecycleHooksReceiveFinalResultAndEndIsFailOpen(t *testing.T) {
	var payloads []hooks.Payload
	runner, err := hooks.NewRunner([]hooks.Hook{&hooks.FuncHook{
		HookName: "observer",
		OnEvents: []hooks.Event{hooks.EventSubagentStart, hooks.EventSubagentEnd},
		Fn: func(_ context.Context, payload hooks.Payload) (hooks.Outcome, error) {
			payloads = append(payloads, payload)
			if payload.Event == hooks.EventSubagentEnd {
				return hooks.Outcome{Deny: true, Message: "too late to deny"}, nil
			}
			return hooks.Outcome{}, nil
		},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(successfulRunnerFactory("done"), nil, time.Hour)
	manager.SetHookRunner(runner)
	registerWorker(t, manager)

	result, err := manager.Dispatch(workerContext(), DispatchRequest{SubagentType: "worker", Prompt: "work", ToolCallID: "call-1"})
	if err != nil || result.Status != message.SubagentCompleted {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	if len(payloads) != 2 || payloads[0].Event != hooks.EventSubagentStart || payloads[1].Event != hooks.EventSubagentEnd {
		t.Fatalf("hook payloads = %#v", payloads)
	}
	start, end := payloads[0], payloads[1]
	if start.TaskID != "call-1" || start.Subagent != "worker" || start.RunID != "parent" || start.ThreadID != "thread" {
		t.Fatalf("start payload = %#v", start)
	}
	if end.TaskID != "call-1" || end.Status != message.SubagentCompleted || end.Output != "done" || end.IsError {
		t.Fatalf("end payload = %#v", end)
	}
}

func TestContextRunnerFactoryReceivesTrustedParentContext(t *testing.T) {
	var gotRunID, gotType string
	factory := func(ctx context.Context, def Definition, _ []string, req DispatchRequest) (*loop.Runner, error) {
		parent, ok := runtime.RunContextFrom(ctx)
		if !ok {
			t.Fatalf("parent run context was not propagated")
		}
		gotRunID, gotType = parent.RunID, req.SubagentType
		h, err := harness.New(harness.Options{Model: faux.New(faux.Text("context child"))})
		if err != nil {
			return nil, err
		}
		if def.Name != "worker" {
			t.Fatalf("definition = %#v", def)
		}
		return h.Runner(), nil
	}
	m := NewManagerWithContextFactory(factory, nil, 0)
	if err := m.Register(Definition{Name: "worker", Description: "worker"}); err != nil {
		t.Fatal(err)
	}
	ctx := testRunContext(context.Background(), runtime.RunContext{RunID: "parent-run", ThreadID: "thread"})
	if _, err := m.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "work"}); err != nil {
		t.Fatal(err)
	}
	if gotRunID != "parent-run" || gotType != "worker" {
		t.Fatalf("factory context/request = %q/%q", gotRunID, gotType)
	}
}

func TestDispatchStopsBeforeFactoryWhenSubagentBudgetIsExhausted(t *testing.T) {
	var factoryCalls atomic.Int32
	manager := NewManager(func(Definition, []string) (*loop.Runner, error) {
		factoryCalls.Add(1)
		return successfulRunnerFactory("done")(Definition{Name: "worker", Description: "worker"}, nil)
	}, nil, time.Hour)
	registerWorker(t, manager)
	budget := runtime.NewBudgetLedger(runtime.BudgetAmount{Subagents: 1})
	ctx := testRunContext(context.Background(), runtime.RunContext{
		RunID: "parent", ThreadID: "thread", Budget: budget,
	})

	if _, err := manager.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "first"}); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	_, err := manager.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "second"})
	if !errors.Is(err, loop.ErrBudgetExhausted) {
		t.Fatalf("second dispatch error = %v, want ErrBudgetExhausted", err)
	}
	if got := factoryCalls.Load(); got != 1 {
		t.Fatalf("factory calls = %d; exhausted dispatch reached child assembly", got)
	}
}

func TestDispatchUsesExplicitSubagentRunState(t *testing.T) {
	manager := NewManager(successfulRunnerFactory("done"), nil, time.Hour)
	registerWorker(t, manager)
	state, err := runtime.NewRunStateMachine("parent", "thread")
	if err != nil {
		t.Fatal(err)
	}
	var phases []runtime.RunPhase
	state.SetObserver(func(snapshot runtime.RunSnapshot) error {
		phases = append(phases, snapshot.Phase)
		return nil
	})
	if err := state.Start(); err != nil {
		t.Fatal(err)
	}
	if err := state.WaitTool(); err != nil {
		t.Fatal(err)
	}
	ctx := testRunContext(context.Background(), runtime.RunContext{
		RunID: "parent", ThreadID: "thread", StateMachine: state,
	})
	if _, err := manager.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "work"}); err != nil {
		t.Fatal(err)
	}
	want := []runtime.RunPhase{runtime.RunRunning, runtime.RunWaitingTool, runtime.RunWaitingSubagent, runtime.RunWaitingTool}
	if !slices.Equal(phases, want) {
		t.Fatalf("run phases = %v, want %v", phases, want)
	}
}

type wideningLifecycle struct{}

func (wideningLifecycle) Start(_ context.Context, parent runtime.RunContext, _ string, _ DispatchRequest) (runtime.RunContext, error) {
	child := parent
	child.AllowedTools = []string{"read", "write"}
	child.AllowedCapabilities = append(child.AllowedCapabilities, "tool.write")
	child.Budget = runtime.NewBudgetLedger(runtime.BudgetAmount{})
	return child, nil
}

func (wideningLifecycle) Finish(context.Context, runtime.RunContext, runtime.RunContext, DispatchRequest, Result) error {
	return nil
}

func TestDispatchCannotWidenCapabilitiesToolsOrBudget(t *testing.T) {
	capabilities := capability.NewRegistry()
	worker := Definition{Name: "worker", Description: "worker", AllowedTools: []string{"read"}}
	if err := capability.RegisterValue(capabilities, capability.Value{
		Definition: capability.Definition{Name: "agent.worker", Kind: capability.KindAgent, Scope: capability.ScopeRun}, Value: worker,
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"model.default", "tool.read", "tool.write"} {
		kind := capability.KindTool
		if name == "model.default" {
			kind = capability.KindModel
		}
		if err := capability.RegisterValue(capabilities, capability.Value{
			Definition: capability.Definition{Name: name, Kind: kind}, Value: name,
		}); err != nil {
			t.Fatal(err)
		}
	}
	view, err := capability.NewView(capabilities.Snapshot(), capabilities.Names())
	if err != nil {
		t.Fatal(err)
	}
	captured := make(chan runtime.RunContext, 1)
	manager := NewManager(func(Definition, []string) (*loop.Runner, error) {
		read := tool.Definition{
			Name: "read", Group: "test", Description: "read", Parameters: json.RawMessage(`{"type":"object"}`),
			Handler: func(ctx context.Context, _ tool.Call) (*tool.Result, error) {
				run, _ := runtime.RunContextFrom(ctx)
				captured <- run
				return &tool.Result{Content: "read"}, nil
			},
		}
		h, buildErr := harness.New(harness.Options{
			Model: faux.New(faux.ToolCall("read", `{}`), faux.Text("done")), Tools: []tool.Definition{read},
		})
		if buildErr != nil {
			return nil, buildErr
		}
		return h.Runner(), nil
	}, nil, time.Hour)
	manager.SetLifecycle(wideningLifecycle{})
	if err := manager.Register(worker); err != nil {
		t.Fatal(err)
	}
	parentBudget := runtime.NewBudgetLedger(runtime.BudgetAmount{Tokens: 100})
	ctx := testRunContext(context.Background(), runtime.RunContext{
		RunID: "parent", ThreadID: "thread", AllowedTools: []string{"read", "write"},
		AllowedCapabilities: capabilities.Names(), Capabilities: view, Budget: parentBudget,
	})
	if _, err := manager.Dispatch(ctx, DispatchRequest{SubagentType: "worker", Prompt: "work"}); err != nil {
		t.Fatal(err)
	}
	child := <-captured
	if !slices.Equal(child.AllowedTools, []string{"read"}) {
		t.Fatalf("child tools = %v", child.AllowedTools)
	}
	if slices.Contains(child.AllowedCapabilities, "tool.write") {
		t.Fatalf("child capabilities widened: %v", child.AllowedCapabilities)
	}
	if _, err := child.Capabilities.Resolve(context.Background(), "tool.write", capability.KindTool, capability.ResolveRequest{}); err == nil {
		t.Fatal("child resolved a capability outside its narrowed view")
	}
	if child.Budget == parentBudget {
		t.Fatal("child reused the parent ledger instead of a narrowed descendant")
	}
}

func TestDispatchAsyncPublishesEarlyFailure(t *testing.T) {
	m := NewManager(nil, nil, 0)
	events := make(chan runtime.Event, 2)
	ctx := testRunContext(context.Background(), runtime.RunContext{
		RunID: "parent-run", ThreadID: "thread",
		Publish: func(_ context.Context, event runtime.Event) (int64, error) {
			events <- event
			return 1, nil
		},
	})
	id, err := m.DispatchAsync(ctx, DispatchRequest{SubagentType: "missing", Prompt: "work"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case started := <-events:
		if started.Type != runtime.EventSubagentStart {
			t.Fatalf("event type = %q", started.Type)
		}
		event := <-events
		if event.Type != runtime.EventSubagentResult {
			t.Fatalf("event type = %q", event.Type)
		}
		var result Result
		if err := json.Unmarshal(event.Data, &result); err != nil {
			t.Fatal(err)
		}
		if result.TaskID != id || result.Status != "failed" || result.Error == "" {
			t.Fatalf("result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async failure event")
	}
}

func TestDispatchAsyncReplacesPendingAfterTerminalStoreRetry(t *testing.T) {
	puts := make(chan Result, 4)
	store := &flakyTaskStore{
		// Put #1 is the async reservation. The first terminal write fails;
		// persistTerminal must immediately write a durable failed state.
		failAt: map[int]error{2: errors.New("transient task store failure")},
		onPut: func(result Result) {
			puts <- result
		},
	}
	manager := NewManager(nil, store, time.Hour)
	id, err := manager.DispatchAsync(workerContext(), DispatchRequest{
		SubagentType: "missing", Prompt: "work", ToolCallID: "async-retry",
	})
	if err != nil || id != "async-retry" {
		t.Fatalf("DispatchAsync() = %q, %v", id, err)
	}
	deadline := time.After(time.Second)
	var terminal Result
	terminalAttempts := 0
	for {
		select {
		case result := <-puts:
			if result.TaskID == id && result.Status == message.SubagentFailed && result.CompletedAt != nil {
				terminal = result
				terminalAttempts++
			}
		case <-deadline:
			t.Fatalf("pending task never reached a terminal state; last=%#v", terminal)
		}
		// The first terminal attempt is deliberately rejected by the store;
		// wait for the retry before reading through TaskStatus.
		if terminalAttempts == 2 {
			break
		}
	}
	got, err := manager.TaskStatus(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != message.SubagentFailed || got.Status == "pending" || !strings.Contains(got.Error, "unknown subagent type") {
		t.Fatalf("TaskStatus() = %#v; pending reservation was not replaced", got)
	}
}
