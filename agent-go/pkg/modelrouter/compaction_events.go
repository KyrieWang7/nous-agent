package modelrouter

import (
	"context"
	"errors"
	"fmt"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
)

func compactHistory(ctx context.Context, st *lifecycle.State, compact func() (bool, error)) (changed bool, err error) {
	st.CompactionAttempt++
	run, ok := runtime.RunContextFrom(ctx)
	tracked := ok && run.StateMachine != nil && run.StateMachine.Snapshot().Phase == runtime.RunRunning
	publish := func(ctx context.Context, typ runtime.EventType, payload any) error {
		if !ok {
			return nil
		}
		ev := runtime.MustEvent(run.EventStreamRunID(), run.ThreadID, typ, payload)
		ev.IdempotencyKey = fmt.Sprintf("run:%s:compaction:%d:%d:%s", run.RunID, st.Iteration, st.CompactionAttempt, typ)
		if run.Publish != nil {
			_, e := run.Publish(ctx, ev)
			return e
		}
		if run.Bus != nil {
			run.Bus.Publish(ctx, ev)
		}
		return nil
	}
	if tracked {
		if err = run.StateMachine.BeginCompaction(); err != nil {
			return false, err
		}
		defer func() {
			if !run.StateMachine.Snapshot().Phase.Terminal() {
				err = errors.Join(err, run.StateMachine.Resume())
			}
		}()
	}
	if err = publish(ctx, runtime.EventCompactionStart, map[string]any{"iteration": st.Iteration}); err != nil {
		return false, err
	}
	changed, err = compact()
	st.Compacted = st.Compacted || changed
	payload := map[string]any{"iteration": st.Iteration, "compacted": changed}
	if err != nil {
		payload["error"] = err.Error()
	}
	err = errors.Join(err, publish(context.WithoutCancel(ctx), runtime.EventCompactionComplete, payload))
	return changed, err
}
