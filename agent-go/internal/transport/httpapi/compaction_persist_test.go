package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/runmanager"
)

// 压缩轮的持久化是 pace-grid 上真实坏过的一类 bug：压缩把内存转录换成了
// "摘要 + 尾部"，但持久化层若写回的是压缩前的完整转录，那么
//  1. 下一轮 LoadHistory 又读回全量，压缩等于被撤销；
//  2. 摘要模型的钱每一轮都要重付一次。
//
// 设计文档 §13.2 要求压缩轮走 ReplaceTranscript(history.All())，
// 即持久化**压缩后**的转录。
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

	store := NewMemoryStore()
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

	srv, err := New(Options{Agent: agent, Store: store, HeartbeatInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	if _, err := store.CreateThread(ctx, newThread("thread-c", nil), false); err != nil {
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
	store := NewMemoryStore()
	agent := agentFunc(func(context.Context, runmanager.AgentRequest) (runmanager.AgentResult, error) {
		return runmanager.AgentResult{
			Messages:  []message.Message{{Role: message.RoleAssistant, Content: "delta only"}},
			Compacted: true,
		}, nil
	})

	srv, err := New(Options{Agent: agent, Store: store, HeartbeatInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	if _, err := store.CreateThread(ctx, newThread("thread-missing-transcript", nil), false); err != nil {
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
