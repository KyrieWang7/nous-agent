// Package swarm provides a one-level PostgreSQL mailbox for the Go Harness.
package swarm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	LeadAgentName         = "lead"
	SystemAgentName       = "system"
	MemberStatusActive    = "active"
	MemberStatusRunning   = "running"
	MemberStatusCompleted = "completed"
	MemberStatusFailed    = "failed"
	MemberStatusRemoved   = "removed"
)

var (
	ErrTeamNotFound   = errors.New("swarm: team not found")
	ErrMemberNotFound = errors.New("swarm: member not found")
	ErrMemberActive   = errors.New("swarm: member name is already active")
	ErrTeamFull       = errors.New("swarm: team has reached its active member limit")
)

type Options struct {
	// MaxTeamSize includes the lead. A value below two uses the production
	// default so direct embedders cannot accidentally create lead-only teams.
	MaxTeamSize int
}

type Team struct {
	ID           string    `json:"id"`
	LeadThreadID string    `json:"lead_thread_id"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}
type Member struct {
	TeamID   string    `json:"team_id"`
	Name     string    `json:"name"`
	ThreadID string    `json:"thread_id,omitempty"`
	Model    string    `json:"model,omitempty"`
	Prompt   string    `json:"-"`
	Status   string    `json:"status"`
	JoinedAt time.Time `json:"joined_at"`
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

type commandExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type Manager struct {
	pool        database
	maxTeamSize int
}

func New(pool database, options ...Options) (*Manager, error) {
	if pool == nil {
		return nil, errors.New("swarm: postgres pool is nil")
	}
	maxTeamSize := 5
	if len(options) > 0 && options[0].MaxTeamSize >= 2 {
		maxTeamSize = options[0].MaxTeamSize
	}
	return &Manager{pool: pool, maxTeamSize: maxTeamSize}, nil
}

func (m *Manager) CreateTeam(ctx context.Context, id, name, leadThread string) (Team, error) {
	return m.CreateTeamWithDescription(ctx, id, name, "", leadThread)
}

func (m *Manager) CreateTeamWithDescription(ctx context.Context, id, name, description, leadThread string) (Team, error) {
	name = strings.TrimSpace(name)
	leadThread = strings.TrimSpace(leadThread)
	if name == "" {
		return Team{}, errors.New("swarm: team name is required")
	}
	if leadThread == "" {
		return Team{}, errors.New("swarm: lead thread is required")
	}
	if id == "" {
		id = fmt.Sprintf("team-%d", time.Now().UnixNano())
	}
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return Team{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var t Team
	err = tx.QueryRow(ctx, `
		INSERT INTO agent_swarm_teams(id,lead_thread_id,name,description)
		VALUES($1,$2,$3,$4)
		RETURNING id,lead_thread_id,name,COALESCE(description,''),created_at`, id, leadThread, name, nullableText(description)).
		Scan(&t.ID, &t.LeadThreadID, &t.Name, &t.Description, &t.CreatedAt)
	if err != nil {
		return Team{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_swarm_team_members(team_id,name,thread_id,status)
		VALUES($1,$2,$3,$4)`, t.ID, LeadAgentName, leadThread, MemberStatusActive); err != nil {
		return Team{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Team{}, err
	}
	return t, nil
}

func (m *Manager) DeleteTeam(ctx context.Context, id string) error {
	_, err := m.pool.Exec(ctx, `DELETE FROM agent_swarm_teams WHERE id=$1`, id)
	return err
}

// DeleteTeamForLead deletes a team only when the trusted thread owns it. An
// empty teamID is accepted only when the thread leads exactly one team.
func (m *Manager) DeleteTeamForLead(ctx context.Context, leadThread, teamID string) (Team, error) {
	team, err := m.TeamForLead(ctx, leadThread, teamID)
	if err != nil {
		return Team{}, err
	}
	tag, err := m.pool.Exec(ctx, `DELETE FROM agent_swarm_teams WHERE id=$1 AND lead_thread_id=$2`, team.ID, leadThread)
	if err != nil {
		return Team{}, err
	}
	if tag.RowsAffected() != 1 {
		return Team{}, ErrTeamNotFound
	}
	return team, nil
}

