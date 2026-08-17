package permission

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

// DurablePrompter connects policy confirmation to ApprovalManager. The run
// pauses in waiting_approval while another process or transport records the
// decision.
type DurablePrompter struct {
	Manager      *runtime.ApprovalManager
	TTL          time.Duration
	PollInterval time.Duration
}

func (p *DurablePrompter) Confirm(ctx context.Context, confirm ConfirmRequest) (bool, error) {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.RunID == "" {
		return false, errors.New("permission: durable approval requires a run context")
	}
	var manager *runtime.ApprovalManager
	var ttl, pollInterval time.Duration
	if p != nil {
		manager = p.Manager
		ttl = p.TTL
		pollInterval = p.PollInterval
	}
	if manager == nil {
		manager = run.Approvals
	}
	if manager == nil {
		return false, errors.New("permission: durable approval manager is not configured")
	}
	approvalID := durableApprovalID(run.RunID, confirm)
	txID := run.RunID + ":tool:" + confirm.ToolCallID
	if confirm.ToolCallID == "" {
		txID = "approval:" + approvalID
	}
	expires := time.Time{}
	if ttl > 0 {
		expires = time.Now().UTC().Add(ttl)
	}
	approval, err := manager.Create(ctx, runtime.ApprovalRequest{
		ID: approvalID, RunID: run.RunID, TransactionID: txID,
		ToolName: confirm.ToolName, Args: append([]byte(nil), confirm.Args...),
		Reason: confirm.Reason, ExpiresAt: expires,
	})
	if err != nil && !errors.Is(err, runtime.ErrApprovalExists) {
		return false, err
	}
	if errors.Is(err, runtime.ErrApprovalExists) {
		approval, err = manager.Get(ctx, approvalID)
		if err != nil {
			return false, err
		}
	}
	if approval.Status == runtime.ApprovalApproved {
		return true, nil
	}
	if approval.Status != runtime.ApprovalPending {
		return false, nil
	}
	if run.StateMachine == nil {
		return false, errors.New("permission: durable approval requires a run state machine")
	}
	previousPhase := run.StateMachine.Snapshot().Phase
	if err := run.StateMachine.WaitApproval(approvalID); err != nil {
		return false, err
	}
	if err := publishApproval(ctx, run, runtime.EventApprovalRequested, approval, approvalID+":requested"); err != nil {
		return false, err
	}
	decision, err := manager.Wait(ctx, approvalID, pollInterval)
	if err != nil {
		return false, err
	}
	if !run.StateMachine.Snapshot().Phase.Terminal() {
		var stateErr error
		if previousPhase == runtime.RunWaitingTool {
			stateErr = run.StateMachine.WaitTool()
		} else {
			stateErr = run.StateMachine.Resume()
		}
		if stateErr != nil {
			return false, stateErr
		}
	}
	if err := publishApproval(context.WithoutCancel(ctx), run, runtime.EventApprovalResolved, decision, approvalID+":resolved"); err != nil {
		return false, err
	}
	return decision.Status == runtime.ApprovalApproved, nil
}

func durableApprovalID(runID string, confirm ConfirmRequest) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s", runID, confirm.ToolCallID, confirm.ToolName, confirm.Args)))
	return "approval-" + hex.EncodeToString(sum[:12])
}

func publishApproval(ctx context.Context, run runtime.RunContext, typ runtime.EventType, approval runtime.ApprovalRequest, key string) error {
	e := runtime.MustEvent(run.EventStreamRunID(), run.ThreadID, typ, approval)
	e.IdempotencyKey = key
	if run.Publish != nil {
		_, err := run.Publish(ctx, e)
		return err
	}
	if run.Bus != nil {
		run.Bus.Publish(ctx, e)
	}
	return nil
}
