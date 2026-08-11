// Package subagent dispatches bounded nested agent loops through one task tool.
package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/hooks"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
	"github.com/redis/go-redis/v9"
)

type Definition struct {
	Name         string
	Description  string
	SystemPrompt string
	AllowedTools []string
	MaxTurns     int
}
type RunnerFactory func(Definition, []string) (*loop.Runner, error)

// ContextRunnerFactory is the context-aware variant used when a child must
// inherit trusted parent run options (for example model_name or reasoning
// settings). RunnerFactory remains supported for fixed-runner embedders.
type ContextRunnerFactory func(context.Context, Definition, []string, DispatchRequest) (*loop.Runner, error)
type DispatchRequest struct {
	// SubagentType, Description and MaxTurns mirror the Python/frontend task
	// tool contract. Agent is retained as a source-compatible legacy alias for
	// callers that predate the cross-language contract.
	SubagentType  string
	Description   string
	Name          string
	MaxTurns      int
	Agent         string
	Prompt        string
	RestrictTools []string
	ToolCallID    string
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
type Result struct {
	TaskID string `json:"task_id"`
	Agent  string `json:"agent"`
	Status string `json:"status"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
	// CoordinationError describes a failure after the child execution reached
	// its terminal state (for example a Swarm announcement transaction). It is
	// deliberately separate from Error so consumers never mistake a completed
	// piece of work for a failed model execution.
	CoordinationError string      `json:"coordination_error,omitempty"`
	Usage             model.Usage `json:"usage"`
	StartedAt         time.Time   `json:"started_at"`
	CompletedAt       *time.Time  `json:"completed_at,omitempty"`
}
type TaskStore interface {
	Put(context.Context, Result, time.Duration) error
	Get(context.Context, string) (Result, error)
}
type Manager struct {
	mu             sync.RWMutex
	defs           map[string]Definition
	factory        RunnerFactory
	contextFactory ContextRunnerFactory
	store          TaskStore
	ttl            time.Duration
	sem            chan struct{}
	lifecycle      DispatchLifecycle
	timeout        TimeoutResolver
	hooks          *hooks.Runner
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
	return &Manager{defs: map[string]Definition{}, factory: factory, contextFactory: contextFactory, store: store, ttl: ttl, sem: make(chan struct{}, limit)}
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
	m.defs[d.Name] = d
	return nil
}
func (m *Manager) Definitions() []Definition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Definition, 0, len(m.defs))
	for _, d := range m.defs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (m *Manager) Dispatch(ctx context.Context, req DispatchRequest) (Result, error) {
	taskID := strings.TrimSpace(req.ToolCallID)
	if taskID == "" {
		taskID = newTaskID()
	}
	result, err := m.dispatch(ctx, req, taskID)
	if err != nil && result.TaskID == "" {
		return m.recordEarlyFailure(ctx, req, taskID, err)
	}
	return result, err
}

func (m *Manager) dispatch(ctx context.Context, req DispatchRequest, taskID string) (Result, error) {
	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	agentName := req.subagentType()
	m.mu.RLock()
	def, ok := m.defs[agentName]
	m.mu.RUnlock()
	if !ok {
		return Result{}, fmt.Errorf("subagent: unknown subagent type %q", agentName)
	}
	if req.MaxTurns > 0 {
		if req.MaxTurns > def.MaxTurns {
			return Result{}, fmt.Errorf("subagent: max_turns %d exceeds the trusted limit %d for %q", req.MaxTurns, def.MaxTurns, def.Name)
		}
		// Definitions are registry state; apply a per-call override to the
		// copy passed to the factory without mutating the registered default.
		// Model input may narrow a profile limit, never widen it.
		def.MaxTurns = req.MaxTurns
	}
	parent, _ := runtime.RunContextFrom(ctx)
	if m.timeout != nil {
		if timeout := m.timeout(parent, req); timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
	}
	allowed, err := restrict(def.AllowedTools, parent.AllowedTools, req.RestrictTools)
	if err != nil {
		return Result{}, err
	}
	if m.factory == nil && m.contextFactory == nil {
		return Result{}, errors.New("subagent: runner factory is nil")
	}
	var runner *loop.Runner
	if m.contextFactory != nil {
		runner, err = m.contextFactory(ctx, def, allowed, req)
	} else {
		runner, err = m.factory(def, allowed)
	}
	if err != nil {
		return Result{}, err
	}
	if err := m.runStartHook(ctx, parent, taskID, def.Name); err != nil {
		return Result{}, err
	}
	childContext := cloneRunContext(parent)
	if m.lifecycle != nil {
		childContext, err = m.lifecycle.Start(ctx, parent, taskID, req)
		if err != nil {
			return Result{}, err
		}
	}
	// The task identity is trusted dispatcher state, not a model argument.
	// Keep it on the child context even when lifecycle code returned a cloned
	// context with Swarm identity fields.
	childContext.SubagentTaskID = taskID
	childContext.ParentRunID = parent.RunID
	childContext.RunID = nestedRunID(parent.RunID, taskID)
	childContext.EventRunID = parent.EventStreamRunID()
	persistCtx := context.WithoutCancel(ctx)
	result := Result{TaskID: taskID, Agent: def.Name, Status: "running", StartedAt: time.Now().UTC()}
	description := taskDescription(req, def.Description)
	publish(persistCtx, parent, runtime.EventSubagentStart, map[string]any{
		"task_id":       result.TaskID,
		"agent":         def.Name, // legacy field retained for existing consumers
		"subagent_type": def.Name,
		"description":   description,
	})
	if err := m.store.Put(persistCtx, result, m.ttl); err != nil {
		result.Status = "failed"
		runErr := fmt.Errorf("persisting running task: %w", err)
		result.Error = runErr.Error()
		completed := time.Now().UTC()
		result.CompletedAt = &completed
		runErr = m.finishLifecycle(persistCtx, parent, childContext, req, &result, runErr)
		runErr = m.persistTerminal(persistCtx, &result, runErr)
		m.runEndHook(persistCtx, parent, result)
		publish(persistCtx, parent, runtime.EventSubagentResult, result)
		return result, runErr
	}
	history := message.NewHistory()
	childJournal := parent.Journal.Child()
	childContext.Journal = childJournal
	ctx = runtime.WithRunContext(ctx, childContext)
	var runResult *loop.Result
	var runErr error
	if err := ctx.Err(); err != nil {
		runErr = fmt.Errorf("subagent: context ended before %q started: %w", def.Name, err)
	} else {
		runResult, runErr = runChild(ctx, runner, loop.Request{ThreadID: parent.ThreadID, RunID: childContext.RunID, AssistantID: def.Name, SystemPrompt: def.SystemPrompt, History: history, Prompt: req.Prompt, Values: cloneValues(childContext.Values)})
		if runErr != nil {
			runErr = fmt.Errorf("subagent: running %q: %w", def.Name, runErr)
		}
	}
	now := time.Now().UTC()
	result.CompletedAt = &now
	if runResult != nil {
		result.Usage = runResult.Usage
	}
	if runErr != nil {
		result.Status = failureStatus(runErr)
		result.Error = runErr.Error()
	} else {
		result.Status = "completed"
		if runResult != nil {
			result.Output = runResult.Output
		}
	}
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
	runErr = m.persistTerminal(persistCtx, &result, runErr)
	m.runEndHook(persistCtx, parent, result)
	publish(persistCtx, parent, runtime.EventSubagentResult, result)
	return result, runErr
}

func (m *Manager) finishLifecycle(ctx context.Context, parent, child runtime.RunContext, req DispatchRequest, result *Result, cause error) error {
	if m.lifecycle == nil {
		return cause
	}
	if err := m.lifecycle.Finish(ctx, parent, child, req, *result); err != nil {
		coordinationErr := fmt.Errorf("finalizing subagent lifecycle: %w", err)
		result.CoordinationError = joinErrorText(result.CoordinationError, coordinationErr)
		cause = errors.Join(cause, coordinationErr)
	}
	return cause
}

func (m *Manager) persistTerminal(ctx context.Context, result *Result, cause error) error {
	if err := m.store.Put(ctx, *result, m.ttl); err == nil {
		return cause
	} else {
		cause = errors.Join(cause, fmt.Errorf("persisting terminal task: %w", err))
	}

	result.Status = message.SubagentFailed
	result.Error = cause.Error()
	if err := m.store.Put(ctx, *result, m.ttl); err != nil {
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
		TaskID: result.TaskID, Subagent: result.Agent, Status: result.Status,
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
func (m *Manager) DispatchAsync(ctx context.Context, req DispatchRequest) (string, error) {
	id := strings.TrimSpace(req.ToolCallID)
	if id == "" {
		id = newTaskID()
	}
	initial := Result{TaskID: id, Agent: req.subagentType(), Status: "pending", StartedAt: time.Now().UTC()}
	if err := m.store.Put(context.WithoutCancel(ctx), initial, m.ttl); err != nil {
		return "", err
	}
	detached := context.WithoutCancel(ctx)
	go func() {
		res, err := m.dispatch(detached, req, id)
		if err != nil && res.TaskID == "" {
			_, _ = m.recordEarlyFailure(detached, req, id, err)
		}
	}()
	return id, nil
}

func (m *Manager) recordEarlyFailure(ctx context.Context, req DispatchRequest, taskID string, cause error) (Result, error) {
	now := time.Now().UTC()
	result := Result{
		TaskID: taskID, Agent: req.subagentType(), Status: failureStatus(cause),
		Error: cause.Error(), StartedAt: now, CompletedAt: &now,
	}
	parent, _ := runtime.RunContextFrom(ctx)
	persistCtx := context.WithoutCancel(ctx)
	publish(persistCtx, parent, runtime.EventSubagentStart, map[string]any{
		"task_id":       taskID,
		"agent":         result.Agent,
		"subagent_type": result.Agent,
		"description":   taskDescription(req, result.Agent),
	})
	cause = m.persistTerminal(persistCtx, &result, cause)
	m.runEndHook(persistCtx, parent, result)
	publish(persistCtx, parent, runtime.EventSubagentResult, result)
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
	return m.store.Get(ctx, id)
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
		if len(parentSet) == 0 || parentSet[n] {
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
func publish(ctx context.Context, p runtime.RunContext, t runtime.EventType, data any) {
	e := runtime.MustEvent(p.EventStreamRunID(), p.ThreadID, t, data)
	if p.Publish != nil {
		p.Publish(ctx, e)
	} else if p.Bus != nil {
		p.Bus.Publish(ctx, e)
	}
}
func newTaskID() string { return fmt.Sprintf("task-%d", time.Now().UnixNano()) }

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

func cloneRunContext(parent runtime.RunContext) runtime.RunContext {
	child := parent
	child.AllowedTools = append([]string(nil), parent.AllowedTools...)
	child.Values = cloneValues(parent.Values)
	return child
}

func cloneValues(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (m *Manager) TaskTool() tool.Definition {
	return tool.Definition{Name: "task", Group: "subagent", Description: "Delegate a bounded task to a specialized subagent and wait for its terminal result.", Parameters: json.RawMessage(`{"type":"object","properties":{"description":{"type":"string","description":"Short description shown in the task card."},"prompt":{"type":"string"},"subagent_type":{"type":"string","description":"Registered subagent type."},"name":{"type":"string","description":"Optional teammate name in swarm mode."},"max_turns":{"type":"integer","minimum":1},"restrict_tools":{"type":"array","items":{"type":"string"}},"agent":{"type":"string","description":"Legacy alias for subagent_type."}},"required":["description","prompt","subagent_type"]}`), Metadata: tool.Metadata{IsAgentState: true, IsConcurrencySafe: true}, Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
		var args struct {
			Description  string   `json:"description"`
			Prompt       string   `json:"prompt"`
			SubagentType string   `json:"subagent_type"`
			Name         string   `json:"name"`
			MaxTurns     int      `json:"max_turns"`
			Restrict     []string `json:"restrict_tools"`
			Agent        string   `json:"agent"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return taskFailure(err.Error()), nil //nolint:nilerr // malformed model input is returned to the model
		}
		agentName := args.SubagentType
		if agentName == "" {
			agentName = args.Agent
		}
		if agentName == "" {
			return taskFailure("missing required field subagent_type"), nil
		}
		if strings.TrimSpace(args.Prompt) == "" {
			return taskFailure("missing required field prompt"), nil
		}
		description := strings.TrimSpace(args.Description)
		// Legacy callers did not send a description. A short prompt fallback
		// keeps their task cards useful without changing dispatch semantics.
		if description == "" {
			description = strings.TrimSpace(args.Prompt)
			if len(description) > 80 {
				description = description[:80]
			}
		}
		req := DispatchRequest{SubagentType: agentName, Description: description, Name: strings.TrimSpace(args.Name), MaxTurns: args.MaxTurns, Agent: args.Agent, Prompt: args.Prompt, RestrictTools: args.Restrict, ToolCallID: call.ID}
		res, err := m.Dispatch(ctx, req)
		if err != nil {
			return taskFailureFromResult(res, err), nil
		}
		return taskSuccess(res.Output), nil
	}}
}

