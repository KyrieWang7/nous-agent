package subagent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/delegation"
	"github.com/google/uuid"
)

const sessionLease = 30 * time.Second

func (m *Manager) taskStatus(ctx context.Context, id string) (Result, error) {
	if parent, ok := runtime.RunContextFrom(ctx); ok {
		if store, ok := m.store.(delegation.ScopedTaskStore); ok {
			return store.GetScoped(ctx, parent.ThreadID, parent.RunID, id)
		}
	}
	return m.store.Get(ctx, id)
}

func (m *Manager) Resume(ctx context.Context, req delegation.Request) (Result, error) {
	if req.SessionID == "" || req.Prompt == "" {
		return Result{}, errors.New("session_id and prompt are required")
	}
	return m.Dispatch(ctx, DispatchRequest{ResumeSessionID: req.SessionID, Prompt: req.Prompt})
}

func (m *Manager) loadResume(ctx context.Context, req *DispatchRequest) (*delegation.Session, error) {
	if req.ResumeSessionID == "" {
		return nil, nil
	}
	store, ok := m.store.(delegation.SessionStore)
	if !ok {
		return nil, errors.New("subagent: persistent sessions unavailable")
	}
	parent, _ := runtime.RunContextFrom(ctx)
	previous, err := store.LoadSession(ctx, parent.ThreadID, req.ResumeSessionID)
	if err != nil {
		return nil, err
	}
	if previous.ParentSessionID != parent.SubagentSessionID {
		return nil, delegation.ErrNotFound
	}
	if delegation.Active(previous.Result.Status) {
		return nil, delegation.ErrBusy
	}
	if req.SubagentType != "" && req.SubagentType != previous.Result.SubagentType {
		return nil, errors.New("subagent: continuation cannot change profile")
	}
	if req.Description == "" {
		req.Description = previous.Result.Description
	}
	req.SubagentType = previous.Result.SubagentType
	req.Provider = previous.Result.Provider
	previous.History = message.RecoverInterruptedTools(previous.History)
	return &previous, nil
}

// sessionExecution owns a fenced database lease independently of a request or
// a tool wait. A failed renewal stops execution; it never permits stale writes.
type sessionExecution struct {
	store   delegation.SessionStore
	mu      sync.Mutex
	session delegation.Session
	cancel  context.CancelFunc
	stop    chan struct{}
	done    chan struct{}
	err     error
}

func (m *Manager) openSession(ctx context.Context, parent runtime.RunContext, result *Result, def Definition, allowed []string, history *message.History, previous *delegation.Session) (context.Context, *sessionExecution, error) {
	store, ok := m.store.(delegation.SessionStore)
	if !ok {
		return ctx, nil, nil
	}
	id := uuid.NewString()
	if previous != nil {
		id = previous.ID
	}
	result.SessionID = id
	var state map[string]any
	if previous != nil {
		state = cloneValues(previous.State)
	}
	session, err := store.Acquire(ctx, delegation.Session{ID: id, ThreadID: parent.ThreadID, ParentSessionID: parent.SubagentSessionID, Result: *result, History: history.All(), State: state, AllowedTools: append([]string{}, allowed...), MaxTurns: def.MaxTurns, SystemPrompt: def.SystemPrompt}, sessionLease)
	if err != nil {
		return ctx, nil, err
	}
	owned, cancel := context.WithCancel(ctx)
	e := &sessionExecution{store: store, session: session, cancel: cancel, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(e.done)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-e.stop:
				return
			case <-tick.C:
				pollCtx, stop := context.WithTimeout(context.WithoutCancel(owned), 5*time.Second)
				canceled, err := store.Heartbeat(pollCtx, session.ID, session.Token, sessionLease)
				stop()
				if err != nil {
					e.mu.Lock()
					e.err = fmt.Errorf("subagent lease: %w", err)
					e.mu.Unlock()
					cancel()
					return
				}
				if canceled {
					cancel()
					// Keep renewing while canceled work is draining its resources.
					// Only finish releases ownership of the terminal session.
				}
			}
		}
	}()
	return owned, e, nil
}

// Child task state belongs to the child session, not the parent's thread-state
// projection. Inherited model options and permission policy are not persisted as
// child overrides, so a continuation uses the current trusted parent options.
func (e *sessionExecution) persistValues(ctx context.Context, values map[string]any) error {
	if e == nil {
		return errors.New("subagent: session state persistence unavailable")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	e.session.State = map[string]any{}
	if todos, ok := values["todos"]; ok {
		e.session.State["todos"] = cloneValue(todos)
	}
	bounded, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	return e.store.Checkpoint(bounded, e.session)
}
func (e *sessionExecution) checkpoint(ctx context.Context, history []message.Message) error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	e.session.History = message.CloneAll(history)
	bounded, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	return e.store.Checkpoint(bounded, e.session)
}

func (e *sessionExecution) pullInputs(ctx context.Context) ([]message.Message, func(context.Context, []message.Message) error, error) {
	if e == nil {
		return nil, nil, nil
	}
	store, ok := e.store.(delegation.InboxStore)
	if !ok {
		return nil, nil, nil
	}
	inputs, err := store.Pending(ctx, e.session.ID, e.session.Token)
	if err != nil {
		return nil, nil, err
	}
	messages := make([]message.Message, 0, len(inputs))
	ids := make([]string, 0, len(inputs))
	for _, input := range inputs {
		ids = append(ids, input.ID)
		messages = append(messages, message.Message{Role: message.RoleUser, Content: input.Content})
	}
	commit := func(ctx context.Context, history []message.Message) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.err != nil {
			return e.err
		}
		snapshot := e.session
		snapshot.History = message.CloneAll(history)
		if err := store.CommitInputs(ctx, snapshot, ids); err != nil {
			return err
		}
		e.session = snapshot
		return nil
	}
	return messages, commit, nil
}
func (e *sessionExecution) finish(ctx context.Context, result *Result, history []message.Message) error {
	if e == nil {
		return nil
	}
	close(e.stop)
	<-e.done
	defer e.cancel()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	e.session.Result = *result
	e.session.History = message.CloneAll(history)
	bounded, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	return e.store.Checkpoint(bounded, e.session)
}

func (m *Manager) putTask(ctx context.Context, result Result) error {
	bounded, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	return m.store.Put(bounded, result, m.ttl)
}
