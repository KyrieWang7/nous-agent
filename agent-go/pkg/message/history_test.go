package message_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
)

func msg(role message.Role, content string) message.Message {
	return message.Message{Role: role, Content: content}
}

func contents(msgs []message.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}

func TestHistory_AppendAndAll(t *testing.T) {
	t.Parallel()

	h := message.NewHistory()
	h.Append(msg(message.RoleUser, "q1"))
	h.Append(msg(message.RoleAssistant, "a1"))

	if got, want := h.Len(), 2; got != want {
		t.Fatalf("Len() = %d, want %d", got, want)
	}
	if got := contents(h.All()); got[0] != "q1" || got[1] != "a1" {
		t.Fatalf("All() = %v", got)
	}
}

func TestHistory_AllReturnsCopy(t *testing.T) {
	t.Parallel()

	h := message.NewHistory()
	h.Append(msg(message.RoleUser, "q1"))

	snapshot := h.All()
	snapshot[0].Content = "mutated"

	if h.All()[0].Content != "q1" {
		t.Fatal("All() exposed the internal slice; callers can corrupt the transcript")
	}
}

// 这是 pace-grid 上的真实 bug：回合开始时保存切片下标，压缩后 len(history) 变小，
// 于是整轮问答都不入库，DB 与内存转录从此分叉。水位必须是"累计 append 次数"。
func TestHistory_SinceSurvivesReplace(t *testing.T) {
	t.Parallel()

	h := message.NewHistory()
	h.Append(msg(message.RoleUser, "q1"))
	h.Append(msg(message.RoleAssistant, "a1"))
	h.Append(msg(message.RoleUser, "q2"))
	h.Append(msg(message.RoleAssistant, "a2"))

	wm := h.Watermark() // 本回合开始

	h.Append(msg(message.RoleUser, "q3"))

	// 压缩：转录被整体替换，长度由 5 缩短为 2
	h.Replace([]message.Message{
		msg(message.RoleSystem, "## Summary"),
		msg(message.RoleUser, "q3"),
	})

	h.Append(msg(message.RoleAssistant, "a3"))

	got := contents(h.Since(wm))
	if len(got) != 2 || got[0] != "q3" || got[1] != "a3" {
		t.Fatalf("Since(watermark) = %v, want [q3 a3]; compaction turn would lose the exchange", got)
	}
}

func TestHistory_SinceClampsWhenTranscriptShorterThanDelta(t *testing.T) {
	t.Parallel()

	h := message.NewHistory()
	for i := range 10 {
		h.Append(msg(message.RoleUser, fmt.Sprintf("q%d", i)))
	}

	wm := h.Watermark()
	h.Append(msg(message.RoleUser, "new1"))
	h.Append(msg(message.RoleUser, "new2"))

	// 压缩到只剩 1 条：回溯量 (2) 大于现有长度 (1)，不得 panic 或越界
	h.Replace([]message.Message{msg(message.RoleSystem, "## Summary")})

	got := h.Since(wm)
	if len(got) != 1 {
		t.Fatalf("Since() = %v, want the single surviving message", contents(got))
	}
}

func TestHistory_SinceCurrentWatermarkIsEmpty(t *testing.T) {
	t.Parallel()

	h := message.NewHistory()
	h.Append(msg(message.RoleUser, "q1"))

	if got := h.Since(h.Watermark()); len(got) != 0 {
		t.Fatalf("Since(now) = %v, want empty", contents(got))
	}
}

func TestHistory_WatermarkNotResetByReplace(t *testing.T) {
	t.Parallel()

	h := message.NewHistory()
	h.Append(msg(message.RoleUser, "q1"))
	h.Append(msg(message.RoleUser, "q2"))

	before := h.Watermark()
	h.Replace([]message.Message{msg(message.RoleSystem, "## Summary")})

	if after := h.Watermark(); after != before {
		t.Fatalf("Replace changed the watermark: %d -> %d; it must count appends, not length", before, after)
	}
}

func TestHistory_ReplaceLastAssistant(t *testing.T) {
	t.Parallel()

	h := message.NewHistory()
	h.Append(msg(message.RoleUser, "q1"))
	h.Append(msg(message.RoleAssistant, "unsafe answer"))
	h.Append(msg(message.RoleTool, "tool output"))

	ok := h.ReplaceLastAssistant(msg(message.RoleAssistant, "safe fallback"))
	if !ok {
		t.Fatal("ReplaceLastAssistant() = false, want true")
	}

	got := contents(h.All())
	want := []string{"q1", "safe fallback", "tool output"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("All() = %v, want %v", got, want)
		}
	}
}

func TestHistory_ReplaceLastAssistantWithoutAssistant(t *testing.T) {
	t.Parallel()

	h := message.NewHistory()
	h.Append(msg(message.RoleUser, "q1"))

	if h.ReplaceLastAssistant(msg(message.RoleAssistant, "x")) {
		t.Fatal("ReplaceLastAssistant() = true with no assistant message present")
	}
	if h.Len() != 1 {
		t.Fatalf("Len() = %d, want 1; nothing should have been appended", h.Len())
	}
}

func TestHistory_ReplaceDoesNotAliasCaller(t *testing.T) {
	t.Parallel()

	h := message.NewHistory()
	replacement := []message.Message{msg(message.RoleSystem, "## Summary")}
	h.Replace(replacement)

	replacement[0].Content = "mutated"

	if h.All()[0].Content != "## Summary" {
		t.Fatal("Replace aliased the caller's slice")
	}
}

func TestHistory_LoadResetsWatermarkBaseline(t *testing.T) {
	t.Parallel()

	h := message.NewHistory()
	h.Load([]message.Message{
		msg(message.RoleUser, "q1"),
		msg(message.RoleAssistant, "a1"),
	})

	// Load 用于从持久化恢复：恢复的消息不算"本回合新增"
	if got := h.Since(h.Watermark()); len(got) != 0 {
		t.Fatalf("Since(watermark) right after Load = %v, want empty", contents(got))
	}
	if h.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", h.Len())
	}
}

func TestHistory_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	h := message.NewHistory()
	var wg sync.WaitGroup

	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Append(msg(message.RoleUser, fmt.Sprintf("q%d", i)))
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = h.All()
			_ = h.Len()
			_ = h.Watermark()
			_ = h.TokenCount()
		}()
	}
	wg.Wait()

	if h.Len() != 50 {
		t.Fatalf("Len() = %d, want 50", h.Len())
	}
}
