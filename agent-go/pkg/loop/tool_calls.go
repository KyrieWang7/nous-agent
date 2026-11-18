package loop

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func (r *Runner) runTools(ctx context.Context, st *lifecycle.State, calls []tool.Call, recovery *toolStepRecovery) (bool, error) {
	for _, call := range calls {
		if !r.toolAvailable(st, call.Name) {
			return false, fmt.Errorf("loop: tool %q is not available in the current capability view", call.Name)
		}
	}
	run, hasRun := runtime.RunContextFrom(ctx)
	if hasRun && run.Budget != nil {
		if err := run.Budget.Charge(runtime.BudgetAmount{ToolCalls: int64(len(calls))}); err != nil {
			if errors.Is(err, runtime.ErrBudgetExceeded) {
				return false, ErrBudgetExhausted
			}
			return false, err
		}
	}
	waitingTool := false
	if hasRun && run.StateMachine != nil && run.StateMachine.Snapshot().Phase == runtime.RunRunning {
		if err := run.StateMachine.WaitTool(); err != nil {
			return false, err
		}
		waitingTool = true
	}
	if waitingTool {
		defer func() {
			if !run.StateMachine.Snapshot().Phase.Terminal() && run.StateMachine.Snapshot().Phase != runtime.RunRunning {
				_ = run.StateMachine.Resume()
			}
		}()
	}
	recovery.dispatched = true
	_, recovery.trackedDispatch = r.cfg.Executor.(*tool.Executor)
	execCtx := tool.WithTransactionObserver(ctx, &runtimeToolTransactionObserver{run: run, recovery: recovery})
	var outcomes []tool.Outcome
	var err error
	var endTurn bool
	if native, ok := r.cfg.Executor.(*tool.Executor); ok {
		batch, batchErr := native.RunBatch(execCtx, calls, r.cfg.Lifecycle.ToolInterceptor(st))
		outcomes, err, endTurn = batch.Outcomes, batchErr, batch.EndTurn
	} else {
		outcomes, err = r.cfg.Executor.Run(execCtx, calls, r.cfg.Lifecycle.ToolInterceptor(st))
		endTurn = r.cfg.Executor.EndTurnRequested()
	}

	// Keep returned outcomes for custom executors. Native committed results were
	// already observed; step settlement appends every result in assistant order.
	for _, o := range outcomes {
		if o.Result == nil {
			continue
		}
		recovery.recordResult(toolOutcomeMessage(o), false)
	}

	if err != nil {
		return false, err
	}
	if endTurn || st.Take() == lifecycle.DirectiveStop {
		return true, nil
	}
	return false, nil
}

func (r *Runner) toolAvailable(st *lifecycle.State, name string) bool {
	if !slices.Contains(st.ToolSet, name) {
		return false
	}
	definition, err := r.cfg.Registry.Get(name)
	if err != nil || !definition.Deferred {
		return err == nil
	}
	return slices.Contains(st.DisclosedTools, name)
}

func toolCallsOf(resp *model.Response) []tool.Call {
	if resp == nil || len(resp.Message.ToolCalls) == 0 {
		return nil
	}
	out := make([]tool.Call, len(resp.Message.ToolCalls))
	for i, tc := range resp.Message.ToolCalls {
		out[i] = tool.Call{ID: tc.ID, Name: tc.Name, Args: tc.Arguments}
	}
	return out
}

func normalizeToolCallIDs(resp *model.Response, runID string, iteration int) error {
	if resp == nil {
		return errors.New("loop: sampler returned no response")
	}
	seen := make(map[string]struct{}, len(resp.Message.ToolCalls))
	for i := range resp.Message.ToolCalls {
		call := &resp.Message.ToolCalls[i]
		if call.ID == "" {
			call.ID = fmt.Sprintf("%s:tool:%d:%d", runID, iteration, i)
		}
		if _, duplicate := seen[call.ID]; duplicate {
			return fmt.Errorf("loop: model returned duplicate tool call id %q", call.ID)
		}
		seen[call.ID] = struct{}{}
	}
	return nil
}
