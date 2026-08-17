package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	transcript "github.com/KyrieWang7/nous-agent/agent-go/pkg/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool       *pgxpool.Pool
	transcript *transcript.Postgres
}

func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	ts, err := transcript.NewPostgres(pool)
	if err != nil {
		return nil, err
	}
	return &PostgresStore{pool: pool, transcript: ts}, nil
}

func (s *PostgresStore) CreateThread(ctx context.Context, t Thread, keep bool) (Thread, error) {
	meta, _ := json.Marshal(t.Metadata)
	state, _ := json.Marshal(t.Values)
	query := `INSERT INTO agent_thread(id,assistant_id,state,metadata,created_at,updated_at) VALUES($1,'lead_agent',$2,$3,$4,$4)`
	if keep {
		query += ` ON CONFLICT(id) DO NOTHING`
	} else {
		query += ` ON CONFLICT(id) DO UPDATE SET state=EXCLUDED.state,metadata=EXCLUDED.metadata,updated_at=EXCLUDED.updated_at`
	}
	if _, err := s.pool.Exec(ctx, query, t.ThreadID, state, meta, t.Created); err != nil {
		return Thread{}, err
	}
	return s.GetThread(ctx, t.ThreadID)
}
func (s *PostgresStore) GetThread(ctx context.Context, id string) (Thread, error) {
	var t Thread
	var title string
	var state, meta []byte
	err := s.pool.QueryRow(ctx, `SELECT id,title,state,metadata,created_at,updated_at FROM agent_thread WHERE id=$1`, id).Scan(&t.ThreadID, &title, &state, &meta, &t.Created, &t.Updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return Thread{}, ErrThreadNotFound
	}
	if err != nil {
		return Thread{}, err
	}
	_ = json.Unmarshal(state, &t.Values)
	_ = json.Unmarshal(meta, &t.Metadata)
	if title != "" {
		if t.Values == nil {
			t.Values = map[string]any{}
		}
		t.Values["title"] = title
	}
	t.Status = "idle"
	return t, nil
}
func (s *PostgresStore) UpdateThread(ctx context.Context, id string, metadata, values map[string]any) (Thread, error) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if values == nil {
		values = map[string]any{}
	}
	meta, _ := json.Marshal(metadata)
	state, _ := json.Marshal(values)
	title, _ := values["title"].(string)
	tag, err := s.pool.Exec(ctx, `UPDATE agent_thread SET metadata=metadata||$2::jsonb,state=state||$3::jsonb,title=CASE WHEN $4='' THEN title ELSE $4 END,updated_at=NOW() WHERE id=$1`, id, meta, state, title)
	if err != nil {
		return Thread{}, err
	}
	if tag.RowsAffected() == 0 {
		return Thread{}, ErrThreadNotFound
	}
	return s.GetThread(ctx, id)
}
func (s *PostgresStore) DeleteThread(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM agent_thread WHERE id=$1`, id)
	return err
}
func (s *PostgresStore) SearchThreads(ctx context.Context) ([]Thread, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM agent_thread ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	var out []Thread
	for _, id := range ids {
		t, err := s.GetThread(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *PostgresStore) LoadHistory(ctx context.Context, id string) ([]message.Message, error) {
	return s.transcript.LoadHistory(ctx, id)
}
func (s *PostgresStore) SaveHistory(ctx context.Context, id string, msgs []message.Message, replace bool) error {
	if replace {
		return s.transcript.ReplaceTranscript(ctx, id, msgs)
	}
	return s.transcript.AppendMessages(ctx, id, msgs)
}
func (s *PostgresStore) CreateRun(ctx context.Context, r Run) error {
	meta, _ := json.Marshal(r.Metadata)
	_, err := s.pool.Exec(ctx, `INSERT INTO agent_run(id,thread_id,assistant_id,status,on_disconnect,metadata,started_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, r.RunID, r.ThreadID, r.AssistantID, r.Status, r.OnDisconnect, meta, r.Created)
	return err
}
func (s *PostgresStore) UpdateRun(ctx context.Context, id string, update RunUpdate) (Run, error) {
	terminal := runStatusTerminal(update.Status)
	tag, err := s.pool.Exec(ctx, `UPDATE agent_run SET status=$2,risk_level=CASE WHEN $3='' THEN risk_level ELSE $3 END,completed_at=CASE WHEN $4 THEN NOW() ELSE completed_at END,duration_ms=CASE WHEN $4 THEN EXTRACT(EPOCH FROM (NOW()-started_at))*1000 ELSE duration_ms END WHERE id=$1 AND status IN ('pending','running')`, id, update.Status, update.RiskLevel, terminal)
	if err != nil {
		return Run{}, err
	}
	if tag.RowsAffected() == 0 {
		return s.GetRun(ctx, id)
	}
	return s.GetRun(ctx, id)
}
func (s *PostgresStore) GetRun(ctx context.Context, id string) (Run, error) {
	var r Run
	var meta []byte
	err := s.pool.QueryRow(ctx, `SELECT id,thread_id,assistant_id,status,on_disconnect,risk_level,metadata,started_at,completed_at FROM agent_run WHERE id=$1`, id).Scan(&r.RunID, &r.ThreadID, &r.AssistantID, &r.Status, &r.OnDisconnect, &r.RiskLevel, &meta, &r.Created, &r.Completed)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("httpapi: get run: %w", err)
	}
	_ = json.Unmarshal(meta, &r.Metadata)
	return r, nil
}
func (s *PostgresStore) ListRuns(ctx context.Context, tid string) ([]Run, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM agent_run WHERE thread_id=$1 ORDER BY started_at`, tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	out := make([]Run, 0, len(ids))
	for _, id := range ids {
		r, err := s.GetRun(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PostgresStore) SaveRunCompletion(ctx context.Context, c RunCompletion) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_run_completion (
			run_id, thread_id, status, iterations, llm_call_count,
			input_tokens, output_tokens, cache_read_tokens, lead_tokens, subagent_tokens,
			auxiliary_tokens, cost_micros, duration_ms, completed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (run_id) DO UPDATE SET
			status=EXCLUDED.status, iterations=EXCLUDED.iterations,
			llm_call_count=EXCLUDED.llm_call_count,
			input_tokens=EXCLUDED.input_tokens, output_tokens=EXCLUDED.output_tokens,
			cache_read_tokens=EXCLUDED.cache_read_tokens,
			lead_tokens=EXCLUDED.lead_tokens, subagent_tokens=EXCLUDED.subagent_tokens,
			auxiliary_tokens=EXCLUDED.auxiliary_tokens, cost_micros=EXCLUDED.cost_micros,
			duration_ms=EXCLUDED.duration_ms, completed_at=EXCLUDED.completed_at`,
		c.RunID, c.ThreadID, c.Status, c.Iterations, c.LLMCalls,
		c.InputTokens, c.OutputTokens, c.CachedInputTokens, c.LeadTokens, c.SubagentTokens,
		c.AuxiliaryTokens, c.CostMicros, c.Duration.Milliseconds(), c.CompletedAt)
	return err
}

func (s *PostgresStore) LatestRunCompletion(ctx context.Context, threadID string) (RunCompletion, bool, error) {
	var c RunCompletion
	var durationMS int64
	err := s.pool.QueryRow(ctx, `SELECT run_id,thread_id,status,iterations,llm_call_count,
		input_tokens,output_tokens,cache_read_tokens,lead_tokens,subagent_tokens,
		auxiliary_tokens,cost_micros,duration_ms,completed_at
		FROM agent_run_completion WHERE thread_id=$1 AND llm_call_count>0
		ORDER BY completed_at DESC LIMIT 1`, threadID).Scan(
		&c.RunID, &c.ThreadID, &c.Status, &c.Iterations, &c.LLMCalls,
		&c.InputTokens, &c.OutputTokens, &c.CachedInputTokens, &c.LeadTokens, &c.SubagentTokens,
		&c.AuxiliaryTokens, &c.CostMicros, &durationMS, &c.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunCompletion{}, false, nil
	}
	if err != nil {
		return RunCompletion{}, false, fmt.Errorf("httpapi: latest run completion: %w", err)
	}
	c.Duration = time.Duration(durationMS) * time.Millisecond
	return c, true, nil
}

var _ Store = (*PostgresStore)(nil)
