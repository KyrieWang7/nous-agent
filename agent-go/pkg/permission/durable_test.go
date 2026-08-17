package permission_test

import (
	"context"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/permission"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

func TestDurablePrompterPausesAndResumesRun(t *testing.T) {
	manager, err := runtime.NewApprovalManager(runtime.NewMemoryApprovalStore())
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
	bus := runtime.NewMemoryBus(runtime.BusOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx = runtime.WithRunContext(ctx, runtime.RunContext{RunID: "run-1", ThreadID: "thread-1", StateMachine: state, Bus: bus})
	prompter := &permission.DurablePrompter{Manager: manager, TTL: time.Minute, PollInterval: time.Millisecond}
	type outcome struct {
		approved bool
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		approved, err := prompter.Confirm(ctx, permission.ConfirmRequest{ToolCallID: "call-1", ToolName: "bash", Reason: "side effect"})
		done <- outcome{approved: approved, err: err}
	}()

	var approvalID string
	deadline := time.After(500 * time.Millisecond)
	for approvalID == "" {
		select {
		case <-deadline:
			t.Fatal("run never entered waiting_approval")
		default:
			snapshot := state.Snapshot()
			if snapshot.Phase == runtime.RunWaitingApproval {
				approvalID = snapshot.ApprovalID
			} else {
				time.Sleep(time.Millisecond)
			}
		}
	}
	if _, err := manager.Decide(ctx, approvalID, true, "user-1"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || !got.approved {
			t.Fatalf("confirmation = %v, %v", got.approved, got.err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("confirmation did not resume")
	}
	if got := state.Snapshot().Phase; got != runtime.RunRunning {
		t.Fatalf("run phase = %s, want running", got)
	}
	events := bus.Backlog("run-1", 0)
	if len(events) != 2 || events[0].Type != runtime.EventApprovalRequested || events[1].Type != runtime.EventApprovalResolved {
		t.Fatalf("approval events = %+v", events)
	}
}
