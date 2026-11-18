package subagent

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

var ErrClosed = errors.New("subagent: manager is closed")

type execution struct {
	key               string
	taskID            string
	parentID          string
	threadID          string
	cancel            context.CancelFunc
	done              chan struct{}
	coordinationError string
}

// accept snapshots trusted input before the first wait. An asynchronous task
// belongs to the parent run, not the tool scheduler's shorter dispatch context.
func (m *Manager) accept(ctx context.Context, req DispatchRequest, id string, background bool) (context.Context, *execution, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	parent, ok := runtime.RunContextFrom(ctx)
	if !ok {
		return nil, nil, errors.New("subagent: dispatch requires a run context")
	}
	parent.Values = cloneValues(parent.Values)
	parent.AllowedTools = slices.Clone(parent.AllowedTools)
	parent.AllowedCapabilities = slices.Clone(parent.AllowedCapabilities)
	parent.DelegationHistory = message.CloneAll(parent.DelegationHistory)
	owner := ctx
	if background && parent.ExecutionContext != nil {
		owner = parent.ExecutionContext
	}
	owned, cancel := context.WithCancel(owner)
	if m.timeout != nil {
		if limit := m.timeout(parent, req); limit > 0 {
			timed, stop := context.WithTimeout(owned, limit)
			original := cancel
			owned = timed
			cancel = func() { stop(); original() }
		}
	}
	handle := &execution{key: parent.ThreadID + "\x00" + parent.RunID + "\x00" + id, taskID: id, parentID: parent.RunID, threadID: parent.ThreadID, cancel: cancel, done: make(chan struct{})}
	m.ownershipMu.Lock()
	if m.closed {
		m.ownershipMu.Unlock()
		cancel()
		return nil, nil, ErrClosed
	}
	if _, exists := m.active[handle.key]; exists {
		m.ownershipMu.Unlock()
		cancel()
		return nil, nil, fmt.Errorf("subagent: task %q is already active", id)
	}
	m.active[handle.key] = handle
	m.ownershipMu.Unlock()
	if background && parent.ExecutionOwner != nil {
		if err := parent.ExecutionOwner.OnDispose(func(context.Context) error {
			handle.cancel()
			<-handle.done
			if handle.coordinationError != "" {
				return errors.New(handle.coordinationError)
			}
			return nil
		}); err != nil {
			m.releaseExecution(handle)
			return nil, nil, err
		}
	}
	return runtime.WithRunContext(owned, parent), handle, nil
}

func (m *Manager) releaseExecution(handle *execution) {
	handle.cancel()
	m.ownershipMu.Lock()
	delete(m.active, handle.key)
	close(handle.done)
	m.ownershipMu.Unlock()
}

// Close rejects new work, cancels accepted and queued children, and waits for
// runner release and terminal persistence. Racing callers share quiescence.
func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.ownershipMu.Lock()
		m.closed = true
		handles := make([]*execution, 0, len(m.active))
		for _, handle := range m.active {
			handles = append(handles, handle)
			handle.cancel()
		}
		m.ownershipMu.Unlock()
		for _, handle := range handles {
			<-handle.done
			if handle.coordinationError != "" {
				m.closeErr = errors.Join(m.closeErr, errors.New(handle.coordinationError))
			}
		}
		close(m.closeDone)
	})
	<-m.closeDone
	return m.closeErr
}

// Interrupt is scoped to direct children of the calling execution. It requests
// cancellation without pretending that resource release has already completed.
func (m *Manager) Interrupt(ctx context.Context, id string) error {
	parent, ok := runtime.RunContextFrom(ctx)
	if !ok {
		return errors.New("subagent: interrupt requires a parent run context")
	}
	key := parent.ThreadID + "\x00" + parent.RunID + "\x00" + id
	m.ownershipMu.Lock()
	defer m.ownershipMu.Unlock()
	handle, ok := m.active[key]
	if !ok {
		return fmt.Errorf("subagent: active child %q not found", id)
	}
	handle.cancel()
	return nil
}

// Wait waits for this parent's accepted child to settle. Canceling this wait
// does not cancel the child; Interrupt or parent execution cancellation does.
func (m *Manager) Wait(ctx context.Context, id string) (Result, error) {
	parent, ok := runtime.RunContextFrom(ctx)
	if !ok {
		return Result{}, errors.New("subagent: wait requires a parent run context")
	}
	key := parent.ThreadID + "\x00" + parent.RunID + "\x00" + id
	m.ownershipMu.Lock()
	handle := m.active[key]
	m.ownershipMu.Unlock()
	if handle != nil {
		restore, err := m.beginSubagentWait(parent.StateMachine)
		if err != nil {
			return Result{}, err
		}
		defer restore()
		select {
		case <-handle.done:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	result, err := m.taskStatus(ctx, id)
	if err != nil {
		return Result{}, err
	}
	if result.ParentRunID != parent.RunID || result.ThreadID != parent.ThreadID {
		return Result{}, fmt.Errorf("subagent: child %q not found", id)
	}
	return result, nil
}

// cloneValueMap clones the JSON-shaped mutable policy/context data while
// retaining opaque typed capabilities, whose ownership belongs to the runtime.
func cloneValueMap(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for name, value := range values {
		out[name] = cloneValue(value)
	}
	return out
}
func cloneValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneValueMap(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = cloneValue(item)
		}
		return out
	case []string:
		return slices.Clone(v)
	case map[string]string:
		out := make(map[string]string, len(v))
		for k, item := range v {
			out[k] = item
		}
		return out
	case []byte:
		return slices.Clone(v)
	default:
		return value
	}
}
