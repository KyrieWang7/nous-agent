// Package runmanager owns one run from admission through terminal
// confirmation. Transports adapt requests and projections at this boundary;
// the manager contains no HTTP or wire-format knowledge.
package runmanager

import (
	"context"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

type Agent interface {
	Run(context.Context, AgentRequest) (AgentResult, error)
}

type RunPreparer interface {
	PrepareRun() (PreparedRun, error)
}

type RunBudgetProvider interface {
	RunBudgetLimits() runtime.BudgetAmount
}

type RunCapabilityProvider interface {
	RunCapabilityNames() []string
}

type RunLimitProvider interface {
	RunMaxRecursionDepth() int
}

type PreparedRun struct {
	Agent               Agent
	AllowedTools        []string
	Pricer              *runtime.Pricer
	BudgetLimits        runtime.BudgetAmount
	AllowedCapabilities []string
	MaxRecursionDepth   int
	Release             func()
}

type AgentRequest struct {
	RunID         string
	ThreadID      string
	AssistantID   string
	Prompt        string
	ContentBlocks []message.ContentBlock
	History       []message.Message
	Config        map[string]any
	Context       map[string]any
}

type AgentResult struct {
	Messages   []message.Message
	Transcript []message.Message
	Output     string
	Iterations int
	Usage      Usage
	Compacted  bool
	Streamed   bool
	Values     map[string]any
	RiskLevel  string
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type Run struct {
	RunID        string
	ThreadID     string
	AssistantID  string
	CreatedAt    time.Time
	OnDisconnect runtime.DisconnectPolicy
}

type Input struct {
	Prompt           string
	ContentBlocks    []message.ContentBlock
	Config           map[string]any
	Context          map[string]any
	PreparationError error
}

type ProjectionKind string

const (
	ProjectionRunStart ProjectionKind = "run_start"
	ProjectionValues   ProjectionKind = "values"
	ProjectionMessage  ProjectionKind = "message"
	ProjectionUsage    ProjectionKind = "usage"
	ProjectionError    ProjectionKind = "error"
	ProjectionRunEnd   ProjectionKind = "run_end"
)

type Projection struct {
	Kind ProjectionKind
	Data any
}

type ProjectionPublisher func(context.Context, Run, Projection) error
