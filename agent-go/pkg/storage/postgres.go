// Package storage persists transcripts independently from HTTP wire formats.
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/jackc/pgx/v5"
)

type database interface {
	Begin(context.Context) (pgx.Tx, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type Postgres struct{ pool database }

func NewPostgres(pool database) (*Postgres, error) {
	if pool == nil {
		return nil, errors.New("storage: postgres pool is nil")
	}
	return &Postgres{pool: pool}, nil
}

func (s *Postgres) LoadHistory(ctx context.Context, threadID string) ([]message.Message, error) {
	rows, err := s.pool.Query(ctx, `SELECT content FROM agent_message WHERE thread_id=$1 AND superseded=FALSE ORDER BY seq`, threadID)
	if err != nil {
		return nil, fmt.Errorf("storage: loading history: %w", err)
	}
	defer rows.Close()
	var out []message.Message
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var m message.Message
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("storage: decoding message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Postgres) AppendMessages(ctx context.Context, threadID string, msgs []message.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		seq, err := nextSeq(ctx, tx, threadID)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			raw, err := json.Marshal(m)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO agent_message(id,thread_id,seq,role,content) VALUES(gen_random_uuid()::text,$1,$2,$3,$4)`, threadID, seq, m.Role, raw); err != nil {
				return err
			}
			seq++
		}
		return nil
	})
}

// ReplaceTranscript marks old rows superseded and appends the complete active
// transcript at fresh sequence numbers. Old rows remain as an audit record.
func (s *Postgres) ReplaceTranscript(ctx context.Context, threadID string, msgs []message.Message) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		seq, err := nextSeq(ctx, tx, threadID)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE agent_message SET superseded=TRUE WHERE thread_id=$1 AND superseded=FALSE`, threadID); err != nil {
			return err
		}
		for _, m := range msgs {
			raw, err := json.Marshal(m)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO agent_message(id,thread_id,seq,role,content) VALUES(gen_random_uuid()::text,$1,$2,$3,$4)`, threadID, seq, m.Role, raw); err != nil {
				return err
			}
			seq++
		}
		return nil
	})
}

func nextSeq(ctx context.Context, tx pgx.Tx, threadID string) (int64, error) {
	var locked string
	if err := tx.QueryRow(ctx, `SELECT id FROM agent_thread WHERE id=$1 FOR UPDATE`, threadID).Scan(&locked); err != nil {
		return 0, err
	}
	var seq int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM agent_message WHERE thread_id=$1`, threadID).Scan(&seq)
	return seq, err
}
