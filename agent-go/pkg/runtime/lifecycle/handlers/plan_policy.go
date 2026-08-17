package handlers

import (
	"context"
	"errors"
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
	if active != true || state.ModelInput == nil {
		return nil
	}
	base := strings.TrimSpace(state.ModelInput.System)
	if base == "" {
		state.ModelInput.System = p.guidance
	} else {
		state.ModelInput.System = base + "\n\n" + p.guidance
	}
	return nil
}
