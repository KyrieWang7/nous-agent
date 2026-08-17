package runtime_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

func TestRunStateMachineEnforcesLifecycle(t *testing.T) {
	m, err := runtime.NewRunStateMachine("run-1", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Complete("too early"); !errors.Is(err, runtime.ErrInvalidRunTransition) {
		t.Fatalf("early completion error = %v", err)
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	if err := m.WaitTool(); err != nil {
		t.Fatal(err)
	}
	if err := m.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := m.WaitApproval("approval-1"); err != nil {
		t.Fatal(err)
	}
	if got := m.Snapshot(); got.Phase != runtime.RunWaitingApproval || got.Version != 4 || got.ApprovalID != "approval-1" {
		t.Fatalf("waiting snapshot = %+v", got)
	}
	if err := m.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := m.Complete("done"); err != nil {
		t.Fatal(err)
	}
	if err := m.Cancel("late"); !errors.Is(err, runtime.ErrRunAlreadyTerminal) {
		t.Fatalf("terminal cancellation error = %v", err)
	}
}

func TestRunStateMachineDoesNotCommitWhenCanonicalPersistenceFails(t *testing.T) {
	machine, err := runtime.NewRunStateMachine("run-1", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	machine.SetObserver(func(runtime.RunSnapshot) error {
		return errors.New("event store unavailable")
	})

	if err := machine.Start(); err == nil {
		t.Fatal("Start() ignored canonical persistence failure")
	}
	if got := machine.Snapshot(); got.Phase != runtime.RunPending || got.Version != 0 {
		t.Fatalf("state changed after failed persistence = %+v", got)
	}
}

func TestRunStateMachineRestoresToolWaitAfterApproval(t *testing.T) {
	m, err := runtime.NewRunStateMachine("run-1", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	if err := m.WaitTool(); err != nil {
		t.Fatal(err)
	}
	if err := m.WaitApproval("approval-1"); err != nil {
		t.Fatal(err)
	}
	if err := m.WaitTool(); err != nil {
		t.Fatal(err)
	}
	if got := m.Snapshot(); got.Phase != runtime.RunWaitingTool || got.ApprovalID != "" {
		t.Fatalf("restored tool state = %+v", got)
	}
	if err := m.Resume(); err != nil {
		t.Fatal(err)
	}
}

func TestRunStateMachineSeparatesUserQuestionsFromApprovals(t *testing.T) {
	m, err := runtime.NewRunStateMachine("run-question", "thread")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	if err := m.WaitTool(); err != nil {
		t.Fatal(err)
	}
	if err := m.WaitUser("question-1"); err != nil {
		t.Fatal(err)
	}
	snapshot := m.Snapshot()
	if snapshot.Phase != runtime.RunWaitingUser || snapshot.QuestionID != "question-1" || snapshot.ApprovalID != "" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if err := m.WaitTool(); err != nil {
		t.Fatal(err)
	}
	if snapshot = m.Snapshot(); snapshot.QuestionID != "" || snapshot.Phase != runtime.RunWaitingTool {
		t.Fatalf("resumed snapshot=%#v", snapshot)
	}
}

func TestRestoreRunStateMachineContinuesVersionedLifecycle(t *testing.T) {
	snapshot := runtime.RunSnapshot{
		RunID: "run-restored", ThreadID: "thread", Phase: runtime.RunWaitingApproval,
		Version: 4, ApprovalID: "approval-1", UpdatedAt: time.Now().UTC(),
	}
	machine, err := runtime.RestoreRunStateMachine(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.Interrupt("process restarted"); err != nil {
		t.Fatal(err)
	}
	got := machine.Snapshot()
	if got.Phase != runtime.RunInterrupted || got.Version != 5 || got.ApprovalID != "" {
		t.Fatalf("restored terminal snapshot = %+v", got)
	}
}

func TestToolTransactionIsIdempotentAndRejectsConflicts(t *testing.T) {
	tx, err := runtime.NewToolTransaction("tx-1", "run-1", runtime.ToolInvocation{ID: "call-1", Name: "echo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Deny("needs approval"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Deny("needs approval"); err != nil {
		t.Fatalf("same denial should be idempotent: %v", err)
	}
	if err := tx.Deny("different reason"); !errors.Is(err, runtime.ErrInvalidToolTransition) {
		t.Fatalf("conflicting denial error = %v", err)
	}
	if got := tx.Snapshot(); got.State != runtime.ToolDenied || got.Error != "needs approval" {
		t.Fatalf("transaction snapshot = %+v", got)
	}
}

func TestBudgetLedgerAggregatesChildSpend(t *testing.T) {
	root := runtime.NewBudgetLedger(runtime.BudgetAmount{Tokens: 10, CostMicros: 100, ToolCalls: 2, Subagents: 1})
	child, err := root.Child(runtime.BudgetAmount{Tokens: 8, CostMicros: 80, ToolCalls: 2, Subagents: 1})
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := child.Reserve(runtime.BudgetAmount{Tokens: 6, CostMicros: 60, ToolCalls: 1, Subagents: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := root.Remaining(); got.Tokens != 4 || got.CostMicros != 40 || got.ToolCalls != 1 || got.Subagents != 0 {
		t.Fatalf("root remaining while reserved = %+v", got)
	}
	if err := reservation.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := root.Used(); got.Tokens != 6 || got.CostMicros != 60 || got.ToolCalls != 1 || got.Subagents != 1 {
		t.Fatalf("root used = %+v", got)
	}
	if _, err := child.Reserve(runtime.BudgetAmount{Tokens: 3, CostMicros: 1}); !errors.Is(err, runtime.ErrBudgetExceeded) {
		t.Fatalf("child overage error = %v", err)
	}
	if _, err := root.Reserve(runtime.BudgetAmount{Tokens: 5, CostMicros: 1}); !errors.Is(err, runtime.ErrBudgetExceeded) {
		t.Fatalf("root aggregate overage error = %v", err)
	}
}

func TestBudgetLedgerPublishesRootCheckpointAndRestores(t *testing.T) {
	root := runtime.NewBudgetLedger(runtime.BudgetAmount{Tokens: 10, ToolCalls: 2})
	checkpoints := make(chan runtime.BudgetSnapshot, 1)
	root.SetObserver(func(snapshot runtime.BudgetSnapshot) error {
		checkpoints <- snapshot
		return nil
	})
	child, err := root.Child(runtime.BudgetAmount{Tokens: 8, ToolCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Charge(runtime.BudgetAmount{Tokens: 6, ToolCalls: 1}); err != nil {
		t.Fatal(err)
	}
	snapshot := <-checkpoints
	if snapshot.Version != 1 || snapshot.Used.Tokens != 6 || snapshot.Used.ToolCalls != 1 {
		t.Fatalf("budget checkpoint = %+v", snapshot)
	}
	restored, err := runtime.RestoreBudgetLedger(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Charge(runtime.BudgetAmount{Tokens: 5}); !errors.Is(err, runtime.ErrBudgetExceeded) {
		t.Fatalf("restored overage error = %v", err)
	}
	if err := restored.Charge(runtime.BudgetAmount{Tokens: 4, ToolCalls: 1}); err != nil {
		t.Fatalf("restored remaining budget was not usable: %v", err)
	}
}

func TestBudgetLedgerRejectsChargeWhenCheckpointPersistenceFails(t *testing.T) {
	root := runtime.NewBudgetLedger(runtime.BudgetAmount{Tokens: 10})
	root.SetObserver(func(runtime.BudgetSnapshot) error {
		return errors.New("event store unavailable")
	})

	err := root.Charge(runtime.BudgetAmount{Tokens: 4})
	if err == nil || !strings.Contains(err.Error(), "persisting budget checkpoint") {
		t.Fatalf("Charge() error = %v", err)
	}
	if got := root.Used(); got != (runtime.BudgetAmount{}) {
		t.Fatalf("usage changed after failed checkpoint = %#v", got)
	}
	if got := root.Remaining().Tokens; got != 10 {
		t.Fatalf("reservation leaked after failed checkpoint: remaining tokens = %d", got)
	}
}

func TestApprovalManagerPersistsDecisionAndExpiry(t *testing.T) {
	ctx := context.Background()
	manager, err := runtime.NewApprovalManager(runtime.NewMemoryApprovalStore())
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create(ctx, runtime.ApprovalRequest{
		ID: "approval-1", RunID: "run-1", TransactionID: "tx-1", ToolName: "bash",
		ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil || created.Status != runtime.ApprovalPending {
		t.Fatalf("created = %+v, err = %v", created, err)
	}
	decided, err := manager.Decide(ctx, "approval-1", true, "user-1")
	if err != nil || decided.Status != runtime.ApprovalApproved || decided.DecisionBy != "user-1" {
		t.Fatalf("decided = %+v, err = %v", decided, err)
	}
	again, err := manager.Decide(ctx, "approval-1", false, "user-2")
	if err != nil || again.Status != runtime.ApprovalApproved {
		t.Fatalf("terminal decision changed = %+v, err = %v", again, err)
	}

	_, err = manager.Create(ctx, runtime.ApprovalRequest{
		ID: "approval-2", RunID: "run-1", TransactionID: "tx-2", ToolName: "rm",
		ExpiresAt: time.Now().Add(-time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	expired, err := manager.Get(ctx, "approval-2")
	if err != nil || expired.Status != runtime.ApprovalExpired {
		t.Fatalf("expired = %+v, err = %v", expired, err)
	}
}
