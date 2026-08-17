package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EventStore struct{ pool *pgxpool.Pool }

func NewEventStore(pool *pgxpool.Pool) (*EventStore, error) {
	if pool == nil {
		return nil, errors.New("runtime/postgres: postgres pool is nil")
	}
	return &EventStore{pool: pool}, nil
}

func (s *EventStore) PutBatch(ctx context.Context, events []runtime.Event) error {
	if len(events) == 0 {
		return nil
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for _, event := range events {
			if _, err := tx.Exec(ctx, `INSERT INTO agent_run_event(run_id,seq,thread_id,event_type,category,idempotency_key,content,created_at) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8) ON CONFLICT DO NOTHING`, event.RunID, event.Seq, event.ThreadID, event.Type, event.Category, event.IdempotencyKey, event.Data, event.CreatedAt); err != nil {
				return fmt.Errorf("runtime/postgres: inserting event: %w", err)
			}
		}
		return nil
	})
}

func (s *EventStore) Get(ctx context.Context, runID string, afterSeq int64, limit int) ([]runtime.Event, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `SELECT seq,thread_id,event_type,category,COALESCE(idempotency_key,''),content,created_at FROM agent_run_event WHERE run_id=$1 AND seq>$2 ORDER BY seq LIMIT $3`, runID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []runtime.Event
	for rows.Next() {
		var event runtime.Event
		var data []byte
		event.RunID = runID
		if err := rows.Scan(&event.Seq, &event.ThreadID, &event.Type, &event.Category, &event.IdempotencyKey, &data, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.Data = json.RawMessage(data)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *EventStore) LastSeq(ctx context.Context, runID string) (int64, error) {
	var last int64
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(seq),0) FROM agent_run_event WHERE run_id=$1`, runID).Scan(&last); err != nil {
		return 0, fmt.Errorf("runtime/postgres: reading event high-water mark: %w", err)
	}
	return last, nil
}

var (
	_ runtime.EventStore          = (*EventStore)(nil)
	_ runtime.EventSequenceReader = (*EventStore)(nil)
)
