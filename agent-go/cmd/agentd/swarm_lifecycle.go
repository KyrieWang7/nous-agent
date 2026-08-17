package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/modelrouter"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/subagent"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/swarm"
)

type swarmLifecycleStore interface {
	TeamForLead(context.Context, string, string) (swarm.Team, error)
	RegisterMember(context.Context, swarm.Member, string) error
	FinalizeMember(context.Context, string, string, string, string) error
}

// swarmSubagentLifecycle belongs to the application assembly layer: it joins
// two independent domain packages without making swarm depend on subagent's
// loop-backed implementation.
type swarmSubagentLifecycle struct {
	store swarmLifecycleStore
}

func newSwarmSubagentLifecycle(store swarmLifecycleStore) *swarmSubagentLifecycle {
	return &swarmSubagentLifecycle{store: store}
}

func (l *swarmSubagentLifecycle) Start(
	ctx context.Context,
	parent runtime.RunContext,
	taskID string,
	req subagent.DispatchRequest,
) (runtime.RunContext, error) {
	child := cloneChildRunContext(parent)
	if l == nil || l.store == nil || !valueBool(parent.Values, valueSwarmEnabled) {
		return child, nil
	}

	requestedTeam, _ := parent.Values["swarm_team_id"].(string)
	requestedTeam = strings.TrimSpace(requestedTeam)
	if requestedTeam == "" {
		return runtime.RunContext{}, errors.New("swarm: no active team; call team_create before dispatching a teammate task")
	}
	team, err := l.store.TeamForLead(ctx, parent.ThreadID, requestedTeam)
	if err != nil {
		return runtime.RunContext{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = generatedTeammateName(req.SubagentType, taskID)
	}
	if err := swarm.ValidateMemberName(name); err != nil {
		return runtime.RunContext{}, err
	}
	modelName, _ := parent.Values[modelrouter.ValueModelName].(string)
	member := swarm.Member{
		TeamID: team.ID,
		Name:   name,
		Model:  modelName,
		Prompt: req.Prompt,
		Status: swarm.MemberStatusRunning,
	}
	joined := fmt.Sprintf("[Joined] %s started working on: %s", name, truncateRunes(strings.TrimSpace(req.Prompt), 100))
	if err := l.store.RegisterMember(ctx, member, joined); err != nil {
		return runtime.RunContext{}, err
	}

	child.SwarmTeamID = team.ID
	child.SwarmAgentName = name
	if child.Values == nil {
		child.Values = make(map[string]any)
	}
	child.Values["swarm_team_id"] = team.ID
	child.Values["swarm_team_name"] = team.Name
	child.Values["swarm_agent_name"] = name
	return child, nil
}

func (l *swarmSubagentLifecycle) Finish(
	ctx context.Context,
	_ runtime.RunContext,
	child runtime.RunContext,
	_ subagent.DispatchRequest,
	result subagent.Result,
) error {
	if l == nil || l.store == nil || child.SwarmTeamID == "" || child.SwarmAgentName == "" {
		return nil
	}
	status, label, detail := swarmTerminalOutcome(result)
	content := fmt.Sprintf("[%s] %s: %s", label, child.SwarmAgentName, truncateRunes(strings.TrimSpace(detail), 200))
	return l.store.FinalizeMember(ctx, child.SwarmTeamID, child.SwarmAgentName, status, content)
}

func swarmTerminalOutcome(result subagent.Result) (status, label, detail string) {
	switch result.Status {
	case message.SubagentCompleted:
		return swarm.MemberStatusCompleted, "Completed", result.Output
	case message.SubagentTimedOut, message.SubagentPollingTimedOut:
		// Member storage projects timeout variants to failed while the Swarm
		// message stream preserves the distinct timeout label.
		return swarm.MemberStatusFailed, "Timeout", result.Error
	default:
		return swarm.MemberStatusFailed, "Failed", result.Error
	}
}

func cloneChildRunContext(parent runtime.RunContext) runtime.RunContext {
	child := parent
	child.AllowedTools = append([]string(nil), parent.AllowedTools...)
	if parent.Values != nil {
		child.Values = make(map[string]any, len(parent.Values)+3)
		for key, value := range parent.Values {
			child.Values[key] = value
		}
	}
	return child
}

func generatedTeammateName(subagentType, taskID string) string {
	prefix := strings.TrimSpace(subagentType)
	if prefix == "" {
		prefix = "agent"
	}
	suffix := strings.TrimPrefix(taskID, "task-")
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	return prefix + "-" + suffix
}

func valueBool(values map[string]any, key string) bool {
	value, _ := values[key].(bool)
	return value
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

var _ subagent.DispatchLifecycle = (*swarmSubagentLifecycle)(nil)
