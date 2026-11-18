package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/inbox"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/metadata"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/storage"
	platformdb "github.com/KyrieWang7/nous-agent/platform-go/postgres"
	"github.com/jackc/pgx/v5"
)

const inboxColumns = `i.id,i.thread_id,i.delivery,i.input,i.status,i.position,COALESCE(i.run_id,''),COALESCE(i.target_run_id,''),COALESCE(r.status,''),i.error,i.created_at,i.updated_at,i.admitted_at,r.started_at`

type inputRow interface{ Scan(...any) error }

func scanInput(row inputRow) (inbox.Entry, error) {
	var e inbox.Entry
	var raw []byte
	err := row.Scan(&e.ID, &e.ThreadID, &e.Delivery, &raw, &e.Status, &e.Position, &e.RunID, &e.TargetRunID, &e.RunStatus, &e.Error, &e.CreatedAt, &e.UpdatedAt, &e.AdmittedAt, &e.RunCreatedAt)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
		return e, inbox.ErrNotFound
	}
	if err != nil {
		return e, err
	}
	return e, json.Unmarshal(raw, &e.Input)
}
func lockInbox(ctx context.Context, tx *platformdb.DB, tid string) error {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM agent_thread WHERE id=$1 FOR UPDATE`, tid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ErrThreadNotFound
	}
	return err
}
func (s *PostgresStore) Enqueue(ctx context.Context, e inbox.Entry) (inbox.Entry, error) {
	if err := e.Validate(); err != nil {
		return e, err
	}
	raw, err := json.Marshal(e.Input)
	if err != nil {
		return e, err
	}
	err = s.pool.Transaction(ctx, func(tx *platformdb.DB) error {
		if err := lockInbox(ctx, tx, e.ThreadID); err != nil {
			return err
		}
		old, err := (&PostgresStore{pool: tx}).GetInput(ctx, e.ThreadID, e.ID)
		if err == nil {
			previous, _ := json.Marshal(old.Input)
			if old.Delivery != e.Delivery || !bytes.Equal(previous, raw) {
				return inbox.ErrConflict
			}
			e = old
			return nil
		}
		if !errors.Is(err, inbox.ErrNotFound) {
			return err
		}
		if e.Delivery == inbox.NextStep {
			err = tx.QueryRow(ctx, `SELECT r.id FROM agent_thread_writer w JOIN agent_run r ON r.id=w.run_id WHERE w.thread_id=$1 AND r.status IN ('pending','running')`, e.ThreadID).Scan(&e.TargetRunID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO agent_session_inbox(thread_id,id,delivery,input,target_run_id) VALUES($1,$2,$3,$4,NULLIF($5,''))`, e.ThreadID, e.ID, e.Delivery, raw, e.TargetRunID)
		if err != nil {
			return err
		}
		e, err = (&PostgresStore{pool: tx}).GetInput(ctx, e.ThreadID, e.ID)
		return err
	})
	return e, err
}
func (s *PostgresStore) GetInput(ctx context.Context, tid, id string) (inbox.Entry, error) {
	return scanInput(s.pool.QueryRow(ctx, `SELECT `+inboxColumns+` FROM agent_session_inbox i LEFT JOIN agent_run r ON r.id=i.run_id WHERE i.thread_id=$1 AND i.id=$2`, tid, id))
}
func (s *PostgresStore) ListInputs(ctx context.Context, tid string, after int64, limit int) ([]inbox.Entry, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT `+inboxColumns+` FROM agent_session_inbox i LEFT JOIN agent_run r ON r.id=i.run_id WHERE i.thread_id=$1 AND i.position>$2 ORDER BY i.position LIMIT $3`, tid, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []inbox.Entry{}
	for rows.Next() {
		e, err := scanInput(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *PostgresStore) CancelInput(ctx context.Context, tid, id string) (inbox.Entry, error) {
	var e inbox.Entry
	err := s.pool.Transaction(ctx, func(tx *platformdb.DB) error {
		if err := lockInbox(ctx, tx, tid); err != nil {
			return err
		}
		var err error
		e, err = (&PostgresStore{pool: tx}).GetInput(ctx, tid, id)
		if err != nil {
			return err
		}
		if e.Status == "admitted" || e.Status == "claimed" {
			return inbox.ErrAdmitted
		}
		if e.Status != "pending" && e.Status != "claimed" {
			return nil
		}
		_, err = tx.Exec(ctx, `UPDATE agent_session_inbox SET status='cancelled',updated_at=NOW() WHERE thread_id=$1 AND id=$2`, tid, id)
		e.Status = "cancelled"
		return err
	})
	return e, err
}
func (s *PostgresStore) ChangeInput(ctx context.Context, tid, id string, change inbox.Change) (inbox.Entry, error) {
	var entry inbox.Entry
	err := s.pool.Transaction(ctx, func(tx *platformdb.DB) error {
		if err := lockInbox(ctx, tx, tid); err != nil {
			return err
		}
		reader := &PostgresStore{pool: tx}
		var err error
		entry, err = reader.GetInput(ctx, tid, id)
		if err != nil {
			return err
		}
		if entry.Status != "pending" || !entry.UpdatedAt.Equal(change.UpdatedAt) {
			return inbox.ErrChanged
		}
		if change.Prompt != nil {
			if len(entry.Input.ContentBlocks) > 0 || strings.Contains(entry.Input.Prompt, "<uploaded_files>") {
				return inbox.ErrTextOnly
			}
			entry.Input.Prompt = strings.TrimSpace(*change.Prompt)
		} else {
			var activeRun string
			err := tx.QueryRow(ctx, `SELECT r.id FROM agent_thread_writer w JOIN agent_run r ON r.id=w.run_id WHERE w.thread_id=$1 AND r.id=$2 AND r.status IN ('pending','running') FOR SHARE OF r`, tid, change.TargetRunID).Scan(&activeRun)
			if errors.Is(err, sql.ErrNoRows) {
				return inbox.ErrNoActiveRun
			}
			if err != nil {
				return err
			}
			entry.Delivery = inbox.NextStep
			entry.TargetRunID = activeRun
			entry.Input.Config = nil
			entry.Input.Context = nil
		}
		raw, err := json.Marshal(entry.Input)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE agent_session_inbox SET input=$3,delivery=$4,target_run_id=NULLIF($5,''),updated_at=GREATEST(clock_timestamp(),updated_at+interval '1 microsecond') WHERE thread_id=$1 AND id=$2`, tid, id, raw, entry.Delivery, entry.TargetRunID)
		if err != nil {
			return err
		}
		entry, err = reader.GetInput(ctx, tid, id)
		return err
	})
	return entry, err
}

