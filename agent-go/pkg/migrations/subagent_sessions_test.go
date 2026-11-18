//go:build docker

package migrations

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/harness"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/delegation"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	runtimepostgres "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/postgres"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/subagent"
	platformdb "github.com/KyrieWang7/nous-agent/platform-go/postgres"
)

type sessionSampler func(context.Context, *lifecycle.State) (*model.Response, bool, error)

func (f sessionSampler) Sample(ctx context.Context, st *lifecycle.State) (*model.Response, bool, error) {
	return f(ctx, st)
}

func TestDurableSubagentSessionsFenceRestartAndAdmitInbox(t *testing.T) {
	url, pool := startPostgres(t)
	if err := Up(url, migrationDir(t)); err != nil {
		t.Fatal(err)
	}
	db, err := platformdb.FromPool(pool)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err = pool.Exec(ctx, `INSERT INTO agent_thread(id) VALUES('child-thread')`); err != nil {
		t.Fatal(err)
	}
	store, err := runtimepostgres.NewSubagentStore(db)
	if err != nil {
		t.Fatal(err)
	}

	// Equal provider tool IDs from different parent runs never overwrite.
	for _, parent := range []string{"parent-a", "parent-b"} {
		if err = store.Put(ctx, delegation.Result{ThreadID: "child-thread", ParentRunID: parent, TaskID: "same-call", Output: parent}, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.Get(ctx, "same-call"); err == nil {
		t.Fatal("ambiguous task ID was accepted")
	}
	for _, parent := range []string{"parent-a", "parent-b"} {
		got, e := store.GetScoped(ctx, "child-thread", parent, "same-call")
		if e != nil || got.Output != parent {
			t.Fatalf("scoped result=%+v err=%v", got, e)
		}
	}

	// Expired owners cannot heartbeat, commit inputs or overwrite a takeover.
	session, err := store.Acquire(ctx, delegation.Session{ID: "lease-session", ThreadID: "child-thread", Result: delegation.Result{Status: "running"}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Acquire(ctx, session, time.Minute); !errors.Is(err, delegation.ErrBusy) {
		t.Fatalf("concurrent acquire=%v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE agent_subagent_session SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, session.ID); err != nil {
		t.Fatal(err)
	}
	stale := session
	recovered, err := store.LoadSession(ctx, "child-thread", session.ID)
	if err != nil || recovered.Result.Status != "interrupted" {
		t.Fatalf("recovery=%+v err=%v", recovered, err)
	}
	recovered.Result.Status = "running"
	session, err = store.Acquire(ctx, recovered, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Checkpoint(ctx, stale); !errors.Is(err, delegation.ErrLeaseLost) {
		t.Fatalf("stale checkpoint=%v", err)
	}
	if _, err = store.Heartbeat(ctx, stale.ID, stale.Token, time.Minute); !errors.Is(err, delegation.ErrLeaseLost) {
		t.Fatalf("stale heartbeat=%v", err)
	}
	if _, err = store.LoadSession(ctx, "another-thread", session.ID); !errors.Is(err, delegation.ErrNotFound) {
		t.Fatalf("cross-thread read=%v", err)
	}

	// Enqueue is idempotent; ACK and history commit are one transaction.
	input := delegation.InboxMessage{ID: "message-1", Content: "steer"}
	for i := 0; i < 2; i++ {
		if _, err = store.Enqueue(ctx, "child-thread", session.ID, input); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := store.Pending(ctx, session.ID, session.Token)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	if err = store.CommitInputs(ctx, stale, []string{input.ID}); !errors.Is(err, delegation.ErrLeaseLost) {
		t.Fatalf("stale ACK=%v", err)
	}
	session.History = []message.Message{{Role: message.RoleUser, Content: input.Content}}
	if err = store.CommitInputs(ctx, session, []string{input.ID}); err != nil {
		t.Fatal(err)
	}
	inputs, err := store.ListInputs(ctx, "child-thread", session.ID)
	if err != nil || len(inputs) != 1 || inputs[0].Status != "consumed" {
		t.Fatalf("inputs=%v err=%v", inputs, err)
	}

	// A newly assembled manager restores the child transcript from PostgreSQL.
	definition := subagent.Definition{Name: "worker", Description: "test worker", MaxTurns: 4}
	capabilities := capability.NewRegistry()
	if err = capability.RegisterValue(capabilities, capability.Value{Definition: capability.Definition{Name: "agent.worker", Kind: capability.KindAgent, Scope: capability.ScopeRun}, Value: definition}); err != nil {
		t.Fatal(err)
	}
	view, err := capability.NewView(capabilities.Snapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	parentContext := func(id string) context.Context {
		state, _ := runtime.NewRunStateMachine(id, "child-thread")
		_ = state.Start()
		return runtime.WithRunContext(ctx, runtime.RunContext{RunID: id, ThreadID: "child-thread", StateMachine: state, Budget: runtime.NewBudgetLedger(runtime.BudgetAmount{}), Capabilities: view})
	}
	makeManager := func(expect string) *subagent.Manager {
		m := subagent.NewManagerWithPreparedFactory(func(subagent.Definition, []string) (subagent.PreparedRunner, error) {
			h, e := harness.New(harness.Options{Sampler: sessionSampler(func(_ context.Context, st *lifecycle.State) (*model.Response, bool, error) {
				raw := ""
				for _, msg := range st.ModelInput.Messages {
					raw += msg.Content + "\n"
				}
				if expect != "" && !strings.Contains(raw, expect) {
					t.Errorf("missing restored history %q in %q", expect, raw)
				}
				return &model.Response{StopReason: model.StopReasonStop, Message: message.Message{Role: message.RoleAssistant, Content: "persisted answer"}}, false, nil
			})})
			if e != nil {
				return subagent.PreparedRunner{}, e
			}
			return subagent.PreparedRunner{Runner: h.Runner(), Release: h.Close}, nil
		}, store, time.Hour)
		if e := m.Register(definition); e != nil {
			t.Fatal(e)
		}
		return m
	}
	first := makeManager("")
	result, err := first.Dispatch(parentContext("initial-parent"), subagent.DispatchRequest{SubagentType: "worker", Prompt: "remember durable fact"})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID == "" {
		t.Fatal("missing durable session ID")
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	second := makeManager("remember durable fact")
	defer second.Close()
	result2, err := second.Resume(parentContext("next-parent"), delegation.Request{SessionID: result.SessionID, Prompt: "continue"})
	if err != nil {
		t.Fatal(err)
	}
	if result2.SessionID != result.SessionID {
		t.Fatal("continuation created another session")
	}
	saved, err := store.LoadSession(ctx, "child-thread", result.SessionID)
	if err != nil || len(saved.History) != 4 || saved.Result.Status != "completed" {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
}

var _ loop.Sampler = sessionSampler(nil)
