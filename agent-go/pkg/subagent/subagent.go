// Package subagent dispatches bounded nested agent loops through one task tool.
package subagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/hooks"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/delegation"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
	"github.com/google/uuid"
)

type Definition struct {
	Name         string
	Description  string
	SystemPrompt string
	AllowedTools []string
	MaxTurns     int
}
type RunnerFactory func(Definition, []string) (*loop.Runner, error)

// PreparedRunner owns a child runner and any runtime resources assembled with
// it. Manager releases it after the child stops using the runner.
type PreparedRunner struct {
	Runner  *loop.Runner
	Release func() error
}

type PreparedRunnerFactory func(Definition, []string) (PreparedRunner, error)

// ContextRunnerFactory is the context-aware variant used when a child must
// inherit trusted parent run options (for example model_name or reasoning
// settings). RunnerFactory remains supported for fixed-runner embedders.
type ContextRunnerFactory func(context.Context, Definition, []string, DispatchRequest) (*loop.Runner, error)
type DispatchRequest struct {
	ResumeSessionID string
	Provider        string // spawn (default) or fork; independent of the agent profile.
	SubagentType    string
	Description     string
	Name            string
	MaxTurns        int
	Prompt          string
	RestrictTools   []string
	ToolCallID      string
}

// DispatchLifecycle lets an optional coordinator attach trusted state to a
// child run without making the subagent package depend on that coordinator.
// Swarm uses it to register and finalize teammates.
type DispatchLifecycle interface {
	Start(context.Context, runtime.RunContext, string, DispatchRequest) (runtime.RunContext, error)
	Finish(context.Context, runtime.RunContext, runtime.RunContext, DispatchRequest, Result) error
}

// TimeoutResolver selects the execution deadline from trusted parent state.
// The model cannot supply a timeout; application assembly may choose distinct
// limits for ordinary subagents and Swarm teammates.
type TimeoutResolver func(runtime.RunContext, DispatchRequest) time.Duration
type Result = delegation.Result
type TaskStore = delegation.TaskStore
type Manager struct {
	mu              sync.RWMutex
	defs            map[string]Definition
	factory         RunnerFactory
	preparedFactory PreparedRunnerFactory
	contextFactory  ContextRunnerFactory
	store           TaskStore
	ttl             time.Duration
	concurrency     int
	slotMu          sync.Mutex
	slots           map[string]*parentSlots
	lifecycle       DispatchLifecycle
	timeout         TimeoutResolver
	hooks           *hooks.Runner
	stateMu         sync.Mutex
	stateWaits      map[*runtime.RunStateMachine]subagentStateWait
	ownershipMu     sync.Mutex
	active          map[string]*execution
	closed          bool
	closeOnce       sync.Once
	closeDone       chan struct{}
	closeErr        error
}

type subagentStateWait struct {
	count   int
	restore runtime.RunPhase
}

// SetLifecycle installs process-level dispatch coordination. It must be called
// during assembly, before the manager is exposed to concurrent runs.
func (m *Manager) SetLifecycle(lifecycle DispatchLifecycle) { m.lifecycle = lifecycle }

// SetHookRunner installs process-level subagent lifecycle hooks. It must be
// called during assembly, before the manager is exposed to concurrent runs.
func (m *Manager) SetHookRunner(runner *hooks.Runner) { m.hooks = runner }

// SetTimeoutResolver installs process-level timeout policy. It must be called
// during assembly, before the manager is exposed to concurrent runs.
func (m *Manager) SetTimeoutResolver(resolve TimeoutResolver) { m.timeout = resolve }

func NewManager(factory RunnerFactory, store TaskStore, ttl time.Duration, maxConcurrent ...int) *Manager {
	return newManager(factory, nil, store, ttl, maxConcurrent...)
}

// NewManagerWithPreparedFactory constructs a manager that owns and releases
// each prepared child runner after its dispatch completes.
func NewManagerWithPreparedFactory(factory PreparedRunnerFactory, store TaskStore, ttl time.Duration, maxConcurrent ...int) *Manager {
	manager := newManager(nil, nil, store, ttl, maxConcurrent...)
	manager.preparedFactory = factory
	return manager
}

// NewManagerWithContextFactory constructs a manager whose factory receives the
// trusted parent context and normalized dispatch request.
func NewManagerWithContextFactory(factory ContextRunnerFactory, store TaskStore, ttl time.Duration, maxConcurrent ...int) *Manager {
	return newManager(nil, factory, store, ttl, maxConcurrent...)
}

