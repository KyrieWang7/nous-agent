package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/swarm"
)

type fakeSwarmSessionStore struct {
	calls     int
	threadID  string
	requested string
	err       error
}

func (f *fakeSwarmSessionStore) TeamForLead(_ context.Context, threadID, requested string) (swarm.Team, error) {
	f.calls++
	f.threadID = threadID
	f.requested = requested
	if f.err != nil {
		return swarm.Team{}, f.err
	}
	return swarm.Team{ID: "team-1", Name: "reviewers"}, nil
}

func TestSwarmSessionResolvesLeadTeamAndSharesTrustedValues(t *testing.T) {
	store := &fakeSwarmSessionStore{}
	runValues := map[string]any{"swarm_enabled": true}
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{ThreadID: "thread-1", Values: runValues})
	st := middleware.NewState(middleware.StateInit{ThreadID: "thread-1"})
	st.SetValue("swarm_enabled", true)
	st.SetValue("swarm_team_id", "team-1")

	if err := NewSwarmSession(store).BeforeAgent(ctx, st); err != nil {
		t.Fatal(err)
	}
	if store.calls != 1 || store.threadID != "thread-1" || store.requested != "team-1" {
		t.Fatalf("store call = %#v", store)
	}
	if got, _ := st.Value("swarm_agent_name"); got != swarm.LeadAgentName {
		t.Fatalf("state agent = %#v", got)
	}
	if runValues["swarm_team_id"] != "team-1" || runValues["swarm_agent_name"] != swarm.LeadAgentName {
		t.Fatalf("run values = %#v", runValues)
	}
}

func TestSwarmSessionUsesTrustedChildIdentity(t *testing.T) {
	store := &fakeSwarmSessionStore{}
	runValues := map[string]any{}
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{
		ThreadID: "thread-1", Values: runValues,
		SwarmTeamID: "team-1", SwarmAgentName: "explore-1",
	})
	st := middleware.NewState(middleware.StateInit{ThreadID: "thread-1", SystemPrompt: "base prompt"})
	st.SetValue("swarm_enabled", true)
	st.SetValue("swarm_agent_name", "forged")

	if err := NewSwarmSession(store).BeforeAgent(ctx, st); err != nil {
		t.Fatal(err)
	}
	if store.calls != 0 {
		t.Fatalf("trusted child unexpectedly resolved a lead team %d time(s)", store.calls)
	}
	if got, _ := st.Value("swarm_agent_name"); got != "explore-1" {
		t.Fatalf("state agent = %#v", got)
	}
	for _, requirement := range []string{
		`teammate "explore-1"`,
		`send_message`,
		`to="team-lead"`,
		`team-wide broadcast`,
		`Ordinary response text is not delivered`,
		`trusted runtime state`,
	} {
		if !strings.Contains(st.SystemPrompt, requirement) {
			t.Fatalf("teammate prompt is missing %q: %q", requirement, st.SystemPrompt)
		}
	}
}

func TestSwarmSessionLeavesFirstTeamCreationToThePublicTool(t *testing.T) {
	store := &fakeSwarmSessionStore{err: swarm.ErrTeamNotFound}
	runValues := map[string]any{"swarm_enabled": true}
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{ThreadID: "thread-1", Values: runValues})
	st := middleware.NewState(middleware.StateInit{ThreadID: "thread-1"})
	st.SetValue("swarm_enabled", true)

	if err := NewSwarmSession(store).BeforeAgent(ctx, st); err != nil {
		t.Fatal(err)
	}
	if store.calls != 1 || store.requested != "" {
		t.Fatalf("store call = %#v", store)
	}
	if teamID, _ := st.Value("swarm_team_id"); teamID != nil {
		t.Fatalf("hidden default team was created: %#v", teamID)
	}
}

func TestSwarmSessionFailsClearlyWithoutPostgres(t *testing.T) {
	st := middleware.NewState(middleware.StateInit{ThreadID: "thread-1"})
	st.SetValue("swarm_enabled", true)
	err := NewSwarmSession(nil).BeforeAgent(context.Background(), st)
	if err == nil || !strings.Contains(err.Error(), "database_url") {
		t.Fatalf("error = %v", err)
	}
}
