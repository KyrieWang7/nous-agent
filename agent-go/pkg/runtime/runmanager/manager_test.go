package runmanager

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/metadata"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/replay"
)

func TestAccountingAdvancedRejectsStaleSnapshots(t *testing.T) {
	current := runtime.Totals{LLMCalls: 2, InputTokens: 20, OutputTokens: 10, SubagentTokens: 12}
	newer := current
	newer.LLMCalls++
	newer.InputTokens += 4
	newer.SubagentTokens += 4
	if !accountingAdvanced(newer, current) {
		t.Fatal("newer accounting snapshot was rejected")
	}
	stale := current
	stale.LLMCalls--
	if accountingAdvanced(stale, current) {
		t.Fatal("stale accounting snapshot was accepted")
	}
	if accountingAdvanced(current, current) {
		t.Fatal("equal accounting snapshot was accepted")
	}
}

func TestManagerPersistsCanonicalTranscriptBeforeProjectionSnapshot(t *testing.T) {
	var orderMu sync.Mutex
	var order []string
	record := func(value string) {
		orderMu.Lock()
		order = append(order, value)
		orderMu.Unlock()
	}
	store := &testMetadataStore{onSaveHistory: func() { record("history") }}
	snapshots := recordingSnapshotStore{save: func(replay.Snapshot) error { record("snapshot"); return nil }}
	agent := agentFunc(func(context.Context, AgentRequest) (AgentResult, error) {
		return AgentResult{Messages: []message.Message{{Role: message.RoleAssistant, Content: "done"}}, Output: "done"}, nil
	})
	manager, registry := newTestManager(t, agent, store, snapshots, func(event runtime.Event) (int64, error) {
		if event.Type == runtime.EventTranscriptAppend {
			record("canonical")
		}
		return 7, nil
	}, nil)
	defer registry.Close()
	run := registeredTestRun(t, registry)
	manager.Execute(context.Background(), run, Input{Prompt: "work"})
	if got := strings.Join(order, ","); got != "canonical,history,snapshot" {
		t.Fatalf("persistence order = %q", got)
	}
	if store.completion.Status != "success" || store.terminal.Status != "success" {
		t.Fatalf("completion=%#v terminal=%#v", store.completion, store.terminal)
	}
}

func TestManagerConvertsAgentPanicAndReleasesPreparedGenerationOnce(t *testing.T) {
	var releases atomic.Int32
	prepared := preparingAgent{prepared: PreparedRun{
		Agent:   agentFunc(func(context.Context, AgentRequest) (AgentResult, error) { panic("boom") }),
		Release: func() { releases.Add(1) },
	}}
	store := &testMetadataStore{}
	var projections []Projection
	manager, registry := newTestManager(t, prepared, store, nil, nil, func(projection Projection) {
		projections = append(projections, projection)
	})
	defer registry.Close()
	run := registeredTestRun(t, registry)
	manager.Execute(context.Background(), run, Input{Prompt: "work"})
	if releases.Load() != 1 {
		t.Fatalf("generation releases = %d, want 1", releases.Load())
	}
	if store.completion.Status != "error" || store.terminal.Status != "error" {
		t.Fatalf("completion=%#v terminal=%#v", store.completion, store.terminal)
	}
	var sawPanicError, sawEnd bool
	for _, projection := range projections {
		switch projection.Kind {
		case ProjectionError:
			err, _ := projection.Data.(error)
			sawPanicError = err != nil && strings.Contains(err.Error(), "panicked")
		case ProjectionRunEnd:
			sawEnd = true
		}
	}
	if !sawPanicError || !sawEnd {
		t.Fatalf("projections = %#v", projections)
	}
}

func TestManagerPersistsCanonicalPlanModeBeforeBudgetAndAgentExecution(t *testing.T) {
	var executed atomic.Bool
	var events []runtime.Event
	agent := agentFunc(func(ctx context.Context, _ AgentRequest) (AgentResult, error) {
		executed.Store(true)
		runContext, ok := runtime.RunContextFrom(ctx)
		if !ok {
			t.Fatal("run context missing")
		}
		if active, _ := runContext.Values["is_plan_mode"].(bool); !active {
			t.Fatalf("is_plan_mode = %#v, want true", runContext.Values["is_plan_mode"])
		}
		return AgentResult{Output: "done"}, nil
	})
	manager, registry := newTestManager(t, agent, &testMetadataStore{}, nil, func(event runtime.Event) (int64, error) {
		events = append(events, event)
		return int64(len(events)), nil
	}, nil)
	defer registry.Close()

	manager.Execute(context.Background(), registeredTestRun(t, registry), Input{Prompt: "work", Context: map[string]any{"mode": "pro"}})
	if !executed.Load() {
		t.Fatal("agent was not executed")
	}

	planIndex, budgetIndex := -1, -1
	for i, event := range events {
		switch event.Type {
		case runtime.EventPlanModeChanged:
			planIndex = i
			if event.Category != runtime.CategoryAudit {
				t.Fatalf("plan event category = %q, want audit", event.Category)
			}
			if event.IdempotencyKey != "run:run-1:plan-mode" {
				t.Fatalf("plan event idempotency key = %q", event.IdempotencyKey)
			}
			var changed runtime.PlanModeChanged
			if err := json.Unmarshal(event.Data, &changed); err != nil {
				t.Fatalf("decode plan event: %v", err)
			}
			if !changed.Active || changed.Mode != runtime.ModePro {
				t.Fatalf("plan event = %#v", changed)
			}
		case runtime.EventBudgetChanged:
			if budgetIndex < 0 {
				budgetIndex = i
			}
		}
	}
	if planIndex < 0 || budgetIndex < 0 || planIndex >= budgetIndex {
		t.Fatalf("event order = %#v, want plan_mode_changed before budget_changed", eventTypes(events))
	}
}