func newManager(factory RunnerFactory, contextFactory ContextRunnerFactory, store TaskStore, ttl time.Duration, maxConcurrent ...int) *Manager {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if store == nil {
		store = NewMemoryTaskStore()
	}
	limit := 3
	if len(maxConcurrent) > 0 && maxConcurrent[0] > 0 {
		limit = maxConcurrent[0]
	}
	return &Manager{defs: map[string]Definition{}, factory: factory, contextFactory: contextFactory, store: store, ttl: ttl, concurrency: limit, slots: make(map[string]*parentSlots), stateWaits: make(map[*runtime.RunStateMachine]subagentStateWait), active: make(map[string]*execution), closeDone: make(chan struct{})}
}
func (m *Manager) Register(d Definition) error {
	if d.Name == "" || d.Description == "" {
		return errors.New("subagent: name and description are required")
	}
	if d.MaxTurns <= 0 {
		d.MaxTurns = 25
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.defs[d.Name]; ok {
		return fmt.Errorf("subagent: %q already registered", d.Name)
	}
	d.AllowedTools = append([]string(nil), d.AllowedTools...)
	m.defs[d.Name] = d
	return nil
}
func (m *Manager) Definitions() []Definition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Definition, 0, len(m.defs))
	for _, d := range m.defs {
		d.AllowedTools = append([]string(nil), d.AllowedTools...)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (m *Manager) Dispatch(ctx context.Context, req DispatchRequest) (Result, error) {
	req.RestrictTools = append([]string(nil), req.RestrictTools...)
	taskID := strings.TrimSpace(req.ToolCallID)
	if taskID == "" {
		taskID = newTaskID()
	}
	ctx, handle, err := m.accept(ctx, req, taskID, false)
	if err != nil {
		return Result{}, err
	}
	defer m.releaseExecution(handle)
	result, err := m.execute(ctx, req, taskID, true)
	handle.coordinationError = result.CoordinationError
	return result, err
}

func (m *Manager) execute(ctx context.Context, req DispatchRequest, taskID string, waitParent bool) (Result, error) {
	result, err := m.dispatch(ctx, req, taskID, waitParent)
	if err != nil && result.TaskID == "" {
		return m.recordEarlyFailure(ctx, req, taskID, err)
	}
	return result, err
}

func (m *Manager) dispatch(ctx context.Context, req DispatchRequest, taskID string, waitParent bool) (result Result, err error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	previous, resumeErr := m.loadResume(ctx, &req)
	if resumeErr != nil {
		return Result{}, resumeErr
	}
	if req.Provider == "" {
		req.Provider = "spawn"
	}
	if req.Provider != "spawn" && req.Provider != "fork" {
		return Result{}, fmt.Errorf("subagent: unknown provider %q", req.Provider)
	}
	agentName := strings.TrimSpace(req.SubagentType)
	if agentName == "" {
		return Result{}, errors.New("subagent: subagent_type is required")
	}
	m.mu.RLock()
	def, ok := m.defs[agentName]
	m.mu.RUnlock()
	if !ok {
		return Result{}, fmt.Errorf("subagent: unknown subagent type %q", agentName)
	}
	def.AllowedTools = append([]string(nil), def.AllowedTools...)
	if req.MaxTurns > 0 {
		if req.MaxTurns > def.MaxTurns {
			return Result{}, fmt.Errorf("subagent: max_turns %d exceeds the trusted limit %d for %q", req.MaxTurns, def.MaxTurns, def.Name)
		}
		// Definitions are registry state; apply a per-call override to the
		// copy passed to the factory without mutating the registered default.
		// Model input may narrow a profile limit, never widen it.
		def.MaxTurns = req.MaxTurns
	}
	parent, ok := runtime.RunContextFrom(ctx)
	if !ok || parent.Budget == nil || parent.StateMachine == nil {
		return Result{}, errors.New("subagent: dispatch requires a run context with budget and state machine")
	}
	if !parent.Capabilities.Initialized() {
		return Result{}, errors.New("subagent: dispatch requires an initialized capability view")
	}
	resolved, err := capability.ResolveViewAs[Definition](ctx, parent.Capabilities, "agent."+agentName, capability.KindAgent, capability.ResolveRequest{
		GenerationID: parent.GenerationID, ThreadID: parent.ThreadID, RunID: parent.RunID, Values: parent.Values,
	})
	if err != nil {
		return Result{}, fmt.Errorf("subagent: resolving agent capability %q: %w", agentName, err)
	}
	if resolved.Name != def.Name {
		return Result{}, fmt.Errorf("subagent: agent capability %q resolved profile %q", agentName, resolved.Name)
	}
	if parent.MaxRecursionDepth > 0 && parent.AgentDepth >= parent.MaxRecursionDepth {
		return Result{}, fmt.Errorf("subagent: maximum recursion depth %d reached", parent.MaxRecursionDepth)
	}
	releaseSlot, err := m.acquireParentSlot(ctx, parent)
	if err != nil {
		return Result{}, err
	}
	defer releaseSlot()
	if err := parent.Budget.Charge(runtime.BudgetAmount{Subagents: 1}); err != nil {
		if errors.Is(err, runtime.ErrBudgetExceeded) {
			return Result{}, fmt.Errorf("subagent: %w", loop.ErrBudgetExhausted)
		}
		return Result{}, fmt.Errorf("subagent: charging budget: %w", err)
	}
	allowed, err := restrict(def.AllowedTools, parent.AllowedTools, req.RestrictTools)
	if err != nil {
		return Result{}, err
	}
	if previous != nil {
		// Continuations inherit the original ceiling and can only narrow it.
		previousTools := set(previous.AllowedTools)
		narrowed := make([]string, 0, len(allowed))
		for _, name := range allowed {
			if previousTools[name] {
				narrowed = append(narrowed, name)
			}
		}
		allowed = narrowed
		if previous.MaxTurns < def.MaxTurns {
			def.MaxTurns = previous.MaxTurns
		}
	}
	if m.factory == nil && m.preparedFactory == nil && m.contextFactory == nil {
		return Result{}, errors.New("subagent: runner factory is nil")
	}
	var runner *loop.Runner
	var releaseRunner func() error
	if m.preparedFactory != nil {
		prepared, prepareErr := m.preparedFactory(def, allowed)
		err = prepareErr
		runner = prepared.Runner
		releaseRunner = prepared.Release
	} else if m.contextFactory != nil {
		runner, err = m.contextFactory(ctx, def, allowed, req)
	} else {
		runner, err = m.factory(def, allowed)
	}
	if releaseRunner != nil {
		release := releaseRunner
		released := false
		releaseRunner = func() (releaseErr error) {
			defer func() {
				if p := recover(); p != nil {
					releaseErr = fmt.Errorf("subagent: runner cleanup panicked: %v", p)
				}
			}()
			if released {
				return nil
			}
			released = true
			if releaseErr := release(); releaseErr != nil {
				return fmt.Errorf("subagent: releasing prepared runner: %w", releaseErr)
			}
			return nil
		}
		defer func() {
			err = errors.Join(err, releaseRunner())
		}()
	}
	if err != nil {
		return Result{}, err
	}
	if runner == nil {
		return Result{}, errors.New("subagent: factory returned no runner")
	}
	if waitParent {
		restoreState, waitErr := m.beginSubagentWait(parent.StateMachine)
		if waitErr != nil {
			return Result{}, waitErr
		}
		defer restoreState()
	}
	if err := m.runStartHook(ctx, parent, taskID, def.Name); err != nil {
		return Result{}, err
	}
	childContext, err := cloneRunContext(parent)
	if err != nil {
		return Result{}, fmt.Errorf("subagent: creating child budget: %w", err)
	}
	trustedBudget := childContext.Budget
	if m.lifecycle != nil {
		childContext, err = startLifecycle(m.lifecycle, ctx, parent, taskID, req)
		if err != nil {
			return Result{}, fmt.Errorf("subagent: starting lifecycle for %q: %w", def.Name, err)
		}
	}
	childContext, err = restrictChildRunContext(parent, childContext, trustedBudget, allowed)
	if err != nil {
		return Result{}, err
	}
	// The task identity is trusted dispatcher state, not a model argument.
	// Keep it on the child context even when lifecycle code returned a cloned
	// context with Swarm identity fields.
	childContext.SubagentTaskID = taskID
	childContext.ParentRunID = parent.RunID
	childContext.RunID = nestedRunID(parent.RunID, taskID)
	childContext.EventRunID = parent.EventStreamRunID()
	childContext.StateMachine, err = runtime.NewRunStateMachine(childContext.RunID, parent.ThreadID)
	if err != nil {
		return Result{}, fmt.Errorf("subagent: creating child run state: %w", err)
	}
	if err := childContext.StateMachine.Start(); err != nil {
		return Result{}, fmt.Errorf("subagent: starting child run state: %w", err)
	}
	persistCtx := context.WithoutCancel(ctx)
	result = Result{Description: taskDescription(req, def.Description), TaskID: taskID, ParentRunID: parent.RunID, ThreadID: parent.ThreadID, RunID: childContext.RunID, Provider: req.Provider, SubagentType: def.Name, Status: "running", StartedAt: time.Now().UTC()}
	history := message.NewHistory()
	if previous != nil {
		history.Load(message.CloneAll(previous.History))
	} else if req.Provider == "fork" {
		history.Load(message.CloneAll(parent.DelegationHistory))
	}
	ctx, session, sessionErr := m.openSession(ctx, parent, &result, def, allowed, history, previous)
	if sessionErr != nil {
		return Result{}, sessionErr
	}
	childContext.SubagentSessionID = result.SessionID
	if session != nil {
		childContext.PersistValues = session.persistValues
		delete(childContext.Values, "todos")
		if previous != nil {
			for key, value := range previous.State {
				childContext.Values[key] = cloneValue(value)
			}
		}
	}
	// Both the initial record and start event must commit before model/tool work.
	startErr := m.putTask(persistCtx, result)
	if startErr == nil {
		startErr = publish(persistCtx, parent, runtime.EventSubagentStart, map[string]any{
			"task_id": result.TaskID, "session_id": result.SessionID, "subagent_type": def.Name, "parent_run_id": parent.RunID,
			"description": taskDescription(req, def.Description), "provider": req.Provider,
		})
	}
	childJournal := parent.Journal.Child()
	childContext.Journal = childJournal
	ctx = runtime.WithRunContext(ctx, childContext)
	var runResult *loop.Result
	var runErr error
	if startErr != nil {
		runErr = fmt.Errorf("subagent: persisting start: %w", startErr)
	} else if err := ctx.Err(); err != nil {
		runErr = fmt.Errorf("subagent: context ended before %q started: %w", def.Name, err)
	} else {
		runResult, runErr = runChild(ctx, runner, loop.Request{ThreadID: parent.ThreadID, RunID: childContext.RunID, AssistantID: def.Name, SystemPrompt: def.SystemPrompt, History: history, Prompt: req.Prompt, Values: cloneValues(childContext.Values), Checkpoint: session.checkpoint, PullInputs: session.pullInputs})
		if runErr != nil {
			runErr = fmt.Errorf("subagent: running %q: %w", def.Name, runErr)
		}
	}
	now := time.Now().UTC()
	result.CompletedAt = &now
	if runResult != nil {
		result.Usage = runResult.Usage
	}
	result.Output = finalAssistantOutput(history.Since(0))
	if runResult != nil && runResult.Output != "" {
		result.Output = runResult.Output
	}
	result.StopReason, runErr = childStopReason(runResult, runErr)
	if runErr != nil {
		result.Status = failureStatus(runErr)
		result.Error = runErr.Error()
	} else {
		result.Status = "completed"
	}

	runErr = releaseChildRunner(releaseRunner, &result, runErr)
	if parent.Journal != nil {
		if childJournal.Totals().LLMCalls == 0 && runResult != nil && runResult.Usage.TotalTokens() > 0 {
			entry := runtime.Entry{Bucket: runtime.BucketLead, Source: def.Name, CallID: req.ToolCallID, Usage: runResult.Usage}
			if runResult.Response != nil {
				entry.CallID = runResult.Response.CallID
				entry.ModelName = runResult.Response.ModelName
			}
			childJournal.Observe(entry)
		}
		parent.Journal.MergeSubagent(result.TaskID, def.Name, childJournal)
	}
	// Coordinators finalize first. A terminal TaskStore result is therefore the
	// authoritative outcome, never a provisional success that can be revoked
	// after an async status poll has already consumed it.
	runErr = m.finishLifecycle(persistCtx, parent, childContext, req, &result, runErr)
	persistedCoordination := result.CoordinationError
	runErr = m.persistTerminal(persistCtx, &result, runErr)
	m.runEndHook(persistCtx, parent, result)
	if publishErr := publish(persistCtx, parent, runtime.EventSubagentResult, result); publishErr != nil {
		result.CoordinationError = joinErrorText(result.CoordinationError, publishErr)
		runErr = errors.Join(runErr, publishErr)
	}
	if sessionErr := session.finish(persistCtx, &result, history.All()); sessionErr != nil {
		result.CoordinationError = joinErrorText(result.CoordinationError, sessionErr)
		runErr = errors.Join(runErr, sessionErr)
	}
	if result.CoordinationError != persistedCoordination {
		runErr = m.persistTerminal(persistCtx, &result, runErr)
	}
	return result, runErr
}

func (m *Manager) beginSubagentWait(state *runtime.RunStateMachine) (func(), error) {
	if state == nil {
		return nil, errors.New("subagent: parent run state machine is required")
	}
	m.stateMu.Lock()
	wait := m.stateWaits[state]
	if wait.count == 0 {
		wait.restore = state.Snapshot().Phase
		if wait.restore != runtime.RunRunning && wait.restore != runtime.RunWaitingTool {
			m.stateMu.Unlock()
			return nil, fmt.Errorf("subagent: cannot dispatch while run is %s", wait.restore)
		}
		if err := state.WaitSubagent(); err != nil {
			m.stateMu.Unlock()
			return nil, err
		}
	}
	wait.count++
	m.stateWaits[state] = wait
	m.stateMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() { m.endSubagentWait(state) })
	}, nil
}

