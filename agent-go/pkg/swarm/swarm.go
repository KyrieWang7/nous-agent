// Package swarm provides a one-level PostgreSQL mailbox for the Go Harness.
package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Team struct {
	ID           string    `json:"id"`
	LeadThreadID string    `json:"lead_thread_id"`
	Name         string    `json:"name"`
	CreatedAt    time.Time `json:"created_at"`
}
type Member struct {
	TeamID   string `json:"team_id"`
	Name     string `json:"name"`
	ThreadID string `json:"thread_id"`
	Status   string `json:"status"`
}
type Message struct {
	ID        int64     `json:"id"`
	TeamID    string    `json:"team_id"`
	From      string    `json:"from_agent"`
	To        string    `json:"to_agent"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}
type Mailbox interface {
	Poll(context.Context, string, string, int) ([]Message, error)
}

// ThreadMailbox resolves the mailbox identity from the trusted run thread.
// The HTTP/model payload never gets to assert which agent it is.
type ThreadMailbox interface {
	PollForThread(context.Context, string, int) ([]Message, error)
}
type database interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Begin(context.Context) (pgx.Tx, error)
}

type Manager struct{ pool database }

func New(pool database) (*Manager, error) {
	if pool == nil {
		return nil, errors.New("swarm: postgres pool is nil")
	}
	return &Manager{pool: pool}, nil
}
func (m *Manager) CreateTeam(ctx context.Context, id, name, leadThread string) (Team, error) {
	if id == "" {
		id = fmt.Sprintf("team-%d", time.Now().UnixNano())
	}
	var t Team
	err := m.pool.QueryRow(ctx, `INSERT INTO agent_swarm_teams(id,lead_thread_id,name) VALUES($1,$2,$3) RETURNING id,lead_thread_id,name,created_at`, id, leadThread, name).Scan(&t.ID, &t.LeadThreadID, &t.Name, &t.CreatedAt)
	return t, err
}
func (m *Manager) DeleteTeam(ctx context.Context, id string) error {
	_, err := m.pool.Exec(ctx, `DELETE FROM agent_swarm_teams WHERE id=$1`, id)
	return err
}
func (m *Manager) AddMember(ctx context.Context, member Member) error {
	if member.Status == "" {
		member.Status = "idle"
	}
	_, err := m.pool.Exec(ctx, `INSERT INTO agent_swarm_team_members(team_id,name,thread_id,status) VALUES($1,$2,$3,$4) ON CONFLICT(team_id,name) DO UPDATE SET thread_id=EXCLUDED.thread_id,status=EXCLUDED.status`, member.TeamID, member.Name, member.ThreadID, member.Status)
	return err
}
func (m *Manager) Members(ctx context.Context, teamID string) ([]Member, error) {
	rows, err := m.pool.Query(ctx, `SELECT team_id,name,thread_id,status FROM agent_swarm_team_members WHERE team_id=$1 ORDER BY name`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var v Member
		if err := rows.Scan(&v.TeamID, &v.Name, &v.ThreadID, &v.Status); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (m *Manager) Send(ctx context.Context, teamID, from, to, content string) error {
	_, err := m.pool.Exec(ctx, `INSERT INTO agent_swarm_messages(team_id,from_agent,to_agent,content) VALUES($1,$2,$3,$4)`, teamID, from, to, content)
	return err
}
func (m *Manager) Poll(ctx context.Context, teamID, agent string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1 || ':' || $2))`, teamID, agent); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT m.id,m.team_id,m.from_agent,m.to_agent,m.content,m.created_at
		FROM agent_swarm_messages m
		WHERE m.team_id=$1 AND (m.to_agent=$2 OR m.to_agent='*')
		  AND NOT EXISTS (
			SELECT 1 FROM agent_swarm_message_receipts r
			WHERE r.message_id=m.id AND r.agent_name=$2
		  )
		ORDER BY m.id LIMIT $3`, teamID, agent, limit)
	if err != nil {
		return nil, err
	}
	var out []Message
	var ids []int64
	for rows.Next() {
		var v Message
		if err := rows.Scan(&v.ID, &v.TeamID, &v.From, &v.To, &v.Content, &v.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, v)
		ids = append(ids, v.ID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(ids) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO agent_swarm_message_receipts(message_id,agent_name) SELECT unnest($1::bigint[]),$2 ON CONFLICT DO NOTHING`, ids, agent); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (m *Manager) PollForThread(ctx context.Context, threadID string, limit int) ([]Message, error) {
	teamID, agent, err := m.resolveIdentity(ctx, threadID, "")
	if err != nil {
		return nil, err
	}
	return m.Poll(ctx, teamID, agent, limit)
}

