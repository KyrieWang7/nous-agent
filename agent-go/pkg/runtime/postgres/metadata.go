package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/metadata"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/storage"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MetadataStore persists the state required by Runtime execution. Product
// thread search and HTTP response DTOs remain in the transport adapter.
type MetadataStore struct {
	pool       *pgxpool.Pool
	transcript *storage.Postgres
}

func NewMetadataStore(pool *pgxpool.Pool) (*MetadataStore, error) {
	if pool == nil {
		return nil, errors.New("runtime/postgres: postgres pool is nil")
	}
	transcript, err := storage.NewPostgres(pool)
	if err != nil {
		return nil, err
	}
	return &MetadataStore{pool: pool, transcript: transcript}, nil
}

func (s *MetadataStore) LoadHistory(ctx context.Context, threadID string) ([]message.Message, error) {
	return s.transcript.LoadHistory(ctx, threadID)
}

func (s *MetadataStore) SaveHistory(ctx context.Context, threadID string, messages []message.Message, replace bool) error {
	if replace {
		return s.transcript.ReplaceTranscript(ctx, threadID, messages)
	}
	return s.transcript.AppendMessages(ctx, threadID, messages)
}

func (s *MetadataStore) LoadThreadValues(ctx context.Context, threadID string) (map[string]any, error) {
	var state []byte
	var title string
	if err := s.pool.QueryRow(ctx, `SELECT state,title FROM agent_thread WHERE id=$1`, threadID).Scan(&state, &title); err != nil {
		return nil, fmt.Errorf("runtime/postgres: loading thread values: %w", err)
	}
	values := map[string]any{}
	if err := json.Unmarshal(state, &values); err != nil {
		return nil, fmt.Errorf("runtime/postgres: decoding thread values: %w", err)
	}
	if title != "" {
		values["title"] = title
	}
	return values, nil
}

func (s *MetadataStore) SaveThreadValues(ctx context.Context, threadID string, values map[string]any) error {
	if values == nil {
		values = map[string]any{}
	}
	state, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("runtime/postgres: encoding thread values: %w", err)
	}
	title, _ := values["title"].(string)
	tag, err := s.pool.Exec(ctx, `UPDATE agent_thread SET state=state||$2::jsonb,title=CASE WHEN $3='' THEN title ELSE $3 END,updated_at=NOW() WHERE id=$1`, threadID, state, title)
	if err != nil {
		return fmt.Errorf("runtime/postgres: saving thread values: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("runtime/postgres: thread %q not found", threadID)
	}
	return nil
}

func (s *MetadataStore) MarkRunRunning(ctx context.Context, runID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE agent_run SET status='running' WHERE id=$1 AND status='pending'`, runID)
	if err != nil {
		return fmt.Errorf("runtime/postgres: marking run running: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var status string
		if err := s.pool.QueryRow(ctx, `SELECT status FROM agent_run WHERE id=$1`, runID).Scan(&status); err != nil {
			return fmt.Errorf("runtime/postgres: loading run after start conflict: %w", err)
		}
		if status != "running" {
			return fmt.Errorf("runtime/postgres: cannot start run %q from status %q", runID, status)
		}
	}
	return nil
}

func (s *MetadataStore) ConfirmRunTerminal(ctx context.Context, runID string, state metadata.TerminalState) (metadata.TerminalState, error) {
	terminal := state.Status != "" && state.Status != "pending" && state.Status != "running"
	if !terminal {
		return metadata.TerminalState{}, fmt.Errorf("runtime/postgres: status %q is not terminal", state.Status)
	}
	_, err := s.pool.Exec(ctx, `UPDATE agent_run SET status=$2,risk_level=$3,completed_at=NOW(),duration_ms=EXTRACT(EPOCH FROM (NOW()-started_at))*1000 WHERE id=$1 AND status IN ('pending','running')`, runID, state.Status, state.RiskLevel)
	if err != nil {
		return metadata.TerminalState{}, fmt.Errorf("runtime/postgres: confirming terminal run: %w", err)
	}
	var confirmed metadata.TerminalState
	if err := s.pool.QueryRow(ctx, `SELECT status,risk_level FROM agent_run WHERE id=$1`, runID).Scan(&confirmed.Status, &confirmed.RiskLevel); err != nil {
		return metadata.TerminalState{}, fmt.Errorf("runtime/postgres: loading terminal run: %w", err)
	}
	return confirmed, nil
}

func (s *MetadataStore) SaveCompletion(ctx context.Context, completion metadata.Completion) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_run_completion (
			run_id,thread_id,status,iterations,llm_call_count,input_tokens,output_tokens,
			cache_read_tokens,lead_tokens,subagent_tokens,auxiliary_tokens,cost_micros,duration_ms,completed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (run_id) DO UPDATE SET
			status=EXCLUDED.status,iterations=EXCLUDED.iterations,llm_call_count=EXCLUDED.llm_call_count,
			input_tokens=EXCLUDED.input_tokens,output_tokens=EXCLUDED.output_tokens,
			cache_read_tokens=EXCLUDED.cache_read_tokens,lead_tokens=EXCLUDED.lead_tokens,
			subagent_tokens=EXCLUDED.subagent_tokens,auxiliary_tokens=EXCLUDED.auxiliary_tokens,
			cost_micros=EXCLUDED.cost_micros,duration_ms=EXCLUDED.duration_ms,completed_at=EXCLUDED.completed_at`,
		completion.RunID, completion.ThreadID, completion.Status, completion.Iterations, completion.LLMCalls,
		completion.InputTokens, completion.OutputTokens, completion.CachedInputTokens, completion.LeadTokens,
		completion.SubagentTokens, completion.AuxiliaryTokens, completion.CostMicros,
		completion.Duration.Milliseconds(), completion.CompletedAt)
	if err != nil {
		return fmt.Errorf("runtime/postgres: saving run completion: %w", err)
	}
	return nil
}

var _ metadata.Store = (*MetadataStore)(nil)
