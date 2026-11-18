package repository

import (
	"context"
	"encoding/json"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/storage"
	"github.com/jackc/pgx/v5"
)

// CreateFork inserts the destination and the completed transcript atomically.
func (s *PostgresStore) CreateFork(ctx context.Context, target model.Thread, source, run, title string, history []message.Message) (model.Thread, error) {
	meta, err := json.Marshal(target.Metadata)
	if err != nil {
		return model.Thread{}, err
	}
	err = pgx.BeginFunc(ctx, s.pool.Native, func(tx pgx.Tx) error {
		var sourceID string
		if err := tx.QueryRow(ctx, `SELECT id FROM agent_thread WHERE id=$1 FOR KEY SHARE`, source).Scan(&sourceID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO agent_thread(id,assistant_id,title,state,metadata,created_at,updated_at)
			VALUES($1,'lead_agent',$2,'{}',$3,$4,$4) ON CONFLICT(id) DO NOTHING`, target.ThreadID, title, meta, target.Created)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var same bool
			if err := tx.QueryRow(ctx, `SELECT COALESCE(metadata->>'forked_from_thread_id'=$2 AND metadata->>'forked_from_run_id'=$3,false) FROM agent_thread WHERE id=$1`, target.ThreadID, source, run).Scan(&same); err != nil {
				return err
			}
			if !same {
				return model.ErrForkConflict
			}
			return nil
		}
		transcript, err := storage.NewPostgres(tx)
		if err != nil {
			return err
		}
		return transcript.AppendMessages(ctx, target.ThreadID, history)
	})
	if err != nil {
		return model.Thread{}, err
	}
	return s.GetThread(ctx, target.ThreadID)
}
