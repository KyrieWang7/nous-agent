package replay_test

import (
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/replay"
)

func TestStateProjectorRebuildsLatestLifecycleSnapshot(t *testing.T) {
	projector := replay.NewStateProjector()
	first, _ := runtime.NewRunStateMachine("run-1", "thread-1")
	_ = first.Start()
	snapshot := first.Snapshot()
	event := runtime.MustEvent("run-1", "thread-1", runtime.EventRunStateChanged, runtime.RunStateChanged{Snapshot: snapshot})
	event.Seq = 2
	if err := projector.Apply(event); err != nil {
		t.Fatal(err)
	}
	if got := projector.Snapshot(); got.Phase != runtime.RunRunning || got.Version != 1 {
		t.Fatalf("snapshot = %+v", got)
	}
	if err := projector.Apply(event); err != nil {
		t.Fatal(err)
	}
	if projector.LastSeq() != 2 {
		t.Fatalf("last seq = %d", projector.LastSeq())
	}
}
