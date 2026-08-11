package main

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/permission"
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
	policy, err := permission.NewPolicy(permission.Config{Mode: permission.ModeAllow})
	if err != nil {
		t.Fatal(err)
	}
	resolver := runtimeToolSet{registry: registry, policy: policy}

	assertTools(t, resolver, nil, []string{"ask_clarification", "read_file", "write_file"})
	assertTools(t, resolver, map[string]any{valueSubagentEnabled: true}, []string{"ask_clarification", "read_file", "task", "write_file"})
	assertTools(t, resolver, map[string]any{valueSwarmEnabled: true}, []string{"ask_clarification", "team_create"})
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
	policy, err := permission.NewPolicy(permission.Config{Mode: permission.ModeReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	resolver := runtimeToolSet{registry: registry, policy: policy, defaultSubagent: true}

	assertTools(t, resolver, nil, []string{"read_file"})
	assertTools(t, resolver, map[string]any{valueSubagentEnabled: false}, []string{"read_file"})
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
	policy, err := permission.NewPolicy(permission.Config{Mode: permission.ModeAllow})
	if err != nil {
		t.Fatal(err)
	}
	resolver := runtimeToolSet{registry: registry, policy: policy}

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
			assertTools(t, resolver, map[string]any{
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
	policy, err := permission.NewPolicy(permission.Config{Mode: permission.ModeAllow})
	if err != nil {
		t.Fatal(err)
	}
	base := runtimeToolSet{registry: registry, policy: policy}
	resolver := newRestrictedToolSet(base, []string{"read_file"})
	state := middleware.NewState(middleware.StateInit{})
	state.SetValue(valueSwarmEnabled, true)
	state.SetValue("swarm_agent_name", "explore-1")

	got, _ := resolver.Resolve(state)
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
	policy, err := permission.NewPolicy(permission.Config{Mode: permission.ModeAllow})
	if err != nil {
		t.Fatal(err)
	}
	base := runtimeToolSet{registry: registry, policy: policy, defaultSubagent: true}
	resolver := newRestrictedToolSet(base, registry.Names())

	got, _ := resolver.Resolve(middleware.NewState(middleware.StateInit{}))
	want := []string{"read_file", "write_file"}
	if !slices.Equal(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
}

func assertTools(t *testing.T, resolver runtimeToolSet, values map[string]any, want []string) {
	t.Helper()
	state := middleware.NewState(middleware.StateInit{})
	for key, value := range values {
		state.SetValue(key, value)
	}
	got, disclosed := resolver.Resolve(state)
	if len(disclosed) != 0 {
		t.Fatalf("disclosed = %v, want none", disclosed)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
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
