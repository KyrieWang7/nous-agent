package replay_test

import (
	"context"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/replay"
)

func TestRebuildTranscriptContinuesAfterSnapshot(t *testing.T) {
	ctx := context.Background()
	snapshots := replay.NewMemorySnapshotStore()
	if err := snapshots.Save(ctx, replay.Snapshot{RunID: "run-1", ThreadID: "thread-1", LastSeq: 2, Messages: []message.Message{{Role: message.RoleUser, Content: "before"}}}); err != nil {
		t.Fatal(err)
	}
	events := runtime.NewMemoryEventStore()
	appendEvent := runtime.MustEvent("run-1", "thread-1", runtime.EventTranscriptAppend, runtime.TranscriptAppend{Messages: []message.Message{{Role: message.RoleAssistant, Content: "after"}}})
	appendEvent.Seq = 3
	if err := events.PutBatch(ctx, []runtime.Event{appendEvent}); err != nil {
		t.Fatal(err)
	}
	got, err := replay.RebuildTranscript(ctx, snapshots, events, "run-1", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != "before" || got[1].Content != "after" {
		t.Fatalf("transcript = %#v", got)
	}
}

func TestRebuildTranscriptDoesNotRequireSnapshot(t *testing.T) {
	ctx := context.Background()
	events := runtime.NewMemoryEventStore()
	event := runtime.MustEvent("run-1", "thread-1", runtime.EventTranscriptReplace, runtime.TranscriptReplace{Messages: []message.Message{{Role: message.RoleAssistant, Content: "canonical"}}})
	event.Seq = 4
	if err := events.PutBatch(ctx, []runtime.Event{event}); err != nil {
		t.Fatal(err)
	}
	got, err := replay.RebuildTranscript(ctx, replay.NewMemorySnapshotStore(), events, "run-1", "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Content != "canonical" {
		t.Fatalf("transcript = %#v", got)
	}
}
