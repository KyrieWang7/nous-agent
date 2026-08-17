package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ToolInvocation is the runtime-neutral identity of a tool call. The tool
// package can adapt its concrete Call into this value without coupling the
// lifecycle ledger to a particular executor implementation.
type ToolInvocation struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type ToolTransactionState string

const (
	ToolPending   ToolTransactionState = "pending"
	ToolRunning   ToolTransactionState = "running"
	ToolSucceeded ToolTransactionState = "succeeded"
	ToolFailed    ToolTransactionState = "failed"
	ToolDenied    ToolTransactionState = "denied"
	ToolCancelled ToolTransactionState = "cancelled"
)

var ErrInvalidToolTransition = errors.New("runtime: invalid tool transaction transition")

type ToolTransactionSnapshot struct {
	TransactionID string               `json:"transaction_id"`
	RunID         string               `json:"run_id"`
	Invocation    ToolInvocation       `json:"invocation"`
	State         ToolTransactionState `json:"state"`
	Result        json.RawMessage      `json:"result,omitempty"`
	Error         string               `json:"error,omitempty"`
	UpdatedAt     time.Time            `json:"updated_at"`
}

// ToolTransaction makes tool execution observable and idempotent. A caller
// may retry the same terminal write; conflicting terminal writes are rejected.
type ToolTransaction struct {
	mu   sync.Mutex
	snap ToolTransactionSnapshot
}

func NewToolTransaction(transactionID, runID string, invocation ToolInvocation) (*ToolTransaction, error) {
	if transactionID == "" || runID == "" || invocation.ID == "" || invocation.Name == "" {
		return nil, errors.New("runtime: transaction id, run id, invocation id and name are required")
	}
	return &ToolTransaction{snap: ToolTransactionSnapshot{
		TransactionID: transactionID,
		RunID:         runID,
		Invocation:    cloneInvocation(invocation),
		State:         ToolPending,
		UpdatedAt:     time.Now().UTC(),
	}}, nil
}

func (t *ToolTransaction) Snapshot() ToolTransactionSnapshot {
	if t == nil {
		return ToolTransactionSnapshot{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return cloneToolSnapshot(t.snap)
}

func (t *ToolTransaction) Begin() error {
	return t.change(ToolRunning)
}

func (t *ToolTransaction) Complete(result any) error {
	return t.changeTerminal(ToolSucceeded, result, "")
}

func (t *ToolTransaction) Fail(err error, result any) error {
	return t.changeTerminal(ToolFailed, result, errorText(err))
}

func (t *ToolTransaction) Deny(reason string) error {
	if reason == "" {
		reason = "tool call denied"
	}
	return t.changeTerminal(ToolDenied, nil, reason)
}

func (t *ToolTransaction) Cancel(reason string) error {
	if reason == "" {
		reason = "tool call cancelled"
	}
	return t.changeTerminal(ToolCancelled, nil, reason)
}

func (t *ToolTransaction) change(next ToolTransactionState) error {
	if t == nil {
		return errors.New("runtime: nil tool transaction")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.snap.State != ToolPending || next != ToolRunning {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidToolTransition, t.snap.State, next)
	}
	t.snap.State = next
	t.snap.UpdatedAt = time.Now().UTC()
	return nil
}

func (t *ToolTransaction) changeTerminal(next ToolTransactionState, result any, errText string) error {
	if t == nil {
		return errors.New("runtime: nil tool transaction")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	encoded, err := json.Marshal(result)
	if result != nil && err != nil {
		return fmt.Errorf("runtime: encoding tool result: %w", err)
	}
	if result == nil {
		encoded = nil
	}
	if t.snap.State == next {
		if string(t.snap.Result) == string(encoded) && t.snap.Error == errText {
			return nil
		}
		return fmt.Errorf("%w: conflicting terminal write for %s", ErrInvalidToolTransition, next)
	}
	if !validToolTerminalTransition(t.snap.State, next) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidToolTransition, t.snap.State, next)
	}
	t.snap.State = next
	t.snap.Error = errText
	t.snap.UpdatedAt = time.Now().UTC()
	if result == nil {
		t.snap.Result = nil
	} else {
		t.snap.Result = append(t.snap.Result[:0], encoded...)
	}
	return nil
}

func validToolTerminalTransition(from, to ToolTransactionState) bool {
	if to == ToolDenied || to == ToolCancelled {
		return from == ToolPending || from == ToolRunning
	}
	return from == ToolRunning && (to == ToolSucceeded || to == ToolFailed)
}

func cloneInvocation(in ToolInvocation) ToolInvocation {
	out := in
	out.Args = append(json.RawMessage(nil), in.Args...)
	return out
}

func cloneToolSnapshot(in ToolTransactionSnapshot) ToolTransactionSnapshot {
	out := in
	out.Invocation = cloneInvocation(in.Invocation)
	out.Result = append(json.RawMessage(nil), in.Result...)
	return out
}
