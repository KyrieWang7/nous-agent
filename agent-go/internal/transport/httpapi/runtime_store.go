package httpapi

import (
	"context"
	"fmt"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/metadata"
)

type runtimeMetadataStore struct{ store Store }

func (s runtimeMetadataStore) LoadHistory(ctx context.Context, threadID string) ([]message.Message, error) {
	return s.store.LoadHistory(ctx, threadID)
}

func (s runtimeMetadataStore) SaveHistory(ctx context.Context, threadID string, messages []message.Message, replace bool) error {
	return s.store.SaveHistory(ctx, threadID, messages, replace)
}

func (s runtimeMetadataStore) LoadThreadValues(ctx context.Context, threadID string) (map[string]any, error) {
	thread, err := s.store.GetThread(ctx, threadID)
	if err != nil {
		return nil, err
	}
	return cloneMap(thread.Values), nil
}

func (s runtimeMetadataStore) SaveThreadValues(ctx context.Context, threadID string, values map[string]any) error {
	_, err := s.store.UpdateThread(ctx, threadID, nil, values)
	return err
}

func (s runtimeMetadataStore) MarkRunRunning(ctx context.Context, runID string) error {
	_, err := s.store.UpdateRun(ctx, runID, RunUpdate{Status: "running"})
	return err
}

func (s runtimeMetadataStore) ConfirmRunTerminal(ctx context.Context, runID string, state metadata.TerminalState) (metadata.TerminalState, error) {
	run, err := s.store.UpdateRun(ctx, runID, RunUpdate{Status: state.Status, RiskLevel: state.RiskLevel})
	if err != nil {
		return metadata.TerminalState{}, err
	}
	if run.Status == "" {
		return metadata.TerminalState{}, fmt.Errorf("httpapi: terminal store returned an empty run status")
	}
	return metadata.TerminalState{Status: run.Status, RiskLevel: run.RiskLevel}, nil
}

func (s runtimeMetadataStore) SaveCompletion(ctx context.Context, c metadata.Completion) error {
	return s.store.SaveRunCompletion(ctx, RunCompletion{
		RunID: c.RunID, ThreadID: c.ThreadID, Status: c.Status,
		Iterations: c.Iterations, LLMCalls: c.LLMCalls,
		InputTokens: c.InputTokens, OutputTokens: c.OutputTokens, CachedInputTokens: c.CachedInputTokens,
		LeadTokens: c.LeadTokens, SubagentTokens: c.SubagentTokens, AuxiliaryTokens: c.AuxiliaryTokens,
		CostMicros: c.CostMicros, Duration: c.Duration, CompletedAt: c.CompletedAt,
	})
}
