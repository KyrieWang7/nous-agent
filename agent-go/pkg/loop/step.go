package loop

import (
	"errors"
	"fmt"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
)

func (d *agentDriver) preStep() (stepDecision, error) {
	ctx := d.ctx
	var err error
	if d.pullInputs != nil && d.commitInputs == nil {
		inputs, commit, pullErr := d.pullInputs(ctx)
		if pullErr != nil {
			return stepStop, pullErr
		}
		if len(inputs) > 0 {
			if d.canonical == nil {
				d.canonical = d.state.History
				proposed := message.NewHistory()
				proposed.Load(d.canonical.All())
				d.state.History = proposed
			}
			d.state.History.AppendAll(inputs)
		}
		// Initial queued input is already proposed by Request.Prompt; it still
		// needs an atomic ACK even when no steering messages were appended.
		d.commitInputs = commit
	}
	if d.state.History.Len() == 0 {
		return stepStop, errors.New("loop: request requires a prompt or a non-empty inbox/history")
	}
	// Assemble the next step from canonical history and current capabilities.
	if err := d.runner.compact(ctx, d.state); err != nil {
		return stepStop, err
	}
	// Resolve history projection and the current tool surface.
	d.state.ModelInput, err = d.runner.buildRequest(ctx, d.state)
	if err != nil {
		return stepStop, err
	}

	d.state.ModelHistory = message.CloneAll(d.state.ModelInput.Messages)
	d.state.RebuildModelHistory = func() []message.Message {
		messages := d.state.History.All()
		if d.runner.cfg.Trimmer != nil {
			messages = d.runner.cfg.Trimmer.Trim(messages)
		}
		return messages
	}

	// Run the fixed pre-step policy and context stages.
	beforeModelWatermark := d.state.History.Watermark()
	if err := d.runner.cfg.Lifecycle.Execute(ctx, lifecycle.StageBeforeModel, d.state); err != nil {
		return stepStop, err
	}
	// Only genuine canonical appends belong to the projection. Searching
	// the full history by value confuses trimmed-out messages with transient
	// reinsertions and loses duplicate occurrences.
	d.state.ModelHistory = append(d.state.ModelHistory, d.state.History.Since(beforeModelWatermark)...)

	switch d.state.Take() {
	case lifecycle.DirectiveStop:
		d.blocked = true
		if d.state.ModelOutput != nil {
			d.result.Response = d.state.ModelOutput
			d.result.StopReason = d.state.ModelOutput.StopReason
			d.result.Output = d.state.ModelOutput.Message.Content
			if d.runner.cfg.Publisher != nil {
				d.runner.cfg.Publisher.PublishReply(ctx, d.state, false)
			}
		}
		return stepStop, nil
	case lifecycle.DirectiveContinue:
		return stepContinue, nil
	}

	return stepEnter, nil
}

// step executes one accepted model/tool step. Settlement completes before
// the driver admits another step, even after governance, cancellation or failure.
func (d *agentDriver) step() (_ stepDecision, stepErr error) {
	ctx, r, st, tracker := d.ctx, d.runner, d.state, d.tracker
	if err := d.prepareRequest(); err != nil {
		return stepStop, err
	}
	var pending *toolStepRecovery
	defer func() {
		if err := pending.settle(ctx, st.History); err != nil {
			stepErr = errors.Join(stepErr, err)
		}
	}()
	// Produce one assistant response for the admitted step.
	resp, streamed, err := r.cfg.Sampler.Sample(ctx, st)
	if err != nil {
		return stepStop, err
	}
	if err := normalizeToolCallIDs(resp, st.RunID, st.Iteration); err != nil {
		return stepStop, err
	}
	st.ModelOutput = resp
	st.Streamed = streamed
	st.OriginalOutput = resp.Message.Content
	d.result.Streamed = d.result.Streamed || streamed
	d.result.Response = resp
	d.result.StopReason = resp.StopReason
	d.maxTokens = d.maxTokens || resp.StopReason == model.StopReasonLength
	// Record every successful provider response immediately after normalizing
	// its tool-call identity. Later budget or governance failures must not leave
	// an input commit without the corresponding output fact.
	if err := commitModelOutput(ctx, st); err != nil {
		return stepStop, err
	}
	d.result.Usage = d.result.Usage.Add(resp.Usage)
	r.observeLeadUsage(ctx, resp)

	// Admit the assistant response into the canonical transcript.
	pending = newToolStepRecovery(ctx, st.History)
	st.History.Append(resp.Message)
	// Persist the assistant's tool intent before any body can have side effects.
	// Recovery can mark unanswered calls unknown after a worker crash.
	if d.checkpoint != nil {
		if err := d.checkpoint(ctx, st.History.All()); err != nil {
			return stepStop, err
		}
	}
	if err := r.chargeModelBudget(ctx, resp); err != nil {
		return stepStop, err
	}

	// Apply the fixed post-model policy stages before publishing.
	if err := r.cfg.Lifecycle.Execute(ctx, lifecycle.StageAfterModel, st); err != nil {
		return stepStop, err
	}
	directive := st.Take()
	r.syncSpend(ctx, tracker)

	// Publish the policy-approved response.
	d.result.Output = st.ModelOutput.Message.Content
	if r.cfg.Publisher != nil {
		r.cfg.Publisher.PublishReply(ctx, st, streamed)
	}

	// A provider output ceiling concludes the turn, even if the partial output
	// contains tool calls or a policy asks to continue. Recovery settles those
	// calls without executing a possibly truncated request.
	if d.maxTokens {
		return stepStop, nil
	}

	switch directive {
	case lifecycle.DirectiveStop:
		return stepStop, nil
	case lifecycle.DirectiveContinue:
		return stepContinue, nil
	}

	// Settle tool dispatch before the driver admits another step.
	if calls := toolCallsOf(st.ModelOutput); len(calls) > 0 {
		if err := tracker.Check(st.Iteration); err != nil {
			return stepStop, err
		}
		stop, err := r.runTools(ctx, st, calls, pending)
		if err != nil {
			return stepStop, err
		}
		if stop {
			return stepStop, nil
		}
		return stepContinue, nil
	}

	// The turn ends only after the stop policy admits it.
	blocking, err := r.evaluateStop(ctx, st)
	if err != nil {
		return stepStop, err
	}
	if blocking != "" {
		d.stopReinjections++
		if d.stopReinjections > r.stopReinjectionLimit() {
			return stepEnter, fmt.Errorf("loop: stop blocked: %s", blocking)
		}
		st.History.Append(message.Message{
			Role:    message.RoleUser,
			Content: fmt.Sprintf("[System] 停止被拦截：%s。请先处理该问题。", blocking),
		})
		return stepContinue, nil
	}

	return stepStop, nil
}
