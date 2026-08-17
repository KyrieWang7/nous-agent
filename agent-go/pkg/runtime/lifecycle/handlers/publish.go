package handlers

import (
	"context"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
)

func publishRuntime(ctx context.Context, st *lifecycle.State, typ runtime.EventType, data any) error {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok {
		return nil
	}
	event := runtime.MustEvent(run.EventStreamRunID(), run.ThreadID, typ, data)
	if run.Publish != nil {
		_, err := run.Publish(ctx, event)
		return err
	}
	if run.Bus != nil {
		run.Bus.Publish(ctx, event)
	}
	return nil
}
