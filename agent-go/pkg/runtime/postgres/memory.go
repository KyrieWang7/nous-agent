package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/memory"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MemoryStore struct{ pool *pgxpool.Pool }

func NewMemoryStore(pool *pgxpool.Pool) (*MemoryStore, error) {
	if pool == nil {
		return nil, errors.New("runtime/postgres: postgres pool is nil")
	}
	return &MemoryStore{pool: pool}, nil
}

func (s *MemoryStore) Add(ctx context.Context, fact memory.Fact) error {
	if fact.CreatedAt.IsZero() {
		fact.CreatedAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO memory_fact (user_id,project_id,thread_id,fact,confidence,created_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (user_id,project_id,thread_id,fact) DO UPDATE
		SET confidence=GREATEST(memory_fact.confidence,EXCLUDED.confidence)`,
		fact.UserID, fact.ProjectID, fact.ThreadID, fact.Text, fact.Confidence, fact.CreatedAt)
	if err != nil {
		return fmt.Errorf("runtime/postgres: saving memory fact: %w", err)
	}
	return nil
}

func (s *MemoryStore) List(ctx context.Context, scope memory.Scope, limit int) ([]memory.Fact, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id,user_id,project_id,thread_id,fact,confidence,created_at
		FROM memory_fact WHERE user_id=$1 AND project_id=$2 AND thread_id=$3
		ORDER BY created_at DESC,id DESC LIMIT $4`, scope.UserID, scope.ProjectID, scope.ThreadID, limit)
	if err != nil {
		return nil, fmt.Errorf("runtime/postgres: listing memory facts: %w", err)
	}
	defer rows.Close()
	var facts []memory.Fact
	for rows.Next() {
		var fact memory.Fact
		if err := rows.Scan(&fact.ID, &fact.UserID, &fact.ProjectID, &fact.ThreadID, &fact.Text, &fact.Confidence, &fact.CreatedAt); err != nil {
			return nil, fmt.Errorf("runtime/postgres: scanning memory fact: %w", err)
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runtime/postgres: listing memory facts: %w", err)
	}
	return facts, nil
}

var _ memory.Store = (*MemoryStore)(nil)
