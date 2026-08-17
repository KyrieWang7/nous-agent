package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type QuestionStore struct{ pool *pgxpool.Pool }

func NewQuestionStore(pool *pgxpool.Pool) (*QuestionStore, error) {
	if pool == nil {
		return nil, errors.New("runtime/postgres: postgres pool is nil")
	}
	return &QuestionStore{pool: pool}, nil
}

func (s *QuestionStore) Get(ctx context.Context, id string) (runtime.QuestionRequest, error) {
	var value runtime.QuestionRequest
	var options, answer []byte
	var expiresAt *time.Time
	err := s.pool.QueryRow(ctx, `SELECT id,run_id,thread_id,tool_call_id,header,question,detail,options,intent,status,answer,created_at,updated_at,expires_at,answered_by FROM agent_question WHERE id=$1`, id).Scan(
		&value.ID, &value.RunID, &value.ThreadID, &value.ToolCallID, &value.Header, &value.Question,
		&value.Detail, &options, &value.Intent, &value.Status, &answer, &value.CreatedAt, &value.UpdatedAt,
		&expiresAt, &value.AnsweredBy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return runtime.QuestionRequest{}, runtime.ErrQuestionNotFound
	}
	if err != nil {
		return runtime.QuestionRequest{}, fmt.Errorf("runtime/postgres: loading question: %w", err)
	}
	if err := json.Unmarshal(options, &value.Options); err != nil {
		return runtime.QuestionRequest{}, fmt.Errorf("runtime/postgres: decoding question options: %w", err)
	}
	if err := json.Unmarshal(answer, &value.Answer); err != nil {
		return runtime.QuestionRequest{}, fmt.Errorf("runtime/postgres: decoding question answer: %w", err)
	}
	if expiresAt != nil {
		value.ExpiresAt = *expiresAt
	}
	return value, nil
}

func (s *QuestionStore) Put(ctx context.Context, value runtime.QuestionRequest) error {
	options, err := json.Marshal(value.Options)
	if err != nil {
		return fmt.Errorf("runtime/postgres: encoding question options: %w", err)
	}
	answer, err := json.Marshal(value.Answer)
	if err != nil {
		return fmt.Errorf("runtime/postgres: encoding question answer: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO agent_question(id,run_id,thread_id,tool_call_id,header,question,detail,options,intent,status,answer,created_at,updated_at,expires_at,answered_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11::jsonb,$12,$13,$14,$15)
		ON CONFLICT(id) DO UPDATE SET
			status=EXCLUDED.status,answer=EXCLUDED.answer,updated_at=EXCLUDED.updated_at,
			expires_at=EXCLUDED.expires_at,answered_by=EXCLUDED.answered_by
		WHERE agent_question.status='pending'`,
		value.ID, value.RunID, value.ThreadID, value.ToolCallID, value.Header, value.Question, value.Detail,
		options, value.Intent, value.Status, answer, value.CreatedAt, value.UpdatedAt, nullableTime(value.ExpiresAt), value.AnsweredBy,
	)
	if err != nil {
		return fmt.Errorf("runtime/postgres: saving question: %w", err)
	}
	return nil
}

var _ runtime.QuestionStore = (*QuestionStore)(nil)
