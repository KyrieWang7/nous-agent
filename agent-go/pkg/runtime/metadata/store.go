// Package metadata defines the Runtime persistence port for run execution.
// Product DTOs and transport query APIs do not cross this boundary.
package metadata

import (
	"context"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
)

type Completion struct {
	RunID             string
	ThreadID          string
	Status            string
	Iterations        int
	LLMCalls          int
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
	LeadTokens        int
	SubagentTokens    int
	AuxiliaryTokens   int
	CostMicros        int64
	Duration          time.Duration
	CompletedAt       time.Time
}

type TerminalState struct {
	Status    string
	RiskLevel string
}

type Store interface {
	LoadHistory(context.Context, string) ([]message.Message, error)
	SaveHistory(context.Context, string, []message.Message, bool) error
	LoadThreadValues(context.Context, string) (map[string]any, error)
	SaveThreadValues(context.Context, string, map[string]any) error
	MarkRunRunning(context.Context, string) error
	ConfirmRunTerminal(context.Context, string, TerminalState) (TerminalState, error)
	SaveCompletion(context.Context, Completion) error
}