// TeamForLead resolves one of the teams owned by leadThread. Model-facing
// callers must obtain leadThread from runtime.RunContext.
func (m *Manager) TeamForLead(ctx context.Context, leadThread, teamID string) (Team, error) {
	if strings.TrimSpace(leadThread) == "" {
		return Team{}, errors.New("swarm: lead thread is required")
	}
	rows, err := m.pool.Query(ctx, `
		SELECT id,lead_thread_id,name,COALESCE(description,''),created_at
		FROM agent_swarm_teams
		WHERE lead_thread_id=$1 AND ($2='' OR id=$2)
		ORDER BY created_at DESC
		LIMIT 2`, leadThread, teamID)
	if err != nil {
		return Team{}, err
	}
	defer rows.Close()
	teams := make([]Team, 0, 2)
	for rows.Next() {
		var team Team
		if err := rows.Scan(&team.ID, &team.LeadThreadID, &team.Name, &team.Description, &team.CreatedAt); err != nil {
			return Team{}, err
		}
		teams = append(teams, team)
	}
	if err := rows.Err(); err != nil {
		return Team{}, err
	}
	if len(teams) == 0 {
		return Team{}, ErrTeamNotFound
	}
	if len(teams) > 1 {
		return Team{}, errors.New("swarm: current thread leads multiple teams; specify team_id")
	}
	return teams[0], nil
}

// EnsureTeam resolves a trusted lead's selected team or creates its default
// team on first use. A requested ID is never created implicitly because it may
// have come from an untrusted request value; TeamForLead must prove ownership.
func (m *Manager) EnsureTeam(ctx context.Context, leadThread, requestedID string) (Team, error) {
	team, err := m.TeamForLead(ctx, leadThread, requestedID)
	if err == nil {
		return team, nil
	}
	if !errors.Is(err, ErrTeamNotFound) || strings.TrimSpace(requestedID) != "" {
		return Team{}, err
	}
	short := strings.TrimSpace(leadThread)
	if len(short) > 8 {
		short = short[:8]
	}
	return m.CreateTeamWithDescription(ctx, "", "team-"+short, "Auto-created swarm team", leadThread)
}

func (m *Manager) AddMember(ctx context.Context, member Member) error {
	return m.addMember(ctx, member, "")
}

// RegisterMember adds a teammate and publishes its join announcement in one
// transaction. Callers never observe an active member without the matching
// lifecycle message, or a message for a member whose registration rolled back.
func (m *Manager) RegisterMember(ctx context.Context, member Member, announcement string) error {
	if strings.TrimSpace(announcement) == "" {
		return errors.New("swarm: joined announcement is required")
	}
	return m.addMember(ctx, member, announcement)
}

