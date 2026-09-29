package deepseek

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func emitText(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, v := range []string{`{"type":"message_start","message":{"id":"reply","model":"ds"}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"ok"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, `{"type":"message_stop"}`} {
		fmt.Fprintf(w, "data: %s\n\n", v)
	}
}
func TestFileCacheAndStaleIDRecovery(t *testing.T) {
	var uploads, calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("anthropic-beta") != filesBeta {
			t.Error("missing files beta")
		}
		if r.URL.Path == "/v1/files" {
			if err := r.ParseMultipartForm(1024); err != nil {
				t.Error(err)
				return
			}
			defer r.MultipartForm.RemoveAll()
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				return
			}
			defer file.Close()
			raw, _ := io.ReadAll(file)
			if string(raw) != "hi" || header.Header.Get("Content-Type") != "image/png" || r.FormValue("expires_after[seconds]") != "604800" {
				t.Errorf("unexpected upload")
			}
			n := uploads.Add(1)
			json.NewEncoder(w).Encode(File{ID: fmt.Sprintf("file-%d", n), Type: "file", Filename: "image", MimeType: "image/png", SizeBytes: 2, CreatedAt: time.Now().UTC()})
			return
		}
		var req struct {
			Messages []turn `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.Messages) != 1 || req.Messages[0].Content[0].Source["type"] != "file" {
			t.Errorf("missing file reference: %+v", req)
		}
		if calls.Add(1) == 2 {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":{"code":"file_not_found","message":"file deleted"}}`)
			return
		}
		emitText(w)
	}))
	defer s.Close()
	m, err := New(model.ProviderConfig{Name: "ds", Model: "ds", BaseURL: s.URL, UseFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	req := model.Request{Messages: []message.Message{{Role: message.RoleUser, ContentBlocks: []message.ContentBlock{{Type: "image", MimeType: "image/png", Data: "aGk="}}}}}
	for i := 0; i < 3; i++ {
		if _, err := m.Complete(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	if uploads.Load() != 2 || calls.Load() != 4 {
		t.Fatalf("uploads=%d calls=%d", uploads.Load(), calls.Load())
	}
	if req.Messages[0].ContentBlocks[0].Data != "aGk=" {
		t.Fatal("canonical image mutated")
	}
}
func TestFilesDoNotRedirectCredentials(t *testing.T) {
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	m, _ := New(model.ProviderConfig{Model: "ds", BaseURL: source.URL, APIKey: "secret"})
	_, err := m.(*Client).UploadFile(context.Background(), []byte("x"), "image/png", "x.png", 3600)
	if err == nil || requests.Load() != 0 {
		t.Fatalf("err=%v redirected=%d", err, requests.Load())
	}
}
func TestReplayAndEmptyToolResult(t *testing.T) {
	m, _ := New(model.ProviderConfig{Model: "ds"})
	c := m.(*Client)
	msg := message.Message{Role: message.RoleAssistant, ReasoningContent: "reason", ToolCalls: []message.ToolCall{{ID: "call", Name: "read", Arguments: json.RawMessage(`{}`)}}}
	rp := replay{Model: "ds", Digest: digest(msg), Blocks: []block{{Type: "thinking", Thinking: "reason", Signature: "sig"}, {Type: "tool_use", ID: "call", Name: "read", Input: json.RawMessage(`{}`)}}}
	msg.AdditionalKwargs = map[string]any{"deepseek_replay": rp}
	req := model.Request{Messages: []message.Message{{Role: message.RoleUser, Content: "question"}, msg, {Role: message.RoleTool, ToolCallID: "call"}}}
	raw, err := c.serialize(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"signature":"sig"`) || !strings.Contains(string(raw), `"content":[]`) {
		t.Fatalf("request=%s", raw)
	}
	req.Messages[1].ReasoningContent = "edited"
	raw, err = c.serialize(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"signature"`) {
		t.Fatal("stale thinking signature reused")
	}
}