func (m *Manager) endSubagentWait(state *runtime.RunStateMachine) {
	m.stateMu.Lock()
	wait, ok := m.stateWaits[state]
	if !ok {
		m.stateMu.Unlock()
		return
	}
	wait.count--
	if wait.count > 0 {
		m.stateWaits[state] = wait
		m.stateMu.Unlock()
		return
	}
	delete(m.stateWaits, state)
	m.stateMu.Unlock()

	if state.Snapshot().Phase.Terminal() {
		return
	}
	if wait.restore == runtime.RunWaitingTool {
		_ = state.WaitTool()
		return
	}
	_ = state.Resume()
}

func (m *Manager) finishLifecycle(ctx context.Context, parent, child runtime.RunContext, req DispatchRequest, result *Result, cause error) error {
	if m.lifecycle == nil {
		return cause
	}
	if err := finishLifecycle(m.lifecycle, ctx, parent, child, req, *result); err != nil {
		coordinationErr := fmt.Errorf("finalizing subagent lifecycle: %w", err)
		result.CoordinationError = joinErrorText(result.CoordinationError, coordinationErr)
		cause = errors.Join(cause, coordinationErr)
	}
	return cause
}

func startLifecycle(lifecycle DispatchLifecycle, ctx context.Context, parent runtime.RunContext, id string, req DispatchRequest) (child runtime.RunContext, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("subagent lifecycle start panicked: %v", p)
		}
	}()
	return lifecycle.Start(ctx, parent, id, req)
}
func finishLifecycle(lifecycle DispatchLifecycle, ctx context.Context, parent, child runtime.RunContext, req DispatchRequest, result Result) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("subagent lifecycle finish panicked: %v", p)
		}
	}()
	return lifecycle.Finish(ctx, parent, child, req, result)
}

