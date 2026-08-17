package runmanager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	runtimedebug "runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/metadata"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/replay"
)

type Options struct {
	Agent        Agent
	Store        metadata.Store
	Registry     *runtime.Registry
	PublishEvent func(context.Context, runtime.Event) (int64, error)
	Project      ProjectionPublisher
	Approvals    *runtime.ApprovalManager
	Questions    *runtime.QuestionManager
	AllowedTools []string
	Pricer       *runtime.Pricer
	Heartbeat    time.Duration
	Logger       *slog.Logger
	Snapshots    replay.SnapshotStore
}

type Manager struct {
	agent        Agent
	store        metadata.Store
	registry     *runtime.Registry
	publishEvent func(context.Context, runtime.Event) (int64, error)
	project      ProjectionPublisher
	approvals    *runtime.ApprovalManager
	questions    *runtime.QuestionManager
	allowedTools []string
	pricer       *runtime.Pricer
	heartbeat    time.Duration
	logger       *slog.Logger
	snapshots    replay.SnapshotStore
}

func New(opts Options) (*Manager, error) {
	if opts.Agent == nil {
		return nil, errors.New("runmanager: agent is required")
	}
	if opts.Store == nil {
		return nil, errors.New("runmanager: metadata store is required")
	}
	if opts.Registry == nil {
		return nil, errors.New("runmanager: run registry is required")
	}
	if opts.PublishEvent == nil {
		return nil, errors.New("runmanager: event publisher is required")
	}
	if opts.Project == nil {
		return nil, errors.New("runmanager: projection publisher is required")
	}
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = 15 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Manager{
		agent: opts.Agent, store: opts.Store, registry: opts.Registry,
		publishEvent: opts.PublishEvent, project: opts.Project, approvals: opts.Approvals, questions: opts.Questions,
		allowedTools: append([]string(nil), opts.AllowedTools...), pricer: opts.Pricer,
		heartbeat: opts.Heartbeat, logger: opts.Logger, snapshots: opts.Snapshots,
	}, nil
}

