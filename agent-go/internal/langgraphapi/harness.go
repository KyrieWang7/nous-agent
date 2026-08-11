package langgraphapi

import (
	"context"
	"errors"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

// HarnessAgent adapts the native loop runner to the HTTP service. The loop is
// unaware of wire formats and persistence; this adapter restores history,
// establishes the per-thread sandbox lease, and returns only this run's new
// transcript entries.
type HarnessAgent struct {
	Runner              *loop.Runner
	SystemPrompt        string
	SystemPromptBuilder func(map[string]any) string
	Sandbox             sandbox.Provider
	InitialValues       map[string]any
}

func (a HarnessAgent) Run(ctx context.Context, req AgentRequest) (AgentResult, error) {
	if a.Runner == nil {
		return AgentResult{}, errors.New("langgraphapi: harness runner is nil")
	}
	history := message.NewHistory()
	for _, m := range req.History {
		history.Append(m)
	}
	watermark := history.Watermark()
	if a.Sandbox != nil {
		lease := sandbox.NewLease(a.Sandbox, req.ThreadID)
		ctx = sandbox.NewContext(ctx, lease)
	}
	values := mergeRunValues(a.InitialValues, req.Config, req.Context)
	if run, ok := runtime.RunContextFrom(ctx); ok {
		run.Values = values
		ctx = runtime.WithRunContext(ctx, run)
	}
	systemPrompt := a.SystemPrompt
	if a.SystemPromptBuilder != nil {
		systemPrompt = a.SystemPromptBuilder(values)
	}
	result, err := a.Runner.Run(ctx, loop.Request{
		ThreadID:      req.ThreadID,
		RunID:         req.RunID,
		AssistantID:   req.AssistantID,
		SystemPrompt:  systemPrompt,
		History:       history,
		Prompt:        req.Prompt,
		ContentBlocks: req.ContentBlocks,
		Values:        values,
	})
	if err != nil {
		return AgentResult{}, err
	}
	agentResult := AgentResult{
		Messages:   history.Since(watermark),
		Output:     result.Output,
		Iterations: result.Iterations,
		Compacted:  result.Compacted,
		Streamed:   result.Streamed,
		Values:     result.Values,
		RiskLevel:  result.RiskLevel,
		Usage: Usage{
			InputTokens:  result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens,
		},
	}
	if result.Compacted {
		agentResult.Transcript = history.All()
	}
	return agentResult, nil
}

func mergeRunValues(sources ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, source := range sources {
		for key, value := range source {
			out[key] = value
		}
		if configurable, ok := source["configurable"].(map[string]any); ok {
			for key, value := range configurable {
				out[key] = value
			}
		}
	}
	return out
}