func (m *Manager) persistTerminal(ctx context.Context, result *Result, cause error) error {
	if err := m.putTask(ctx, *result); err == nil {
		return cause
	} else {
		cause = errors.Join(cause, fmt.Errorf("persisting terminal task: %w", err))
	}

	result.Status = message.SubagentFailed
	result.Error = cause.Error()
	if err := m.putTask(ctx, *result); err != nil {
		cause = errors.Join(cause, fmt.Errorf("persisting final task state: %w", err))
		result.Error = cause.Error()
	}
	return cause
}

func (m *Manager) runStartHook(ctx context.Context, parent runtime.RunContext, taskID, agent string) error {
	if m.hooks == nil || !m.hooks.Has(hooks.EventSubagentStart) {
		return nil
	}
	result := m.hooks.Run(ctx, hooks.Payload{
		Event: hooks.EventSubagentStart, ThreadID: parent.ThreadID, RunID: parent.RunID,
		TaskID: taskID, Subagent: agent,
	})
	if !result.Deny {
		return nil
	}
	reason := strings.TrimSpace(result.Message)
	if reason == "" {
		reason = "dispatch denied"
	}
	return fmt.Errorf("subagent: start denied by hook %q: %s", result.DeniedBy, reason)
}

func (m *Manager) runEndHook(ctx context.Context, parent runtime.RunContext, result Result) {
	if m.hooks == nil || !m.hooks.Has(hooks.EventSubagentEnd) {
		return
	}
	m.hooks.Run(ctx, hooks.Payload{
		Event: hooks.EventSubagentEnd, ThreadID: parent.ThreadID, RunID: parent.RunID,
		TaskID: result.TaskID, Subagent: result.SubagentType, Status: result.Status,
		Output: result.Output, Error: resultErrorText(result),
		IsError: result.Status != message.SubagentCompleted || result.CoordinationError != "",
	})
}

