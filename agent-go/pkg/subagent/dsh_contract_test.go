package subagent

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/harness"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/scope"
)

func TestChildOutputLimitReturnsPartialFailure(t *testing.T) {
	m := NewManager(func(Definition, []string) (*loop.Runner, error) {
		h, err := harness.New(harness.Options{Model: faux.New(faux.Truncated("partial answer"))})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}, nil, time.Hour)
	registerWorker(t, m)
	result, err := m.Dispatch(workerContext(), DispatchRequest{SubagentType: "worker", Prompt: "work", ToolCallID: "truncated"})
	if !errors.Is(err, ErrOutputLimit) || result.Status != message.SubagentFailed || result.StopReason != "max-tokens" || result.Output != "partial answer" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	toolResult := taskFailureFromResult(result, err)
	if !toolResult.IsError {
		t.Fatal("truncated output was presented as successful")
	}
}

func TestPreparedFactoryFailureReleasesPartialResources(t *testing.T) {
	buildErr := errors.New("assembly failed")
	releaseErr := errors.New("dispose failed")
	released := 0
	m := NewManagerWithPreparedFactory(func(Definition, []string) (PreparedRunner, error) {
		return PreparedRunner{Release: func() error { released++; return releaseErr }}, buildErr
	}, nil, time.Hour)
	registerWorker(t, m)
	_, err := m.Dispatch(workerContext(), DispatchRequest{SubagentType: "worker", Prompt: "work"})
	if !errors.Is(err, buildErr) || !errors.Is(err, releaseErr) || released != 1 {
		t.Fatalf("err=%v releases=%d", err, released)
	}
}

func TestSpawnAndForkUseDetachedCompletedHistory(t *testing.T) {
	for _, provider := range []string{"spawn", "fork"} {
		t.Run(provider, func(t *testing.T) {
			model := faux.New(faux.Text("child answer"))
			m := NewManager(func(Definition, []string) (*loop.Runner, error) {
				h, err := harness.New(harness.Options{Model: model})
				if err != nil {
					return nil, err
				}
				return h.Runner(), nil
			}, nil, time.Hour)
			registerWorker(t, m)
			ctx := workerContext()
			parent, _ := runtime.RunContextFrom(ctx)
			parent.DelegationHistory = []message.Message{{Role: message.RoleUser, Content: "previous"}, {Role: message.RoleAssistant, Content: "completed"}}
			ctx = runtime.WithRunContext(ctx, parent)
			result, err := m.Dispatch(ctx, DispatchRequest{Provider: provider, SubagentType: "worker", Prompt: "current task"})
			if err != nil {
				t.Fatal(err)
			}
			request := model.Requests()[0]
			expected := 1
			if provider == "fork" {
				expected = 3
			}
			if len(request.Messages) != expected || request.Messages[len(request.Messages)-1].Content != "current task" {
				t.Fatal(request.Messages)
			}
			if result.Output != "child answer" || parent.DelegationHistory[1].Content != "completed" {
				t.Fatal("child output or seed isolation failed")
			}
		})
	}
}