func (m *Manager) Execute(ctx context.Context, run Run, input Input) {
	startedAt := time.Now()
	state, err := runtime.NewRunStateMachine(run.RunID, run.ThreadID)
	if err != nil {
		m.preparationFailure(ctx, run, startedAt, err)
		return
	}
	state.SetObserver(func(snapshot runtime.RunSnapshot) error {
		event := runtime.MustEvent(run.RunID, run.ThreadID, runtime.EventRunStateChanged, runtime.RunStateChanged{Snapshot: snapshot})
		event.IdempotencyKey = fmt.Sprintf("run:%s:state:%d", run.RunID, snapshot.Version)
		_, publishErr := m.publishEvent(context.WithoutCancel(ctx), event)
		return publishErr
	})

	agent, allowedTools, pricer := m.agent, append([]string(nil), m.allowedTools...), m.pricer
	budgetLimits := runtime.BudgetAmount{}
	maxRecursionDepth := 1
	var allowedCapabilities []string
	if provider, ok := agent.(RunBudgetProvider); ok {
		budgetLimits = provider.RunBudgetLimits()
	}
	if provider, ok := agent.(RunCapabilityProvider); ok {
		allowedCapabilities = provider.RunCapabilityNames()
	}
	if provider, ok := agent.(RunLimitProvider); ok {
		maxRecursionDepth = provider.RunMaxRecursionDepth()
	}
	if preparer, ok := m.agent.(RunPreparer); ok {
		prepared, prepareErr := preparer.PrepareRun()
		if prepareErr != nil {
			m.preparationFailure(ctx, run, startedAt, prepareErr)
			return
		}
		if prepared.Agent == nil {
			m.preparationFailure(ctx, run, startedAt, errors.New("runmanager: prepared run has no agent"))
			return
		}
		agent, allowedTools, pricer = prepared.Agent, append([]string(nil), prepared.AllowedTools...), prepared.Pricer
		budgetLimits = prepared.BudgetLimits
		allowedCapabilities = append([]string(nil), prepared.AllowedCapabilities...)
		maxRecursionDepth = prepared.MaxRecursionDepth
		if prepared.Release != nil {
			defer prepared.Release()
		}
	}

	keepAliveCtx, stopKeepAlive := context.WithCancel(ctx)
	keepAliveDone := make(chan struct{})
	go func() {
		defer close(keepAliveDone)
		m.registry.KeepAlive(keepAliveCtx, run.RunID, m.heartbeat)
	}()
	defer func() {
		stopKeepAlive()
		<-keepAliveDone
	}()

	persistCtx := context.WithoutCancel(ctx)
	journal := runtime.NewJournal(pricer)
	result := AgentResult{}
	var runErr error
	if input.PreparationError != nil {
		runErr = input.PreparationError
	}
	if runErr == nil {
		runErr = m.project(persistCtx, run, Projection{Kind: ProjectionRunStart, Data: map[string]string{"run_id": run.RunID}})
	}
	if runErr == nil {
		if err := m.store.MarkRunRunning(persistCtx, run.RunID); err != nil {
			runErr = fmt.Errorf("starting run: %w", err)
		} else if err := state.Start(); err != nil {
			runErr = err
		}
	}

	var history []message.Message
	var threadValues map[string]any
	if runErr == nil {
		history, err = m.store.LoadHistory(ctx, run.ThreadID)
		if err != nil {
			runErr = fmt.Errorf("loading history: %w", err)
		}
	}
	if runErr == nil {
		threadValues, err = m.store.LoadThreadValues(ctx, run.ThreadID)
		if err != nil {
			runErr = fmt.Errorf("loading thread state: %w", err)
		}
	}
	var runValues map[string]any
	var budget *runtime.BudgetLedger
	if runErr == nil {
		runValues = mergeMaps(input.Config, threadValues, input.Context)
		policy, policyErr := runtime.ResolveModePolicy(runValues)
		if policyErr != nil {
			runErr = policyErr
		} else {
			// Normalize product intent once at admission. Kernel handlers consume
			// these canonical values; transports cannot silently redefine policy.
			runValues["mode"] = string(policy.Mode)
			runValues["thinking_enabled"] = policy.ThinkingEnabled
			runValues["is_plan_mode"] = policy.PlanMode
			runValues["subagent_enabled"] = policy.SubagentEnabled
			runValues["swarm_enabled"] = policy.SwarmEnabled
			if policy.ReasoningEffort != "" {
				runValues["reasoning_effort"] = policy.ReasoningEffort
			} else {
				delete(runValues, "reasoning_effort")
			}
			planEvent := runtime.MustEvent(run.RunID, run.ThreadID, runtime.EventPlanModeChanged, runtime.PlanModeChanged{Active: policy.PlanMode, Mode: policy.Mode})
			planEvent.IdempotencyKey = "run:" + run.RunID + ":plan-mode"
			if _, publishErr := m.publishEvent(context.WithoutCancel(ctx), planEvent); publishErr != nil {
				runErr = fmt.Errorf("persisting plan mode: %w", publishErr)
			}
		}
		if runErr == nil {
			budget = runtime.NewBudgetLedger(budgetLimits)
			budget.SetObserver(func(snapshot runtime.BudgetSnapshot) error {
				return m.publishBudgetSnapshot(context.WithoutCancel(ctx), run, snapshot)
			})
			if err := m.publishBudgetSnapshot(context.WithoutCancel(ctx), run, budget.Snapshot()); err != nil {
				runErr = err
			}
		}
	}
	if runErr == nil {
		ctx = runtime.WithRunContext(ctx, runtime.RunContext{
			RunID: run.RunID, EventRunID: run.RunID, ThreadID: run.ThreadID,
			AllowedTools: allowedTools, AllowedCapabilities: allowedCapabilities,
			Values: runValues, Journal: journal, StateMachine: state, Budget: budget,
			MaxRecursionDepth: maxRecursionDepth, Approvals: m.approvals, Questions: m.questions, Publish: m.publishEvent,
		})
		result, runErr = m.runAgent(ctx, agent, AgentRequest{
			RunID: run.RunID, ThreadID: run.ThreadID, AssistantID: run.AssistantID,
			Prompt: input.Prompt, ContentBlocks: input.ContentBlocks, History: history,
			Config: input.Config, Context: mergeMaps(threadValues, input.Context),
		})
	}

	status := statusForError(runErr)
	if runErr == nil {
		if journal.Totals().LLMCalls == 0 {
			journal.Observe(runtime.Entry{Bucket: runtime.BucketLead, Source: "lead", CallID: run.RunID, Usage: model.Usage{InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens}})
		}
		if len(result.Messages) == 0 && result.Output != "" {
			result.Messages = []message.Message{{Role: message.RoleAssistant, Content: result.Output}}
		}
		toSave := result.Messages
		if result.Compacted {
			toSave = result.Transcript
			if toSave == nil {
				runErr = errors.New("compacted agent result is missing the complete transcript")
			}
		}
		var transcriptSeq int64
		if runErr == nil {
			eventType, payload := runtime.EventTranscriptAppend, any(runtime.TranscriptAppend{Messages: toSave})
			if result.Compacted {
				eventType, payload = runtime.EventTranscriptReplace, runtime.TranscriptReplace{Messages: toSave}
			}
			event := runtime.MustEvent(run.RunID, run.ThreadID, eventType, payload)
			event.IdempotencyKey = "run:" + run.RunID + ":transcript"
			transcriptSeq, err = m.publishEvent(persistCtx, event)
			if err != nil {
				runErr = fmt.Errorf("persisting canonical transcript: %w", err)
			}
		}
		if runErr == nil {
			if err := m.store.SaveHistory(persistCtx, run.ThreadID, toSave, result.Compacted); err != nil {
				runErr = fmt.Errorf("saving history projection: %w", err)
			} else {
				m.saveSnapshot(persistCtx, run, transcriptSeq, history, toSave, result.Compacted)
			}
		}
		if runErr == nil && len(result.Values) > 0 {
			if err := m.store.SaveThreadValues(persistCtx, run.ThreadID, result.Values); err != nil {
				runErr = fmt.Errorf("saving thread state: %w", err)
			}
		}
		if runErr == nil {
			values, loadErr := m.store.LoadThreadValues(persistCtx, run.ThreadID)
			if loadErr != nil {
				runErr = fmt.Errorf("loading final state: %w", loadErr)
			} else if err := m.project(persistCtx, run, Projection{Kind: ProjectionValues, Data: values}); err != nil {
				runErr = fmt.Errorf("publishing final state: %w", err)
			}
		}
		if runErr == nil && !result.Streamed {
			for _, msg := range result.Messages {
				if msg.Role == message.RoleUser || msg.Role == message.RoleSystem {
					continue
				}
				if err := m.project(persistCtx, run, Projection{Kind: ProjectionMessage, Data: msg}); err != nil {
					runErr = fmt.Errorf("publishing message projection: %w", err)
					break
				}
			}
		}
		if runErr == nil {
			if err := m.project(persistCtx, run, Projection{Kind: ProjectionUsage, Data: journal.Totals()}); err != nil {
				runErr = fmt.Errorf("publishing usage projection: %w", err)
			}
		}
		status = statusForError(runErr)
	}
	if runErr != nil {
		if err := m.project(persistCtx, run, Projection{Kind: ProjectionError, Data: runErr}); err != nil {
			m.logger.ErrorContext(ctx, "publishing run error", "run_id", run.RunID, "error", err)
		}
	}

	totals := journal.Totals()
	completion := completionFrom(run, status, result.Iterations, totals, time.Since(startedAt))
	completionSaved := false
	if err := m.store.SaveCompletion(persistCtx, completion); err != nil {
		finalErr := fmt.Errorf("saving run completion: %w", err)
		if runErr == nil {
			runErr, status, completion.Status = finalErr, "error", "error"
			m.publishError(ctx, persistCtx, run, "completion", finalErr)
		} else {
			m.logger.ErrorContext(ctx, "saving run completion", "run_id", run.RunID, "error", err)
		}
	} else {
		completionSaved = true
	}

	declaredRisk := result.RiskLevel
	result.RiskLevel = normalizedRisk(declaredRisk, status)
	persistedStatus := status
	terminal, terminalErr := m.store.ConfirmRunTerminal(persistCtx, run.RunID, metadata.TerminalState{Status: status, RiskLevel: result.RiskLevel})
	if terminalErr != nil {
		finalErr := fmt.Errorf("saving terminal run state: %w", terminalErr)
		if runErr == nil {
			runErr, status, completion.Status = finalErr, "error", "error"
			result.RiskLevel = normalizedRisk(declaredRisk, status)
			m.publishError(ctx, persistCtx, run, "terminal state", finalErr)
			if completionSaved {
				if err := m.store.SaveCompletion(persistCtx, completion); err != nil {
					m.logger.ErrorContext(ctx, "reconciling completion after terminal failure", "run_id", run.RunID, "error", err)
				}
			}
		} else {
			m.logger.ErrorContext(ctx, "updating terminal run state", "run_id", run.RunID, "error", terminalErr)
		}
		persistedStatus = status
		if retry, retryErr := m.store.ConfirmRunTerminal(persistCtx, run.RunID, metadata.TerminalState{Status: status, RiskLevel: result.RiskLevel}); retryErr != nil {
			m.logger.ErrorContext(ctx, "retrying terminal run state", "run_id", run.RunID, "error", retryErr)
		} else {
			persistedStatus = retry.Status
		}
	} else {
		persistedStatus = terminal.Status
	}
	if persistedStatus != status {
		status, completion.Status = persistedStatus, persistedStatus
		result.RiskLevel = normalizedRisk(declaredRisk, status)
		conflict := fmt.Errorf("run terminal state was already confirmed as %s", status)
		if runErr == nil {
			runErr = conflict
			m.publishError(ctx, persistCtx, run, "terminal conflict", conflict)
		}
		if completionSaved {
			if err := m.store.SaveCompletion(persistCtx, completion); err != nil {
				m.logger.ErrorContext(ctx, "reconciling completion with first terminal writer", "run_id", run.RunID, "error", err)
			}
		}
	}

	if completionSaved {
		var usageMu sync.Mutex
		persisted := totals
		journal.SetOnChange(func(updated runtime.Totals) {
			usageMu.Lock()
			defer usageMu.Unlock()
			if !accountingAdvanced(updated, persisted) {
				return
			}
			late := completion
			applyTotals(&late, updated)
			if err := m.store.SaveCompletion(persistCtx, late); err != nil {
				m.logger.ErrorContext(ctx, "saving late subagent usage", "run_id", run.RunID, "error", err)
				return
			}
			persisted = updated
		})
	}

	runtimeStatus := runtime.StatusCompleted
	switch status {
	case "cancelled":
		runtimeStatus = runtime.StatusCancelled
	case "interrupted":
		runtimeStatus = runtime.StatusInterrupted
	case "error":
		runtimeStatus = runtime.StatusFailed
	}
	var stateErr error
	switch status {
	case "success":
		stateErr = state.Complete("agent completed")
	case "cancelled":
		stateErr = state.Cancel(errorString(runErr))
	case "interrupted":
		stateErr = state.Interrupt(errorString(runErr))
	default:
		stateErr = state.Fail(runErr)
	}
	if stateErr != nil {
		m.logger.ErrorContext(ctx, "persisting terminal run state event", "run_id", run.RunID, "error", stateErr)
	}
	if err := m.registry.Complete(persistCtx, run.RunID, runtimeStatus); err != nil {
		m.logger.ErrorContext(ctx, "completing runtime registry record", "run_id", run.RunID, "error", err)
	}
	if err := m.project(persistCtx, run, Projection{Kind: ProjectionRunEnd, Data: runtime.RunEnd{Status: status, Output: result.Output, Iterations: result.Iterations, RiskLevel: result.RiskLevel, Error: errorString(runErr)}}); err != nil {
		m.logger.ErrorContext(ctx, "publishing run end", "run_id", run.RunID, "error", err)
	}
}

