package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func TestHTTPTransportListsAndCallsToolOverSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "text/event-stream")
		var result any
		if req.Method == "tools/list" {
			result = map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "echo text", "inputSchema": map[string]any{"type": "object"}}}}
		} else {
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}
		}
		raw, _ := json.Marshal(result)
		_, _ = fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":%s}\n\n", req.ID, raw)
	}))
	defer server.Close()
	client := New(&HTTPTransport{URL: server.URL}, "remote")
	defs, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 || defs[0].Name != "remote_echo" {
		t.Fatalf("defs=%#v", defs)
	}
	res, err := defs[0].Handler(context.Background(), tool.Call{Args: json.RawMessage(`{"text":"x"}`)})
	if err != nil || res.Content != "ok" {
		t.Fatalf("res=%#v err=%v", res, err)
	}
}

type failingTransport struct{}

func (failingTransport) Call(context.Context, Request) (Response, error) {
	return Response{}, fmt.Errorf("offline")
}
func (failingTransport) Close() error { return nil }
func TestUnavailableServerDoesNotFailRegistration(t *testing.T) {
	registry := tool.NewRegistry()
	if err := New(failingTransport{}, "").RegisterAvailable(context.Background(), registry, nil); err != nil {
		t.Fatal(err)
	}
	if len(registry.Names()) != 0 {
		t.Fatalf("tools=%v", registry.Names())
	}
}
