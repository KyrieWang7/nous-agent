// Package subagent dispatches bounded nested agent loops through one task tool.
package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

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
type DispatchRequest struct {
	Agent         string
	Prompt        string
	RestrictTools []string
	ToolCallID    string
}
type Result struct {
	TaskID      string      `json:"task_id"`
	Agent       string      `json:"agent"`
	Status      string      `json:"status"`
	Output      string      `json:"output,omitempty"`
	Error       string      `json:"error,omitempty"`
	Usage       model.Usage `json:"usage"`
	StartedAt   time.Time   `json:"started_at"`
	CompletedAt *time.Time  `json:"completed_at,omitempty"`
}
type TaskStore interface {
	Put(context.Context, Result, time.Duration) error
	Get(context.Context, string) (Result, error)
}
type Manager struct {
	mu      sync.RWMutex
	defs    map[string]Definition
	factory RunnerFactory
	store   TaskStore
	ttl     time.Duration
	sem     chan struct{}
}

func NewManager(factory RunnerFactory, store TaskStore, ttl time.Duration, maxConcurrent ...int) *Manager {
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
	return &Manager{defs: map[string]Definition{}, factory: factory, store: store, ttl: ttl, sem: make(chan struct{}, limit)}
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
	return m.dispatch(ctx, req, "")
}

func (m *Manager) dispatch(ctx context.Context, req DispatchRequest, taskID string) (Result, error) {
	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	m.mu.RLock()
	def, ok := m.defs[req.Agent]
	m.mu.RUnlock()
	if !ok {
		return Result{}, fmt.Errorf("subagent: unknown agent %q", req.Agent)
	}
	parent, _ := runtime.RunContextFrom(ctx)
	allowed, err := restrict(def.AllowedTools, parent.AllowedTools, req.RestrictTools)
	if err != nil {
		return Result{}, err
	}
	if m.factory == nil {
		return Result{}, errors.New("subagent: runner factory is nil")
	}
	runner, err := m.factory(def, allowed)
	if err != nil {
		return Result{}, err
	}
	if taskID == "" {
		taskID = newTaskID()
	}
	persistCtx := context.WithoutCancel(ctx)
	result := Result{TaskID: taskID, Agent: def.Name, Status: "running", StartedAt: time.Now().UTC()}
	_ = m.store.Put(persistCtx, result, m.ttl)
	publish(persistCtx, parent, runtime.EventSubagentStart, map[string]any{"task_id": result.TaskID, "agent": def.Name})
	history := message.NewHistory()
	childJournal := runtime.NewJournal(nil)
	childContext := parent
	childContext.Journal = childJournal
	ctx = runtime.WithRunContext(ctx, childContext)
	runResult, runErr := runner.Run(ctx, loop.Request{ThreadID: parent.ThreadID, RunID: parent.RunID + ":" + result.TaskID, AssistantID: def.Name, SystemPrompt: def.SystemPrompt, History: history, Prompt: req.Prompt})
	now := time.Now().UTC()
	result.CompletedAt = &now
	if runErr != nil {
		result.Status = "failed"
		result.Error = runErr.Error()
	} else {
		result.Status = "completed"
		result.Output = runResult.Output
		result.Usage = runResult.Usage
		if parent.Journal != nil {
			if childJournal.Totals().LLMCalls == 0 {
				childJournal.Observe(runtime.Entry{Bucket: runtime.BucketLead, Source: def.Name, CallID: req.ToolCallID, Usage: runResult.Usage})
			}
			parent.Journal.Merge(def.Name, childJournal)
		}
	}
	_ = m.store.Put(persistCtx, result, m.ttl)
	publish(persistCtx, parent, runtime.EventSubagentResult, result)
	return result, runErr
}
func (m *Manager) DispatchAsync(ctx context.Context, req DispatchRequest) (string, error) {
	id := newTaskID()
	initial := Result{TaskID: id, Agent: req.Agent, Status: "pending", StartedAt: time.Now().UTC()}
	if err := m.store.Put(context.WithoutCancel(ctx), initial, m.ttl); err != nil {
		return "", err
	}
	detached := context.WithoutCancel(ctx)
	go func() {
		res, err := m.dispatch(detached, req, id)
		if err != nil {
			res.Status = "failed"
			res.Error = err.Error()
		}
		_ = m.store.Put(detached, res, m.ttl)
	}()
	return id, nil
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
	e := runtime.MustEvent(p.RunID, p.ThreadID, t, data)
	if p.Publish != nil {
		p.Publish(ctx, e)
	} else if p.Bus != nil {
		p.Bus.Publish(ctx, e)
	}
}
func newTaskID() string { return fmt.Sprintf("task-%d", time.Now().UnixNano()) }

func (m *Manager) TaskTool() tool.Definition {
	return tool.Definition{Name: "task", Group: "subagent", Description: "Delegate a bounded task to a specialized subagent.", Parameters: json.RawMessage(`{"type":"object","properties":{"agent":{"type":"string"},"prompt":{"type":"string"},"restrict_tools":{"type":"array","items":{"type":"string"}},"async":{"type":"boolean"}},"required":["agent","prompt"]}`), Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
		var args struct {
			Agent    string   `json:"agent"`
			Prompt   string   `json:"prompt"`
			Restrict []string `json:"restrict_tools"`
			Async    bool     `json:"async"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return &tool.Result{Content: err.Error(), IsError: true}, nil //nolint:nilerr // malformed model input is returned to the model
		}
		req := DispatchRequest{Agent: args.Agent, Prompt: args.Prompt, RestrictTools: args.Restrict, ToolCallID: call.ID}
		if args.Async {
			id, err := m.DispatchAsync(ctx, req)
			if err != nil {
				return &tool.Result{Content: err.Error(), IsError: true}, nil //nolint:nilerr // dispatch failures are model-visible tool results
			}
			return &tool.Result{Content: `{"task_id":"` + id + `","status":"pending"}`}, nil
		}
		res, err := m.Dispatch(ctx, req)
		raw, _ := json.Marshal(res)
		return &tool.Result{Content: string(raw), IsError: err != nil}, nil
	}}
}

func Builtins() []Definition {
	return []Definition{{Name: "general-purpose", Description: "Multi-step research and implementation", MaxTurns: 25}, {Name: "explore", Description: "Read-only codebase exploration", AllowedTools: []string{"ls", "read_file"}, MaxTurns: 15}, {Name: "plan", Description: "Analyze and produce an implementation plan", AllowedTools: []string{"ls", "read_file"}, MaxTurns: 15}, {Name: "bash", Description: "Run bounded command-line investigation", AllowedTools: []string{"bash", "ls", "read_file"}, MaxTurns: 10}, {Name: "verification", Description: "Run tests and verify completed work", AllowedTools: []string{"bash", "ls", "read_file"}, MaxTurns: 15}}
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