func (s *PostgresStore) PendingThreads(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT thread_id FROM agent_session_inbox WHERE status IN ('pending','claimed') AND NOT EXISTS (
 SELECT 1 FROM agent_thread_writer w JOIN agent_run r ON r.id=w.run_id JOIN agent_run_registry registry ON registry.run_id=r.id
 WHERE w.thread_id=agent_session_inbox.thread_id AND r.status IN ('pending','running') AND registry.expires_at>NOW()
 AND registry.payload->>'status'='running' AND (registry.payload->>'heartbeat_at')::timestamptz>NOW()-INTERVAL '60 seconds'
 ) GROUP BY thread_id ORDER BY MIN(position) LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (s *PostgresStore) HasPending(ctx context.Context, tid string) (bool, error) {
	var pending bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_session_inbox WHERE thread_id=$1 AND status IN ('pending','claimed'))`, tid).Scan(&pending)
	return pending, err
}

// Claim requires the caller's thread execution lease. An unadmitted claim left
// by an interrupted worker is safe to requeue: provider calls require Admit.
func (s *PostgresStore) Claim(ctx context.Context, tid, rid string) (inbox.Entry, bool, error) {
	var e inbox.Entry
	found := false
	err := s.pool.Transaction(ctx, func(tx *platformdb.DB) error {
		if err := lockInbox(ctx, tx, tid); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_session_inbox i SET status=CASE WHEN r.status='interrupted' THEN 'pending' WHEN r.status='cancelled' THEN 'cancelled' ELSE 'rejected' END,error=CASE WHEN r.status='interrupted' THEN '' ELSE 'run ended before model admission' END,updated_at=NOW() FROM agent_run r WHERE i.thread_id=$1 AND i.run_id=r.id AND i.status='claimed' AND r.status NOT IN ('pending','running')`, tid); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_session_inbox i SET status='cancelled',updated_at=NOW() FROM agent_run r WHERE i.thread_id=$1 AND i.target_run_id=r.id AND r.status='cancelled' AND i.status='pending' AND i.delivery='next_step'`, tid); err != nil {
			return err
		}
		var err error
		e, err = scanInput(tx.QueryRow(ctx, `SELECT `+inboxColumns+` FROM agent_session_inbox i LEFT JOIN agent_run r ON r.id=i.run_id WHERE i.thread_id=$1 AND i.status='pending' ORDER BY (i.delivery='next_turn') DESC,i.position LIMIT 1`, tid))
		if errors.Is(err, inbox.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		run := model.Run{RunID: rid, ThreadID: tid, AssistantID: "lead_agent", Status: "pending", OnDisconnect: "continue", Created: time.Now().UTC(), Metadata: map[string]any{"inbox_message_id": e.ID}}
		if err = createRun(ctx, tx, run); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE agent_session_inbox SET status='claimed',run_id=$3,target_run_id=NULL,updated_at=NOW() WHERE thread_id=$1 AND id=$2`, tid, e.ID, rid)
		if err != nil {
			return err
		}
		// Match DSH's turn boundary: all next-step inputs precede at most one
		// next-turn message. The source reserves them just before request assembly.
		if _, err = tx.Exec(ctx, `UPDATE agent_session_inbox SET target_run_id=$2,updated_at=NOW() WHERE thread_id=$1 AND status='pending' AND delivery='next_step'`, tid, rid); err != nil {
			return err
		}
		found = true
		e.RunID = rid
		e.RunStatus = "pending"
		e.Status = "claimed"
		return nil
	})
	return e, found, err
}