func (m *Manager) resolveIdentity(ctx context.Context, threadID, requestedTeam string) (string, string, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT team_id,agent_name FROM (
			SELECT id AS team_id,'lead'::text AS agent_name FROM agent_swarm_teams WHERE lead_thread_id=$1
			UNION ALL
			SELECT team_id,name AS agent_name FROM agent_swarm_team_members WHERE thread_id=$1
		) identities
		WHERE $2='' OR team_id=$2
		LIMIT 2`, threadID, requestedTeam)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	type identity struct{ team, agent string }
	var found []identity
	for rows.Next() {
		var v identity
		if err := rows.Scan(&v.team, &v.agent); err != nil {
			return "", "", err
		}
		found = append(found, v)
	}
	if err := rows.Err(); err != nil {
		return "", "", err
	}
	if len(found) == 0 {
		return "", "", errors.New("swarm: current thread is not a member of the requested team")
	}
	if len(found) > 1 {
		return "", "", errors.New("swarm: current thread belongs to multiple teams; specify team_id")
	}
	return found[0].team, found[0].agent, nil
}

func (m *Manager) validateRecipient(ctx context.Context, teamID, recipient string) error {
	if recipient == "*" {
		return nil
	}
	var exists bool
	err := m.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_swarm_team_members WHERE team_id=$1 AND name=$2) OR ($2='lead' AND EXISTS(SELECT 1 FROM agent_swarm_teams WHERE id=$1))`, teamID, recipient).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("swarm: recipient %q is not in team %q", recipient, teamID)
	}
	return nil
}

func (m *Manager) Tools() []tool.Definition {
	return []tool.Definition{{Name: "send_message", Group: "swarm", Description: "Send a message to a teammate or * for broadcast.", Parameters: json.RawMessage(`{"type":"object","properties":{"team_id":{"type":"string"},"to":{"type":"string"},"content":{"type":"string"}},"required":["to","content"]}`), Handler: func(ctx context.Context, c tool.Call) (*tool.Result, error) {
		var a struct {
			TeamID  string `json:"team_id"`
			To      string `json:"to"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return nil, err
		}
		run, ok := runtime.RunContextFrom(ctx)
		if !ok || run.ThreadID == "" {
			return &tool.Result{Content: "swarm: trusted run identity is unavailable", IsError: true}, nil
		}
		teamID, from, err := m.resolveIdentity(ctx, run.ThreadID, a.TeamID)
		if err != nil {
			return &tool.Result{Content: err.Error(), IsError: true}, nil //nolint:nilerr // identity failures are model-visible tool results
		}
		if err := m.validateRecipient(ctx, teamID, a.To); err != nil {
			return &tool.Result{Content: err.Error(), IsError: true}, nil //nolint:nilerr // recipient validation is a tool result
		}
		if err := m.Send(ctx, teamID, from, a.To, a.Content); err != nil {
			return &tool.Result{Content: err.Error(), IsError: true}, nil //nolint:nilerr // send failures are model-visible tool results
		}
		return &tool.Result{Content: "message sent"}, nil
	}}, {Name: "list_teammates", Group: "swarm", Description: "List team members.", Parameters: json.RawMessage(`{"type":"object","properties":{"team_id":{"type":"string"}}}`), Metadata: tool.Metadata{IsReadOnly: true, IsConcurrencySafe: true}, Handler: func(ctx context.Context, c tool.Call) (*tool.Result, error) {
		var a struct {
			TeamID string `json:"team_id"`
		}
		if err := json.Unmarshal(c.Args, &a); err != nil {
			return nil, err
		}
		run, ok := runtime.RunContextFrom(ctx)
		if !ok || run.ThreadID == "" {
			return &tool.Result{Content: "swarm: trusted run identity is unavailable", IsError: true}, nil
		}
		teamID, _, err := m.resolveIdentity(ctx, run.ThreadID, a.TeamID)
		if err != nil {
			return &tool.Result{Content: err.Error(), IsError: true}, nil //nolint:nilerr // identity failures are model-visible tool results
		}
		members, err := m.Members(ctx, teamID)
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(members)
		return &tool.Result{Content: string(raw)}, nil
	}}}
}
