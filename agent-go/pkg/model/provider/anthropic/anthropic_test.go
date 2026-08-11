package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

func TestCompleteIgnoresOpenAICompatibleExtraBody(t *testing.T) {
	var payload map[string]any
	temperature := 0.2
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = fmt.Fprint(w, `{"id":"m1","model":"claude","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer srv.Close()

	m, err := New(model.ProviderConfig{
		Name:             "a",
		Model:            "claude",
		BaseURL:          srv.URL,
		SupportsThinking: true,
		Temperature:      &temperature,
		ExtraBody:        map[string]any{"openai_only": true},
		ThinkingExtraBody: map[string]any{
			"deepseek_only": true,
			"thinking":      map[string]any{"type": "deepseek-enabled"},
			"temperature":   1.0,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Complete(context.Background(), model.Request{Thinking: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["openai_only"]; ok {
		t.Fatalf("ordinary OpenAI extra_body leaked into Anthropic request: %#v", payload)
	}
	if _, ok := payload["deepseek_only"]; ok {
		t.Fatalf("conditional DeepSeek extra_body leaked into Anthropic request: %#v", payload)
	}
	if payload["temperature"] != temperature {
		t.Fatalf("temperature = %v, want %v", payload["temperature"], temperature)
	}
	thinking, ok := payload["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("native Anthropic thinking was overridden: %#v", payload["thinking"])
	}
}

func TestCompleteMapsThinkingToolsAndStop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("anthropic-version") == "" {
			t.Error("missing version header")
		}
		_, _ = fmt.Fprint(w, `{"id":"m1","model":"claude","stop_reason":"tool_use","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"use tool"},{"type":"tool_use","id":"t1","name":"read","input":{"path":"x"}}],"usage":{"input_tokens":4,"output_tokens":5,"cache_creation_input_tokens":2,"cache_read_input_tokens":3}}`)
	}))
	defer srv.Close()
	m, err := New(model.ProviderConfig{Name: "a", Model: "claude", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := m.Complete(context.Background(), model.Request{System: "sys", Messages: []message.Message{{Role: message.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != model.StopReasonToolCalls || resp.Message.ReasoningContent != "hmm" || len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("resp=%#v", resp)
	}
	if resp.Usage.InputTokens != 9 || resp.Usage.OutputTokens != 5 || resp.Usage.CachedInputTokens != 3 {
		t.Fatalf("usage=%#v", resp.Usage)
	}
}

func TestStreamNormalizesCachedInputUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"model\":\"claude\",\"usage\":{\"input_tokens\":4,\"cache_creation_input_tokens\":2,\"cache_read_input_tokens\":3}}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()
	m, err := New(model.ProviderConfig{Name: "a", Model: "claude", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := m.Stream(context.Background(), model.Request{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	resp, err := reader.Result()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.InputTokens != 9 || resp.Usage.OutputTokens != 5 || resp.Usage.CachedInputTokens != 3 {
		t.Fatalf("usage=%#v", resp.Usage)
	}
}
