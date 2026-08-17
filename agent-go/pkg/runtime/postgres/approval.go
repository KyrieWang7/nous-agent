package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ApprovalStore provides cross-process approval recovery. The update
// predicate makes terminal decisions first-writer-wins at the database layer.
type ApprovalStore struct{ pool *pgxpool.Pool }

func NewApprovalStore(pool *pgxpool.Pool) (*ApprovalStore, error) {
	if pool == nil {
		return nil, errors.New("runtime/postgres: postgres pool is nil")
	}
	return &ApprovalStore{pool: pool}, nil
}

func (s *ApprovalStore) Get(ctx context.Context, id string) (runtime.ApprovalRequest, error) {
	var req runtime.ApprovalRequest
	var args []byte
	var expiresAt *time.Time
	err := s.pool.QueryRow(ctx, `SELECT id,run_id,transaction_id,tool_name,args,reason,status,created_at,updated_at,expires_at,decision_by FROM agent_approval WHERE id=$1`, id).Scan(
		&req.ID, &req.RunID, &req.TransactionID, &req.ToolName, &args, &req.Reason,
		&req.Status, &req.CreatedAt, &req.UpdatedAt, &expiresAt, &req.DecisionBy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return runtime.ApprovalRequest{}, runtime.ErrApprovalNotFound
	}
	if err != nil {
		return runtime.ApprovalRequest{}, fmt.Errorf("runtime/postgres: loading approval: %w", err)
	}
	req.Args = append([]byte(nil), args...)
	if expiresAt != nil {
		req.ExpiresAt = *expiresAt
	}
	return req, nil
}

func (s *ApprovalStore) Put(ctx context.Context, req runtime.ApprovalRequest) error {
	args := req.Args
	if len(args) == 0 {
		args = []byte(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_approval(id,run_id,transaction_id,tool_name,args,reason,status,created_at,updated_at,expires_at,decision_by)
		VALUES($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9,$10,$11)
		ON CONFLICT(id) DO UPDATE SET
			status=EXCLUDED.status, updated_at=EXCLUDED.updated_at,
			expires_at=EXCLUDED.expires_at, decision_by=EXCLUDED.decision_by
		WHERE agent_approval.status='pending'`,
		req.ID, req.RunID, req.TransactionID, req.ToolName, args, req.Reason,
		req.Status, req.CreatedAt, req.UpdatedAt, nullableTime(req.ExpiresAt), req.DecisionBy,
	)
	if err != nil {
		return fmt.Errorf("runtime/postgres: saving approval: %w", err)
	}
	return nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

var _ runtime.ApprovalStore = (*ApprovalStore)(nil)
