package langgraphapi

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
)

var (
	ErrThreadNotFound = errors.New("langgraphapi: thread not found")
	ErrRunNotFound    = errors.New("langgraphapi: run not found")
)

// Store captures adapter state without imposing a database on the harness.
// The in-memory and Postgres implementations share this contract.
type Store interface {
	CreateThread(context.Context, Thread, bool) (Thread, error)
	GetThread(context.Context, string) (Thread, error)
	UpdateThread(context.Context, string, map[string]any, map[string]any) (Thread, error)
	DeleteThread(context.Context, string) error
	SearchThreads(context.Context) ([]Thread, error)
	LoadHistory(context.Context, string) ([]message.Message, error)
	SaveHistory(context.Context, string, []message.Message, bool) error
	CreateRun(context.Context, Run) error
	UpdateRun(context.Context, string, RunUpdate) (Run, error)
	GetRun(context.Context, string) (Run, error)
	ListRuns(context.Context, string) ([]Run, error)
	SaveRunCompletion(context.Context, RunCompletion) error
	LatestRunCompletion(context.Context, string) (RunCompletion, bool, error)
}

type MemoryStore struct {
	mu          sync.RWMutex
	threads     map[string]Thread
	history     map[string][]message.Message
	runs        map[string]Run
	runOrder    []string
	completions map[string]RunCompletion
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		threads:     make(map[string]Thread),
		history:     make(map[string][]message.Message),
		runs:        make(map[string]Run),
		completions: make(map[string]RunCompletion),
	}
}

func (s *MemoryStore) SaveRunCompletion(_ context.Context, completion RunCompletion) error {
	s.mu.Lock()
	s.completions[completion.RunID] = completion
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) RunCompletion(runID string) (RunCompletion, bool) {
	s.mu.RLock()
	completion, ok := s.completions[runID]
	s.mu.RUnlock()
	return completion, ok
}

func (s *MemoryStore) LatestRunCompletion(_ context.Context, threadID string) (RunCompletion, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest RunCompletion
	found := false
	for _, completion := range s.completions {
		if completion.ThreadID != threadID || completion.LLMCalls == 0 {
			continue
		}
		if !found || completion.CompletedAt.After(latest.CompletedAt) {
			latest, found = completion, true
		}
	}
	return latest, found, nil
}

func (s *MemoryStore) CreateThread(_ context.Context, t Thread, keep bool) (Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.threads[t.ThreadID]; ok && keep {
		return cloneThread(old), nil
	}
	s.threads[t.ThreadID] = cloneThread(t)
	return cloneThread(t), nil
}

func (s *MemoryStore) GetThread(_ context.Context, id string) (Thread, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.threads[id]
	if !ok {
		return Thread{}, ErrThreadNotFound
	}
	return cloneThread(t), nil
}

func (s *MemoryStore) UpdateThread(_ context.Context, id string, metadata, values map[string]any) (Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.threads[id]
	if !ok {
		return Thread{}, ErrThreadNotFound
	}
	if metadata != nil {
		if t.Metadata == nil {
			t.Metadata = make(map[string]any)
		}
		for k, v := range metadata {
			t.Metadata[k] = v
		}
	}
	if values != nil {
		if t.Values == nil {
			t.Values = make(map[string]any)
		}
		for k, v := range values {
			t.Values[k] = v
		}
	}
	t.Updated = time.Now().UTC()
	s.threads[id] = t
	return cloneThread(t), nil
}

func (s *MemoryStore) DeleteThread(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.threads, id)
	delete(s.history, id)
	return nil
}

func (s *MemoryStore) SearchThreads(_ context.Context) ([]Thread, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Thread, 0, len(s.threads))
	for _, t := range s.threads {
		out = append(out, cloneThread(t))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

func (s *MemoryStore) LoadHistory(_ context.Context, id string) ([]message.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return message.CloneAll(s.history[id]), nil
}

func (s *MemoryStore) SaveHistory(_ context.Context, id string, msgs []message.Message, replace bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if replace {
		s.history[id] = message.CloneAll(msgs)
	} else {
		s.history[id] = append(s.history[id], message.CloneAll(msgs)...)
	}
	t, ok := s.threads[id]
	if ok {
		t.Updated = time.Now().UTC()
		s.threads[id] = t
	}
	return nil
}

func (s *MemoryStore) CreateRun(_ context.Context, run Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[run.RunID] = run
	s.runOrder = append(s.runOrder, run.RunID)
	return nil
}

func (s *MemoryStore) UpdateRun(_ context.Context, id string, update RunUpdate) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return Run{}, ErrRunNotFound
	}
	r.Status = update.Status
	if update.RiskLevel != "" {
		r.RiskLevel = update.RiskLevel
	}
	if update.Status != "pending" && update.Status != "running" {
		now := time.Now().UTC()
		r.Completed = &now
	}
	s.runs[id] = r
	return r, nil
}

func (s *MemoryStore) GetRun(_ context.Context, id string) (Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.runs[id]
	if !ok {
		return Run{}, ErrRunNotFound
	}
	return r, nil
}

func (s *MemoryStore) ListRuns(_ context.Context, threadID string) ([]Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Run, 0)
	for _, id := range s.runOrder {
		r := s.runs[id]
		if r.ThreadID == threadID {
			out = append(out, r)
		}
	}
	return out, nil
}

func cloneThread(t Thread) Thread {
	t.Metadata = cloneMap(t.Metadata)
	t.Values = cloneMap(t.Values)
	t.Config = cloneMap(t.Config)
	return t
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
