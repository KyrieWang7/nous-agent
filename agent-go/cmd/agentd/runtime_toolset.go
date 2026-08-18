package main

import (
	"context"
	"errors"
	"slices"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/permission"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

const (
	valueSubagentEnabled = "subagent_enabled"
	valueSwarmEnabled    = "swarm_enabled"
	valueSwarmTeamID     = "swarm_team_id"
)

// runtimeToolSet computes the production tool surface for every model turn.
// Registration describes server capabilities; run values decide which of
// those capabilities are exposed to the current model invocation.
type runtimeToolSet struct {
	registry         *tool.Registry
	policyCapability string
	defaultSubagent  bool
	defaultSwarm     bool
}

func (r runtimeToolSet) Resolve(ctx context.Context, st *lifecycle.State) ([]string, []string, error) {
	if r.registry == nil {
		return nil, nil, errors.New("agentd: tool registry is nil")
	}
	policy, err := resolveToolPolicy(ctx, r.policyCapability)
	if err != nil {
		return nil, nil, err
	}
	if run, ok := runtime.RunContextFrom(ctx); ok && run.AgentDepth > 0 {
		policy = policy.ForDelegation()
	}

	swarmEnabled := runBool(st, valueSwarmEnabled, r.defaultSwarm)
	subagentEnabled := runBool(st, valueSubagentEnabled, r.defaultSubagent) || swarmEnabled
	hasSwarmTeam := runString(st, valueSwarmTeamID) != ""
	allowed := policy.AllowedTools(r.registry, r.registry.Names())

	out := allowed[:0]
	for _, name := range allowed {
		definition, err := r.registry.Get(name)
		if err != nil {
			continue
		}
		switch definition.Group {
		case "subagent":
			if !subagentEnabled {
				continue
			}
		case "swarm":
			if !swarmEnabled {
				continue
			}
		}
		switch name {
		case "task":
			if swarmEnabled && isLeadRun(st) && !hasSwarmTeam {
				continue
			}
		case "team_create":
			if hasSwarmTeam {
				continue
			}
		case "team_delete":
			if !hasSwarmTeam {
				continue
			}
		}
		if name == "exit_plan_mode" && !runBool(st, "is_plan_mode", false) {
			continue
		}
		if swarmEnabled && isLeadRun(st) && !isCoordinatorTool(definition) {
			continue
		}
		if runBool(st, "is_plan_mode", false) && !planningToolAllowed(definition) {
			continue
		}
		out = append(out, name)
	}
	return slices.Clone(out), nil, nil
}

func planningToolAllowed(definition tool.Definition) bool {
	if definition.Group == "planning" || definition.Name == "ask_clarification" {
		return true
	}
	return definition.Metadata.IsReadOnly && definition.Name != "present_files"
}

// restrictedToolSet applies one subagent profile after runtime feature and
// permission filtering. Children cannot recursively dispatch tasks or mutate
// team lifecycle, but Swarm teammates may still communicate.
type restrictedToolSet struct {
	base    runtimeToolSet
	allowed map[string]struct{}
}

func newRestrictedToolSet(base runtimeToolSet, allowed []string) restrictedToolSet {
	set := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		set[name] = struct{}{}
	}
	return restrictedToolSet{base: base, allowed: set}
}

func (r restrictedToolSet) Resolve(ctx context.Context, st *lifecycle.State) ([]string, []string, error) {
	allowed, disclosed, err := r.base.Resolve(ctx, st)
	if err != nil {
		return nil, nil, err
	}
	out := allowed[:0]
	for _, name := range allowed {
		_, profileAllowed := r.allowed[name]
		// Swarm communication is attached by trusted child identity and remains
		// available to every teammate profile. The profile allowlist governs work
		// tools; applying it to the mailbox tools silently breaks coordination for
		// explore/plan/bash/verification agents.
		if !profileAllowed && !isSwarmCommunicationTool(name) {
			continue
		}
		switch name {
		case "task", "ask_clarification", "present_files", "team_create", "team_delete":
			continue
		}
		out = append(out, name)
	}
	return slices.Clone(out), disclosed, nil
}

func resolveToolPolicy(ctx context.Context, name string) (*permission.Policy, error) {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || !run.Capabilities.Initialized() {
		return nil, errors.New("agentd: tool policy requires an initialized capability view")
	}
	if name == "" {
		return nil, errors.New("agentd: tool policy capability name is empty")
	}
	return capability.ResolveViewAs[*permission.Policy](ctx, run.Capabilities, name, capability.KindPolicy, capability.ResolveRequest{
		GenerationID: run.GenerationID, ThreadID: run.ThreadID, RunID: run.RunID, Values: run.Values,
	})
}

func isSwarmCommunicationTool(name string) bool {
	return name == "send_message" || name == "list_teammates"
}

func runBool(st *lifecycle.State, key string, fallback bool) bool {
	if st == nil {
		return fallback
	}
	value, ok := st.Value(key)
	if !ok || value == nil {
		return fallback
	}
	enabled, ok := value.(bool)
	return ok && enabled
}

func runString(st *lifecycle.State, key string) string {
	if st == nil {
		return ""
	}
	value, _ := st.Value(key)
	result, _ := value.(string)
	return result
}

func isCoordinatorTool(definition tool.Definition) bool {
	switch definition.Group {
	case "subagent", "swarm", "interaction", "planning":
		return true
	}
	switch definition.Name {
	case "present_file", "present_files", "view_image":
		return true
	default:
		return false
	}
}

func isLeadRun(st *lifecycle.State) bool {
	if st == nil {
		return true
	}
	value, _ := st.Value("swarm_agent_name")
	name, _ := value.(string)
	return name == "" || name == "lead"
}