func resultErrorText(result Result) string {
	return joinErrorText(result.Error, errors.New(result.CoordinationError))
}

func joinErrorText(existing string, next error) string {
	if next == nil {
		return strings.TrimSpace(existing)
	}
	nextText := strings.TrimSpace(next.Error())
	if strings.TrimSpace(existing) == "" {
		return nextText
	}
	if nextText == "" {
		return strings.TrimSpace(existing)
	}
	return errors.Join(errors.New(strings.TrimSpace(existing)), errors.New(nextText)).Error()
}

func releaseChildRunner(release func() error, result *Result, cause error) error {
	if release == nil {
		return cause
	}
	releaseErr := release()
	if releaseErr == nil {
		return cause
	}
	if result != nil {
		result.CoordinationError = joinErrorText(result.CoordinationError, releaseErr)
	}
	return errors.Join(cause, releaseErr)
}

// DispatchAsync transfers accepted work to the parent execution owner, rather
// than detaching cancellation. Close and Interrupt cancel and drain that work.
func (m *Manager) DispatchAsync(ctx context.Context, req DispatchRequest) (string, error) {
	req.RestrictTools = append([]string(nil), req.RestrictTools...)
	if strings.TrimSpace(req.SubagentType) == "" {
		return "", errors.New("subagent: subagent_type is required")
	}
	parent, ok := runtime.RunContextFrom(ctx)
	if !ok || parent.Budget == nil {
		return "", errors.New("subagent: dispatch requires a run context with budget")
	}
	id := strings.TrimSpace(req.ToolCallID)
	if id == "" {
		id = newTaskID()
	}
	owned, handle, err := m.accept(ctx, req, id, true)
	if err != nil {
		return "", err
	}
	initial := Result{TaskID: id, ParentRunID: parent.RunID, ThreadID: parent.ThreadID, SubagentType: strings.TrimSpace(req.SubagentType), Status: "pending", StartedAt: time.Now().UTC()}
	if err := m.putTask(context.WithoutCancel(ctx), initial); err != nil {
		m.releaseExecution(handle)
		return "", err
	}
	go func() {
		defer m.releaseExecution(handle)
		result, _ := m.execute(owned, req, id, false)
		handle.coordinationError = result.CoordinationError
	}()
	return id, nil
}