func (m *Manager) addMember(ctx context.Context, member Member, announcement string) error {
	member.TeamID = strings.TrimSpace(member.TeamID)
	member.Name = canonicalAgentName(member.Name)
	if member.TeamID == "" || member.Name == "" {
		return errors.New("swarm: team_id and member name are required")
	}
	if reservedMemberName(member.Name) {
		return fmt.Errorf("swarm: member name %q is reserved", member.Name)
	}
	if err := ValidateMemberName(member.Name); err != nil {
		return err
	}
	if member.Status == "" {
		member.Status = MemberStatusActive
	}
	if err := validateMemberStatus(member.Status); err != nil {
		return err
	}
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize membership changes for one team across service instances. The
	// name check and capacity check must describe the same database snapshot.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, member.TeamID); err != nil {
		return err
	}
	var existingStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM agent_swarm_team_members WHERE team_id=$1 AND name=$2`, member.TeamID, member.Name).Scan(&existingStatus)
	switch {
	case err == nil && activeMemberStatus(existingStatus):
		return fmt.Errorf("%w: %q", ErrMemberActive, member.Name)
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return err
	}
	var active int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM agent_swarm_team_members WHERE team_id=$1 AND status IN ('idle','active','running')`, member.TeamID).Scan(&active); err != nil {
		return err
	}
	if active >= m.maxTeamSize {
		return fmt.Errorf("%w: maximum %d including lead", ErrTeamFull, m.maxTeamSize)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_swarm_team_members(team_id,name,thread_id,model,prompt,status)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(team_id,name) DO UPDATE SET
			thread_id=EXCLUDED.thread_id,
			model=EXCLUDED.model,
			prompt=EXCLUDED.prompt,
			status=EXCLUDED.status,
			joined_at=NOW()`, member.TeamID, member.Name, nullableText(member.ThreadID), nullableText(member.Model), nullableText(member.Prompt), member.Status); err != nil {
		return err
	}
	if strings.TrimSpace(announcement) != "" {
		if err := insertMessage(ctx, tx, member.TeamID, SystemAgentName, "*", announcement); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (m *Manager) BindMemberThread(ctx context.Context, teamID, name, threadID string) error {
	if strings.TrimSpace(threadID) == "" {
		return errors.New("swarm: member thread is required")
	}
	name = canonicalAgentName(name)
	tag, err := m.pool.Exec(ctx, `UPDATE agent_swarm_team_members SET thread_id=$3 WHERE team_id=$1 AND name=$2 AND name<>$4`, teamID, name, threadID, LeadAgentName)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrMemberNotFound
	}
	return nil
}

func (m *Manager) UpdateMemberStatus(ctx context.Context, teamID, name, status string) error {
	if err := validateMemberStatus(status); err != nil {
		return err
	}
	return updateMemberStatus(ctx, m.pool, teamID, canonicalAgentName(name), status)
}

// FinalizeMember updates a teammate's terminal status and publishes the
// terminal announcement atomically. The shared wildcard row is intentionally
// retained; per-member receipts provide independent delivery without
// duplicating Gateway output.
func (m *Manager) FinalizeMember(ctx context.Context, teamID, name, status, announcement string) error {
	if !terminalMemberStatus(status) {
		return fmt.Errorf("swarm: invalid terminal member status %q", status)
	}
	if strings.TrimSpace(announcement) == "" {
		return errors.New("swarm: terminal announcement is required")
	}
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := updateMemberStatus(ctx, tx, teamID, canonicalAgentName(name), status); err != nil {
		return err
	}
	if err := insertMessage(ctx, tx, teamID, SystemAgentName, "*", announcement); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func updateMemberStatus(ctx context.Context, executor commandExecutor, teamID, name, status string) error {
	tag, err := executor.Exec(ctx, `UPDATE agent_swarm_team_members SET status=$3 WHERE team_id=$1 AND name=$2 AND name<>$4`, teamID, name, status, LeadAgentName)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrMemberNotFound
	}
	return nil
}

func (m *Manager) RemoveMember(ctx context.Context, teamID, name string) error {
	return m.UpdateMemberStatus(ctx, teamID, name, MemberStatusRemoved)
}

func (m *Manager) Members(ctx context.Context, teamID string) ([]Member, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT team_id,name,thread_id,model,prompt,status,joined_at
		FROM agent_swarm_team_members WHERE team_id=$1 ORDER BY joined_at,name`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var v Member
		var threadID, modelName, prompt sql.NullString
		if err := rows.Scan(&v.TeamID, &v.Name, &threadID, &modelName, &prompt, &v.Status, &v.JoinedAt); err != nil {
			return nil, err
		}
		v.ThreadID = threadID.String
		v.Model = modelName.String
		v.Prompt = prompt.String
		v.Name = canonicalAgentName(v.Name)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (m *Manager) Send(ctx context.Context, teamID, from, to, content string) error {
	return insertMessage(ctx, m.pool, teamID, from, to, content)
}

func insertMessage(ctx context.Context, executor commandExecutor, teamID, from, to, content string) error {
	from = canonicalAgentName(from)
	to = canonicalAgentName(to)
	_, err := executor.Exec(ctx, `INSERT INTO agent_swarm_messages(team_id,from_agent,to_agent,content) VALUES($1,$2,$3,$4)`, teamID, from, to, content)
	return err
}
func (m *Manager) Poll(ctx context.Context, teamID, agent string, limit int) ([]Message, error) {
	agent = canonicalAgentName(agent)
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
		WHERE m.team_id=$1 AND (
			m.to_agent=$2 OR (
				m.to_agent='*' AND m.from_agent<>$2 AND EXISTS (
					SELECT 1 FROM agent_swarm_team_members recipient
					WHERE recipient.team_id=m.team_id
					  AND recipient.name=$2
					  AND recipient.joined_at<=m.created_at
				)
			)
		)
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
		v.From = canonicalAgentName(v.From)
		v.To = canonicalAgentName(v.To)
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
			SELECT id AS team_id,$3::text AS agent_name FROM agent_swarm_teams WHERE lead_thread_id=$1
			UNION ALL
			SELECT m.team_id,m.name AS agent_name
			FROM agent_swarm_team_members m
			WHERE m.thread_id=$1 AND m.status IN ('idle','active','running')
			  AND NOT EXISTS (
				SELECT 1 FROM agent_swarm_teams t
				WHERE t.id=m.team_id AND t.lead_thread_id=$1
			  )
		) identities
		WHERE $2='' OR team_id=$2
		LIMIT 2`, threadID, requestedTeam, LeadAgentName)
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
		v.agent = canonicalAgentName(v.agent)
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
	recipient = canonicalAgentName(recipient)
	if recipient == "*" {
		return nil
	}
	var exists bool
	err := m.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM agent_swarm_team_members
			WHERE team_id=$1 AND name=$2 AND status IN ('idle','active','running')
		) OR ($2=$3 AND EXISTS(SELECT 1 FROM agent_swarm_teams WHERE id=$1))`, teamID, recipient, LeadAgentName).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("swarm: recipient %q is not in team %q", recipient, teamID)
	}
	return nil
}

