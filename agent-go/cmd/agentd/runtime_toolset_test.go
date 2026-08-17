package main

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/permission"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func TestRuntimeToolSetAppliesRunCapabilities(t *testing.T) {
	registry := tool.NewRegistry()
	for _, definition := range []tool.Definition{
		testTool("read_file", "file:read", true),
		testTool("write_file", "file:write", false),
		testTool("ask_clarification", "interaction", true),
		testTool("task", "subagent", false),
		testTool("team_create", "swarm", false),
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := permission.NewPolicy(permission.Config{Preset: permission.PresetDangerFullAccess})
	if err != nil {
		t.Fatal(err)
	}
	resolver := runtimeToolSet{registry: registry, policyCapability: "policy.tools"}
	ctx := policyContext(t, policy)

	assertTools(t, ctx, resolver, nil, []string{"ask_clarification", "read_file", "write_file"})
	assertTools(t, ctx, resolver, map[string]any{valueSubagentEnabled: true}, []string{"ask_clarification", "read_file", "task", "write_file"})
	assertTools(t, ctx, resolver, map[string]any{valueSwarmEnabled: true}, []string{"ask_clarification", "team_create"})
}

func TestRuntimeToolSetHonoursDefaultsAndPermission(t *testing.T) {
	registry := tool.NewRegistry()
	for _, definition := range []tool.Definition{
		testTool("read_file", "file:read", true),
		testTool("write_file", "file:write", false),
		testTool("task", "subagent", false),
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := permission.NewPolicy(permission.Config{Preset: permission.PresetReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	resolver := runtimeToolSet{registry: registry, policyCapability: "policy.tools", defaultSubagent: true}
	ctx := policyContext(t, policy)

	assertTools(t, ctx, resolver, nil, []string{"read_file"})
	assertTools(t, ctx, resolver, map[string]any{valueSubagentEnabled: false}, []string{"read_file"})
}

func TestRuntimeToolSetPinsDelegatedApprovalToNever(t *testing.T) {
	registry := tool.NewRegistry()
	for _, definition := range []tool.Definition{
		testTool("read_file", "file:read", true),
		testTool("external_write", "external", false),
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := permission.NewPolicy(permission.Config{Preset: permission.PresetWorkspaceWrite})
	if err != nil {
		t.Fatal(err)
	}
	ctx := policyContext(t, policy)
	run, _ := runtime.RunContextFrom(ctx)
	run.AgentDepth = 1
	ctx = runtime.WithRunContext(context.Background(), run)
	resolver := runtimeToolSet{registry: registry, policyCapability: "policy.tools"}
	assertTools(t, ctx, resolver, nil, []string{"read_file"})
}

func TestRuntimeToolSetExposesOnlyValidSwarmLifecycleAction(t *testing.T) {
	registry := tool.NewRegistry()
	for _, definition := range []tool.Definition{
		testTool("task", "subagent", false),
		testTool("team_create", "swarm", false),
		testTool("team_delete", "swarm", false),
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := permission.NewPolicy(permission.Config{Preset: permission.PresetDangerFullAccess})
	if err != nil {
		t.Fatal(err)
	}
	resolver := runtimeToolSet{registry: registry, policyCapability: "policy.tools"}
	ctx := policyContext(t, policy)

	tests := []struct {
		name   string
		teamID string
		want   []string
	}{
		{name: "before team creation", want: []string{"team_create"}},
		{name: "after team creation", teamID: "team-1", want: []string{"task", "team_delete"}},
		{name: "after team deletion", teamID: "", want: []string{"team_create"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertTools(t, ctx, resolver, map[string]any{
				valueSwarmEnabled: true,
				valueSwarmTeamID:  tt.teamID,
			}, tt.want)
		})
	}
}

func TestRestrictedToolSetGivesSwarmTeammatesWorkToolsWithoutNestedOrchestration(t *testing.T) {
	registry := tool.NewRegistry()
	for _, definition := range []tool.Definition{
		testTool("read_file", "file:read", true),
		testTool("write_file", "file:write", false),
		testTool("task", "subagent", true),
		testTool("ask_clarification", "interaction", true),
		testTool("present_files", "interaction", true),
		testTool("team_create", "swarm", false),
		testTool("send_message", "swarm", false),
		testTool("list_teammates", "swarm", true),
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := permission.NewPolicy(permission.Config{Preset: permission.PresetDangerFullAccess})
	if err != nil {
		t.Fatal(err)
	}
	base := runtimeToolSet{registry: registry, policyCapability: "policy.tools"}
	resolver := newRestrictedToolSet(base, []string{"read_file"})
	state := lifecycle.NewState(lifecycle.StateInit{})
	state.SetValue(valueSwarmEnabled, true)
	state.SetValue("swarm_agent_name", "explore-1")

	got, _, err := resolver.Resolve(policyContext(t, policy), state)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"list_teammates", "read_file", "send_message"}
	if !slices.Equal(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
}

func TestRestrictedToolSetInheritedProfileExcludesChildControlTools(t *testing.T) {
	registry := tool.NewRegistry()
	for _, definition := range []tool.Definition{
		testTool("ask_clarification", "interaction", true),
		testTool("present_files", "interaction", true),
		testTool("read_file", "file:read", true),
		testTool("task", "subagent", true),
		testTool("write_file", "file:write", false),
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := permission.NewPolicy(permission.Config{Preset: permission.PresetDangerFullAccess})
	if err != nil {
		t.Fatal(err)
	}
	base := runtimeToolSet{registry: registry, policyCapability: "policy.tools", defaultSubagent: true}
	resolver := newRestrictedToolSet(base, registry.Names())

	got, _, err := resolver.Resolve(policyContext(t, policy), lifecycle.NewState(lifecycle.StateInit{}))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"read_file", "write_file"}
	if !slices.Equal(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
}

func assertTools(t *testing.T, ctx context.Context, resolver runtimeToolSet, values map[string]any, want []string) {
	t.Helper()
	state := lifecycle.NewState(lifecycle.StateInit{})
	for key, value := range values {
		state.SetValue(key, value)
	}
	got, disclosed, err := resolver.Resolve(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(disclosed) != 0 {
		t.Fatalf("disclosed = %v, want none", disclosed)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
}

func policyContext(t *testing.T, policy *permission.Policy) context.Context {
	t.Helper()
	registry := capability.NewRegistry()
	if err := capability.RegisterValue(registry, capability.Value{Definition: capability.Definition{Name: "policy.tools", Kind: capability.KindPolicy, Scope: capability.ScopeRun}, Value: policy}); err != nil {
		t.Fatal(err)
	}
	view, err := capability.NewView(registry.Snapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "run-1", ThreadID: "thread-1", Capabilities: view})
}

func testTool(name, group string, readOnly bool) tool.Definition {
	return tool.Definition{
		Name:       name,
		Group:      group,
		Parameters: json.RawMessage(`{"type":"object"}`),
		Metadata:   tool.Metadata{IsReadOnly: readOnly},
		Handler: func(context.Context, tool.Call) (*tool.Result, error) {
			return &tool.Result{}, nil
		},
	}
}
