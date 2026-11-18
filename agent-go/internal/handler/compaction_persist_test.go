package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/model"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/repository"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/service"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/runmanager"
)

func TestCompactionTurnPersistsTheCompactedTranscript(t *testing.T) {
	t.Parallel()

	existing := []message.Message{
		{Role: message.RoleUser, Content: "q1"},
		{Role: message.RoleAssistant, Content: "a1"},
		{Role: message.RoleUser, Content: "q2"},
		{Role: message.RoleAssistant, Content: "a2"},
		{Role: message.RoleUser, Content: "q3"},
		{Role: message.RoleAssistant, Content: "a3"},
	}

	// 本轮发生压缩：agent 把转录压成 "摘要 + 本轮问答"
	compacted := []message.Message{
		{Role: message.RoleSystem, Content: "## Summary\n\nearlier turns compressed"},
		{Role: message.RoleUser, Content: "q4"},
		{Role: message.RoleAssistant, Content: "a4"},
	}

	store := repository.NewMemoryStore()
	agent := agentFunc(func(_ context.Context, req runmanager.AgentRequest) (runmanager.AgentResult, error) {
		if len(req.History) != len(existing) {
			t.Errorf("agent saw %d history messages, want %d", len(req.History), len(existing))
		}
		return runmanager.AgentResult{
			Messages:   compacted[1:],
			Transcript: compacted,
			Output:     "a4",
			Compacted:  true,
			Iterations: 1,
		}, nil
	})

	srv, err := newHTTPServer(service.Options{Agent: agent, Store: store, HeartbeatInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	if _, err := store.CreateThread(ctx, model.NewThread("thread-c", nil), false); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory(ctx, "thread-c", existing, false); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(ts.URL+"/api/v1/threads/thread-c/runs", "application/json",
		strings.NewReader(`{"input":{"messages":[{"type":"human","content":"q4"}]},"on_disconnect":"continue"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	readSSE(t, resp.Body)

	got, err := store.LoadHistory(ctx, "thread-c")
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != len(compacted) {
		t.Fatalf("persisted %d messages, want %d (the compacted transcript).\n"+
			"persisted: %v\n"+
			"a compaction turn must persist the compacted transcript; otherwise the next run "+
			"reloads the full history and pays for the summary again every turn",
			len(got), len(compacted), contentsOf(got))
	}
	if got[0].Role != message.RoleSystem || !strings.Contains(got[0].Content, "Summary") {
		t.Fatalf("first persisted message = %+v, want the summary", got[0])
	}
}

func TestCompactionTurnWithoutTranscriptDoesNotOverwriteHistory(t *testing.T) {
	t.Parallel()

	existing := []message.Message{
		{Role: message.RoleUser, Content: "q1"},
		{Role: message.RoleAssistant, Content: "a1"},
	}
	store := repository.NewMemoryStore()
	agent := agentFunc(func(context.Context, runmanager.AgentRequest) (runmanager.AgentResult, error) {
		return runmanager.AgentResult{
			Messages:  []message.Message{{Role: message.RoleAssistant, Content: "delta only"}},
			Compacted: true,
		}, nil
	})

	srv, err := newHTTPServer(service.Options{Agent: agent, Store: store, HeartbeatInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	if _, err := store.CreateThread(ctx, model.NewThread("thread-missing-transcript", nil), false); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory(ctx, "thread-missing-transcript", existing, false); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(ts.URL+"/api/v1/threads/thread-missing-transcript/runs", "application/json",
		strings.NewReader(`{"input":{"messages":[{"type":"human","content":"q2"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	events := readSSE(t, resp.Body)
	assertEventOrder(t, events, "metadata", "error", "end")

	got, err := store.LoadHistory(ctx, "thread-missing-transcript")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(existing) || got[0].Content != existing[0].Content || got[1].Content != existing[1].Content {
		t.Fatalf("history changed after invalid compacted result: %#v", got)
	}
}

func contentsOf(msgs []message.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = string(m.Role) + ":" + m.Content
	}
	return out
}
