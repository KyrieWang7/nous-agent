package message_test

import (
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
)

func TestTrimmer_UnderLimitReturnsInput(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		msg(message.RoleSystem, "sys"),
		msg(message.RoleUser, "q1"),
		msg(message.RoleAssistant, "a1"),
	}

	tr := message.NewTrimmer(100_000, nil)
	got := tr.Trim(msgs)

	if len(got) != len(msgs) {
		t.Fatalf("Trim() dropped messages under the limit: %v", contents(got))
	}
}

func TestTrimmer_ZeroLimitDisabled(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		msg(message.RoleUser, strings.Repeat("x", 10_000)),
	}

	tr := message.NewTrimmer(0, nil)
	if got := tr.Trim(msgs); len(got) != 1 {
		t.Fatalf("Trim() with limit 0 should be a no-op, got %d messages", len(got))
	}
}

func TestTrimmer_KeepsLeadingSystemMessage(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("x", 4_000) // ~1000 tokens each

	msgs := []message.Message{
		msg(message.RoleSystem, "SYSTEM ANCHOR"),
		msg(message.RoleUser, big),
		msg(message.RoleAssistant, big),
		msg(message.RoleUser, big),
		msg(message.RoleAssistant, big),
	}

	tr := message.NewTrimmer(2_500, nil)
	got := tr.Trim(msgs)

	if len(got) == 0 || got[0].Role != message.RoleSystem || got[0].Content != "SYSTEM ANCHOR" {
		t.Fatalf("Trim() dropped the leading system message: %v", contents(got))
	}
	if len(got) >= len(msgs) {
		t.Fatalf("Trim() did not trim anything: %d messages", len(got))
	}
}

func TestTrimmer_TrimsFromTheFront(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("x", 4_000)

	msgs := []message.Message{
		msg(message.RoleUser, "OLDEST"),
		msg(message.RoleAssistant, big),
		msg(message.RoleUser, big),
		msg(message.RoleAssistant, "NEWEST"),
	}

	tr := message.NewTrimmer(1_500, nil)
	got := tr.Trim(msgs)

	if len(got) == 0 {
		t.Fatal("Trim() returned nothing")
	}
	if got[len(got)-1].Content != "NEWEST" {
		t.Fatalf("Trim() dropped the newest message: %v", contents(got))
	}
	for _, m := range got {
		if m.Content == "OLDEST" {
			t.Fatalf("Trim() kept the oldest message instead of trimming from the front: %v", contents(got))
		}
	}
}

// 裁剪切点不得落在工具事务中间：半个事务会让下一次模型请求非法
// （tool_calls 没有对应的 tool 结果）。
func TestTrimmer_NeverSplitsToolTransaction(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("x", 4_000)

	msgs := []message.Message{
		msg(message.RoleUser, big),
		{
			Role:      message.RoleAssistant,
			ToolCalls: []message.ToolCall{{ID: "c1", Name: "ls"}},
		},
		{Role: message.RoleTool, ToolCallID: "c1", Content: big},
		msg(message.RoleAssistant, "final"),
	}

	tr := message.NewTrimmer(1_200, nil)
	got := tr.Trim(msgs)

	// 要么事务的 assistant 与 tool 都在，要么都不在
	var hasCall, hasResult bool
	for _, m := range got {
		if m.StartsToolTransaction() {
			hasCall = true
		}
		if m.Role == message.RoleTool && m.ToolCallID == "c1" {
			hasResult = true
		}
	}
	if hasCall != hasResult {
		t.Fatalf("Trim() split a tool transaction: hasCall=%v hasResult=%v, msgs=%v",
			hasCall, hasResult, contents(got))
	}
}

func TestTrimmer_AlwaysKeepsAtLeastLastMessage(t *testing.T) {
	t.Parallel()

	// 单条消息本身就超限：仍必须保留它，否则请求为空
	msgs := []message.Message{
		msg(message.RoleUser, strings.Repeat("x", 400_000)),
	}

	tr := message.NewTrimmer(10, nil)
	if got := tr.Trim(msgs); len(got) != 1 {
		t.Fatalf("Trim() = %d messages, want 1; an over-limit final message must survive", len(got))
	}
}

func TestTrimmer_CustomCounter(t *testing.T) {
	t.Parallel()

	// 每条消息恒定 100 token 的计数器，便于精确断言
	tr := message.NewTrimmer(250, fixedCounter{})

	msgs := []message.Message{
		msg(message.RoleUser, "a"),
		msg(message.RoleAssistant, "b"),
		msg(message.RoleUser, "c"),
		msg(message.RoleAssistant, "d"),
	}

	got := tr.Trim(msgs)
	if len(got) != 2 {
		t.Fatalf("Trim() = %d messages (%v), want 2 with a 100-token-per-message counter and limit 250",
			len(got), contents(got))
	}
}

type fixedCounter struct{}

func (fixedCounter) Count(string) int { return 96 } // +4 overhead = 100
