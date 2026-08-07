package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/langgraphapi"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/config"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	mw "github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware/builtin"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

func TestAgentHTTPStreamEndToEnd(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"call-1\",\"model\":\"stub\",\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"call-1\",\"model\":\"stub\",\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{\"content\":\"lo\"}}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()

	cfg := config.Defaults()
	cfg.Models = []config.ModelConfig{{Name: "stub", Provider: "openai-compatible", Model: "stub", BaseURL: provider.URL, ContextLength: 1000}}
	cfg.DefaultModel = "stub"
	cfg.Sandbox.Enabled = false
	cfg.Subagents.Enabled = false
	cfg.Title.Enabled = false
	cfg.Summarization.Enabled = false

	built, err := buildAgent(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()
	wantAfterModel := []string{mw.NameSafetyFinishReason, mw.NameLoopDetection, mw.NameSubagentLimit, mw.NameTokenUsage, "telemetry"}
	if got := built.chain.StageOrder(middleware.StageAfterModel); !slices.Equal(got, wantAfterModel) {
		t.Fatalf("after-model order = %v, want %v", got, wantAfterModel)
	}
	api, err := langgraphapi.New(langgraphapi.Options{Agent: built.agent, AllowedTools: built.tools})
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	server := httptest.NewServer(api)
	defer server.Close()

	postJSON(t, server.URL+"/api/v1/threads", `{"thread_id":"e2e-thread"}`)
	resp, err := http.Post(server.URL+"/api/v1/threads/e2e-thread/runs", "application/json", strings.NewReader(`{"input":{"messages":[{"type":"human","content":"say hello"}]},"on_disconnect":"continue"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var messageChunks []string
	var events []string
	scanner := bufio.NewScanner(resp.Body)
	var event string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			events = append(events, event)
		case event == "messages" && strings.HasPrefix(line, "data:"):
			var data []map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &data); err != nil {
				t.Fatal(err)
			}
			if len(data) > 0 {
				if content, ok := data[0]["content"].(string); ok {
					messageChunks = append(messageChunks, content)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(messageChunks, ""); got != "hello" {
		t.Fatalf("streamed content = %q, events = %v", got, events)
	}
	for _, required := range []string{"metadata", "messages", "values", "custom", "end"} {
		if !contains(events, required) {
			t.Fatalf("missing event %q in %v", required, events)
		}
	}
}

func TestPublishModelStreamEventUsesParentRunEnvelopeForChildState(t *testing.T) {
	t.Parallel()

	var published runtime.Event
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{
		RunID:    "parent-run",
		ThreadID: "parent-thread",
		Publish: func(_ context.Context, event runtime.Event) int64 {
			published = event
			return 1
		},
	})
	st := middleware.NewState(middleware.StateInit{
		RunID:    "parent-run:task-1",
		ThreadID: "child-state-thread",
	})
	st.Iteration = 2

	publishModelStreamEvent(ctx, st, model.StreamEvent{Type: model.StreamTextDelta, Delta: "hello"})

	if published.RunID != "parent-run" || published.ThreadID != "parent-thread" {
		t.Fatalf("event envelope = %s/%s, want parent-run/parent-thread", published.RunID, published.ThreadID)
	}
	var payload runtime.ContentDelta
	if err := json.Unmarshal(published.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MessageID != "parent-run:task-1:2" {
		t.Fatalf("message id = %q, want child run identity", payload.MessageID)
	}
}

func postJSON(t *testing.T, url, body string) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		t.Fatalf("POST %s returned %s", url, resp.Status)
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
