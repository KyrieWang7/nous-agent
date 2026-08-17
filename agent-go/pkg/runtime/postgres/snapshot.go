package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/replay"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SnapshotStore is the PostgreSQL projection-cache adapter. Canonical events
// remain authoritative; replacing or deleting rows does not lose run facts.
type SnapshotStore struct{ pool *pgxpool.Pool }

func NewSnapshotStore(pool *pgxpool.Pool) (*SnapshotStore, error) {
	if pool == nil {
		return nil, errors.New("runtime/postgres: postgres pool is nil")
	}
	return &SnapshotStore{pool: pool}, nil
}

func (s *SnapshotStore) Save(ctx context.Context, snapshot replay.Snapshot) error {
	if snapshot.RunID == "" || snapshot.ThreadID == "" {
		return errors.New("runtime/postgres: snapshot run id and thread id are required")
	}
	messages, err := json.Marshal(snapshot.Messages)
	if err != nil {
		return fmt.Errorf("runtime/postgres: encoding snapshot messages: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO agent_projection_snapshot(run_id,thread_id,last_seq,messages,created_at)
		VALUES($1,$2,$3,$4::jsonb,$5)
		ON CONFLICT(run_id,thread_id) DO UPDATE SET
			last_seq=EXCLUDED.last_seq, messages=EXCLUDED.messages, created_at=EXCLUDED.created_at
		WHERE agent_projection_snapshot.last_seq <= EXCLUDED.last_seq`,
		snapshot.RunID, snapshot.ThreadID, snapshot.LastSeq, messages, snapshot.CreatedAt)
	if err != nil {
		return fmt.Errorf("runtime/postgres: saving projection snapshot: %w", err)
	}
	return nil
}

func (s *SnapshotStore) Load(ctx context.Context, runID, threadID string) (replay.Snapshot, error) {
	var snapshot replay.Snapshot
	var messages []byte
	err := s.pool.QueryRow(ctx, `SELECT run_id,thread_id,last_seq,messages,created_at FROM agent_projection_snapshot WHERE run_id=$1 AND thread_id=$2`, runID, threadID).Scan(
		&snapshot.RunID, &snapshot.ThreadID, &snapshot.LastSeq, &messages, &snapshot.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return replay.Snapshot{}, replay.ErrSnapshotNotFound
	}
	if err != nil {
		return replay.Snapshot{}, fmt.Errorf("runtime/postgres: loading projection snapshot: %w", err)
	}
	if err := json.Unmarshal(messages, &snapshot.Messages); err != nil {
		return replay.Snapshot{}, fmt.Errorf("runtime/postgres: decoding snapshot messages: %w", err)
	}
	if snapshot.Messages == nil {
		snapshot.Messages = []message.Message{}
	}
	return snapshot, nil
}

var _ replay.SnapshotStore = (*SnapshotStore)(nil)
