package prompt_test

import (
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/prompt"
)

func TestProductionIsStableAndSorted(t *testing.T) {
	opts := prompt.ProductionOptions{
		Skills: []prompt.Skill{
			{Name: "zeta", Description: "Z"},
			{Name: "alpha", Description: "A"},
		},
		Subagents: []prompt.Subagent{
			{Name: "verification", Description: "Verify work"},
			{Name: "explore", Description: "Explore code"},
		},
		MaxConcurrent: 2,
	}
	first := prompt.Production(opts, prompt.RuntimeOptions{Subagents: true})
	second := prompt.Production(opts, prompt.RuntimeOptions{Subagents: true})
	if first != second {
		t.Fatal("production prompt is not stable")
	}
	if strings.Index(first, `name="alpha"`) > strings.Index(first, `name="zeta"`) {
		t.Fatal("skills are not sorted")
	}
	if strings.Index(first, `name="explore"`) > strings.Index(first, `name="verification"`) {
		t.Fatal("subagents are not sorted")
	}
	if !strings.Contains(first, "at most 2 task calls") {
		t.Fatal("subagent concurrency limit is missing")
	}
}

func TestProductionCapabilitySections(t *testing.T) {
	opts := prompt.ProductionOptions{Subagents: []prompt.Subagent{{Name: "general-purpose", Description: "General work"}}}
	plain := prompt.Production(opts, prompt.RuntimeOptions{})
	if strings.Contains(plain, "## Subagents") || strings.Contains(plain, "## Swarm Mode") {
		t.Fatal("disabled capability sections leaked into the prompt")
	}
	swarm := prompt.Production(opts, prompt.RuntimeOptions{Swarm: true})
	for _, section := range []string{"## Subagents", "## Swarm Mode", "create the team with team_create", "task is unavailable", "swarm_batch", "reviewer", "trusted run context"} {
		if !strings.Contains(swarm, section) {
			t.Fatalf("swarm prompt is missing %q", section)
		}
	}
}

func TestProductionEscapesCatalogMetadata(t *testing.T) {
	got := prompt.Production(prompt.ProductionOptions{Skills: []prompt.Skill{{Name: `bad\"name`, Description: "<inject>"}}}, prompt.RuntimeOptions{})
	for _, escaped := range []string{"&#34;", "&lt;inject&gt;"} {
		if !strings.Contains(got, escaped) {
			t.Fatalf("prompt is missing escaped value %q", escaped)
		}
	}
}

func TestProductionDescribesConfiguredACPAgents(t *testing.T) {
	got := prompt.Production(prompt.ProductionOptions{
		WorkspaceRoot: "/mnt/user-data",
		ACPAgents:     []prompt.Subagent{{Name: "codex", Description: "coding specialist"}},
	}, prompt.RuntimeOptions{})
	for _, want := range []string{"## External ACP Agents", "invoke_acp_agent", `name="codex"`, "coding specialist", "acp-workspace"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %s", want, got)
		}
	}
}