func (m *Manager) Tools() []tool.Definition {
	return []tool.Definition{
		m.teamCreateTool(),
		m.teamDeleteTool(),
		m.sendMessageTool(),
		m.listTeammatesTool(),
	}
}

func (m *Manager) teamCreateTool() tool.Definition {
	return tool.Definition{
		Name:        "team_create",
		Group:       "swarm",
		Description: "Create a collaboration team led by the current thread.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"description":{"type":"string"}},"required":["name"]}`),
		Metadata:    tool.Metadata{IsAgentState: true},
		Handler: func(ctx context.Context, c tool.Call) (*tool.Result, error) {
			var args struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			}
			if err := json.Unmarshal(c.Args, &args); err != nil {
				return nil, err
			}
			threadID, err := trustedThreadID(ctx)
			if err != nil {
				return toolFailure(err), nil
			}
			team, err := m.CreateTeamWithDescription(ctx, "", args.Name, args.Description, threadID)
			if err != nil {
				return toolFailure(err), nil
			}
			setRunTeam(ctx, team)
			raw, _ := json.Marshal(struct {
				Team Team   `json:"team"`
				Role string `json:"role"`
			}{Team: team, Role: LeadAgentName})
			return &tool.Result{Content: string(raw)}, nil
		},
	}
}

func (m *Manager) teamDeleteTool() tool.Definition {
	return tool.Definition{
		Name:        "team_delete",
		Group:       "swarm",
		Description: "Delete a team led by the current thread after its work is complete.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"team_id":{"type":"string"}}}`),
		Metadata:    tool.Metadata{IsAgentState: true},
		Handler: func(ctx context.Context, c tool.Call) (*tool.Result, error) {
			var args struct {
				TeamID string `json:"team_id"`
			}
			if err := json.Unmarshal(c.Args, &args); err != nil {
				return nil, err
			}
			threadID, err := trustedThreadID(ctx)
			if err != nil {
				return toolFailure(err), nil
			}
			team, err := m.DeleteTeamForLead(ctx, threadID, args.TeamID)
			if err != nil {
				return toolFailure(err), nil
			}
			clearRunTeam(ctx, team.ID)
			raw, _ := json.Marshal(struct {
				TeamID string `json:"team_id"`
				Name   string `json:"name"`
				Status string `json:"status"`
			}{TeamID: team.ID, Name: team.Name, Status: "deleted"})
			return &tool.Result{Content: string(raw)}, nil
		},
	}
}