func (m *Manager) preparationFailure(ctx context.Context, run Run, started time.Time, cause error) {
	persistCtx := context.WithoutCancel(ctx)
	if err := m.project(persistCtx, run, Projection{Kind: ProjectionRunStart, Data: map[string]string{"run_id": run.RunID}}); err != nil {
		m.logger.ErrorContext(ctx, "publishing failed run metadata", "run_id", run.RunID, "error", err)
	}
	_ = m.store.SaveCompletion(persistCtx, metadata.Completion{RunID: run.RunID, ThreadID: run.ThreadID, Status: "error", Duration: time.Since(started), CompletedAt: time.Now().UTC()})
	_, _ = m.store.ConfirmRunTerminal(persistCtx, run.RunID, metadata.TerminalState{Status: "error", RiskLevel: "unknown"})
	_ = m.registry.Complete(persistCtx, run.RunID, runtime.StatusFailed)
	if err := m.project(persistCtx, run, Projection{Kind: ProjectionError, Data: cause}); err != nil {
		m.logger.ErrorContext(ctx, "publishing preparation error", "run_id", run.RunID, "error", err)
	}
	if err := m.project(persistCtx, run, Projection{Kind: ProjectionRunEnd, Data: runtime.RunEnd{Status: "error", Error: cause.Error()}}); err != nil {
		m.logger.ErrorContext(ctx, "publishing failed run end", "run_id", run.RunID, "error", err)
	}
	m.logger.ErrorContext(ctx, "preparing agent run", "run_id", run.RunID, "elapsed", time.Since(started), "error", cause)
}

