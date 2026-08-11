package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/modelrouter"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/subagent"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/swarm"
)

type lifecycleMessage struct {
	teamID  string
	from    string
	to      string
	content string
}

type fakeSwarmLifecycleStore struct {
	team       swarm.Team
	members    []swarm.Member
	statuses   []string
	messages   []lifecycleMessage
	ensureCall int
}

func (f *fakeSwarmLifecycleStore) TeamForLead(_ context.Context, leadThread, requested string) (swarm.Team, error) {
	f.ensureCall++
	if leadThread == "" {
		return swarm.Team{}, errors.New("missing lead")
	}
	if requested != "" && requested != f.team.ID {
		return swarm.Team{}, swarm.ErrTeamNotFound
	}
	return f.team, nil
}

func (f *fakeSwarmLifecycleStore) RegisterMember(_ context.Context, member swarm.Member, announcement string) error {
	f.members = append(f.members, member)
	f.messages = append(f.messages, lifecycleMessage{teamID: member.TeamID, from: swarm.SystemAgentName, to: "*", content: announcement})
	return nil
}

func (f *fakeSwarmLifecycleStore) FinalizeMember(_ context.Context, teamID, name, status, announcement string) error {
	f.statuses = append(f.statuses, teamID+":"+name+":"+status)
	f.messages = append(f.messages, lifecycleMessage{teamID: teamID, from: swarm.SystemAgentName, to: "*", content: announcement})
	return nil
}

func TestSwarmSubagentLifecyclePublishesTrustedMembership(t *testing.T) {
	t.Parallel()
	store := &fakeSwarmLifecycleStore{team: swarm.Team{ID: "team-1", Name: "review"}}
	lifecycle := newSwarmSubagentLifecycle(store)
	parentValues := map[string]any{
		valueSwarmEnabled:          true,
		"swarm_team_id":            "team-1",
		modelrouter.ValueModelName: "model-a",
	}
	parent := runtime.RunContext{ThreadID: "thread-1", Values: parentValues, AllowedTools: []string{"read_file"}}
	prompt := "Inspect the repository and report findings"
	child, err := lifecycle.Start(context.Background(), parent, "task-123456789", subagent.DispatchRequest{
		SubagentType: "explore",
		Name:         "reviewer",
		Prompt:       prompt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if child.SwarmTeamID != "team-1" || child.SwarmAgentName != "reviewer" {
		t.Fatalf("child identity = %#v", child)
	}
	if parent.Values["swarm_agent_name"] != nil {
		t.Fatalf("parent values were mutated: %#v", parent.Values)
	}
	if len(store.members) != 1 || store.members[0].Model != "model-a" || store.members[0].Prompt != prompt {
		t.Fatalf("members = %#v", store.members)
	}
	if len(store.messages) != 1 || store.messages[0].from != "system" || store.messages[0].to != "*" {
		t.Fatalf("join messages = %#v", store.messages)
	}

	result := subagent.Result{Status: "completed", Output: "done"}
	if err := lifecycle.Finish(context.Background(), parent, child, subagent.DispatchRequest{}, result); err != nil {
		t.Fatal(err)
	}
	if len(store.statuses) != 1 || store.statuses[0] != "team-1:reviewer:completed" {
		t.Fatalf("statuses = %#v", store.statuses)
	}
	if len(store.messages) != 2 || store.messages[1].content != "[Completed] reviewer: done" {
		t.Fatalf("messages = %#v", store.messages)
	}
}

func TestSwarmSubagentLifecycleIsInactiveForPlainSubagents(t *testing.T) {
	t.Parallel()
	store := &fakeSwarmLifecycleStore{team: swarm.Team{ID: "team-1"}}
	parent := runtime.RunContext{ThreadID: "thread-1", Values: map[string]any{valueSwarmEnabled: false}}
	child, err := newSwarmSubagentLifecycle(store).Start(context.Background(), parent, "task-1", subagent.DispatchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if store.ensureCall != 0 || child.SwarmTeamID != "" {
		t.Fatalf("inactive lifecycle touched swarm: calls=%d child=%#v", store.ensureCall, child)
	}
}

func TestSwarmSubagentLifecycleRequiresAnExplicitTeam(t *testing.T) {
	t.Parallel()
	store := &fakeSwarmLifecycleStore{team: swarm.Team{ID: "team-1"}}
	parent := runtime.RunContext{ThreadID: "thread-1", Values: map[string]any{valueSwarmEnabled: true}}
	_, err := newSwarmSubagentLifecycle(store).Start(context.Background(), parent, "task-1", subagent.DispatchRequest{Name: "reviewer"})
	if err == nil || !strings.Contains(err.Error(), "team_create") {
		t.Fatalf("Start() error = %v, want actionable team_create error", err)
	}
	if store.ensureCall != 0 || len(store.members) != 0 {
		t.Fatalf("unbound task touched swarm: calls=%d members=%#v", store.ensureCall, store.members)
	}
}

func TestSwarmSubagentLifecycleRejectsInvalidMemberNameBeforeStoreAccess(t *testing.T) {
	t.Parallel()
	store := &fakeSwarmLifecycleStore{team: swarm.Team{ID: "team-1"}}
	parent := runtime.RunContext{
		ThreadID: "thread-1",
		Values: map[string]any{
			valueSwarmEnabled: true,
			valueSwarmTeamID:  "team-1",
		},
	}

	_, err := newSwarmSubagentLifecycle(store).Start(
		context.Background(),
		parent,
		"task-1",
		subagent.DispatchRequest{Name: "../reviewer"},
	)
	if err == nil || !strings.Contains(err.Error(), "must start with") {
		t.Fatalf("Start() error = %v, want invalid member name", err)
	}
	if store.ensureCall != 1 || len(store.members) != 0 || len(store.messages) != 0 {
		t.Fatalf("invalid teammate was persisted: calls=%d members=%#v messages=%#v", store.ensureCall, store.members, store.messages)
	}
}

func TestSwarmSubagentLifecyclePublishesTimeoutWithoutExpandingMemberStatus(t *testing.T) {
	t.Parallel()
	store := &fakeSwarmLifecycleStore{}
	child := runtime.RunContext{SwarmTeamID: "team-1", SwarmAgentName: "reviewer"}
	result := subagent.Result{Status: message.SubagentTimedOut, Error: "context deadline exceeded"}

	err := newSwarmSubagentLifecycle(store).Finish(
		context.Background(),
		runtime.RunContext{},
		child,
		subagent.DispatchRequest{},
		result,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.statuses) != 1 || store.statuses[0] != "team-1:reviewer:failed" {
		t.Fatalf("statuses = %#v", store.statuses)
	}
	if len(store.messages) != 1 || store.messages[0].content != "[Timeout] reviewer: context deadline exceeded" {
		t.Fatalf("messages = %#v", store.messages)
	}
}
