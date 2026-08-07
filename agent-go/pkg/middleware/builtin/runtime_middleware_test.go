package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

func TestThreadDataAndUploads(t *testing.T) {
	base := t.TempDir()
	h := message.NewHistory()
	h.Append(message.Message{Role: message.RoleUser, Content: "inspect uploads"})
	st := middleware.NewState(middleware.StateInit{ThreadID: "thread-1", History: h})
	if err := NewThreadData(base, false).BeforeAgent(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	paths, _ := st.Values["thread_data"].(map[string]string)
	if err := os.WriteFile(filepath.Join(paths["uploads_path"], "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewUploads().BeforeAgent(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	last := h.All()[h.Len()-1]
	if last.Role != message.RoleSystem || !strings.Contains(last.Content, "notes.txt") {
		t.Fatalf("upload injection = %#v", last)
	}
}

func TestTokenUsageRecordsLeadJournal(t *testing.T) {
	j := runtime.NewJournal(nil)
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{Journal: j})
	st := middleware.NewState(middleware.StateInit{})
	st.ModelOutput = &model.Response{CallID: "call-1", Usage: model.Usage{InputTokens: 3, OutputTokens: 2}}
	if err := NewTokenUsage().AfterModel(ctx, st); err != nil {
		t.Fatal(err)
	}
	if got := j.Totals().LeadTokens; got != 5 {
		t.Fatalf("lead tokens = %d", got)
	}
}

func TestSubagentLimitTruncatesOnlyTaskCalls(t *testing.T) {
	h := message.NewHistory()
	resp := &model.Response{Message: message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{Name: "task"}, {Name: "read_file"}, {Name: "task"}, {Name: "task"}}}}
	h.Append(resp.Message)
	st := middleware.NewState(middleware.StateInit{History: h})
	st.ModelOutput = resp
	if err := NewSubagentLimit(2).AfterModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if got := len(resp.Message.ToolCalls); got != 3 {
		t.Fatalf("tool calls = %d", got)
	}
	if dropped, _ := st.Value("subagent_calls_dropped"); dropped != 1 {
		t.Fatalf("dropped = %#v", dropped)
	}
}

func TestViewImageInjectsToolImageIntoNextModelRequest(t *testing.T) {
	h := message.NewHistory()
	h.Append(message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "image-1", Name: "view_image"}}})
	h.Append(message.Message{Role: message.RoleTool, ToolCallID: "image-1", Name: "view_image", ContentBlocks: []message.ContentBlock{{Type: "image", MimeType: "image/png", Data: "AAAA"}}})
	st := middleware.NewState(middleware.StateInit{History: h})
	st.SetValue("supports_vision", true)
	st.ModelInput = &model.Request{Messages: h.All()}
	if err := NewViewImage().BeforeModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	last := st.ModelInput.Messages[len(st.ModelInput.Messages)-1]
	if last.Role != message.RoleUser || len(last.ContentBlocks) != 1 {
		t.Fatalf("injected message = %#v", last)
	}
}
