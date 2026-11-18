package loop

import (
	"context"
	"errors"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
)

// agentDriver owns one turn, its step admission and terminal cleanup.
// Runner remains reusable: execution state is never stored on the shared runner.
type agentDriver struct {
	runner           *Runner
	ctx              context.Context
	state            *lifecycle.State
	canonical        *message.History
	checkpoint       func(context.Context, []message.Message) error
	pullInputs       func(context.Context) ([]message.Message, func(context.Context, []message.Message) error, error)
	commitInputs     func(context.Context, []message.Message) error
	maxTokens        bool
	blocked          bool
	tracker          *Tracker
	result           *Result
	stopReinjections int
}

type stepDecision uint8

const (
	stepEnter stepDecision = iota
	stepContinue
	stepStop
)

// run is the outer Agent driver. preStep assembles the next request context;
// step owns one model response and all tool outcomes before the next boundary.
func (d *agentDriver) run() (_ *Result, runErr error) {
	ctx, r, st, res := d.ctx, d.runner, d.state, d.result
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if err := d.boundary(ctx, runtime.EventAgentTurnStart, "", nil); err != nil {
		return res, err
	}
	defer func() {
		runErr = errors.Join(runErr, r.cfg.Lifecycle.Execute(context.WithoutCancel(ctx), lifecycle.StageAfterAgent, st))
		r.syncSpend(ctx, d.tracker)
		res.Compacted = st.Compacted && d.canonical == nil
		res.CostMicros = d.tracker.CostMicros()
		res.Values = cloneValues(st.Values)
		res.RiskLevel = st.RiskLevel
		reason := "completed"
		if d.blocked {
			reason = "blocked"
		}
		if d.maxTokens {
			reason = "max-tokens"
		}
		if runErr != nil {
			reason = "failed"
		}
		if ctx.Err() != nil {
			reason = "canceled"
		}
		res.EndReason = reason
		runErr = errors.Join(runErr, d.boundary(context.WithoutCancel(ctx), runtime.EventAgentTurnEnd, reason, runErr))
	}()
	if err := r.cfg.Lifecycle.Execute(ctx, lifecycle.StageBeforeAgent, st); err != nil {
		return res, err
	}
	if st.Take() == lifecycle.DirectiveStop {
		d.blocked = true
		return res, nil
	}
	for iteration := 0; ; iteration++ {
		st.Iteration = iteration
		res.Iterations = iteration + 1
		if err := ctx.Err(); err != nil {
			res.Iterations = iteration
			return res, err
		}
		r.syncSpend(ctx, d.tracker)
		if err := d.tracker.Check(iteration); err != nil {
			res.Iterations = iteration
			return res, err
		}
		decision, err := d.preStep()
		if err != nil {
			return res, err
		}
		switch decision {
		case stepContinue:
			continue
		case stepStop:
			return res, nil
		}
		decision, err = d.runStep()
		if err != nil {
			return res, err
		}
		if decision == stepStop {
			return res, nil
		}
	}
}

// runStep brackets an admitted step. step's own deferred tool recovery completes
// before step/end, including cancellation, journal failure and policy errors.
func (d *agentDriver) runStep() (_ stepDecision, stepErr error) {
	if err := d.boundary(d.ctx, runtime.EventAgentStepStart, "", nil); err != nil {
		return stepStop, err
	}
	defer func() {
		if d.checkpoint != nil && d.canonical == nil {
			stepErr = errors.Join(stepErr, d.checkpoint(context.WithoutCancel(d.ctx), d.state.History.All()))
		}
		stepErr = errors.Join(stepErr, d.boundary(context.WithoutCancel(d.ctx), runtime.EventAgentStepEnd, "", stepErr))
	}()
	return d.step()
}
