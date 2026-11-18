package loop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
)

func (r *Runner) seed(st *lifecycle.State, req Request) error {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" && len(req.ContentBlocks) == 0 {
		if st.History.Len() == 0 && req.PullInputs == nil {
			return errors.New("loop: request requires a prompt or a non-empty history")
		}
		return nil
	}

	st.History.Append(message.Message{
		Role:          message.RoleUser,
		Content:       prompt,
		ContentBlocks: req.ContentBlocks,
	})
	return nil
}

func (r *Runner) compact(ctx context.Context, st *lifecycle.State) error {
	if sampler, ok := r.cfg.Sampler.(interface{ ManagesCompaction() bool }); ok && sampler.ManagesCompaction() {
		return nil
	}
	if r.cfg.Compactor == nil {
		return nil
	}
	run, hasRun := runtime.RunContextFrom(ctx)
	trackState := false
	if decider, ok := r.cfg.Compactor.(CompactionDecider); ok && decider.ShouldCompact(st.History) && hasRun && run.StateMachine != nil && run.StateMachine.Snapshot().Phase == runtime.RunRunning {
		if err := run.StateMachine.BeginCompaction(); err != nil {
			return fmt.Errorf("loop: beginning compaction: %w", err)
		}
		trackState = true
		if err := publishCompactionEvent(ctx, run, runtime.EventCompactionStart, st.Iteration, map[string]any{"iteration": st.Iteration}); err != nil {
			return err
		}
	}
	compacted, err := r.cfg.Compactor.MaybeCompact(ctx, st.History)
	if trackState {
		payload := map[string]any{"iteration": st.Iteration, "compacted": compacted}
		if err != nil {
			payload["error"] = err.Error()
		}
		if publishErr := publishCompactionEvent(context.WithoutCancel(ctx), run, runtime.EventCompactionComplete, st.Iteration, payload); publishErr != nil && err == nil {
			err = publishErr
		}
		if !run.StateMachine.Snapshot().Phase.Terminal() {
			if resumeErr := run.StateMachine.Resume(); resumeErr != nil && err == nil {
				err = resumeErr
			}
		}
	}
	if err != nil {
		return fmt.Errorf("loop: compaction: %w", err)
	}
	// 标志一旦置位就保持：持久化层要知道"本回合内发生过压缩"，
	// 而不是"最后一轮是否压缩"。
	st.Compacted = st.Compacted || compacted
	return nil
}

func publishCompactionEvent(ctx context.Context, run runtime.RunContext, typ runtime.EventType, iteration int, payload any) error {
	event := runtime.MustEvent(run.EventStreamRunID(), run.ThreadID, typ, payload)
	event.IdempotencyKey = fmt.Sprintf("run:%s:compaction:%d:%s", run.RunID, iteration, typ)
	if run.Publish != nil {
		_, err := run.Publish(ctx, event)
		return err
	}
	if run.Bus != nil {
		run.Bus.Publish(ctx, event)
	}
	return nil
}

func (r *Runner) buildRequest(ctx context.Context, st *lifecycle.State) (*model.Request, error) {
	msgs := st.History.All()
	if r.cfg.Trimmer != nil {
		msgs = r.cfg.Trimmer.Trim(msgs)
	}

	allow, disclosed, err := r.resolveToolSet(ctx, st)
	if err != nil {
		return nil, err
	}
	st.ToolSet = allow
	st.DisclosedTools = disclosed

	return &model.Request{
		System:   st.SystemPrompt,
		Messages: msgs,
		Tools:    r.cfg.Registry.Schemas(allow, disclosed...),
	}, nil
}

func (r *Runner) resolveToolSet(ctx context.Context, st *lifecycle.State) (allow, disclosed []string, err error) {
	if r.cfg.ToolSet != nil {
		allow, disclosed, err = r.cfg.ToolSet.Resolve(ctx, st)
		if err != nil {
			return nil, nil, fmt.Errorf("loop: resolving tool capabilities: %w", err)
		}
	} else {
		// 没有解析器时披露全部已注册工具。这是库的默认行为；
		// 生产装配必须提供解析器以让权限与 skill 收窄生效。
		allow = r.cfg.Registry.Names()
	}
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || !run.Capabilities.Initialized() {
		return allow, disclosed, nil
	}
	capabilities := make(map[string]struct{}, len(run.Capabilities.Names()))
	for _, name := range run.Capabilities.Names() {
		capabilities[name] = struct{}{}
	}
	filtered := allow[:0]
	for _, name := range allow {
		if _, permitted := capabilities["tool."+name]; permitted {
			filtered = append(filtered, name)
		}
	}
	return filtered, disclosed, nil
}

