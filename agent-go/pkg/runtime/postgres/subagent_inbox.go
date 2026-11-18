package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/delegation"
	platformdb "github.com/KyrieWang7/nous-agent/platform-go/postgres"
)

func (s *SubagentStore) Enqueue(ctx context.Context, thread, id string, input delegation.InboxMessage) (delegation.InboxMessage, error) {
	if input.ID == "" || input.Content == "" {
		return input, errors.New("message id and content are required")
	}
	err := s.db.Transaction(ctx, func(tx *platformdb.DB) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM agent_subagent_session WHERE id=$1 AND thread_id=$2 FOR UPDATE`, id, thread).Scan(&status); err != nil {
			return err
		}
		// Identical retries return the original delivery state, even after idle.
		var existing delegation.InboxMessage
		err := tx.QueryRow(ctx, `SELECT id,content,status FROM agent_subagent_inbox WHERE session_id=$1 AND id=$2`, id, input.ID).Scan(&existing.ID, &existing.Content, &existing.Status)
		if err == nil {
			if existing.Content != input.Content {
				return delegation.ErrMessageConflict
			}
			input = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !delegation.Active(status) {
			return delegation.ErrBusy
		}
		_, err = tx.Exec(ctx, `INSERT INTO agent_subagent_inbox(session_id,id,content) VALUES($1,$2,$3)`, id, input.ID, input.Content)
		input.Status = "pending"
		return err
	})
	return input, err
}
func (s *SubagentStore) Pending(ctx context.Context, id, token string) ([]delegation.InboxMessage, error) {
	rows, err := s.db.Query(ctx, `SELECT i.id,i.content,i.status FROM agent_subagent_inbox i JOIN agent_subagent_session s ON s.id=i.session_id WHERE s.id=$1 AND s.token=$2 AND s.lease_until>NOW() AND i.status='pending' ORDER BY i.position LIMIT 100`, id, token)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []delegation.InboxMessage{}
	for rows.Next() {
		var v delegation.InboxMessage
		if err = rows.Scan(&v.ID, &v.Content, &v.Status); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *SubagentStore) CommitInputs(ctx context.Context, session delegation.Session, ids []string) error {
	return s.db.Transaction(ctx, func(tx *platformdb.DB) error {
		if err := (&SubagentStore{db: tx}).Checkpoint(ctx, session); err != nil {
			return err
		}
		for _, id := range ids {
			tag, err := tx.Exec(ctx, `UPDATE agent_subagent_inbox SET status='consumed',consumed_at=NOW() WHERE session_id=$1 AND id=$2 AND status='pending'`, session.ID, id)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return errors.New("inbox admission conflict")
			}
		}
		return nil
	})
}
func (s *SubagentStore) ListInputs(ctx context.Context, thread, id string) ([]delegation.InboxMessage, error) {
	if _, err := s.LoadSession(ctx, thread, id); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT id,content,status FROM agent_subagent_inbox WHERE session_id=$1 ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []delegation.InboxMessage{}
	for rows.Next() {
		var v delegation.InboxMessage
		if err = rows.Scan(&v.ID, &v.Content, &v.Status); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
