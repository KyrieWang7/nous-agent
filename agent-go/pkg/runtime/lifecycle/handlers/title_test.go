package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
)

func TestTitleSkipsAssistantToolCallMessages(t *testing.T) {
	history := message.NewHistory()
	history.Append(message.Message{Role: message.RoleUser, Content: "fix the build"})
	history.Append(message.Message{
		Role: message.RoleAssistant,
		ToolCalls: []message.ToolCall{{
			ID:        "call-1",
			Name:      "read_file",
			Arguments: json.RawMessage(`{"path":"go.mod"}`),
		}},
	})
	history.Append(message.Message{Role: message.RoleTool, ToolCallID: "call-1", Name: "read_file", Content: "module example"})
	history.Append(message.Message{Role: message.RoleAssistant, Content: "The build is fixed."})

	model := faux.New(faux.Text("Fix build"))
	state := lifecycle.NewState(lifecycle.StateInit{History: history})
	if err := NewTitle(model, 8, 80).AfterAgent(context.Background(), state); err != nil {
		t.Fatal(err)
	}

	requests := model.Requests()
	if len(requests) != 1 || len(requests[0].Messages) != 2 {
		t.Fatalf("requests = %#v, want one request with two messages", requests)
	}
	if requests[0].Messages[0].Role != message.RoleUser || requests[0].Messages[1].Content != "The build is fixed." {
		t.Fatalf("title sample = %#v", requests[0].Messages)
	}
	if title, ok := state.Value("title"); !ok || title != "Fix build" {
		t.Fatalf("title = %q, ok = %t", title, ok)
	}
}

func TestTitleWaitsForTextAnswerAfterClarification(t *testing.T) {
	history := message.NewHistory()
	history.Append(message.Message{Role: message.RoleUser, Content: "help"})
	history.Append(message.Message{
		Role:      message.RoleAssistant,
		ToolCalls: []message.ToolCall{{ID: "call-1", Name: ClarificationToolName}},
	})
	history.Append(message.Message{Role: message.RoleTool, ToolCallID: "call-1", Name: ClarificationToolName, Content: "Which environment?"})

	model := faux.New(faux.Text("unused"))
	state := lifecycle.NewState(lifecycle.StateInit{History: history})
	if err := NewTitle(model, 8, 80).AfterAgent(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if len(model.Requests()) != 0 {
		t.Fatal("title model was called before a final text answer existed")
	}
}