func (r DispatchRequest) subagentType() string {
	if strings.TrimSpace(r.SubagentType) != "" {
		return strings.TrimSpace(r.SubagentType)
	}
	return strings.TrimSpace(r.Agent)
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

func taskFailureFromResult(res Result, err error) *tool.Result {
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
	s.tasks[r.TaskID] = r
	s.mu.Unlock()
	return nil
}
func (s *MemoryTaskStore) Get(_ context.Context, id string) (Result, error) {
	s.mu.RLock()
	r, ok := s.tasks[id]
	s.mu.RUnlock()
	if !ok {
		return Result{}, fmt.Errorf("subagent: task %q not found", id)
	}
	return r, nil
}

type RedisTaskStore struct {
	client redis.UniversalClient
	prefix string
}

func NewRedisTaskStore(client redis.UniversalClient) *RedisTaskStore {
	return &RedisTaskStore{client: client, prefix: "nous-agent:task:"}
}
func (s *RedisTaskStore) Put(ctx context.Context, r Result, ttl time.Duration) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, s.prefix+r.TaskID, raw, ttl).Err()
}
func (s *RedisTaskStore) Get(ctx context.Context, id string) (Result, error) {
	raw, err := s.client.Get(ctx, s.prefix+id).Bytes()
	if err != nil {
		return Result{}, err
	}
	var r Result
	err = json.Unmarshal(raw, &r)
	return r, err
}
