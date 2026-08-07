// Package memory extracts and injects bounded long-term facts.
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

type Fact struct {
	ID         int64     `json:"id"`
	ThreadID   string    `json:"thread_id"`
	Text       string    `json:"fact"`
	Confidence float64   `json:"confidence"`
	CreatedAt  time.Time `json:"created_at"`
}
type Store interface {
	List(context.Context, string, int) ([]Fact, error)
	Add(context.Context, Fact) error
}
type Options struct {
	MaxFacts            int
	ConfidenceThreshold float64
	InjectionTokens     int
}
type Manager struct {
	model model.Model
	store Store
	opts  Options
}

func New(model model.Model, store Store, opts Options) (*Manager, error) {
	if model == nil {
		return nil, errors.New("memory: model is nil")
	}
	if store == nil {
		store = NewMemoryStore()
	}
	if opts.MaxFacts <= 0 {
		opts.MaxFacts = 50
	}
	if opts.ConfidenceThreshold <= 0 {
		opts.ConfidenceThreshold = .7
	}
	if opts.InjectionTokens <= 0 {
		opts.InjectionTokens = 1000
	}
	return &Manager{model: model, store: store, opts: opts}, nil
}
func (m *Manager) Extract(ctx context.Context, threadID string, msgs []message.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	raw, _ := json.Marshal(msgs)
	resp, err := m.model.Complete(ctx, model.Request{System: "Extract durable user preferences and project facts. Return only a JSON array of objects with fact and confidence. Return [] when there are none.", Messages: []message.Message{{Role: message.RoleUser, Content: string(raw)}}})
	if err != nil {
		return err
	}
	var found []struct {
		Fact       string  `json:"fact"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Message.Content)), &found); err != nil {
		return fmt.Errorf("memory: decoding extraction: %w", err)
	}
	for _, f := range found {
		if strings.TrimSpace(f.Fact) == "" || f.Confidence < m.opts.ConfidenceThreshold {
			continue
		}
		if err := m.store.Add(ctx, Fact{ThreadID: threadID, Text: f.Fact, Confidence: f.Confidence, CreatedAt: time.Now().UTC()}); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) Prompt(ctx context.Context, threadID string) (string, error) {
	facts, err := m.store.List(ctx, threadID, m.opts.MaxFacts)
	if err != nil {
		return "", err
	}
	if len(facts) == 0 {
		return "", nil
	}
	budget := m.opts.InjectionTokens * 4
	var b strings.Builder
	b.WriteString("Known durable facts for this thread:\n")
	used := 0
	for _, f := range facts {
		line := fmt.Sprintf("- %s\n", f.Text)
		if used+len(line) > budget {
			break
		}
		b.WriteString(line)
		used += len(line)
	}
	return strings.TrimSpace(b.String()), nil
}

type MemoryStore struct {
	mu       sync.RWMutex
	next     int64
	byThread map[string][]Fact
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{byThread: map[string][]Fact{}} }
func (s *MemoryStore) Add(_ context.Context, f Fact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, old := range s.byThread[f.ThreadID] {
		if old.Text == f.Text {
			return nil
		}
	}
	s.next++
	f.ID = s.next
	s.byThread[f.ThreadID] = append(s.byThread[f.ThreadID], f)
	return nil
}
func (s *MemoryStore) List(_ context.Context, threadID string, limit int) ([]Fact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]Fact(nil), s.byThread[threadID]...)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
