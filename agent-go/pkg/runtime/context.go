package runtime

import (
	"context"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
)

type RunContext struct {
	RunID       string
	ParentRunID string
	// GenerationID identifies the immutable capability assembly pinned for this
	// run. Reloadable transports must keep the corresponding generation alive
	// until the run releases it.
	GenerationID string
	// EventRunID identifies the root run whose SSE/event-store partition owns
	// externally visible events. Child executions keep their own RunID for
	// isolation and telemetry while publishing into the root run's stream.
	EventRunID          string
	ThreadID            string
	AllowedTools        []string
	AllowedCapabilities []string
	Capabilities        capability.View
	AgentDepth          int
	MaxRecursionDepth   int
	// Values are the flattened run-scoped options used by nested runners. The
	// map belongs to one run and must be cloned before a child mutates it.
	Values map[string]any
	// Swarm identity is assigned by trusted lifecycle code, never by model tool
	// arguments. Lead runs normally leave these empty and resolve by ThreadID.
	SwarmTeamID    string
	SwarmAgentName string
	// SubagentTaskID is assigned by the trusted dispatcher. It is empty for a
	// lead run; stream publishers use it to keep child progress out of the
	// parent's normal message channel.
	SubagentTaskID string
	Journal        *Journal
	// StateMachine is the durable lifecycle authority for this run. Kernel
	// execution requires it to be present and aligned with RunID and ThreadID.
	StateMachine *RunStateMachine
	// Budget is the hierarchical accounting ledger inherited by child runs.
	Budget *BudgetLedger
	// Approvals is the durable approval manager used by policy capabilities.
	Approvals *ApprovalManager
	// Questions is a human collaboration capability. It carries answers and
	// feedback only; it must never be consulted as a permission grant.
	Questions *QuestionManager
	Bus       Bus
	// Publish is the authoritative event path for a run. HTTP adapters use it
	// to fan out to the live bus and durable replay store with one sequence.
	Publish func(context.Context, Event) (int64, error)
	// PersistValues durably replaces the current thread-state projection and
	// publishes the same snapshot to live consumers.
	PersistValues func(context.Context, map[string]any) error
}
type runContextKey struct{}

func WithRunContext(ctx context.Context, r RunContext) context.Context {
	return context.WithValue(ctx, runContextKey{}, r)
}
func RunContextFrom(ctx context.Context) (RunContext, bool) {
	r, ok := ctx.Value(runContextKey{}).(RunContext)
	return r, ok
}

// EventStreamRunID returns the durable event-stream partition for this run.
// Root callers can omit EventRunID; nested runners explicitly inherit it.
func (r RunContext) EventStreamRunID() string {
	if r.EventRunID != "" {
		return r.EventRunID
	}
	return r.RunID
}
