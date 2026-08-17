package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunAgentStreamsVersionedAgentAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/threads/thread-1/runs" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["assistant_id"] != "reviewer" || payload["on_disconnect"] != "continue" {
			t.Fatalf("payload = %#v", payload)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: end\ndata: {\"status\":\"success\"}\n\n"))
	}))
	defer server.Close()
	var output bytes.Buffer
	err := runAgent([]string{"--server", server.URL, "--thread", "thread-1", "--assistant", "reviewer", "--prompt", "inspect"}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "event: end") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestRunAgentRejectsMissingPrompt(t *testing.T) {
	if err := runAgent(nil, &bytes.Buffer{}); err == nil {
		t.Fatal("missing prompt was accepted")
	}
}
