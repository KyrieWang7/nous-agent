package replay_test

import (
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/replay"
)

func TestProjectorRebuildsAppendAndReplaceEventsIdempotently(t *testing.T) {
	p := replay.NewProjector()
	appendEvent := runtime.MustEvent("r1", "t1", runtime.EventTranscriptAppend, runtime.TranscriptAppend{Messages: []message.Message{{Role: message.RoleUser, Content: "q"}, {Role: message.RoleAssistant, Content: "a"}}})
	appendEvent.Seq = 2
	if err := p.Apply(appendEvent); err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(appendEvent); err != nil {
		t.Fatal(err)
	}
	replaceEvent := runtime.MustEvent("r1", "t1", runtime.EventTranscriptReplace, runtime.TranscriptReplace{Messages: []message.Message{{Role: message.RoleSystem, Content: "summary"}, {Role: message.RoleUser, Content: "next"}}})
	replaceEvent.Seq = 3
	if err := p.Apply(replaceEvent); err != nil {
		t.Fatal(err)
	}
	h := p.History()
	got := h.All()
	if len(got) != 2 || got[0].Content != "summary" || got[1].Content != "next" {
		t.Fatalf("projected history = %#v", got)
	}
	if p.LastSeq() != 3 {
		t.Fatalf("last seq = %d", p.LastSeq())
	}
}

func TestProjectorIgnoresNonCanonicalEventsAndReportsMalformedPayload(t *testing.T) {
	p := replay.NewProjector()
	ui := runtime.MustEvent("r1", "t1", runtime.EventContentDelta, runtime.ContentDelta{Delta: "partial"})
	ui.Seq = 1
	if err := p.Apply(ui); err != nil {
		t.Fatal(err)
	}
	if p.History().Len() != 0 {
		t.Fatal("UI delta changed transcript")
	}
	bad := runtime.Event{RunID: "r1", ThreadID: "t1", Type: runtime.EventTranscriptAppend, Seq: 2, Data: []byte("{")}
	if err := p.Apply(bad); err == nil {
		t.Fatal("expected malformed transcript error")
	}
}
