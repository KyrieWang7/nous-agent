package handlers_test

import (
	"context"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle/handlers"
)

func TestPlanPolicyInjectsGuidanceWithoutChangingToolCatalog(t *testing.T) {
	policy, err := handlers.NewPlanPolicy("Plan carefully and present concrete steps.")
	if err != nil {
		t.Fatal(err)
	}
	state := lifecycle.NewState(lifecycle.StateInit{})
	state.SetValue("is_plan_mode", true)
	state.ToolSet = []string{"write_todos", "bash"}
	state.ModelInput = &model.Request{
		System: "base",
		Tools:  []model.ToolSchema{{Name: "write_todos"}, {Name: "bash"}},
	}
	if err := policy.BeforeModel(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.ModelInput.System, "Plan carefully") {
		t.Fatalf("system prompt = %q", state.ModelInput.System)
	}
	if len(state.ToolSet) != 2 || len(state.ModelInput.Tools) != 2 {
		t.Fatalf("plan mode changed the stable tool catalog: %v / %v", state.ToolSet, state.ModelInput.Tools)
	}
}

func TestPlanPolicyIsInactiveWithoutPlanState(t *testing.T) {
	policy, err := handlers.NewPlanPolicy("Plan carefully.")
	if err != nil {
		t.Fatal(err)
	}
	state := lifecycle.NewState(lifecycle.StateInit{})
	state.ModelInput = &model.Request{System: "base"}
	if err := policy.BeforeModel(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if state.ModelInput.System != "base" {
		t.Fatalf("system prompt = %q", state.ModelInput.System)
	}
}

func TestPlanPolicyRejectsBlankGuidance(t *testing.T) {
	if _, err := handlers.NewPlanPolicy("  "); err == nil {
		t.Fatal("blank guidance was accepted")
	}
}
