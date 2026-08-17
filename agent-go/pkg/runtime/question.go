package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type QuestionStatus string

const (
	QuestionPending   QuestionStatus = "pending"
	QuestionAnswered  QuestionStatus = "answered"
	QuestionDismissed QuestionStatus = "dismissed"
	QuestionExpired   QuestionStatus = "expired"
)

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type QuestionAnswer struct {
	Selected []string `json:"selected,omitempty"`
	Custom   string   `json:"custom,omitempty"`
}

type QuestionRequest struct {
	ID         string           `json:"id"`
	RunID      string           `json:"run_id"`
	ThreadID   string           `json:"thread_id,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Header     string           `json:"header"`
	Question   string           `json:"question"`
	Detail     string           `json:"detail,omitempty"`
	Options    []QuestionOption `json:"options,omitempty"`
	Intent     string           `json:"intent,omitempty"`
	Status     QuestionStatus   `json:"status"`
	Answer     QuestionAnswer   `json:"answer,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
	ExpiresAt  time.Time        `json:"expires_at,omitempty"`
	AnsweredBy string           `json:"answered_by,omitempty"`
}

var (
	ErrQuestionNotFound = errors.New("runtime: question not found")
	ErrQuestionExists   = errors.New("runtime: question already exists")
)

type QuestionStore interface {
	Get(context.Context, string) (QuestionRequest, error)
	Put(context.Context, QuestionRequest) error
}

type MemoryQuestionStore struct {
	mu     sync.Mutex
	values map[string]QuestionRequest
}

func NewMemoryQuestionStore() *MemoryQuestionStore {
	return &MemoryQuestionStore{values: make(map[string]QuestionRequest)}
}

func (s *MemoryQuestionStore) Get(_ context.Context, id string) (QuestionRequest, error) {
	if s == nil {
		return QuestionRequest{}, ErrQuestionNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[id]
	if !ok {
		return QuestionRequest{}, ErrQuestionNotFound
	}
	return cloneQuestion(value), nil
}

func (s *MemoryQuestionStore) Put(_ context.Context, value QuestionRequest) error {
	if s == nil || value.ID == "" {
		return errors.New("runtime: question id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]QuestionRequest)
	}
	s.values[value.ID] = cloneQuestion(value)
	return nil
}

type QuestionManager struct {
	store QuestionStore
	mu    sync.Mutex
}

func NewQuestionManager(store QuestionStore) (*QuestionManager, error) {
	if store == nil {
		return nil, errors.New("runtime: question store is required")
	}
	return &QuestionManager{store: store}, nil
}

func (m *QuestionManager) Create(ctx context.Context, value QuestionRequest) (QuestionRequest, error) {
	if m == nil || m.store == nil {
		return QuestionRequest{}, errors.New("runtime: nil question manager")
	}
	if value.ID == "" || value.RunID == "" || value.Header == "" || value.Question == "" {
		return QuestionRequest{}, errors.New("runtime: question id, run id, header and question are required")
	}
	if len(value.Options) == 0 {
		return QuestionRequest{}, errors.New("runtime: question requires at least one option")
	}
	seen := make(map[string]struct{}, len(value.Options))
	for _, option := range value.Options {
		if option.Label == "" {
			return QuestionRequest{}, errors.New("runtime: question option label is required")
		}
		if _, duplicate := seen[option.Label]; duplicate {
			return QuestionRequest{}, fmt.Errorf("runtime: duplicate question option %q", option.Label)
		}
		seen[option.Label] = struct{}{}
	}
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	if value.Status == "" {
		value.Status = QuestionPending
	}
	if value.Status != QuestionPending {
		return QuestionRequest{}, errors.New("runtime: new question must be pending")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, err := m.store.Get(ctx, value.ID); err == nil {
		return existing, ErrQuestionExists
	} else if !errors.Is(err, ErrQuestionNotFound) {
		return QuestionRequest{}, err
	}
	return value, m.store.Put(ctx, value)
}

func (m *QuestionManager) Get(ctx context.Context, id string) (QuestionRequest, error) {
	if m == nil || m.store == nil {
		return QuestionRequest{}, ErrQuestionNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	value, err := m.store.Get(ctx, id)
	if err != nil {
		return QuestionRequest{}, err
	}
	if value.Status == QuestionPending && !value.ExpiresAt.IsZero() && !time.Now().Before(value.ExpiresAt) {
		value.Status, value.AnsweredBy, value.UpdatedAt = QuestionExpired, "system", time.Now().UTC()
		if err := m.store.Put(ctx, value); err != nil {
			return QuestionRequest{}, err
		}
	}
	return value, nil
}

func (m *QuestionManager) Wait(ctx context.Context, id string, pollInterval time.Duration) (QuestionRequest, error) {
	if pollInterval <= 0 {
		pollInterval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		value, err := m.Get(ctx, id)
		if err != nil {
			return QuestionRequest{}, err
		}
		if value.Status != QuestionPending {
			return value, nil
		}
		select {
		case <-ctx.Done():
			return QuestionRequest{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *QuestionManager) Answer(ctx context.Context, id string, answer QuestionAnswer, answeredBy string) (QuestionRequest, error) {
	return m.resolve(ctx, id, QuestionAnswered, answer, answeredBy)
}

func (m *QuestionManager) Dismiss(ctx context.Context, id, answeredBy string) (QuestionRequest, error) {
	return m.resolve(ctx, id, QuestionDismissed, QuestionAnswer{}, answeredBy)
}

func (m *QuestionManager) resolve(ctx context.Context, id string, status QuestionStatus, answer QuestionAnswer, answeredBy string) (QuestionRequest, error) {
	if m == nil || m.store == nil {
		return QuestionRequest{}, ErrQuestionNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	value, err := m.store.Get(ctx, id)
	if err != nil {
		return QuestionRequest{}, err
	}
	if value.Status != QuestionPending {
		return value, nil
	}
	if !value.ExpiresAt.IsZero() && !time.Now().Before(value.ExpiresAt) {
		status, answer, answeredBy = QuestionExpired, QuestionAnswer{}, "system"
	}
	if status == QuestionAnswered {
		if err := validateQuestionAnswer(value, answer); err != nil {
			return QuestionRequest{}, err
		}
	}
	value.Status, value.Answer, value.AnsweredBy, value.UpdatedAt = status, cloneAnswer(answer), answeredBy, time.Now().UTC()
	if err := m.store.Put(ctx, value); err != nil {
		return QuestionRequest{}, err
	}
	return m.store.Get(ctx, id)
}

func validateQuestionAnswer(question QuestionRequest, answer QuestionAnswer) error {
	if len(answer.Selected) != 1 {
		return errors.New("runtime: question answer must select exactly one option")
	}
	for _, option := range question.Options {
		if answer.Selected[0] == option.Label {
			return nil
		}
	}
	return fmt.Errorf("runtime: unknown question option %q", answer.Selected[0])
}

func cloneQuestion(in QuestionRequest) QuestionRequest {
	out := in
	out.Options = append([]QuestionOption(nil), in.Options...)
	out.Answer = cloneAnswer(in.Answer)
	return out
}

func cloneAnswer(in QuestionAnswer) QuestionAnswer {
	in.Selected = append([]string(nil), in.Selected...)
	return in
}
