package memory

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, errors.New("memory: postgres pool is nil")
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) Add(ctx context.Context, f Fact) error {
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO memory_fact (thread_id, fact, confidence, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (thread_id, fact) DO UPDATE
		SET confidence = GREATEST(memory_fact.confidence, EXCLUDED.confidence)`,
		f.ThreadID, f.Text, f.Confidence, f.CreatedAt)
	return err
}

func (s *PostgresStore) List(ctx context.Context, threadID string, limit int) ([]Fact, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, thread_id, fact, confidence, created_at
		FROM memory_fact WHERE thread_id = $1
		ORDER BY created_at DESC, id DESC LIMIT $2`, threadID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Fact
	for rows.Next() {
		var f Fact
		if err := rows.Scan(&f.ID, &f.ThreadID, &f.Text, &f.Confidence, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