func TestManagerFailsClosedWhenPlanModePersistenceFails(t *testing.T) {
	var executed atomic.Bool
	var events []runtime.EventType
	store := &testMetadataStore{}
	agent := agentFunc(func(context.Context, AgentRequest) (AgentResult, error) {
		executed.Store(true)
		return AgentResult{}, nil
	})
	manager, registry := newTestManager(t, agent, store, nil, func(event runtime.Event) (int64, error) {
		events = append(events, event.Type)
		if event.Type == runtime.EventPlanModeChanged {
			return 0, errors.New("event store unavailable")
		}
		return int64(len(events)), nil
	}, nil)
	defer registry.Close()

	manager.Execute(context.Background(), registeredTestRun(t, registry), Input{Prompt: "work", Context: map[string]any{"mode": "pro"}})
	if executed.Load() {
		t.Fatal("agent executed after plan mode persistence failure")
	}
	for _, eventType := range events {
		if eventType == runtime.EventBudgetChanged {
			t.Fatalf("budget snapshot published after plan mode persistence failure: %#v", events)
		}
	}
	if store.completion.Status != "error" || store.terminal.Status != "error" {
		t.Fatalf("completion=%#v terminal=%#v", store.completion, store.terminal)
	}
}

func eventTypes(events []runtime.Event) []runtime.EventType {
	types := make([]runtime.EventType, len(events))
	for i, event := range events {
		types[i] = event.Type
	}
	return types
}

type agentFunc func(context.Context, AgentRequest) (AgentResult, error)

func (f agentFunc) Run(ctx context.Context, request AgentRequest) (AgentResult, error) {
	return f(ctx, request)
}

type preparingAgent struct{ prepared PreparedRun }

func (p preparingAgent) Run(context.Context, AgentRequest) (AgentResult, error) {
	return AgentResult{}, errors.New("unprepared agent executed")
}
func (p preparingAgent) PrepareRun() (PreparedRun, error) { return p.prepared, nil }

type testMetadataStore struct {
	history       []message.Message
	values        map[string]any
	completion    metadata.Completion
	terminal      metadata.TerminalState
	onSaveHistory func()
}

func (s *testMetadataStore) LoadHistory(context.Context, string) ([]message.Message, error) {
	return message.CloneAll(s.history), nil
}
func (s *testMetadataStore) SaveHistory(_ context.Context, _ string, messages []message.Message, replace bool) error {
	if s.onSaveHistory != nil {
		s.onSaveHistory()
	}
	if replace {
		s.history = message.CloneAll(messages)
	} else {
		s.history = append(s.history, message.CloneAll(messages)...)
	}
	return nil
}
func (s *testMetadataStore) LoadThreadValues(context.Context, string) (map[string]any, error) {
	out := make(map[string]any, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out, nil
}
func (s *testMetadataStore) SaveThreadValues(_ context.Context, _ string, values map[string]any) error {
	s.values = values
	return nil
}
func (*testMetadataStore) MarkRunRunning(context.Context, string) error { return nil }
func (s *testMetadataStore) ConfirmRunTerminal(_ context.Context, _ string, state metadata.TerminalState) (metadata.TerminalState, error) {
	s.terminal = state
	return state, nil
}
func (s *testMetadataStore) SaveCompletion(_ context.Context, completion metadata.Completion) error {
	s.completion = completion
	return nil
}

type recordingSnapshotStore struct{ save func(replay.Snapshot) error }

func (s recordingSnapshotStore) Save(_ context.Context, snapshot replay.Snapshot) error {
	return s.save(snapshot)
}
func (recordingSnapshotStore) Load(context.Context, string, string) (replay.Snapshot, error) {
	return replay.Snapshot{}, replay.ErrSnapshotNotFound
}

func newTestManager(t *testing.T, agent Agent, store metadata.Store, snapshots replay.SnapshotStore, publish func(runtime.Event) (int64, error), project func(Projection)) (*Manager, *runtime.Registry) {
	t.Helper()
	registry, err := runtime.NewRegistry(context.Background(), runtime.RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if publish == nil {
		var seq atomic.Int64
		publish = func(runtime.Event) (int64, error) { return seq.Add(1), nil }
	}
	manager, err := New(Options{
		Agent: agent, Store: store, Registry: registry, Snapshots: snapshots, Heartbeat: time.Hour,
		PublishEvent: func(_ context.Context, event runtime.Event) (int64, error) { return publish(event) },
		Project: func(_ context.Context, _ Run, projection Projection) error {
			if project != nil {
				project(projection)
			}
			return nil
		},
	})
	if err != nil {
		registry.Close()
		t.Fatal(err)
	}
	return manager, registry
}

func registeredTestRun(t *testing.T, registry *runtime.Registry) Run {
	t.Helper()
	run := Run{RunID: "run-1", ThreadID: "thread-1", AssistantID: "lead", CreatedAt: time.Now().UTC(), OnDisconnect: runtime.DisconnectContinue}
	if _, err := registry.Register(context.Background(), runtime.RunRecord{RunID: run.RunID, ThreadID: run.ThreadID, Status: runtime.StatusRunning, OnDisconnect: run.OnDisconnect}); err != nil {
		t.Fatal(err)
	}
	return run
}
