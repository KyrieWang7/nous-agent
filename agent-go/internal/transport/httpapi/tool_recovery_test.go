package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/harness"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/replay"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

type rejectToolResponse struct{}

func (rejectToolResponse) Name() string { return "rejectToolResponse" }
func (rejectToolResponse) AfterModel(_ context.Context, st *lifecycle.State) error {
	if len(st.ModelOutput.Message.ToolCalls) > 0 {
		return errors.New("governance unavailable")
	}
	return nil
}

func TestFailedToolStepPersistsBalancedTranscriptBeforeRunEnd(t *testing.T) {
	model := faux.New(faux.ToolCall("write", `{}`), faux.Text("continued safely"))
	h, err := harness.New(harness.Options{
		Model: model,
		Tools: []tool.Definition{{Name: "write", Group: "test", Handler: func(context.Context, tool.Call) (*tool.Result, error) {
			t.Error("rejected tool executed")
			return nil, nil
		}}},
		LifecycleHandlers: []lifecycle.Handler{rejectToolResponse{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	store := NewMemoryStore()
	events := runtime.NewMemoryEventStore()
	srv, err := New(Options{Agent: harness.Agent{Runner: h.Runner(), Capabilities: h.Runtime().Generation.Capabilities()}, Store: store, EventStore: events, AllowedTools: []string{"write"}})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	server := httptest.NewServer(srv)
	defer server.Close()
	ctx := context.Background()
	if _, err := store.CreateThread(ctx, newThread("recovery-thread", nil), false); err != nil {
		t.Fatal(err)
	}
	post := func(prompt string) {
		t.Helper()
		resp, err := http.Post(server.URL+"/api/v1/threads/recovery-thread/runs", "application/json", strings.NewReader(`{"input":{"messages":[{"type":"human","content":"`+prompt+`"}]},"on_disconnect":"continue"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("HTTP status %s", resp.Status)
		}
		readSSE(t, resp.Body)
	}
	post("try a tool")
	runs, err := store.ListRuns(ctx, "recovery-thread")
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%+v, error=%v", runs, err)
	}
	if runs[0].Status != "error" {
		t.Fatalf("failed run status=%s", runs[0].Status)
	}
	history, err := store.LoadHistory(ctx, "recovery-thread")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || len(history[1].ToolCalls) != 1 || history[2].Role != message.RoleTool || history[2].ToolCallID != history[1].ToolCalls[0].ID || history[2].AdditionalKwargs["tool_recovery_code"] != "TOOL_NOT_STARTED" {
		t.Fatalf("failed transcript is not paired: %+v", history)
	}
	replayed, err := replay.RebuildTranscript(ctx, nil, events, runs[0].RunID, "recovery-thread")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(history, replayed) {
		t.Fatalf("event replay differs from persisted transcript: %+v vs %+v", replayed, history)
	}
	log, err := events.Get(ctx, runs[0].RunID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	var resultSeq, transcriptSeq, endSeq int64
	for _, event := range log {
		switch event.Type {
		case runtime.EventToolResult:
			resultSeq = event.Seq
		case runtime.EventTranscriptAppend:
			transcriptSeq = event.Seq
		case runtime.EventRunEnd:
			endSeq = event.Seq
		}
	}
	if resultSeq <= 0 || transcriptSeq <= resultSeq || endSeq <= transcriptSeq {
		t.Fatalf("invalid recovery/close order: result=%d transcript=%d end=%d", resultSeq, transcriptSeq, endSeq)
	}
	post("continue")
	requests := model.Requests()
	if len(requests) != 2 || len(requests[1].Messages) != 4 {
		t.Fatalf("next model input=%+v", requests)
	}
	if !reflect.DeepEqual(requests[1].Messages[:3], history) {
		t.Fatalf("next run lost recovery facts: %+v", requests[1].Messages)
	}
}
