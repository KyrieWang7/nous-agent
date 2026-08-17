package harness

import (
	"context"
	"errors"
	"fmt"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/runmanager"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

// Agent adapts a native Kernel runner to the Runtime RunManager. It pins one
// immutable capability view and contains no transport or wire-format logic.
type Agent struct {
	Runner              *loop.Runner
	SystemPrompt        string
	SystemPromptBuilder func(map[string]any) string
	InitialValues       map[string]any
	Capabilities        capability.Snapshot
	GenerationID        string
	SandboxCapability   string
	BudgetLimits        runtime.BudgetAmount
	MaxRecursionDepth   int
	AllowedCapabilities []string
}

func (a Agent) RunBudgetLimits() runtime.BudgetAmount { return a.BudgetLimits }
func (a Agent) RunMaxRecursionDepth() int             { return a.MaxRecursionDepth }
func (a Agent) RunCapabilityNames() []string {
	return append([]string(nil), a.AllowedCapabilities...)
}

func (a Agent) Run(ctx context.Context, req runmanager.AgentRequest) (runmanager.AgentResult, error) {
	if a.Runner == nil {
		return runmanager.AgentResult{}, errors.New("harness: runner is nil")
	}
	history := message.NewHistory()
	for _, msg := range req.History {
		history.Append(msg)
	}
	watermark := history.Watermark()
	values := mergeRunValues(a.InitialValues, req.Config, req.Context)
	run, hasRun := runtime.RunContextFrom(ctx)
	allowedCapabilities := a.AllowedCapabilities
	if hasRun && run.AllowedCapabilities != nil {
		allowedCapabilities = run.AllowedCapabilities
	}
	view, err := capability.NewView(a.Capabilities, allowedCapabilities)
	if err != nil {
		return runmanager.AgentResult{}, fmt.Errorf("harness: building run capability view: %w", err)
	}
	if hasRun {
		run.AllowedCapabilities = view.Names()
		run.Capabilities = view
		if run.GenerationID == "" {
			run.GenerationID = a.GenerationID
		}
		run.Values = values
		ctx = runtime.WithRunContext(ctx, run)
	}
	sandboxProvider, err := a.resolveSandbox(ctx, req, view)
	if err != nil {
		return runmanager.AgentResult{}, err
	}
	if sandboxProvider != nil {
		lease := sandbox.NewLease(sandboxProvider, req.ThreadID)
		ctx = sandbox.NewContext(ctx, lease)
	}
	systemPrompt := a.SystemPrompt
	if a.SystemPromptBuilder != nil {
		systemPrompt = a.SystemPromptBuilder(values)
	}
	result, err := a.Runner.Run(ctx, loop.Request{
		ThreadID: req.ThreadID, RunID: req.RunID, AssistantID: req.AssistantID,
		SystemPrompt: systemPrompt, History: history, Prompt: req.Prompt,
		ContentBlocks: req.ContentBlocks, Values: values,
	})
	if err != nil {
		return runmanager.AgentResult{}, err
	}
	agentResult := runmanager.AgentResult{
		Messages: history.Since(watermark), Output: result.Output,
		Iterations: result.Iterations, Compacted: result.Compacted,
		Streamed: result.Streamed, Values: result.Values, RiskLevel: result.RiskLevel,
		Usage: runmanager.Usage{InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens},
	}
	if result.Compacted {
		agentResult.Transcript = history.All()
	}
	return agentResult, nil
}

func (a Agent) resolveSandbox(ctx context.Context, req runmanager.AgentRequest, view capability.View) (sandbox.Provider, error) {
	if a.SandboxCapability == "" {
		return nil, nil
	}
	provider, err := capability.ResolveViewAs[sandbox.Provider](ctx, view, a.SandboxCapability, capability.KindSandbox, capability.ResolveRequest{
		GenerationID: a.GenerationID, ThreadID: req.ThreadID, RunID: req.RunID,
	})
	if err != nil {
		return nil, fmt.Errorf("harness: resolving sandbox capability: %w", err)
	}
	return provider, nil
}

func mergeRunValues(sources ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, source := range sources {
		for key, value := range source {
			out[key] = value
		}
	}
	return out
}