func (m *Manager) recordEarlyFailure(ctx context.Context, req DispatchRequest, taskID string, cause error) (Result, error) {
	now := time.Now().UTC()
	result := Result{
		TaskID: taskID, SubagentType: strings.TrimSpace(req.SubagentType), Status: failureStatus(cause),
		Error: cause.Error(), StartedAt: now, CompletedAt: &now,
	}
	parent, _ := runtime.RunContextFrom(ctx)
	result.ParentRunID, result.ThreadID, result.Provider = parent.RunID, parent.ThreadID, req.Provider
	result.StopReason = "error"
	if errors.Is(cause, context.Canceled) {
		result.StopReason = "aborted"
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		result.StopReason = "timed-out"
	}
	persistCtx := context.WithoutCancel(ctx)
	cause = errors.Join(cause, publish(persistCtx, parent, runtime.EventSubagentStart, map[string]any{
		"task_id":       taskID,
		"subagent_type": result.SubagentType,
		"description":   taskDescription(req, result.SubagentType),
	}))
	cause = m.persistTerminal(persistCtx, &result, cause)
	m.runEndHook(persistCtx, parent, result)
	cause = errors.Join(cause, publish(persistCtx, parent, runtime.EventSubagentResult, result))
	return result, cause
}

func taskDescription(req DispatchRequest, fallback string) string {
	description := strings.TrimSpace(req.Description)
	if description == "" {
		description = strings.TrimSpace(fallback)
	}
	return description
}

func (m *Manager) TaskStatus(ctx context.Context, id string) (Result, error) {
	return m.taskStatus(ctx, id)
}

func restrict(base, parent, requested []string) ([]string, error) {
	parentSet := set(parent)
	baseSet := set(base)
	allowed := make([]string, 0)
	if len(baseSet) == 0 {
		for n := range parentSet {
			allowed = append(allowed, n)
		}
	}
	for n := range baseSet {
		if parentSet[n] {
			allowed = append(allowed, n)
		}
	}
	if len(requested) > 0 {
		allowedSet := set(allowed)
		for _, n := range requested {
			if !allowedSet[n] {
				return nil, fmt.Errorf("subagent: requested tool %q would widen the inherited policy", n)
			}
		}
		allowed = append([]string(nil), requested...)
	}
	sort.Strings(allowed)
	return allowed, nil
}
func set(in []string) map[string]bool {
	m := map[string]bool{}
	for _, n := range in {
		m[n] = true
	}
	return m
}
func publish(ctx context.Context, p runtime.RunContext, t runtime.EventType, data any) error {
	e := runtime.MustEvent(p.EventStreamRunID(), p.ThreadID, t, data)
	if p.Publish != nil {
		_, err := p.Publish(ctx, e)
		return err
	} else if p.Bus != nil {
		p.Bus.Publish(ctx, e)
	}
	return nil
}
func newTaskID() string { return "task-" + uuid.NewString() }

func nestedRunID(parentRunID, taskID string) string {
	if strings.TrimSpace(parentRunID) == "" {
		return taskID
	}
	return parentRunID + ":" + taskID
}

func runChild(ctx context.Context, runner *loop.Runner, request loop.Request) (result *loop.Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = nil
			err = fmt.Errorf("subagent: child execution panicked: %v", recovered)
		}
	}()
	return runner.Run(ctx, request)
}

func cloneRunContext(parent runtime.RunContext) (runtime.RunContext, error) {
	child := parent
	child.AllowedTools = append([]string(nil), parent.AllowedTools...)
	child.AllowedCapabilities = append([]string(nil), parent.AllowedCapabilities...)
	child.Values = cloneValues(parent.Values)
	child.DelegationHistory = message.CloneAll(parent.DelegationHistory)
	if child.Values == nil {
		child.Values = make(map[string]any)
	}
	// New child agents begin outside the parent's collaboration-only plan mode.
	child.Values["is_plan_mode"] = false
	// Child runs share the root budget but keep an independent local limit.
	budget, err := parent.Budget.Child(runtime.BudgetAmount{})
	if err != nil {
		return runtime.RunContext{}, err
	}
	child.Budget = budget
	// A child gets its own lifecycle authority once the trusted dispatcher has
	// assigned its nested RunID; never let it mutate the parent's state machine.
	child.StateMachine = nil
	return child, nil
}

