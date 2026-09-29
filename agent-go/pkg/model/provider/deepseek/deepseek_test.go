package deepseek

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMessagesRequestAndSparseToolIndex(t *testing.T) {
	var payload map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "key" {
			t.Errorf("request %s headers=%v", r.URL.Path, r.Header)
		}
		json.NewDecoder(r.Body).Decode(&payload)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, v := range []string{
			`{"type":"message_start","message":{"id":"m1","model":"ds","usage":{"input_tokens":4,"cache_read_input_tokens":3}}}`,
			`{"type":"content_block_start","index":2,"content_block":{"type":"thinking","thinking":"reason","signature":"sig"}}`,
			`{"type":"content_block_stop","index":2}`,
			`{"type":"content_block_start","index":7,"content_block":{"type":"tool_use","id":"call","name":"read","input":{}}}`,
			`{"type":"content_block_delta","index":7,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"x\"}"}}`,
			`{"type":"content_block_stop","index":7}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
			`{"type":"message_stop"}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", v)
		}
	}))
	defer s.Close()
	m, err := New(model.ProviderConfig{Name: "test", Model: "ds", BaseURL: s.URL + "/v1", APIKey: "key", SupportsThinking: true})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := m.Complete(context.Background(), model.Request{MaxTokens: 99, Thinking: true, Messages: []message.Message{{Role: message.RoleSystem, Content: "summary"}, {Role: message.RoleUser, Content: "question", ContentBlocks: []message.ContentBlock{{Type: "image", MimeType: "image/png", Data: "aGk="}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].ID != "call" || resp.Message.ReasoningContent != "reason" || resp.Usage.InputTokens != 7 {
		t.Fatalf("resp=%+v", resp)
	}
	if payload["max_tokens"] != float64(99) || payload["system"] != "summary" || payload["stream"] != true {
		t.Fatalf("payload=%+v", payload)
	}
	if resp.Message.AdditionalKwargs["deepseek_replay"] == nil {
		t.Fatal("thinking signature missing")
	}
}

func TestAssistantBlocksAreNotSilentlyDropped(t *testing.T) {
	m, _ := New(model.ProviderConfig{Model: "ds"})
	c := m.(*Client)
	req := model.Request{Messages: []message.Message{{Role: message.RoleAssistant, ContentBlocks: []message.ContentBlock{{Type: "text", Text: "block text"}}}}}
	raw, err := c.serialize(context.Background(), req)
	if err != nil || !strings.Contains(string(raw), "block text") {
		t.Fatalf("body=%s err=%v", raw, err)
	}
	req.Messages[0].ContentBlocks[0].Type = "image"
	if _, err := c.serialize(context.Background(), req); !errors.Is(err, model.ErrInvalidRequest) {
		t.Fatalf("unsupported content silently dropped: %v", err)
	}
}
