package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresEventStore struct{ pool *pgxpool.Pool }

func NewPostgresEventStore(pool *pgxpool.Pool) (*PostgresEventStore, error) {
	if pool == nil {
		return nil, errors.New("runtime: postgres pool is nil")
	}
	return &PostgresEventStore{pool: pool}, nil
}
func (s *PostgresEventStore) PutBatch(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for _, e := range events {
			if _, err := tx.Exec(ctx, `INSERT INTO agent_run_event(run_id,seq,thread_id,event_type,category,content,created_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(run_id,seq) DO NOTHING`, e.RunID, e.Seq, e.ThreadID, e.Type, e.Category, e.Data, e.CreatedAt); err != nil {
				return fmt.Errorf("runtime: inserting event: %w", err)
			}
		}
		return nil
	})
}
func (s *PostgresEventStore) Get(ctx context.Context, runID string, afterSeq int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `SELECT seq,thread_id,event_type,category,content,created_at FROM agent_run_event WHERE run_id=$1 AND seq>$2 ORDER BY seq LIMIT $3`, runID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var data []byte
		e.RunID = runID
		if err := rows.Scan(&e.Seq, &e.ThreadID, &e.Type, &e.Category, &data, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}
