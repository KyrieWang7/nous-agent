package runtime

import (
	"context"
	"errors"
	"sync"
	"time"
)

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
	ApprovalExpired  ApprovalStatus = "expired"
)

type ApprovalRequest struct {
	ID            string         `json:"id"`
	RunID         string         `json:"run_id"`
	TransactionID string         `json:"transaction_id"`
	ToolName      string         `json:"tool_name"`
	Args          []byte         `json:"args,omitempty"`
	Reason        string         `json:"reason,omitempty"`
	Status        ApprovalStatus `json:"status"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	ExpiresAt     time.Time      `json:"expires_at,omitempty"`
	DecisionBy    string         `json:"decision_by,omitempty"`
}

var (
	ErrApprovalNotFound = errors.New("runtime: approval not found")
	ErrApprovalExists   = errors.New("runtime: approval already exists")
)

// ApprovalStore is the durable boundary. A database-backed implementation can
// replace MemoryApprovalStore without changing run or tool lifecycle code.
type ApprovalStore interface {
	Get(context.Context, string) (ApprovalRequest, error)
	Put(context.Context, ApprovalRequest) error
}

type MemoryApprovalStore struct {
	mu     sync.Mutex
	values map[string]ApprovalRequest
}

func NewMemoryApprovalStore() *MemoryApprovalStore {
	return &MemoryApprovalStore{values: make(map[string]ApprovalRequest)}
}

func (s *MemoryApprovalStore) Get(_ context.Context, id string) (ApprovalRequest, error) {
	if s == nil {
		return ApprovalRequest{}, ErrApprovalNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	req, ok := s.values[id]
	if !ok {
		return ApprovalRequest{}, ErrApprovalNotFound
	}
	return cloneApproval(req), nil
}

func (s *MemoryApprovalStore) Put(_ context.Context, req ApprovalRequest) error {
	if s == nil || req.ID == "" {
		return errors.New("runtime: approval id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]ApprovalRequest)
	}
	s.values[req.ID] = cloneApproval(req)
	return nil
}

// ApprovalManager owns durable, idempotent approval transitions. It is safe
// for HTTP callbacks and worker completion races to call Decide concurrently.
type ApprovalManager struct {
	store ApprovalStore
	mu    sync.Mutex
}

func NewApprovalManager(store ApprovalStore) (*ApprovalManager, error) {
	if store == nil {
		return nil, errors.New("runtime: approval store is required")
	}
	return &ApprovalManager{store: store}, nil
}

func (m *ApprovalManager) Create(ctx context.Context, req ApprovalRequest) (ApprovalRequest, error) {
	if m == nil || m.store == nil {
		return ApprovalRequest{}, errors.New("runtime: nil approval manager")
	}
	if req.ID == "" || req.RunID == "" || req.TransactionID == "" || req.ToolName == "" {
		return ApprovalRequest{}, errors.New("runtime: approval id, run id, transaction id and tool name are required")
	}
	now := time.Now().UTC()
	if req.CreatedAt.IsZero() {
		req.CreatedAt = now
	}
	req.UpdatedAt = now
	if req.Status == "" {
		req.Status = ApprovalPending
	}
	if req.Status != ApprovalPending {
		return ApprovalRequest{}, errors.New("runtime: new approval must be pending")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, err := m.store.Get(ctx, req.ID); err == nil {
		return existing, ErrApprovalExists
	} else if !errors.Is(err, ErrApprovalNotFound) {
		return ApprovalRequest{}, err
	}
	return req, m.store.Put(ctx, req)
}

func (m *ApprovalManager) Get(ctx context.Context, id string) (ApprovalRequest, error) {
	if m == nil || m.store == nil {
		return ApprovalRequest{}, ErrApprovalNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	req, err := m.store.Get(ctx, id)
	if err != nil {
		return ApprovalRequest{}, err
	}
	if req.Status == ApprovalPending && !req.ExpiresAt.IsZero() && !time.Now().Before(req.ExpiresAt) {
		if err := m.transition(ctx, req, ApprovalExpired, "system"); err != nil {
			return ApprovalRequest{}, err
		}
		req.Status = ApprovalExpired
		req.DecisionBy = "system"
		req.UpdatedAt = time.Now().UTC()
	}
	return req, nil
}

// Wait blocks until an approval reaches a terminal decision or ctx ends. The
// store remains authoritative, so this works across processes and restarts.
func (m *ApprovalManager) Wait(ctx context.Context, id string, pollInterval time.Duration) (ApprovalRequest, error) {
	if pollInterval <= 0 {
		pollInterval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		req, err := m.Get(ctx, id)
		if err != nil {
			return ApprovalRequest{}, err
		}
		if req.Status != ApprovalPending {
			return req, nil
		}
		select {
		case <-ctx.Done():
			return ApprovalRequest{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *ApprovalManager) Decide(ctx context.Context, id string, approved bool, decidedBy string) (ApprovalRequest, error) {
	if m == nil || m.store == nil {
		return ApprovalRequest{}, ErrApprovalNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	req, err := m.store.Get(ctx, id)
	if err != nil {
		return ApprovalRequest{}, err
	}
	if req.Status != ApprovalPending {
		return req, nil
	}
	if !req.ExpiresAt.IsZero() && !time.Now().Before(req.ExpiresAt) {
		req.Status = ApprovalExpired
	} else if approved {
		req.Status = ApprovalApproved
	} else {
		req.Status = ApprovalRejected
	}
	req.DecisionBy = decidedBy
	req.UpdatedAt = time.Now().UTC()
	if err := m.store.Put(ctx, req); err != nil {
		return ApprovalRequest{}, err
	}
	return m.store.Get(ctx, id)
}

func (m *ApprovalManager) transition(ctx context.Context, req ApprovalRequest, status ApprovalStatus, by string) error {
	req.Status, req.DecisionBy, req.UpdatedAt = status, by, time.Now().UTC()
	return m.store.Put(ctx, req)
}

func cloneApproval(in ApprovalRequest) ApprovalRequest {
	out := in
	out.Args = append([]byte(nil), in.Args...)
	return out
}