func (m *Manager) sendMessageTool() tool.Definition {
	return tool.Definition{
		Name:        "send_message",
		Group:       "swarm",
		Description: "Send a message to a teammate or * for broadcast.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"team_id":{"type":"string"},"to":{"type":"string"},"content":{"type":"string"}},"required":["to","content"]}`),
		Metadata:    tool.Metadata{IsAgentState: true},
		Handler: func(ctx context.Context, c tool.Call) (*tool.Result, error) {
			var args struct {
				TeamID  string `json:"team_id"`
				To      string `json:"to"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(c.Args, &args); err != nil {
				return nil, err
			}
			teamID, from, err := m.trustedIdentity(ctx, args.TeamID)
			if err != nil {
				return toolFailure(err), nil
			}
			if err := m.validateRecipient(ctx, teamID, args.To); err != nil {
				return toolFailure(err), nil
			}
			if err := m.Send(ctx, teamID, from, args.To, args.Content); err != nil {
				return toolFailure(err), nil
			}
			return &tool.Result{Content: "message sent"}, nil
		},
	}
}

func (m *Manager) listTeammatesTool() tool.Definition {
	return tool.Definition{
		Name:        "list_teammates",
		Group:       "swarm",
		Description: "List team members.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"team_id":{"type":"string"}}}`),
		Metadata:    tool.Metadata{IsReadOnly: true, IsConcurrencySafe: true},
		Handler: func(ctx context.Context, c tool.Call) (*tool.Result, error) {
			var args struct {
				TeamID string `json:"team_id"`
			}
			if err := json.Unmarshal(c.Args, &args); err != nil {
				return nil, err
			}
			teamID, _, err := m.trustedIdentity(ctx, args.TeamID)
			if err != nil {
				return toolFailure(err), nil
			}
			members, err := m.Members(ctx, teamID)
			if err != nil {
				return nil, err
			}
			raw, _ := json.Marshal(members)
			return &tool.Result{Content: string(raw)}, nil
		},
	}
}

func trustedThreadID(ctx context.Context) (string, error) {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || strings.TrimSpace(run.ThreadID) == "" {
		return "", errors.New("swarm: trusted run identity is unavailable")
	}
	if agent := canonicalAgentName(run.SwarmAgentName); agent != "" && agent != LeadAgentName {
		return "", errors.New("swarm: only the lead agent may change team lifecycle")
	}
	return run.ThreadID, nil
}

func setRunTeam(ctx context.Context, team Team) {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Values == nil {
		return
	}
	run.Values["swarm_team_id"] = team.ID
	run.Values["swarm_team_name"] = team.Name
	run.Values["swarm_agent_name"] = LeadAgentName
}

func clearRunTeam(ctx context.Context, deletedID string) {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Values == nil {
		return
	}
	current, _ := run.Values["swarm_team_id"].(string)
	if current != "" && current != deletedID {
		return
	}
	// Thread state is merge-persisted, so explicit empty values are required
	// to clear a deleted team instead of resurrecting its old identifier.
	run.Values["swarm_team_id"] = ""
	run.Values["swarm_team_name"] = ""
	run.Values["swarm_agent_name"] = LeadAgentName
}

// trustedIdentity prefers a lifecycle-assigned child identity. Lead runs do
// not carry one and are resolved through their persisted thread membership.
// A model-supplied team_id may narrow the lookup, but can never replace the
// identity stored in RunContext.
func (m *Manager) trustedIdentity(ctx context.Context, requestedTeam string) (string, string, error) {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || strings.TrimSpace(run.ThreadID) == "" {
		return "", "", errors.New("swarm: trusted run identity is unavailable")
	}
	trustedAgent := canonicalAgentName(run.SwarmAgentName)
	if run.SwarmTeamID != "" || trustedAgent != "" {
		if run.SwarmTeamID == "" || trustedAgent == "" {
			return "", "", errors.New("swarm: trusted child identity is incomplete")
		}
		if requestedTeam != "" && requestedTeam != run.SwarmTeamID {
			return "", "", errors.New("swarm: requested team does not match the trusted child identity")
		}
		return run.SwarmTeamID, trustedAgent, nil
	}
	return m.resolveIdentity(ctx, run.ThreadID, requestedTeam)
}

func toolFailure(err error) *tool.Result {
	return &tool.Result{Content: err.Error(), IsError: true}
}

func nullableText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func canonicalAgentName(name string) string {
	return strings.TrimSpace(name)
}

// ValidateMemberName enforces the stable identifier used in mailbox addresses,
// lifecycle messages, Gateway events, and teammate tabs.
func ValidateMemberName(name string) error {
	const maxMemberNameRunes = 64
	if count := utf8.RuneCountInString(name); count == 0 || count > maxMemberNameRunes {
		return fmt.Errorf("swarm: member name must contain 1-%d characters", maxMemberNameRunes)
	}
	for index, r := range name {
		if index == 0 && !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			return fmt.Errorf("swarm: member name %q must start with a letter or number", name)
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("swarm: member name %q contains unsupported characters", name)
	}
	return nil
}

func reservedMemberName(name string) bool {
	if name == "*" {
		return true
	}
	return strings.EqualFold(name, LeadAgentName) ||
		strings.EqualFold(name, SystemAgentName)
}

func validateMemberStatus(status string) error {
	switch status {
	case MemberStatusActive, MemberStatusRunning, MemberStatusCompleted, MemberStatusFailed, MemberStatusRemoved:
		return nil
	default:
		return fmt.Errorf("swarm: invalid member status %q", status)
	}
}

func activeMemberStatus(status string) bool {
	switch status {
	case "idle", MemberStatusActive, MemberStatusRunning:
		return true
	default:
		return false
	}
}

func terminalMemberStatus(status string) bool {
	switch status {
	case MemberStatusCompleted, MemberStatusFailed, MemberStatusRemoved:
		return true
	default:
		return false
	}
}