func TestAsyncCancellationOwnershipAndConcurrentCloseDrainRelease(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	releasing := make(chan struct{})
	allowRelease := make(chan struct{})
	// Blocking the model is sufficient to exercise ownership without a sandbox.
	factory := func(Definition, []string) (PreparedRunner, error) {
		h, err := harness.New(harness.Options{Sampler: sampleFunc(func(ctx context.Context, _ *lifecycle.State) (*model.Response, bool, error) {
			close(started)
			<-ctx.Done()
			close(canceled)
			return nil, false, ctx.Err()
		})})
		if err != nil {
			return PreparedRunner{}, err
		}
		return PreparedRunner{Runner: h.Runner(), Release: func() error { close(releasing); <-allowRelease; return h.Close() }}, nil
	}
	m := NewManagerWithPreparedFactory(factory, nil, time.Hour)
	registerWorker(t, m)
	ownerCtx, ownerCancel := context.WithCancel(context.Background())
	defer ownerCancel()
	ctx := workerContext()
	parent, _ := runtime.RunContextFrom(ctx)
	parent.ExecutionContext = ownerCtx
	callCtx, callCancel := context.WithCancel(runtime.WithRunContext(ctx, parent))
	id, err := m.DispatchAsync(callCtx, DispatchRequest{SubagentType: "worker", Prompt: "work", ToolCallID: "owned"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if parent.StateMachine.Snapshot().Phase != runtime.RunRunning {
		t.Fatal("background child put its parent into a waiting state")
	}
	callCancel()
	select {
	case <-canceled:
		t.Fatal("short-lived dispatch context canceled accepted background work")
	default:
	}
	foreign := parent
	foreign.RunID = "another-parent"
	if err := m.Interrupt(runtime.WithRunContext(context.Background(), foreign), id); err == nil {
		t.Fatal("another parent interrupted the child")
	}
	if err := m.Interrupt(runtime.WithRunContext(context.Background(), parent), id); err != nil {
		t.Fatal(err)
	}
	<-canceled
	<-releasing
	closeResults := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); closeResults <- m.Close() }()
	}
	select {
	case <-closeResults:
		t.Fatal("Close returned while prepared resources were still held")
	default:
	}
	close(allowRelease)
	wg.Wait()
	for range 2 {
		if err := <-closeResults; err != nil {
			t.Fatal(err)
		}
	}
	result, err := m.Wait(runtime.WithRunContext(context.Background(), parent), id)
	if err != nil || result.Status != message.SubagentCancelled || result.StopReason != "aborted" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := m.DispatchAsync(runtime.WithRunContext(context.Background(), parent), DispatchRequest{SubagentType: "worker", Prompt: "late"}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

type sampleFunc func(context.Context, *lifecycle.State) (*model.Response, bool, error)

func (f sampleFunc) Sample(ctx context.Context, st *lifecycle.State) (*model.Response, bool, error) {
	return f(ctx, st)
}

func TestAsyncParentScopeWaitsUntilChildResourcesRelease(t *testing.T) {
	owner := scope.New(context.Background())
	started := make(chan struct{})
	released := make(chan struct{})
	m := NewManagerWithPreparedFactory(func(Definition, []string) (PreparedRunner, error) {
		h, err := harness.New(harness.Options{Sampler: sampleFunc(func(ctx context.Context, _ *lifecycle.State) (*model.Response, bool, error) {
			close(started)
			<-ctx.Done()
			return nil, false, ctx.Err()
		})})
		if err != nil {
			return PreparedRunner{}, err
		}
		return PreparedRunner{Runner: h.Runner(), Release: func() error { close(released); return h.Close() }}, nil
	}, nil, time.Hour)
	registerWorker(t, m)
	parent, _ := runtime.RunContextFrom(workerContext())
	parent.ExecutionOwner = owner
	parent.ExecutionContext = owner.Context()
	id, err := m.DispatchAsync(runtime.WithRunContext(owner.Context(), parent), DispatchRequest{SubagentType: "worker", Prompt: "work"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err := owner.Dispose(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-released:
	default:
		t.Fatal("parent scope did not drain its child")
	}
	result, err := m.Wait(runtime.WithRunContext(context.Background(), parent), id)
	if err != nil || result.CompletedAt == nil {
		t.Fatalf("terminal=%+v err=%v", result, err)
	}
	_ = m.Close()
}

func TestChildSnapshotsPolicyMapsAndProfileToolLists(t *testing.T) {
	m := NewManager(successfulRunnerFactory("done"), nil, time.Hour)
	tools := []string{"read"}
	if err := m.Register(Definition{Name: "worker", Description: "worker", AllowedTools: tools}); err != nil {
		t.Fatal(err)
	}
	tools[0] = "write"
	defs := m.Definitions()
	defs[0].AllowedTools[0] = "bash"
	if !slices.Equal(m.Definitions()[0].AllowedTools, []string{"read"}) {
		t.Fatal("profile shares mutable tool list")
	}
	values := map[string]any{"nested": []any{map[string]any{"allowed": true}}}
	cloned := cloneValues(values)
	cloned["nested"].([]any)[0].(map[string]any)["allowed"] = false
	if values["nested"].([]any)[0].(map[string]any)["allowed"] != true {
		t.Fatal("child policy mutation reached parent")
	}
}

type continueFirst struct{}

func (continueFirst) Name() string { return "continue-first" }
func (continueFirst) AfterModel(_ context.Context, st *lifecycle.State) error {
	if st.Iteration == 0 {
		st.Directive = lifecycle.DirectiveContinue
	}
	return nil
}

func TestEmptyTerminalAssistantPreservesEarlierChildOutput(t *testing.T) {
	m := NewManager(func(Definition, []string) (*loop.Runner, error) {
		h, err := harness.New(harness.Options{Model: faux.New(faux.Text("useful answer"), faux.Text("")), LifecycleHandlers: []lifecycle.Handler{continueFirst{}}})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}, nil, time.Hour)
	registerWorker(t, m)
	result, err := m.Dispatch(workerContext(), DispatchRequest{SubagentType: "worker", Prompt: "work"})
	if err != nil || result.Output != "useful answer" || result.StopReason != "completed" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

type refuseInput struct{}

func (refuseInput) Name() string { return "refuse-input" }
func (refuseInput) BeforeModel(_ context.Context, st *lifecycle.State) error {
	st.Directive = lifecycle.DirectiveStop
	return nil
}

func TestPolicyRefusalDoesNotBecomeCompletedTask(t *testing.T) {
	model := faux.New(faux.Text("never"))
	m := NewManager(func(Definition, []string) (*loop.Runner, error) {
		h, err := harness.New(harness.Options{Model: model, LifecycleHandlers: []lifecycle.Handler{refuseInput{}}})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}, nil, time.Hour)
	registerWorker(t, m)
	result, err := m.Dispatch(workerContext(), DispatchRequest{SubagentType: "worker", Prompt: "work"})
	if !errors.Is(err, ErrRefused) || result.Status != message.SubagentFailed || result.StopReason != "refusal" || model.CallCount() != 0 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, model.CallCount())
	}
}

func TestLoopCapturesForkPrefixBeforeCurrentInputAdmission(t *testing.T) {
	childModel := faux.New(faux.Text("child"))
	manager := NewManager(func(Definition, []string) (*loop.Runner, error) {
		h, err := harness.New(harness.Options{Model: childModel})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}, nil, time.Hour)
	registerWorker(t, manager)
	parentHarness, err := harness.New(harness.Options{Sampler: sampleFunc(func(ctx context.Context, _ *lifecycle.State) (*model.Response, bool, error) {
		_, err := manager.Dispatch(ctx, DispatchRequest{Provider: "fork", SubagentType: "worker", Prompt: "child task"})
		return &model.Response{Message: message.Message{Role: message.RoleAssistant, Content: "parent"}}, false, err
	})})
	if err != nil {
		t.Fatal(err)
	}
	history := message.NewHistory()
	history.Load([]message.Message{{Role: message.RoleUser, Content: "previous"}, {Role: message.RoleAssistant, Content: "previous answer"}})
	if _, err := parentHarness.Runner().Run(workerContext(), loop.Request{RunID: "parent", ThreadID: "thread", History: history, Prompt: "in-flight parent input"}); err != nil {
		t.Fatal(err)
	}
	messages := childModel.Requests()[0].Messages
	if len(messages) != 3 || messages[0].Content != "previous" || messages[1].Content != "previous answer" || messages[2].Content != "child task" {
		t.Fatalf("fork copied in-flight input: %+v", messages)
	}
	_ = parentHarness.Close()
	_ = manager.Close()
}

func TestParentScopeReportsAsyncChildResourceReleaseFailure(t *testing.T) {
	owner := scope.New(context.Background())
	started := make(chan struct{})
	m := NewManagerWithPreparedFactory(func(Definition, []string) (PreparedRunner, error) {
		h, err := harness.New(harness.Options{Sampler: sampleFunc(func(ctx context.Context, _ *lifecycle.State) (*model.Response, bool, error) {
			close(started)
			<-ctx.Done()
			return nil, false, ctx.Err()
		})})
		if err != nil {
			return PreparedRunner{}, err
		}
		return PreparedRunner{Runner: h.Runner(), Release: func() error { _ = h.Close(); return errors.New("child resource release failed") }}, nil
	}, nil, time.Hour)
	registerWorker(t, m)
	parent, _ := runtime.RunContextFrom(workerContext())
	parent.ExecutionOwner = owner
	parent.ExecutionContext = owner.Context()
	id, err := m.DispatchAsync(runtime.WithRunContext(owner.Context(), parent), DispatchRequest{SubagentType: "worker", Prompt: "work"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err := owner.Dispose(); err == nil {
		t.Fatal("parent scope lost child cleanup failure")
	}
	result, err := m.Wait(runtime.WithRunContext(context.Background(), parent), id)
	if err != nil || result.CoordinationError == "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	_ = m.Close()
}
