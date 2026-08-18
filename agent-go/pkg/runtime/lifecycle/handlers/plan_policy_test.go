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

func TestPlanPolicyBlocksExitUntilReviewAndTodoCompletion(t *testing.T) {
	policy, err := handlers.NewPlanPolicy("Plan carefully.")
	if err != nil {
		t.Fatal(err)
	}
	state := lifecycle.NewState(lifecycle.StateInit{})
	state.SetValue("mode", "pro")
	state.SetValue("is_plan_mode", true)
	if reason, err := policy.Evaluate(context.Background(), "stop", state); err != nil || !strings.Contains(reason, "write_todos") {
		t.Fatalf("empty plan gate = %q, err=%v", reason, err)
	}
	state.SetValue("todos", []map[string]string{{"content": "Inspect", "status": "pending"}})
	if reason, err := policy.Evaluate(context.Background(), "stop", state); err != nil || !strings.Contains(reason, "exit_plan_mode") {
		t.Fatalf("review gate = %q, err=%v", reason, err)
	}
	state.SetValue("is_plan_mode", false)
	if reason, err := policy.Evaluate(context.Background(), "stop", state); err != nil || !strings.Contains(reason, "incomplete todos") {
		t.Fatalf("execution gate = %q, err=%v", reason, err)
	}
	state.SetValue("todos", []map[string]string{{"content": "Inspect", "status": "completed"}})
	if reason, err := policy.Evaluate(context.Background(), "stop", state); err != nil || reason != "" {
		t.Fatalf("completed gate = %q, err=%v", reason, err)
	}
}

func TestPlanPolicyInjectsExecutionGuidanceForIncompleteTodos(t *testing.T) {
	policy, err := handlers.NewPlanPolicy("Plan carefully.")
	if err != nil {
		t.Fatal(err)
	}
	state := lifecycle.NewState(lifecycle.StateInit{})
	state.SetValue("mode", "ultra")
	state.SetValue("is_plan_mode", false)
	state.SetValue("todos", []any{map[string]any{"content": "Verify", "status": "in_progress"}})
	state.ModelInput = &model.Request{System: "base"}
	if err := policy.BeforeModel(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.ModelInput.System, "executing an approved structured plan") {
		t.Fatalf("system prompt = %q", state.ModelInput.System)
	}
}
