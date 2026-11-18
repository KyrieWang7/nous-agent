package loop

import (
	"context"
	"errors"
	"fmt"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

func prepareRunContext(ctx context.Context, req Request) (context.Context, error) {
	run, _ := runtime.RunContextFrom(ctx)
	if run.RunID == "" {
		run.RunID = req.RunID
	}
	if run.ThreadID == "" {
		run.ThreadID = req.ThreadID
	}
	if run.RunID == "" || run.ThreadID == "" {
		return ctx, errors.New("loop: request requires run_id and thread_id")
	}
	if run.RunID != req.RunID || run.ThreadID != req.ThreadID {
		return ctx, fmt.Errorf("loop: run context identity %q/%q does not match request %q/%q", run.RunID, run.ThreadID, req.RunID, req.ThreadID)
	}
	if run.StateMachine == nil {
		state, err := runtime.NewRunStateMachine(run.RunID, run.ThreadID)
		if err != nil {
			return ctx, err
		}
		if err := state.Start(); err != nil {
			return ctx, err
		}
		run.StateMachine = state
	}
	snapshot := run.StateMachine.Snapshot()
	if snapshot.RunID != req.RunID || snapshot.ThreadID != req.ThreadID {
		return ctx, fmt.Errorf("loop: run state identity %q/%q does not match request %q/%q", snapshot.RunID, snapshot.ThreadID, req.RunID, req.ThreadID)
	}
	if snapshot.Phase != runtime.RunRunning {
		return ctx, fmt.Errorf("loop: run state must be %s, got %s", runtime.RunRunning, snapshot.Phase)
	}
	run.ExecutionContext = ctx
	run.DelegationHistory = message.CloneAll(req.History.All())
	return runtime.WithRunContext(ctx, run), nil
}

func (r *Runner) syncSpend(ctx context.Context, tracker *Tracker) {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Journal == nil {
		return
	}
	tokens, cost := run.Journal.Spend()
	tracker.ObserveSpend(tokens, cost)
}

func (r *Runner) installBudget(ctx context.Context) (context.Context, error) {
	run, _ := runtime.RunContextFrom(ctx)
	limits := runtime.BudgetAmount{
		Tokens:     int64(r.cfg.Limits.MaxTokens),
		CostMicros: r.cfg.Limits.MaxCostMicros,
		ToolCalls:  int64(r.cfg.Limits.MaxToolCalls),
		Subagents:  int64(r.cfg.Limits.MaxSubagents),
	}
	var budget *runtime.BudgetLedger
	var err error
	if run.Budget != nil {
		budget, err = run.Budget.Child(limits)
	} else {
		budget = runtime.NewBudgetLedger(limits)
	}
	if err != nil {
		return ctx, err
	}
	run.Budget = budget
	return runtime.WithRunContext(ctx, run), nil
}

func (r *Runner) chargeModelBudget(ctx context.Context, response *model.Response) error {
	if response == nil {
		return nil
	}
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Budget == nil {
		return nil
	}
	amount := runtime.BudgetAmount{Tokens: int64(response.Usage.TotalTokens())}
	if run.Journal != nil {
		amount.CostMicros = run.Journal.EstimateCost(response.ModelName, response.Usage)
	}
	if err := run.Budget.Charge(amount); err != nil {
		if errors.Is(err, runtime.ErrBudgetExceeded) {
			return ErrBudgetExhausted
		}
		return err
	}
	return nil
}

func (r *Runner) observeLeadUsage(ctx context.Context, response *model.Response) {
	if response == nil {
		return
	}
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Journal == nil {
		return
	}
	run.Journal.Observe(runtime.Entry{
		Bucket: runtime.BucketLead, Source: "lead", CallID: response.CallID,
		ModelName: response.ModelName, Usage: response.Usage,
	})
}