// Steering reserves a complete proposal under the same row lock used by
// withdrawal. Once preparation starts, users stop the run instead of removing
// input from a model request which may already have been frozen.
func (s *PostgresStore) Steering(ctx context.Context, tid, rid string) ([]inbox.Entry, error) {
	out := []inbox.Entry{}
	err := s.pool.Transaction(ctx, func(tx *platformdb.DB) error {
		if err := lockInbox(ctx, tx, tid); err != nil {
			return err
		}
		var writer string
		if err := tx.QueryRow(ctx, `SELECT run_id FROM agent_thread_writer WHERE thread_id=$1`, tid).Scan(&writer); err != nil {
			return err
		}
		if writer != rid {
			return metadata.ErrStaleWriter
		}
		rows, err := tx.Query(ctx, `SELECT `+inboxColumns+` FROM agent_session_inbox i LEFT JOIN agent_run r ON r.id=i.run_id WHERE i.thread_id=$1 AND i.status='pending' AND i.delivery='next_step' AND i.target_run_id=$2 ORDER BY i.position`, tid, rid)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			e, err := scanInput(rows)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
		for i := range out {
			if _, err := tx.Exec(ctx, `UPDATE agent_session_inbox SET status='claimed',run_id=$3,updated_at=NOW() WHERE thread_id=$1 AND id=$2`, tid, out[i].ID, rid); err != nil {
				return err
			}
			out[i].Status = "claimed"
			out[i].RunID = rid
		}
		return nil
	})
	return out, err
}

// Transcript replacement and input ACK use the shared pool in one native
// transaction. A stale worker can neither publish canonical input nor ACK it.
func (s *PostgresStore) Admit(ctx context.Context, tid, rid string, ids []string, history []message.Message) error {
	ctx = metadata.WithWriter(ctx, rid)
	return pgx.BeginFunc(ctx, s.pool.Native, func(tx pgx.Tx) error {
		transcript, err := storage.NewPostgres(tx)
		if err != nil {
			return err
		}
		if err = transcript.ReplaceTranscript(ctx, tid, history); err != nil {
			return err
		}
		for _, id := range ids {
			tag, err := tx.Exec(ctx, `UPDATE agent_session_inbox SET status='admitted',run_id=$3,admitted_at=NOW(),updated_at=NOW() WHERE thread_id=$1 AND id=$2 AND ((status='claimed' AND run_id=$3) OR (status='pending' AND delivery='next_step' AND target_run_id=$3))`, tid, id, rid)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return errors.New("input cancelled or admission ownership changed")
			}
		}
		return nil
	})
}
func (s *PostgresStore) Finish(ctx context.Context, tid, rid, status string, retry bool) error {
	return s.pool.Transaction(ctx, func(tx *platformdb.DB) error {
		if err := lockInbox(ctx, tx, tid); err != nil {
			return err
		}
		result := "rejected"
		if retry {
			result = "pending"
		} else if status == "cancelled" {
			result = "cancelled"
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_session_inbox SET status=$3,error=CASE WHEN $3='rejected' THEN 'run ended before model admission' ELSE '' END,updated_at=NOW() WHERE thread_id=$1 AND run_id=$2 AND status='claimed'`, tid, rid, result); err != nil {
			return err
		}
		if retry {
			_, err := tx.Exec(ctx, `UPDATE agent_session_inbox SET target_run_id=NULL,updated_at=NOW() WHERE thread_id=$1 AND target_run_id=$2 AND status='pending' AND delivery='next_step'`, tid, rid)
			return err
		}
		if status == "cancelled" && !retry {
			_, err := tx.Exec(ctx, `UPDATE agent_session_inbox SET status='cancelled',updated_at=NOW() WHERE thread_id=$1 AND target_run_id=$2 AND status='pending' AND delivery='next_step'`, tid, rid)
			return err
		}
		return nil
	})
}

var _ inbox.Store = (*PostgresStore)(nil)
