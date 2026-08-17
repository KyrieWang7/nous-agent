package replay_test

import (
	"context"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/replay"
)

func TestSnapshotStoreCopiesMessages(t *testing.T) {
	s := replay.NewMemorySnapshotStore()
	in := replay.Snapshot{RunID: "r1", ThreadID: "t1", LastSeq: 7, Messages: []message.Message{{Role: message.RoleUser, Content: "q"}}}
	if err := s.Save(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	in.Messages[0].Content = "mutated"
	out, err := s.Load(context.Background(), "r1", "t1")
	if err != nil {
		t.Fatal(err)
	}
	if out.LastSeq != 7 || out.Messages[0].Content != "q" {
		t.Fatalf("snapshot = %#v", out)
	}
}