func restrictChildRunContext(parent, child runtime.RunContext, trustedBudget *runtime.BudgetLedger, allowedTools []string) (runtime.RunContext, error) {
	child.GenerationID = parent.GenerationID
	child.AgentDepth = parent.AgentDepth + 1
	child.MaxRecursionDepth = parent.MaxRecursionDepth
	child.AllowedTools = append([]string(nil), allowedTools...)
	child.Budget = trustedBudget
	child.StateMachine = nil
	child.Approvals = parent.Approvals
	child.Bus = parent.Bus
	child.Publish = parent.Publish

	if parent.AllowedCapabilities == nil {
		child.AllowedCapabilities = nil
		child.Capabilities = parent.Capabilities
		return child, nil
	}
	allowedToolCapabilities := make(map[string]struct{}, len(allowedTools))
	for _, name := range allowedTools {
		allowedToolCapabilities["tool."+name] = struct{}{}
	}
	names := make([]string, 0, len(parent.AllowedCapabilities))
	for _, name := range parent.AllowedCapabilities {
		if strings.HasPrefix(name, "tool.") {
			if _, ok := allowedToolCapabilities[name]; !ok {
				continue
			}
		}
		names = append(names, name)
	}
	child.AllowedCapabilities = names
	if !parent.Capabilities.Empty() {
		view, err := parent.Capabilities.Restrict(names)
		if err != nil {
			return runtime.RunContext{}, fmt.Errorf("subagent: restricting capability view: %w", err)
		}
		child.Capabilities = view
	}
	return child, nil
}

func cloneValues(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	return cloneValueMap(in)
}

func (m *Manager) TaskTool() tool.Definition {
	return tool.Definition{Name: "task", Group: "subagent", Description: "Delegate a bounded task to a specialized child session; wait for its result or run it as supervised background work.", Parameters: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Continue a previously returned child session; profile and tool ceiling remain fixed."},"background":{"type":"boolean","description":"Run concurrently under this parent turn; use subagent_control to wait or interrupt."},"description":{"type":"string","description":"Short description shown in the task card."},"prompt":{"type":"string"},"subagent_type":{"type":"string","description":"Registered subagent type."},"provider":{"type":"string","enum":["spawn","fork"],"description":"spawn starts fresh; fork inherits only completed parent turns."},"name":{"type":"string","description":"Optional teammate name in swarm mode."},"max_turns":{"type":"integer","minimum":1},"restrict_tools":{"type":"array","items":{"type":"string"}}},"required":["description","prompt","subagent_type"]}`), Metadata: tool.Metadata{IsAgentState: true, IsConcurrencySafe: true}, Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
		var args struct {
			ResumeSessionID string   `json:"session_id"`
			Background      bool     `json:"background"`
			Provider        string   `json:"provider"`
			Description     string   `json:"description"`
			Prompt          string   `json:"prompt"`
			SubagentType    string   `json:"subagent_type"`
			Name            string   `json:"name"`
			MaxTurns        int      `json:"max_turns"`
			Restrict        []string `json:"restrict_tools"`
		}
		decoder := json.NewDecoder(bytes.NewReader(call.Args))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&args); err != nil {
			return taskFailure(err.Error()), nil //nolint:nilerr // malformed model input is returned to the model
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			if err == nil {
				err = errors.New("task arguments must contain one JSON value")
			}
			return taskFailure(err.Error()), nil //nolint:nilerr // malformed model input is returned to the model
		}
		agentName := strings.TrimSpace(args.SubagentType)
		if agentName == "" {
			return taskFailure("missing required field subagent_type"), nil
		}
		if strings.TrimSpace(args.Prompt) == "" {
			return taskFailure("missing required field prompt"), nil
		}
		description := strings.TrimSpace(args.Description)
		if description == "" {
			return taskFailure("missing required field description"), nil
		}
		m.mu.RLock()
		profile, registered := m.defs[agentName]
		m.mu.RUnlock()
		if registered && args.MaxTurns > profile.MaxTurns {
			return taskFailure(fmt.Sprintf("subagent: max_turns %d exceeds the trusted limit %d for %q", args.MaxTurns, profile.MaxTurns, agentName)), nil
		}
		req := DispatchRequest{ResumeSessionID: args.ResumeSessionID, Provider: args.Provider, SubagentType: agentName, Description: description, Name: strings.TrimSpace(args.Name), MaxTurns: args.MaxTurns, Prompt: args.Prompt, RestrictTools: args.Restrict, ToolCallID: call.ID}
		if args.Background {
			id, err := m.DispatchAsync(ctx, req)
			if err != nil {
				return taskFailure(err.Error()), nil
			}
			return &tool.Result{Content: "Task accepted: " + id + ". Use subagent_control to wait; leaving this parent turn cancels unfinished children.", AdditionalKwargs: map[string]any{"subagent_state": "pending", "subagent_task_id": id}}, nil
		}
		res, err := m.Dispatch(ctx, req)
		if err != nil {
			return taskFailureFromResult(res, err), nil
		}
		result := taskSuccess(res.Output)
		result.Content += "\nSession ID: " + res.SessionID
		result.AdditionalKwargs["subagent_session_id"] = res.SessionID
		result.AdditionalKwargs["subagent_provider"] = res.Provider
		result.AdditionalKwargs["subagent_stop_reason"] = res.StopReason
		return result, nil
	}}
}

