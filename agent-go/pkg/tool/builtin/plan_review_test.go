package builtin_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool/builtin"
)

func TestExitPlanModeApprovesThroughQuestionCapability(t *testing.T) {
	fixture := newPlanReviewFixture(t, nil)
	resultCh := fixture.execute(t)
	question := fixture.waitQuestion(t)
	if _, err := fixture.manager.Answer(context.Background(), question.ID, runtime.QuestionAnswer{Selected: []string{"Approve"}}, "user"); err != nil {
		t.Fatal(err)
	}
	result := <-resultCh
	if result.err != nil || result.value == nil || result.value.IsError {
		t.Fatalf("result=%#v err=%v", result.value, result.err)
	}
	if active, _ := fixture.values["is_plan_mode"].(bool); active {
		t.Fatal("plan mode stayed active after durable approval")
	}
	todos := fixture.values["todos"].([]map[string]string)
	if todos[0]["status"] != "in_progress" {
		t.Fatalf("approved todos = %#v, want first task in_progress", todos)
	}
	if got := fixture.eventTypes(); len(got) != 3 || got[0] != runtime.EventQuestionRequested || got[1] != runtime.EventQuestionResolved || got[2] != runtime.EventPlanModeChanged {
		t.Fatalf("events=%#v", got)
	}
	if snapshot := fixture.state.Snapshot(); snapshot.Phase != runtime.RunWaitingTool || snapshot.QuestionID != "" {
		t.Fatalf("state=%#v", snapshot)
	}
}

func TestExitPlanModeKeepsPlanningWithFeedback(t *testing.T) {
	fixture := newPlanReviewFixture(t, nil)
	resultCh := fixture.execute(t)
	question := fixture.waitQuestion(t)
	if _, err := fixture.manager.Answer(context.Background(), question.ID, runtime.QuestionAnswer{Selected: []string{"Keep planning"}, Custom: "add rollback"}, "user"); err != nil {
		t.Fatal(err)
	}
	result := <-resultCh
	if result.err != nil || result.value == nil || !result.value.IsError || result.value.Content == "" {
		t.Fatalf("result=%#v err=%v", result.value, result.err)
	}
	if active, _ := fixture.values["is_plan_mode"].(bool); !active {
		t.Fatal("plan mode exited after keep-planning answer")
	}
	for _, eventType := range fixture.eventTypes() {
		if eventType == runtime.EventPlanModeChanged {
			t.Fatal("inactive plan event was persisted without approval")
		}
	}
}

func TestExitPlanModeFailsClosedWhenInactiveEventCannotPersist(t *testing.T) {
	fixture := newPlanReviewFixture(t, func(event runtime.Event) error {
		if event.Type == runtime.EventPlanModeChanged {
			return errors.New("event store unavailable")
		}
		return nil
	})
	resultCh := fixture.execute(t)
	question := fixture.waitQuestion(t)
	if _, err := fixture.manager.Answer(context.Background(), question.ID, runtime.QuestionAnswer{Selected: []string{"Approve"}}, "user"); err != nil {
		t.Fatal(err)
	}
	result := <-resultCh
	if result.err == nil {
		t.Fatalf("result=%#v, want persistence error", result.value)
	}
	if active, _ := fixture.values["is_plan_mode"].(bool); !active {
		t.Fatal("plan mode exited after persistence failure")
	}
}

type planReviewResult struct {
	value *tool.Result
	err   error
}

type planReviewFixture struct {
	ctx        context.Context
	manager    *runtime.QuestionManager
	state      *runtime.RunStateMachine
	values     map[string]any
	requested  chan runtime.QuestionRequest
	mu         sync.Mutex
	events     []runtime.EventType
	definition tool.Definition
}

func newPlanReviewFixture(t *testing.T, fail func(runtime.Event) error) *planReviewFixture {
	t.Helper()
	manager, err := runtime.NewQuestionManager(runtime.NewMemoryQuestionStore())
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	if err := capability.RegisterValue(registry, capability.Value{Definition: capability.Definition{
		Name: "interaction.questions", Kind: capability.KindInteraction, Scope: capability.ScopeRun,
	}, Value: manager}); err != nil {
		t.Fatal(err)
	}
	view, err := capability.NewView(registry.Snapshot(), []string{"interaction.questions"})
	if err != nil {
		t.Fatal(err)
	}
	state, err := runtime.NewRunStateMachine("run-1", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(); err != nil {
		t.Fatal(err)
	}
	if err := state.WaitTool(); err != nil {
		t.Fatal(err)
	}
	fixture := &planReviewFixture{
		manager: manager, state: state, values: map[string]any{
			"is_plan_mode": true, "mode": "pro",
			"todos": []map[string]string{{"content": "Build", "status": "pending"}},
		},
		requested: make(chan runtime.QuestionRequest, 1), definition: builtin.ExitPlanMode("interaction.questions"),
	}
	run := runtime.RunContext{
		RunID: "run-1", EventRunID: "run-1", ThreadID: "thread-1", GenerationID: "generation-1",
		Values: fixture.values, Capabilities: view, StateMachine: state, Questions: manager,
		Publish: func(_ context.Context, event runtime.Event) (int64, error) {
			fixture.mu.Lock()
			fixture.events = append(fixture.events, event.Type)
			seq := int64(len(fixture.events))
			fixture.mu.Unlock()
			if event.Type == runtime.EventQuestionRequested {
				var question runtime.QuestionRequest
				if err := json.Unmarshal(event.Data, &question); err == nil {
					fixture.requested <- question
				}
			}
			if fail != nil {
				return seq, fail(event)
			}
			return seq, nil
		},
		PersistValues: func(_ context.Context, values map[string]any) error {
			fixture.values = values
			return nil
		},
	}
	fixture.ctx = runtime.WithRunContext(context.Background(), run)
	return fixture
}

func (f *planReviewFixture) execute(t *testing.T) <-chan planReviewResult {
	t.Helper()
	result := make(chan planReviewResult, 1)
	go func() {
		value, err := f.definition.Handler(f.ctx, tool.Call{ID: "call-1", Name: f.definition.Name, Args: json.RawMessage(`{"plan":"# Release plan\n\n1. Build\n2. Verify"}`)})
		result <- planReviewResult{value: value, err: err}
	}()
	return result
}

func (f *planReviewFixture) waitQuestion(t *testing.T) runtime.QuestionRequest {
	t.Helper()
	select {
	case question := <-f.requested:
		return question
	case <-time.After(2 * time.Second):
		t.Fatal("plan review question was not published")
		return runtime.QuestionRequest{}
	}
}

func (f *planReviewFixture) eventTypes() []runtime.EventType {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]runtime.EventType(nil), f.events...)
}
