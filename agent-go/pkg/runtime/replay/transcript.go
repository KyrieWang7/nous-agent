package replay

import (
	"context"
	"errors"
	"fmt"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

// RebuildTranscript loads an optional snapshot and then applies every
// canonical event after its cursor. A missing snapshot is equivalent to an
// empty cache and never changes correctness.
func RebuildTranscript(ctx context.Context, snapshots SnapshotStore, events runtime.EventStore, runID, threadID string) ([]message.Message, error) {
	if events == nil {
		return nil, errors.New("replay: event store is required")
	}
	projector := NewProjector()
	if snapshots != nil {
		snapshot, err := snapshots.Load(ctx, runID, threadID)
		switch {
		case err == nil:
			projector = NewProjectorFromSnapshot(snapshot)
		case errors.Is(err, ErrSnapshotNotFound):
		default:
			return nil, fmt.Errorf("replay: loading transcript snapshot: %w", err)
		}
	}
	var applyErr error
	if err := runtime.Replay(ctx, events, runID, projector.LastSeq(), 200, func(event runtime.Event) bool {
		applyErr = projector.Apply(event)
		return applyErr == nil
	}); err != nil {
		return nil, fmt.Errorf("replay: applying transcript events: %w", err)
	}
	if applyErr != nil {
		return nil, applyErr
	}
	return projector.History().All(), nil
}
