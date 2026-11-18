package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/delegation"
	platformdb "github.com/KyrieWang7/nous-agent/platform-go/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type SubagentStore struct{ db *platformdb.DB }

func NewSubagentStore(db *platformdb.DB) (*SubagentStore, error) {
	if db == nil {
		return nil, errors.New("subagent store: database is required")
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	var version int
	var dirty bool
	if err := db.QueryRow(ctx, `SELECT version,dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		return nil, fmt.Errorf("subagent store: schema unavailable; run agentctl migrate up: %w", err)
	}
	if dirty || version < 15 {
		return nil, errors.New("subagent store: schema 015 or newer required; run agentctl migrate up")
	}
	return &SubagentStore{db}, nil
}

// Task results are authoritative records: TTL affects caches, never PostgreSQL.
func (s *SubagentStore) Put(ctx context.Context, result delegation.Result, _ time.Duration) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO agent_subagent_task(thread_id,parent_run_id,task_id,result) VALUES($1,$2,$3,$4)
        ON CONFLICT(thread_id,parent_run_id,task_id) DO UPDATE SET result=EXCLUDED.result,updated_at=NOW()`, result.ThreadID, result.ParentRunID, result.TaskID, raw)
	return err
}
func (s *SubagentStore) GetScoped(ctx context.Context, thread, parent, task string) (delegation.Result, error) {
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT result FROM agent_subagent_task WHERE thread_id=$1 AND parent_run_id=$2 AND task_id=$3`, thread, parent, task).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return delegation.Result{}, delegation.ErrNotFound
	}
	if err != nil {
		return delegation.Result{}, err
	}
	var result delegation.Result
	err = json.Unmarshal(raw, &result)
	return result, err
}
func (s *SubagentStore) Get(ctx context.Context, task string) (delegation.Result, error) {
	rows, err := s.db.Query(ctx, `SELECT result FROM agent_subagent_task WHERE task_id=$1 LIMIT 2`, task)
	if err != nil {
		return delegation.Result{}, err
	}
	defer func() { _ = rows.Close() }()
	var result delegation.Result
	count := 0
	for rows.Next() {
		count++
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return result, err
		}
		if err = json.Unmarshal(raw, &result); err != nil {
			return result, err
		}
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	if count == 0 {
		return result, delegation.ErrNotFound
	}
	if count > 1 {
		return delegation.Result{}, errors.New("ambiguous task ID; thread and parent run are required")
	}
	return result, nil
}

func (s *SubagentStore) Acquire(ctx context.Context, in delegation.Session, ttl time.Duration) (delegation.Session, error) {
	in.Token = uuid.NewString()
	in.LeaseUntil = time.Now().UTC().Add(ttl)
	in.CancelRequested = false
	raw, err := json.Marshal(in)
	if err != nil {
		return in, err
	}
	tag, err := s.db.Exec(ctx, `INSERT INTO agent_subagent_session(id,thread_id,parent_session_id,token,status,lease_until,payload)
        VALUES($1,$2,$3,$4,$5,NOW()+$6::interval,$7)
        ON CONFLICT(id) DO UPDATE SET token=EXCLUDED.token,status=EXCLUDED.status,lease_until=EXCLUDED.lease_until,cancel_requested=FALSE,payload=EXCLUDED.payload,updated_at=NOW()
        WHERE agent_subagent_session.thread_id=EXCLUDED.thread_id AND agent_subagent_session.parent_session_id=EXCLUDED.parent_session_id
        AND (agent_subagent_session.status NOT IN ('pending','running') OR agent_subagent_session.lease_until<NOW())`, in.ID, in.ThreadID, in.ParentSessionID, in.Token, in.Result.Status, fmt.Sprintf("%f seconds", ttl.Seconds()), raw)
	if err != nil {
		return in, err
	}
	if tag.RowsAffected() != 1 {
		return in, delegation.ErrBusy
	}
	return in, nil
}
func (s *SubagentStore) Checkpoint(ctx context.Context, in delegation.Session) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `UPDATE agent_subagent_session SET payload=$3,status=$4,updated_at=NOW() WHERE id=$1 AND token=$2 AND lease_until>NOW() AND status IN ('pending','running')`, in.ID, in.Token, raw, in.Result.Status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return delegation.ErrLeaseLost
	}
	return nil
}
func (s *SubagentStore) Heartbeat(ctx context.Context, id, token string, ttl time.Duration) (bool, error) {
	var canceled bool
	err := s.db.QueryRow(ctx, `UPDATE agent_subagent_session SET lease_until=NOW()+$3::interval WHERE id=$1 AND token=$2 AND lease_until>NOW() AND status IN ('pending','running') RETURNING cancel_requested`, id, token, fmt.Sprintf("%f seconds", ttl.Seconds())).Scan(&canceled)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return false, delegation.ErrLeaseLost
	}
	return canceled, err
}
func (s *SubagentStore) LoadSession(ctx context.Context, thread, id string) (delegation.Session, error) {
	// An expired owner cannot write again. Reconcile without replaying tools.
	_, err := s.db.Exec(ctx, `UPDATE agent_subagent_session SET status='interrupted',updated_at=NOW(),payload=jsonb_set(jsonb_set(jsonb_set(payload,'{result,status}','"interrupted"'),'{result,stop_reason}','"interrupted"'),'{result,error}','"worker lease expired; explicit continuation required"') WHERE id=$1 AND thread_id=$2 AND status IN ('pending','running') AND lease_until<NOW()`, id, thread)
	if err != nil {
		return delegation.Session{}, err
	}
	var raw []byte
	var lease time.Time
	var canceled bool
	err = s.db.QueryRow(ctx, `SELECT payload,lease_until,cancel_requested FROM agent_subagent_session WHERE id=$1 AND thread_id=$2`, id, thread).Scan(&raw, &lease, &canceled)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return delegation.Session{}, delegation.ErrNotFound
	}
	if err != nil {
		return delegation.Session{}, err
	}
	var session delegation.Session
	err = json.Unmarshal(raw, &session)
	session.LeaseUntil = lease
	session.CancelRequested = canceled
	return session, err
}
func (s *SubagentStore) ListSessions(ctx context.Context, thread string) ([]delegation.Session, error) {
	rows, err := s.db.Query(ctx, `SELECT id FROM agent_subagent_session WHERE thread_id=$1 ORDER BY created_at,id`, thread)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]delegation.Session, 0, len(ids))
	for _, id := range ids {
		v, e := s.LoadSession(ctx, thread, id)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *SubagentStore) CancelSession(ctx context.Context, thread, id string) error {
	tag, err := s.db.Exec(ctx, `UPDATE agent_subagent_session SET cancel_requested=TRUE WHERE thread_id=$1 AND id=$2`, thread, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return delegation.ErrNotFound
	}
	return nil
}

var _ delegation.TaskStore = (*SubagentStore)(nil)
var _ delegation.SessionStore = (*SubagentStore)(nil)
