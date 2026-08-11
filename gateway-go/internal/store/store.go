package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Thread struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	ModelName    string    `json:"model_name"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	IsArchived   bool      `json:"is_archived"`
	MessageCount int64     `json:"message_count"`
}

type ThreadPatch struct {
	Title      *string
	ModelName  *string
	IsArchived *bool
}

type Fact struct {
	ID         string    `json:"id"`
	Content    string    `json:"content"`
	Category   string    `json:"category"`
	Confidence float64   `json:"confidence"`
	CreatedAt  time.Time `json:"createdAt"`
	Source     string    `json:"source"`
}

type Team struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type Member struct {
	Name     string     `json:"name"`
	Status   string     `json:"status"`
	Model    *string    `json:"model"`
	JoinedAt *time.Time `json:"joined_at"`
}

type Message struct {
	ID        int64     `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type database interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Close()
}

type Store struct{ pool database }

func Open(ctx context.Context, url string) (*Store, error) {
	if url == "" {
		return &Store{}, nil
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
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

func (s *Store) ListThreads(ctx context.Context, limit, offset int, includeArchived bool) ([]Thread, int, error) {
	if !s.Available() {
		return []Thread{}, 0, nil
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
	threads := make([]Thread, 0)
	for rows.Next() {
		var item Thread
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

func (s *Store) CreateThread(ctx context.Context, id, title, model string) (Thread, error) {
	if !s.Available() {
		return Thread{}, errors.New("database is not configured")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO agent_thread(id,title,model_name) VALUES($1,$2,$3)
      ON CONFLICT(id) DO UPDATE SET title=COALESCE(NULLIF(EXCLUDED.title,''),agent_thread.title), model_name=COALESCE(NULLIF(EXCLUDED.model_name,''),agent_thread.model_name)`, id, title, model)
	if err != nil {
		return Thread{}, err
	}
	return s.GetThread(ctx, id)
}

func (s *Store) GetThread(ctx context.Context, id string) (Thread, error) {
	if !s.Available() {
		return Thread{}, ErrNotFound
	}
	var item Thread
	err := s.pool.QueryRow(ctx, `SELECT t.id,t.title,t.model_name,t.created_at,t.updated_at,
      COALESCE((t.metadata->>'is_archived')::boolean,FALSE),COUNT(m.id) FILTER (WHERE m.superseded=FALSE)
      FROM agent_thread t LEFT JOIN agent_message m ON m.thread_id=t.id WHERE t.id=$1 GROUP BY t.id`, id).
		Scan(&item.ID, &item.Title, &item.ModelName, &item.CreatedAt, &item.UpdatedAt, &item.IsArchived, &item.MessageCount)
	if err != nil {
		return Thread{}, ErrNotFound
	}
	return item, nil
}

func (s *Store) UpdateThread(ctx context.Context, id string, patch ThreadPatch) (Thread, error) {
	if !s.Available() {
		return Thread{}, ErrNotFound
	}
	result, err := s.pool.Exec(ctx, `UPDATE agent_thread SET
      title=CASE WHEN $2::boolean THEN $3 ELSE title END,
      model_name=CASE WHEN $4::boolean THEN $5 ELSE model_name END,
      metadata=CASE WHEN $6::boolean THEN jsonb_set(metadata,'{is_archived}',to_jsonb($7::boolean),true) ELSE metadata END,
      updated_at=NOW() WHERE id=$1`, id, patch.Title != nil, value(patch.Title), patch.ModelName != nil, value(patch.ModelName), patch.IsArchived != nil, boolValue(patch.IsArchived))
	if err != nil {
		return Thread{}, err
	}
	if result.RowsAffected() == 0 {
		return Thread{}, ErrNotFound
	}
	return s.GetThread(ctx, id)
}

func (s *Store) DeleteThread(ctx context.Context, id string) error {
	if !s.Available() {
		return ErrNotFound
	}
	result, err := s.pool.Exec(ctx, `DELETE FROM agent_thread WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Facts(ctx context.Context) ([]Fact, error) {
	if !s.Available() {
		return []Fact{}, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,fact,confidence,created_at,thread_id FROM memory_fact ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	facts := make([]Fact, 0)
	for rows.Next() {
		var fact Fact
		fact.Category = "context"
		if err := rows.Scan(&fact.ID, &fact.Content, &fact.Confidence, &fact.CreatedAt, &fact.Source); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func (s *Store) Teams(ctx context.Context, threadID string) ([]Team, error) {
	if !s.Available() {
		return []Team{}, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id,name,description,created_at FROM agent_swarm_teams WHERE lead_thread_id=$1 ORDER BY created_at DESC`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Team, 0)
	for rows.Next() {
		var item Team
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

func (s *Store) Members(ctx context.Context, teamID string) ([]Member, error) {
	if !s.Available() {
		return []Member{}, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT name,status,model,joined_at FROM agent_swarm_team_members WHERE team_id=$1 ORDER BY joined_at,name`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Member, 0)
	for rows.Next() {
		var item Member
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

func (s *Store) Messages(ctx context.Context, teamID string, afterID int64, limit int) ([]Message, error) {
	if !s.Available() {
		return []Message{}, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id,from_agent,to_agent,content,created_at FROM agent_swarm_messages WHERE team_id=$1 AND id>$2 ORDER BY id LIMIT $3`, teamID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Message, 0)
	for rows.Next() {
		var item Message
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
