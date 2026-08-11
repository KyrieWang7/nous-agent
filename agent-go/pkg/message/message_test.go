package message_test

import (
	"encoding/json"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
)

func TestClone_DeepCopiesToolCalls(t *testing.T) {
	t.Parallel()

	orig := message.Message{
		Role:    message.RoleAssistant,
		Content: "calling",
		ToolCalls: []message.ToolCall{
			{ID: "c1", Name: "ls", Arguments: json.RawMessage(`{"path":"/"}`)},
		},
	}

	dup := message.Clone(orig)
	dup.ToolCalls[0].Name = "mutated"
	dup.ToolCalls[0].Arguments[0] = 'X'

	if orig.ToolCalls[0].Name != "ls" {
		t.Fatalf("Clone shared ToolCalls slice: orig name = %q", orig.ToolCalls[0].Name)
	}
	if string(orig.ToolCalls[0].Arguments) != `{"path":"/"}` {
		t.Fatalf("Clone shared Arguments buffer: orig args = %s", orig.ToolCalls[0].Arguments)
	}
}

func TestClone_DeepCopiesContentBlocks(t *testing.T) {
	t.Parallel()

	orig := message.Message{
		Role:          message.RoleUser,
		ContentBlocks: []message.ContentBlock{{Type: "text", Text: "hi"}},
	}

	dup := message.Clone(orig)
	dup.ContentBlocks[0].Text = "mutated"

	if orig.ContentBlocks[0].Text != "hi" {
		t.Fatalf("Clone shared ContentBlocks slice: orig text = %q", orig.ContentBlocks[0].Text)
	}
}

func TestClone_NilSlicesStayNil(t *testing.T) {
	t.Parallel()

	dup := message.Clone(message.Message{Role: message.RoleUser, Content: "hi"})

	if dup.ToolCalls != nil {
		t.Errorf("ToolCalls = %v, want nil", dup.ToolCalls)
	}
	if dup.ContentBlocks != nil {
		t.Errorf("ContentBlocks = %v, want nil", dup.ContentBlocks)
	}
}

func TestClone_DeepCopiesAdditionalKwargs(t *testing.T) {
	t.Parallel()

	orig := message.Message{AdditionalKwargs: map[string]any{
		"nested": map[string]any{"status": "completed"},
	}}
	dup := message.Clone(orig)
	dup.AdditionalKwargs["nested"].(map[string]any)["status"] = "failed"
	if got := orig.AdditionalKwargs["nested"].(map[string]any)["status"]; got != "completed" {
		t.Fatalf("Clone shared AdditionalKwargs: status = %v", got)
	}
}

func TestExtractSubagentStatusContract(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, content, want string
	}{
		{"succeeded", "Task Succeeded. Result: ok", message.SubagentCompleted},
		{"failed", "Task failed. Error: boom", message.SubagentFailed},
		{"cancelled", "Task cancelled by user.", message.SubagentCancelled},
		{"timed out", "Task timed out. Error: 900 seconds", message.SubagentTimedOut},
		{"polling timed out", "Task polling timed out after 1 minutes", message.SubagentPollingTimedOut},
		{"unknown", "Investigating ...", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := message.ExtractSubagentStatus(tc.content)
			if tc.want == "" {
				if ok || got != "" {
					t.Fatalf("status = %q, ok=%v, want no status", got, ok)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("status = %q, ok=%v, want %q", got, ok, tc.want)
			}
		})
	}
}

func TestStampSubagentStatus(t *testing.T) {
	t.Parallel()
	msg := message.StampSubagentStatus(message.Message{
		Role:    message.RoleTool,
		Name:    "task",
		Content: "Task failed. Error: underlying failure",
	})
	if got := msg.AdditionalKwargs[message.SubagentStatusKey]; got != message.SubagentFailed {
		t.Fatalf("status = %v, want %q", got, message.SubagentFailed)
	}
	if got := msg.AdditionalKwargs[message.SubagentErrorKey]; got != "underlying failure" {
		t.Fatalf("error = %v, want underlying failure", got)
	}
}

func TestStartsToolTransaction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  message.Message
		want bool
	}{
		{
			name: "assistant with tool calls",
			msg: message.Message{
				Role:      message.RoleAssistant,
				ToolCalls: []message.ToolCall{{ID: "c1", Name: "ls"}},
			},
			want: true,
		},
		{
			name: "assistant without tool calls",
			msg:  message.Message{Role: message.RoleAssistant, Content: "done"},
			want: false,
		},
		{
			name: "user message is never a transaction start",
			msg: message.Message{
				Role:      message.RoleUser,
				ToolCalls: []message.ToolCall{{ID: "c1", Name: "ls"}},
			},
			want: false,
		},
		{
			name: "tool result",
			msg:  message.Message{Role: message.RoleTool, ToolCallID: "c1"},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.msg.StartsToolTransaction(); got != tc.want {
				t.Errorf("StartsToolTransaction() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestToolTransactionSpans(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{Role: message.RoleSystem, Content: "sys"},                                         // 0
		{Role: message.RoleUser, Content: "q"},                                             // 1
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "a"}, {ID: "b"}}}, // 2
		{Role: message.RoleTool, ToolCallID: "a"},                                          // 3
		{Role: message.RoleTool, ToolCallID: "b"},                                          // 4
		{Role: message.RoleAssistant, Content: "answer"},                                   // 5
		{Role: message.RoleUser, Content: "q2"},                                            // 6
		{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "c"}}},            // 7
		{Role: message.RoleTool, ToolCallID: "c"},                                          // 8
	}

	got := message.ToolTransactionSpans(msgs)
	want := []message.Span{{Start: 2, End: 5}, {Start: 7, End: 9}}

	if len(got) != len(want) {
		t.Fatalf("got %d spans (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("span[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestToolTransactionSpans_Empty(t *testing.T) {
	t.Parallel()

	if got := message.ToolTransactionSpans(nil); got != nil {
		t.Errorf("ToolTransactionSpans(nil) = %v, want nil", got)
	}
}