func commitModelInput(ctx context.Context, st *lifecycle.State) error {
	if st == nil || st.ModelInput == nil {
		return errors.New("loop: model input commit requires a request")
	}
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Publish == nil {
		return nil
	}
	request := st.ModelInput
	event, err := runtime.NewEvent(run.EventStreamRunID(), run.ThreadID, runtime.EventModelInputCommitted, runtime.ModelInputCommitted{
		ExecutionRunID: run.RunID, ParentRunID: run.ParentRunID, SubagentTaskID: run.SubagentTaskID,
		GenerationID: run.GenerationID, Iteration: st.Iteration, Attempt: st.ModelAttempt, ModelName: st.ModelName, EffectiveOutputTokens: st.EffectiveOutputTokens, System: request.System,
		Messages: message.CloneAll(request.Messages), Tools: append([]model.ToolSchema(nil), request.Tools...),
		MaxTokens: request.MaxTokens, Temperature: request.Temperature, Thinking: request.Thinking,
		ExtraBody: request.ExtraBody, EnablePromptCache: request.EnablePromptCache,
	})
	if err != nil {
		return fmt.Errorf("loop: encoding model input commit: %w", err)
	}
	event.IdempotencyKey = fmt.Sprintf("run:%s:model-input:%d", run.RunID, st.Iteration)
	if st.ModelAttempt > 1 {
		event.IdempotencyKey += fmt.Sprintf(":attempt:%d", st.ModelAttempt)
	}
	if _, err := run.Publish(context.WithoutCancel(ctx), event); err != nil {
		return fmt.Errorf("loop: persisting model input commit: %w", err)
	}
	return nil
}

func commitModelOutput(ctx context.Context, st *lifecycle.State) error {
	if st == nil || st.ModelOutput == nil {
		return errors.New("loop: model output commit requires a response")
	}
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Publish == nil {
		return nil
	}
	response := st.ModelOutput
	event, err := runtime.NewEvent(run.EventStreamRunID(), run.ThreadID, runtime.EventModelOutputCommitted, runtime.ModelOutputCommitted{
		ExecutionRunID: run.RunID, ParentRunID: run.ParentRunID, SubagentTaskID: run.SubagentTaskID,
		GenerationID: run.GenerationID, Iteration: st.Iteration, Message: message.Clone(response.Message),
		StopReason: response.StopReason, CallID: response.CallID, ModelName: response.ModelName, Usage: response.Usage,
	})
	if err != nil {
		return fmt.Errorf("loop: encoding model output commit: %w", err)
	}
	event.IdempotencyKey = fmt.Sprintf("run:%s:model-output:%d", run.RunID, st.Iteration)
	if _, err := run.Publish(context.WithoutCancel(ctx), event); err != nil {
		return fmt.Errorf("loop: persisting model output commit: %w", err)
	}
	return nil
}

// prepareRequest installs the write-ahead barrier for every actual model attempt.
func (d *agentDriver) prepareRequest() error {
	ctx := d.ctx
	// BeforeModel may perform metered work (for example compaction or a
	// policy lookup). Re-read the shared budget before starting the lead
	// model so that auxiliary and child usage cannot bypass the ceiling.
	d.runner.syncSpend(ctx, d.tracker)
	if err := d.tracker.Check(d.state.Iteration); err != nil {
		return err
	}
	// The exact model-visible request is a durable write-ahead record. If
	// persistence fails, do not call the model: a response without its input
	// ledger cannot be replayed or audited deterministically.
	d.state.ModelAttempt = 0
	d.state.BeforeModelAttempt = func(attemptCtx context.Context, info model.Info) error {
		d.runner.syncSpend(attemptCtx, d.tracker)
		if err := d.tracker.Check(d.state.Iteration); err != nil {
			return err
		}
		d.state.ModelName = info.Name
		d.state.EffectiveOutputTokens = info.MaxOutputTokens
		d.state.ModelAttempt++
		if err := attemptCtx.Err(); err != nil {
			return err
		}
		if err := commitModelInput(attemptCtx, d.state); err != nil {
			return err
		}
		if d.commitInputs != nil {
			if err := d.commitInputs(attemptCtx, d.state.History.All()); err != nil {
				return err
			}
			d.commitInputs = nil
		}
		d.admitInput()
		if d.checkpoint != nil {
			if err := d.checkpoint(attemptCtx, d.state.History.All()); err != nil {
				return err
			}
		}
		return nil
	}
	if sampler, ok := d.runner.cfg.Sampler.(interface{ ManagesModelAttempts() bool }); !ok || !sampler.ManagesModelAttempts() {
		if err := d.state.BeforeModelAttempt(ctx, model.Info{}); err != nil {
			return err
		}
	}

	return nil
}

// admitInput publishes the proposed transcript exactly once. Retries still
// journal their actual model input, but never duplicate the initial user batch.
func (d *agentDriver) admitInput() {
	if d.canonical == nil {
		return
	}
	if d.state.Compacted {
		d.canonical.Replace(d.state.History.All())
	} else {
		d.canonical.AppendAll(d.state.History.Since(0))
	}
	d.state.History = d.canonical
	d.canonical = nil
}
