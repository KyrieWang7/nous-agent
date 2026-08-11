package builtin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/swarm"
)

const NameSwarmSession = "swarmSession"

type swarmSessionStore interface {
	TeamForLead(context.Context, string, string) (swarm.Team, error)
}

// SwarmSession resolves the run's team before the first model call. Runtime
// request values may select a team owned by the current thread, but only a
// trusted child RunContext may assert a teammate identity.
type SwarmSession struct {
	store swarmSessionStore
}

func NewSwarmSession(store swarmSessionStore) *SwarmSession {
	return &SwarmSession{store: store}
}

func (*SwarmSession) Name() string { return NameSwarmSession }

func (s *SwarmSession) BeforeAgent(ctx context.Context, st *middleware.State) error {
	value, exists := st.Value("swarm_enabled")
	if !exists || value == nil {
		return nil
	}
	enabled, ok := value.(bool)
	if !ok {
		return errors.New("swarm session: swarm_enabled must be a boolean")
	}
	if !enabled {
		return nil
	}
	if s == nil || s.store == nil {
		return errors.New("swarm session: swarm mode requires runtime.database_url")
	}

	run, ok := runtime.RunContextFrom(ctx)
	if !ok || strings.TrimSpace(run.ThreadID) == "" {
		return errors.New("swarm session: trusted run context is unavailable")
	}
	if run.SwarmTeamID != "" || run.SwarmAgentName != "" {
		if run.SwarmTeamID == "" || run.SwarmAgentName == "" {
			return errors.New("swarm session: trusted child identity is incomplete")
		}
		setSwarmSessionValues(st, run.Values, run.SwarmTeamID, "", run.SwarmAgentName)
		st.SystemPrompt = strings.TrimSpace(st.SystemPrompt) + fmt.Sprintf(`

<swarm_teammate>
You are teammate %q in team %q. Complete only the delegated task. Your final
task result is returned to the lead automatically. Any interim coordination or
message for another teammate must use send_message: use to="team-lead" for the
lead, a specific teammate name for a direct message, or to="*" sparingly for a
team-wide broadcast. Ordinary response text is not delivered to teammates'
mailboxes. Never claim another teammate or team identity; sender and team are
derived from trusted runtime state. Do not create/delete teams or dispatch
nested tasks; the lead owns orchestration.
</swarm_teammate>`, run.SwarmAgentName, run.SwarmTeamID)
		return nil
	}

	requested, _ := st.Value("swarm_team_id")
	requestedID, _ := requested.(string)
	team, err := s.store.TeamForLead(ctx, run.ThreadID, strings.TrimSpace(requestedID))
	if errors.Is(err, swarm.ErrTeamNotFound) && strings.TrimSpace(requestedID) == "" {
		// The first Swarm turn must expose team_create. Creating a hidden default
		// team here made that public lifecycle tool unreachable in production.
		return nil
	}
	if err != nil {
		return fmt.Errorf("swarm session: resolving lead team: %w", err)
	}
	setSwarmSessionValues(st, run.Values, team.ID, team.Name, swarm.LeadAgentName)
	return nil
}

func setSwarmSessionValues(st *middleware.State, runValues map[string]any, teamID, teamName, agentName string) {
	values := map[string]string{
		"swarm_team_id":    teamID,
		"swarm_agent_name": agentName,
	}
	if teamName != "" {
		values["swarm_team_name"] = teamName
	}
	for key, value := range values {
		st.SetValue(key, value)
		if runValues != nil {
			runValues[key] = value
		}
	}
}
