package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	domain "github.com/KyrieWang7/nous-agent/gateway-go/internal/model"
	platformdb "github.com/KyrieWang7/nous-agent/platform-go/postgres"
)

type Store struct{ pool *platformdb.DB }

func Open(ctx context.Context, url string) (*Store, error) {
	if url == "" {
		return &Store{}, nil
	}
	pool, err := platformdb.Open(ctx, url)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) Available() bool { return s != nil && s.pool != nil }

func (s *Store) ListThreads(ctx context.Context, limit, offset int, includeArchived bool) ([]domain.Thread, int, error) {
	if !s.Available() {
		return []domain.Thread{}, 0, nil
	}
	filter := "AND COALESCE((t.metadata->>'is_archived')::boolean,FALSE)=FALSE"
	if includeArchived {
		filter = ""
	}
	query := `SELECT t.id,t.title,t.model_name,t.created_at,t.updated_at,
        COALESCE((t.metadata->>'is_archived')::boolean,FALSE),
        COUNT(m.id) FILTER (WHERE m.superseded=FALSE)
      FROM agent_thread t LEFT JOIN agent_message m ON m.thread_id=t.id
      WHERE TRUE ` + filter + ` GROUP BY t.id ORDER BY t.updated_at DESC LIMIT $1 OFFSET $2`
	rows, err := s.pool.Query(ctx, query, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	threads := make([]domain.Thread, 0)
	for rows.Next() {
		var item domain.Thread
		if err := rows.Scan(&item.ID, &item.Title, &item.ModelName, &item.CreatedAt, &item.UpdatedAt, &item.IsArchived, &item.MessageCount); err != nil {
			return nil, 0, err
		}
		threads = append(threads, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM agent_thread t WHERE TRUE `+filter).Scan(&total); err != nil {
		return nil, 0, err
	}
	return threads, total, nil
}

func (s *Store) CreateThread(ctx context.Context, id, title, model string) (domain.Thread, error) {
	if !s.Available() {
		return domain.Thread{}, errors.New("database is not configured")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO agent_thread(id,title,model_name) VALUES($1,$2,$3)
      ON CONFLICT(id) DO UPDATE SET title=COALESCE(NULLIF(EXCLUDED.title,''),agent_thread.title), model_name=COALESCE(NULLIF(EXCLUDED.model_name,''),agent_thread.model_name)`, id, title, model)
	if err != nil {
		return domain.Thread{}, err
	}
	return s.GetThread(ctx, id)
}

func (s *Store) GetThread(ctx context.Context, id string) (domain.Thread, error) {
	if !s.Available() {
		return domain.Thread{}, domain.ErrNotFound
	}
	var item domain.Thread
	err := s.pool.QueryRow(ctx, `SELECT t.id,t.title,t.model_name,t.created_at,t.updated_at,
      COALESCE((t.metadata->>'is_archived')::boolean,FALSE),COUNT(m.id) FILTER (WHERE m.superseded=FALSE)
      FROM agent_thread t LEFT JOIN agent_message m ON m.thread_id=t.id WHERE t.id=$1 GROUP BY t.id`, id).
		Scan(&item.ID, &item.Title, &item.ModelName, &item.CreatedAt, &item.UpdatedAt, &item.IsArchived, &item.MessageCount)
	if err != nil {
		return domain.Thread{}, domain.ErrNotFound
	}
	return item, nil
}

func (s *Store) UpdateThread(ctx context.Context, id string, patch domain.ThreadPatch) (domain.Thread, error) {
	if !s.Available() {
		return domain.Thread{}, domain.ErrNotFound
	}
	result, err := s.pool.Exec(ctx, `UPDATE agent_thread SET
      title=CASE WHEN $2::boolean THEN $3 ELSE title END,
      model_name=CASE WHEN $4::boolean THEN $5 ELSE model_name END,
      metadata=CASE WHEN $6::boolean THEN jsonb_set(metadata,'{is_archived}',to_jsonb($7::boolean),true) ELSE metadata END,
      updated_at=NOW() WHERE id=$1`, id, patch.Title != nil, value(patch.Title), patch.ModelName != nil, value(patch.ModelName), patch.IsArchived != nil, boolValue(patch.IsArchived))
	if err != nil {
		return domain.Thread{}, err
	}
	if result.RowsAffected() == 0 {
		return domain.Thread{}, domain.ErrNotFound
	}
	return s.GetThread(ctx, id)
}

func (s *Store) DeleteThread(ctx context.Context, id string) error {
	if !s.Available() {
		return domain.ErrNotFound
	}
	result, err := s.pool.Exec(ctx, `DELETE FROM agent_thread WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) ListSchedules(ctx context.Context, threadID string) ([]domain.Schedule, error) {
	if !s.Available() {
		return []domain.Schedule{}, nil
	}
	query := `SELECT id,thread_id,name,schedule_type,expression,timezone,input,enabled,next_run_at,last_run_at,created_at,updated_at FROM agent_schedule`
	args := []any{}
	if threadID != "" {
		query += " WHERE thread_id=$1"
		args = append(args, threadID)
	}
	query += " ORDER BY next_run_at ASC"
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Schedule{}
	for rows.Next() {
		var item domain.Schedule
		var raw []byte
		var last *time.Time
		if err := rows.Scan(&item.ID, &item.ThreadID, &item.Name, &item.ScheduleType, &item.Expression, &item.Timezone, &raw, &item.Enabled, &item.NextRunAt, &last, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &item.Input)
		item.LastRunAt = last
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) GetSchedule(ctx context.Context, id string) (domain.Schedule, error) {
	if !s.Available() {
		return domain.Schedule{}, domain.ErrNotFound
	}
	var item domain.Schedule
	var raw []byte
	var last *time.Time
	err := s.pool.QueryRow(ctx, `SELECT id,thread_id,name,schedule_type,expression,timezone,input,enabled,next_run_at,last_run_at,created_at,updated_at FROM agent_schedule WHERE id=$1`, id).Scan(&item.ID, &item.ThreadID, &item.Name, &item.ScheduleType, &item.Expression, &item.Timezone, &raw, &item.Enabled, &item.NextRunAt, &last, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return domain.Schedule{}, domain.ErrNotFound
	}
	_ = json.Unmarshal(raw, &item.Input)
	item.LastRunAt = last
	return item, nil
}

func (s *Store) CreateSchedule(ctx context.Context, item domain.Schedule) (domain.Schedule, error) {
	if !s.Available() {
		return domain.Schedule{}, errors.New("database is not configured")
	}
	raw, err := json.Marshal(item.Input)
	if err != nil {
		return domain.Schedule{}, err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO agent_schedule(id,thread_id,name,schedule_type,expression,timezone,input,enabled,next_run_at) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9)`, item.ID, item.ThreadID, item.Name, item.ScheduleType, item.Expression, item.Timezone, raw, item.Enabled, item.NextRunAt)
	if err != nil {
		return domain.Schedule{}, err
	}
	return s.GetSchedule(ctx, item.ID)
}

func (s *Store) UpdateSchedule(ctx context.Context, id string, patch domain.SchedulePatch) (domain.Schedule, error) {
	if !s.Available() {
		return domain.Schedule{}, domain.ErrNotFound
	}
	raw, _ := json.Marshal(patch.Input)
	result, err := s.pool.Exec(ctx, `UPDATE agent_schedule SET name=CASE WHEN $2 THEN $3 ELSE name END,schedule_type=CASE WHEN $4 THEN $5 ELSE schedule_type END,expression=CASE WHEN $6 THEN $7 ELSE expression END,timezone=CASE WHEN $8 THEN $9 ELSE timezone END,input=CASE WHEN $10 THEN $11::jsonb ELSE input END,enabled=CASE WHEN $12 THEN $13 ELSE enabled END,next_run_at=CASE WHEN $14 THEN $15 ELSE next_run_at END,updated_at=NOW() WHERE id=$1`, id, patch.Name != nil, value(patch.Name), patch.ScheduleType != nil, value(patch.ScheduleType), patch.Expression != nil, value(patch.Expression), patch.Timezone != nil, value(patch.Timezone), patch.Input != nil, raw, patch.Enabled != nil, boolValue(patch.Enabled), patch.NextRunAt != nil, timeValue(patch.NextRunAt))
	if err != nil {
		return domain.Schedule{}, err
	}
	if result.RowsAffected() == 0 {
		return domain.Schedule{}, domain.ErrNotFound
	}
	return s.GetSchedule(ctx, id)
}

func (s *Store) DeleteSchedule(ctx context.Context, id string) error {
	if !s.Available() {
		return domain.ErrNotFound
	}
	result, err := s.pool.Exec(ctx, `DELETE FROM agent_schedule WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) Facts(ctx context.Context) ([]domain.Fact, error) {
	if !s.Available() {
		return []domain.Fact{}, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,fact,confidence,created_at,thread_id FROM memory_fact ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	facts := make([]domain.Fact, 0)
	for rows.Next() {
		var fact domain.Fact
		fact.Category = "context"
		if err := rows.Scan(&fact.ID, &fact.Content, &fact.Confidence, &fact.CreatedAt, &fact.Source); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func (s *Store) Teams(ctx context.Context, threadID string) ([]domain.Team, error) {
	if !s.Available() {
		return []domain.Team{}, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id,name,description,created_at FROM agent_swarm_teams WHERE lead_thread_id=$1 ORDER BY created_at DESC`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Team, 0)
	for rows.Next() {
		var item domain.Team
		var description sql.NullString
		if err := rows.Scan(&item.ID, &item.Name, &description, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.Description = optionalString(description)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) TeamExists(ctx context.Context, teamID string) (bool, error) {
	if !s.Available() {
		return false, nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_swarm_teams WHERE id=$1)`, teamID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (s *Store) Members(ctx context.Context, teamID string) ([]domain.Member, error) {
	if !s.Available() {
		return []domain.Member{}, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT name,status,model,joined_at FROM agent_swarm_team_members WHERE team_id=$1 ORDER BY joined_at,name`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Member, 0)
	for rows.Next() {
		var item domain.Member
		var model sql.NullString
		var joinedAt sql.NullTime
		if err := rows.Scan(&item.Name, &item.Status, &model, &joinedAt); err != nil {
			return nil, err
		}
		item.Model = optionalString(model)
		item.JoinedAt = optionalTime(joinedAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) Messages(ctx context.Context, teamID string, afterID int64, limit int) ([]domain.Message, error) {
	if !s.Available() {
		return []domain.Message{}, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id,from_agent,to_agent,content,created_at FROM agent_swarm_messages WHERE team_id=$1 AND id>$2 ORDER BY id LIMIT $3`, teamID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Message, 0)
	for rows.Next() {
		var item domain.Message
		if err := rows.Scan(&item.ID, &item.From, &item.To, &item.Content, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func value(pointer *string) string {
	if pointer == nil {
		return ""
	}
	return *pointer
}

func boolValue(pointer *bool) bool {
	return pointer != nil && *pointer
}

func timeValue(pointer *time.Time) time.Time {
	if pointer == nil {
		return time.Time{}
	}
	return *pointer
}

func optionalString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func optionalTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}