func (m *Manager) runAgent(ctx context.Context, agent Agent, request AgentRequest) (result AgentResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			m.logger.ErrorContext(ctx, "agent execution panicked", "panic", recovered, "stack", string(runtimedebug.Stack()))
			result = AgentResult{}
			err = errors.New("agent execution panicked")
		}
	}()
	return agent.Run(ctx, request)
}

func (m *Manager) publishBudgetSnapshot(ctx context.Context, run Run, snapshot runtime.BudgetSnapshot) error {
	snapshot.RunID, snapshot.ThreadID = run.RunID, run.ThreadID
	event := runtime.MustEvent(run.RunID, run.ThreadID, runtime.EventBudgetChanged, snapshot)
	event.IdempotencyKey = fmt.Sprintf("run:%s:budget:%d", run.RunID, snapshot.Version)
	_, err := m.publishEvent(ctx, event)
	return err
}

func (m *Manager) saveSnapshot(ctx context.Context, run Run, seq int64, previous, saved []message.Message, replaced bool) {
	if m.snapshots == nil {
		return
	}
	messages := message.CloneAll(saved)
	if !replaced {
		messages = append(message.CloneAll(previous), messages...)
	}
	if err := m.snapshots.Save(ctx, replay.Snapshot{RunID: run.RunID, ThreadID: run.ThreadID, LastSeq: seq, Messages: messages, CreatedAt: time.Now().UTC()}); err != nil {
		m.logger.WarnContext(ctx, "saving replay snapshot", "run_id", run.RunID, "error", err)
	}
}