func dispatchError(res Result, err error) string {
	if strings.TrimSpace(res.Error) != "" {
		return strings.TrimSpace(res.Error)
	}
	if err == nil {
		return "subagent dispatch failed"
	}
	return err.Error()
}

func failureStatus(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return message.SubagentCancelled
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, loop.ErrDeadline):
		return message.SubagentTimedOut
	default:
		return message.SubagentFailed
	}
}

func taskFailureFromResult(res Result, err error) (failure *tool.Result) {
	defer func() {
		if failure == nil {
			return
		}
		if res.SessionID != "" {
			failure.AdditionalKwargs["subagent_session_id"] = res.SessionID
		}
		if res.StopReason != "" {
			failure.AdditionalKwargs["subagent_stop_reason"] = res.StopReason
		}
		if res.Output != "" && res.Status != message.SubagentCompleted {
			failure.Content += "\nPartial result: " + res.Output
		}
	}()
	errText := dispatchError(res, err)
	if res.Status == message.SubagentCompleted && res.CoordinationError != "" {
		// The child completed successfully, but the coordinator could not make
		// its terminal state durable. Keep the structured status as completed so
		// clients do not display a false execution failure.
		content := "Task Succeeded. Result: " + res.Output + "\nCoordination warning: " + res.CoordinationError
		return &tool.Result{
			Content:          content,
			IsError:          true,
			AdditionalKwargs: message.MakeSubagentAdditionalKwargs(message.SubagentCompleted, res.CoordinationError),
		}
	}
	switch res.Status {
	case message.SubagentCancelled:
		return &tool.Result{
			Content:          "Task cancelled by user.",
			IsError:          true,
			AdditionalKwargs: message.MakeSubagentAdditionalKwargs(message.SubagentCancelled, ""),
		}
	case message.SubagentTimedOut:
		return &tool.Result{
			Content:          "Task timed out. Error: " + errText,
			IsError:          true,
			AdditionalKwargs: message.MakeSubagentAdditionalKwargs(message.SubagentTimedOut, errText),
		}
	default:
		return taskFailure(errText)
	}
}

func taskSuccess(output string) *tool.Result {
	content := "Task Succeeded. Result: " + output
	return &tool.Result{Content: content, AdditionalKwargs: message.MakeSubagentAdditionalKwargs(message.SubagentCompleted, "")}
}

func taskFailure(errText string) *tool.Result {
	errText = strings.TrimSpace(errText)
	if errText == "" {
		errText = "subagent task failed"
	}
	return &tool.Result{
		Content:          "Task failed. Error: " + errText,
		IsError:          true,
		AdditionalKwargs: message.MakeSubagentAdditionalKwargs(message.SubagentFailed, errText),
	}
}

type MemoryTaskStore struct {
	mu    sync.RWMutex
	tasks map[string]Result
}

func NewMemoryTaskStore() *MemoryTaskStore { return &MemoryTaskStore{tasks: map[string]Result{}} }
func (s *MemoryTaskStore) Put(_ context.Context, r Result, _ time.Duration) error {
	s.mu.Lock()
	s.tasks[r.ThreadID+"\x00"+r.ParentRunID+"\x00"+r.TaskID] = r
	s.mu.Unlock()
	return nil
}
func (s *MemoryTaskStore) Get(_ context.Context, id string) (Result, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result Result
	found := false
	for _, r := range s.tasks {
		if r.TaskID == id {
			if found {
				return Result{}, errors.New("ambiguous task ID")
			}
			result = r
			found = true
		}
	}
	if !found {
		return Result{}, delegation.ErrNotFound
	}
	return result, nil
}
func (s *MemoryTaskStore) GetScoped(_ context.Context, thread, parent, id string) (Result, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.tasks[thread+"\x00"+parent+"\x00"+id]
	if !ok {
		return Result{}, delegation.ErrNotFound
	}
	return r, nil
}
