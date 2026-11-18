package loop

import (
	"context"
	"fmt"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

func (d *agentDriver) boundary(ctx context.Context, typ runtime.EventType, reason string, cause error) error {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Publish == nil {
		return nil
	}
	data := runtime.AgentBoundary{ExecutionRunID: run.RunID, GenerationID: run.GenerationID, Turn: 1, Reason: reason}
	if typ == runtime.EventAgentStepStart || typ == runtime.EventAgentStepEnd {
		data.Step = d.state.Iteration + 1
	}
	if cause != nil {
		data.Error = cause.Error()
	}
	event, err := runtime.NewEvent(run.EventStreamRunID(), run.ThreadID, typ, data)
	if err != nil {
		return err
	}
	event.IdempotencyKey = fmt.Sprintf("run:%s:turn:%d:step:%d:%s", run.RunID, data.Turn, data.Step, typ)
	if _, err := run.Publish(ctx, event); err != nil {
		return fmt.Errorf("loop: persisting %s: %w", typ, err)
	}
	return nil
}