func (m *Manager) publishError(logCtx, persistCtx context.Context, run Run, operation string, cause error) {
	if err := m.project(persistCtx, run, Projection{Kind: ProjectionError, Data: cause}); err != nil {
		m.logger.ErrorContext(logCtx, "publishing "+operation+" error", "run_id", run.RunID, "error", err)
	}
}

func completionFrom(run Run, status string, iterations int, totals runtime.Totals, duration time.Duration) metadata.Completion {
	c := metadata.Completion{RunID: run.RunID, ThreadID: run.ThreadID, Status: status, Iterations: iterations, Duration: duration, CompletedAt: time.Now().UTC()}
	applyTotals(&c, totals)
	return c
}

func applyTotals(c *metadata.Completion, totals runtime.Totals) {
	c.LLMCalls, c.InputTokens, c.OutputTokens = totals.LLMCalls, totals.InputTokens, totals.OutputTokens
	c.CachedInputTokens, c.LeadTokens, c.SubagentTokens = totals.CachedInputTokens, totals.LeadTokens, totals.SubagentTokens
	c.AuxiliaryTokens, c.CostMicros = totals.AuxiliaryTokens, totals.CostMicros
}

func statusForError(err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "error"
}

func normalizedRisk(level, status string) string {
	if level = strings.ToLower(strings.TrimSpace(level)); level != "" {
		return level
	}
	if status == "success" {
		return "pass"
	}
	return "unknown"
}

func accountingAdvanced(next, current runtime.Totals) bool {
	if next.LLMCalls < current.LLMCalls || next.InputTokens < current.InputTokens || next.OutputTokens < current.OutputTokens || next.CachedInputTokens < current.CachedInputTokens || next.LeadTokens < current.LeadTokens || next.SubagentTokens < current.SubagentTokens || next.AuxiliaryTokens < current.AuxiliaryTokens || next.CostMicros < current.CostMicros {
		return false
	}
	return next != current
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func mergeMaps(sources ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, source := range sources {
		for key, value := range source {
			out[key] = value
		}
	}
	return out
}
