package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/runmanager"
)

func TestRawEventsPaginationAndRunOwnership(t *testing.T) {
	store := NewMemoryStore()
	if err := store.CreateRun(context.Background(), Run{RunID: "run", ThreadID: "thread", Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	events := runtime.NewMemoryEventStore()
	var batch []runtime.Event
	for _, seq := range []int64{1, 4, 7} {
		event := runtime.MustEvent("run", "thread", runtime.EventModelInputCommitted, map[string]any{"private": "canonical"})
		event.Seq = seq
		batch = append(batch, event)
	}
	if err := events.PutBatch(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Options{Store: store, EventStore: events, Agent: agentFunc(func(context.Context, runmanager.AgentRequest) (runmanager.AgentResult, error) {
		return runmanager.AgentResult{}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	tests := []struct {
		query string
		seqs  []int64
		next  int64
		more  bool
	}{
		{"?limit=2", []int64{1, 4}, 4, true},
		{"?limit=2&after=4", []int64{7}, 7, false},
		{"?limit=2&after=7", []int64{}, 7, false},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/threads/thread/runs/run/events/raw"+tc.query, nil))
			if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d headers=%v body=%s", rec.Code, rec.Header(), rec.Body.String())
			}
			var page struct {
				Events []runtime.Event `json:"events"`
				Next   int64           `json:"next_after"`
				More   bool            `json:"has_more"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if page.Events == nil || len(page.Events) != len(tc.seqs) || page.Next != tc.next || page.More != tc.more {
				t.Fatalf("page=%+v want seqs=%v next=%d more=%v", page, tc.seqs, tc.next, tc.more)
			}
			for i, event := range page.Events {
				if event.Seq != tc.seqs[i] || event.Type != runtime.EventModelInputCommitted {
					t.Fatalf("event=%+v", event)
				}
			}
		})
	}
	for _, path := range []string{"/api/v1/threads/other/runs/run/events/raw", "/api/v1/threads/thread/runs/missing/events/raw"} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("wrong owner or missing run: status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

func TestToolCallDeltaProjectsIndexedPreparingChunk(t *testing.T) {
	delta := runtime.ToolCallDelta{MessageID: "run:0", ToolCallIndex: 2, ToolCallID: "call", ToolCallName: "write_file", ArgumentsDelta: `{"path":"a.txt"`}
	event := runtime.MustEvent("run", "thread", runtime.EventToolCallDelta, delta)
	if event.Category != runtime.CategoryTrace || !clientVisibleRuntimeEvent(event) {
		t.Fatalf("event=%+v", event)
	}
	wire, err := projectRuntimeEvent(event)
	if err != nil || wire.Event != "messages" {
		t.Fatalf("wire=%+v err=%v", wire, err)
	}
	var tuple []json.RawMessage
	if err := json.Unmarshal(wire.Data, &tuple); err != nil {
		t.Fatal(err)
	}
	var chunk wireMessage
	if err := json.Unmarshal(tuple[0], &chunk); err != nil {
		t.Fatal(err)
	}
	if chunk.ID != delta.MessageID || len(chunk.ToolCalls) != 0 || len(chunk.ToolCallChunks) != 1 {
		t.Fatalf("chunk=%+v", chunk)
	}
	call := chunk.ToolCallChunks[0]
	if call.Index != 2 || call.ID != "call" || call.Name != "write_file" || call.Args != delta.ArgumentsDelta {
		t.Fatalf("call=%+v", call)
	}
}
