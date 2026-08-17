package replay

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
)

// Snapshot is a performance checkpoint of a projected transcript. It is not
// the source of truth; LastSeq points back to the canonical event stream.
type Snapshot struct {
	RunID     string
	ThreadID  string
	LastSeq   int64
	Messages  []message.Message
	CreatedAt time.Time
}

// SnapshotStore persists projection checkpoints.
type SnapshotStore interface {
	Save(context.Context, Snapshot) error
	Load(context.Context, string, string) (Snapshot, error)
}

var ErrSnapshotNotFound = errors.New("replay: snapshot not found")

// MemorySnapshotStore is the test and single-process implementation.
type MemorySnapshotStore struct {
	mu    sync.RWMutex
	byKey map[string]Snapshot
}

func NewMemorySnapshotStore() *MemorySnapshotStore {
	return &MemorySnapshotStore{byKey: make(map[string]Snapshot)}
}

func (s *MemorySnapshotStore) Save(_ context.Context, snapshot Snapshot) error {
	if s == nil {
		return errors.New("replay: snapshot store is nil")
	}
	if snapshot.ThreadID == "" {
		return errors.New("replay: snapshot thread id is empty")
	}
	s.mu.Lock()
	s.byKey[key(snapshot.RunID, snapshot.ThreadID)] = cloneSnapshot(snapshot)
	s.mu.Unlock()
	return nil
}

func (s *MemorySnapshotStore) Load(_ context.Context, runID, threadID string) (Snapshot, error) {
	if s == nil {
		return Snapshot{}, errors.New("replay: snapshot store is nil")
	}
	s.mu.RLock()
	snapshot, ok := s.byKey[key(runID, threadID)]
	s.mu.RUnlock()
	if !ok {
		return Snapshot{}, ErrSnapshotNotFound
	}
	return cloneSnapshot(snapshot), nil
}

func key(runID, threadID string) string { return runID + "\x00" + threadID }

func cloneSnapshot(in Snapshot) Snapshot {
	in.Messages = message.CloneAll(in.Messages)
	return in
}
