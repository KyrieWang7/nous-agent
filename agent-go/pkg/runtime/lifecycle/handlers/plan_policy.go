package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
)

const NamePlanPolicy = "planPolicy"

type PlanPolicy struct {
	guidance string
}

func NewPlanPolicy(guidance string) (*PlanPolicy, error) {
	guidance = strings.TrimSpace(guidance)
	if guidance == "" {
		return nil, errors.New("plan policy requires non-empty guidance")
	}
	return &PlanPolicy{guidance: guidance}, nil
}

func (PlanPolicy) Name() string { return NamePlanPolicy }

func (p *PlanPolicy) BeforeModel(_ context.Context, state *lifecycle.State) error {
	active, _ := state.Value("is_plan_mode")
	if state.ModelInput == nil {
		return nil
	}
	guidance := p.guidance
	if active != true {
		if !managedPlanMode(state) || !hasIncompleteTodos(state) {
			return nil
		}
		guidance = "You are executing an approved structured plan. Work through the current todos, call write_todos immediately whenever a task status changes, and do not give the final answer until every task is completed."
	}
	base := strings.TrimSpace(state.ModelInput.System)
	if base == "" {
		state.ModelInput.System = guidance
	} else {
		state.ModelInput.System = base + "\n\n" + guidance
	}
	return nil
}

// Evaluate is the kernel stop gate for managed plans. A clean model response
// cannot bypass plan review or leave approved work partially completed.
func (p *PlanPolicy) Evaluate(_ context.Context, _ string, state *lifecycle.State) (string, error) {
	active, _ := state.Value("is_plan_mode")
	if active == true {
		if !hasAnyTodos(state) {
			return "plan mode requires a structured task list; call write_todos, then submit the complete plan with exit_plan_mode", nil
		}
		return "plan mode is still active; submit the complete plan with exit_plan_mode and wait for user approval before executing it", nil
	}
	if managedPlanMode(state) && hasIncompleteTodos(state) {
		return "the approved plan still has incomplete todos; continue execution and update their statuses with write_todos before finishing", nil
	}
	return "", nil
}

func managedPlanMode(state *lifecycle.State) bool {
	value, _ := state.Value("mode")
	mode := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	return mode == "pro" || mode == "ultra"
}

func hasAnyTodos(state *lifecycle.State) bool {
	value, ok := state.Value("todos")
	if !ok || value == nil {
		return false
	}
	switch todos := value.(type) {
	case []map[string]string:
		return len(todos) > 0
	case []any:
		return len(todos) > 0
	default:
		return false
	}
}

func hasIncompleteTodos(state *lifecycle.State) bool {
	value, ok := state.Value("todos")
	if !ok || value == nil {
		return false
	}
	switch todos := value.(type) {
	case []map[string]string:
		for _, todo := range todos {
			if todo["status"] != "completed" {
				return true
			}
		}
	case []any:
		for _, raw := range todos {
			todo, valid := raw.(map[string]any)
			if !valid || todo["status"] != "completed" {
				return true
			}
		}
	}
	return false
}
