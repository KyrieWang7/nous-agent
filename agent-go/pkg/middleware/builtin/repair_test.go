package builtin_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware/builtin"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

func hist(msgs ...message.Message) *message.History {
	h := message.NewHistory()
	h.Load(msgs)
	return h
}

func user(content string) message.Message {
	return message.Message{Role: message.RoleUser, Content: content}
}

func assistantCalls(ids ...string) message.Message {
	m := message.Message{Role: message.RoleAssistant}
	for _, id := range ids {
		m.ToolCalls = append(m.ToolCalls, message.ToolCall{
			ID: id, Name: "ls", Arguments: json.RawMessage(`{}`),
		})
	}
	return m
}

func toolResult(id string) message.Message {
	return message.Message{Role: message.RoleTool, ToolCallID: id, Name: "ls", Content: "ok"}
}

// pairedness 检查转录里每个 tool_call 都有对应结果 —— 这是供应商接受请求的前提。
func pairedness(t *testing.T, msgs []message.Message) {
	t.Helper()

	for _, span := range message.ToolTransactionSpans(msgs) {
		call := msgs[span.Start]
		got := make(map[string]struct{})
		for i := span.Start + 1; i < span.End; i++ {
			got[msgs[i].ToolCallID] = struct{}{}
		}
		for _, tc := range call.ToolCalls {
			if _, ok := got[tc.ID]; !ok {
				t.Fatalf("tool call %q has no result; the next request would be rejected", tc.ID)
			}
		}
	}
}

// --- DanglingToolCall ---

func TestDanglingToolCall_FillsMissingResults(t *testing.T) {
	t.Parallel()

	h := hist(user("q"), assistantCalls("a", "b"), toolResult("a"))

	st := middleware.NewState(middleware.StateInit{History: h})
	if err := builtin.NewDanglingToolCall().BeforeModel(context.Background(), st); err != nil {
		t.Fatalf("BeforeModel() error = %v", err)
	}

	msgs := h.All()
	pairedness(t, msgs)

	if len(msgs) != 4 {
		t.Fatalf("history = %d messages, want 4 after repair", len(msgs))
	}
	// 占位结果必须紧跟在事务内，而不是追加到末尾
	if msgs[3].Role != message.RoleTool || msgs[3].ToolCallID != "b" {
		t.Fatalf("placeholder is misplaced: %+v", msgs[3])
	}
	if !msgs[3].IsError {
		t.Error("the placeholder should be marked as an error so the model knows it did not run")
	}
}

// 占位结果追加到末尾仍然是非法请求：它必须落在对应的事务里。
func TestDanglingToolCall_PlaceholderGoesInsideTheTransaction(t *testing.T) {
	t.Parallel()

	h := hist(
		user("q1"),
		assistantCalls("a"), // 缺结果
		user("q2"),          // 后面还有别的消息
		message.Message{Role: message.RoleAssistant, Content: "answer"},
	)

	st := middleware.NewState(middleware.StateInit{History: h})
	if err := builtin.NewDanglingToolCall().BeforeModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}

	msgs := h.All()
	pairedness(t, msgs)

	if msgs[2].Role != message.RoleTool || msgs[2].ToolCallID != "a" {
		t.Fatalf("placeholder was not inserted right after its call:\n%s", dumpRoles(msgs))
	}
	if msgs[3].Content != "q2" {
		t.Fatalf("insertion disturbed the following messages:\n%s", dumpRoles(msgs))
	}
}

func TestDanglingToolCall_RepairsMultipleTransactions(t *testing.T) {
	t.Parallel()

	h := hist(
		user("q1"),
		assistantCalls("a"), // 缺
		user("q2"),
		assistantCalls("b", "c"),
		toolResult("b"), // 缺 c
	)

	st := middleware.NewState(middleware.StateInit{History: h})
	if err := builtin.NewDanglingToolCall().BeforeModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}

	pairedness(t, h.All())
}

func TestDanglingToolCall_LeavesCompleteTranscriptAlone(t *testing.T) {
	t.Parallel()

	h := hist(user("q"), assistantCalls("a"), toolResult("a"))
	before := h.Len()

	st := middleware.NewState(middleware.StateInit{History: h})
	if err := builtin.NewDanglingToolCall().BeforeModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}

	if h.Len() != before {
		t.Fatalf("history changed from %d to %d messages with nothing to repair", before, h.Len())
	}
}

func TestDanglingToolCall_NoToolCallsAtAll(t *testing.T) {
	t.Parallel()

	h := hist(user("q"), message.Message{Role: message.RoleAssistant, Content: "a"})

	st := middleware.NewState(middleware.StateInit{History: h})
	if err := builtin.NewDanglingToolCall().BeforeModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if h.Len() != 2 {
		t.Fatalf("history = %d messages, want 2", h.Len())
	}
}

func TestDanglingToolCall_NilHistory(t *testing.T) {
	t.Parallel()

	st := middleware.NewState(middleware.StateInit{})
	if err := builtin.NewDanglingToolCall().BeforeModel(context.Background(), st); err != nil {
		t.Fatalf("BeforeModel() with a nil history error = %v", err)
	}
}

// --- SafetyFinishReason ---

func TestSafetyFinishReason_DropsToolCallsOnLength(t *testing.T) {
	t.Parallel()

	h := hist(user("q"))
	call := assistantCalls("a", "b")
	h.Append(call)

	st := middleware.NewState(middleware.StateInit{History: h})
	st.ModelOutput = &model.Response{Message: call, StopReason: model.StopReasonLength}

	if err := builtin.NewSafetyFinishReason().AfterModel(context.Background(), st); err != nil {
		t.Fatalf("AfterModel() error = %v", err)
	}

	if len(st.ModelOutput.Message.ToolCalls) != 0 {
		t.Fatal("truncated tool calls were not dropped")
	}
	if !strings.Contains(st.ModelOutput.Message.Content, "2 tool calls were discarded") {
		t.Errorf("content = %q, want it to explain the drop", st.ModelOutput.Message.Content)
	}
	if !strings.Contains(st.ModelOutput.Message.Content, "Re-issue") {
		t.Errorf("content = %q, want actionable guidance", st.ModelOutput.Message.Content)
	}
}

// 只改 ModelOutput 的话，带残缺 tool_calls 的消息会留在历史里，
// 下一轮请求依然非法。
func TestSafetyFinishReason_AlsoFixesTheTranscript(t *testing.T) {
	t.Parallel()

	h := hist(user("q"))
	call := assistantCalls("a")
	h.Append(call)

	st := middleware.NewState(middleware.StateInit{History: h})
	st.ModelOutput = &model.Response{Message: call, StopReason: model.StopReasonContentFilter}

	if err := builtin.NewSafetyFinishReason().AfterModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}

	for _, m := range h.All() {
		if len(m.ToolCalls) > 0 {
			t.Fatal("the transcript still carries the truncated tool calls")
		}
	}
	pairedness(t, h.All())
}

func TestSafetyFinishReason_LeavesNormalResponsesAlone(t *testing.T) {
	t.Parallel()

	call := assistantCalls("a")
	st := middleware.NewState(middleware.StateInit{History: hist(user("q"), call)})
	st.ModelOutput = &model.Response{Message: call, StopReason: model.StopReasonToolCalls}

	if err := builtin.NewSafetyFinishReason().AfterModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if len(st.ModelOutput.Message.ToolCalls) != 1 {
		t.Fatal("a normal tool-calling response was stripped")
	}
}

func TestSafetyFinishReason_PreservesExistingText(t *testing.T) {
	t.Parallel()

	call := assistantCalls("a")
	call.Content = "let me look that up"

	st := middleware.NewState(middleware.StateInit{History: hist(user("q"), call)})
	st.ModelOutput = &model.Response{Message: call, StopReason: model.StopReasonLength}

	if err := builtin.NewSafetyFinishReason().AfterModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.ModelOutput.Message.Content, "let me look that up") {
		t.Errorf("the model's own text was discarded: %q", st.ModelOutput.Message.Content)
	}
}

func TestSafetyFinishReason_SingularWording(t *testing.T) {
	t.Parallel()

	call := assistantCalls("a")
	st := middleware.NewState(middleware.StateInit{History: hist(user("q"), call)})
	st.ModelOutput = &model.Response{Message: call, StopReason: model.StopReasonLength}

	if err := builtin.NewSafetyFinishReason().AfterModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.ModelOutput.Message.Content, "1 tool call was") {
		t.Errorf("content = %q, want singular wording", st.ModelOutput.Message.Content)
	}
}

func TestSafetyFinishReason_CustomReasons(t *testing.T) {
	t.Parallel()

	call := assistantCalls("a")
	st := middleware.NewState(middleware.StateInit{History: hist(user("q"), call)})
	st.ModelOutput = &model.Response{Message: call, StopReason: "refusal"}

	if err := builtin.NewSafetyFinishReason("refusal").AfterModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if len(st.ModelOutput.Message.ToolCalls) != 0 {
		t.Fatal("a custom stop reason was not honoured")
	}
}

func TestSafetyFinishReason_NilOutput(t *testing.T) {
	t.Parallel()

	st := middleware.NewState(middleware.StateInit{History: message.NewHistory()})
	if err := builtin.NewSafetyFinishReason().AfterModel(context.Background(), st); err != nil {
		t.Fatalf("AfterModel() with no output error = %v", err)
	}
}

// --- LoopDetection ---

func repeatedCall(n int) []message.Message {
	var msgs []message.Message
	msgs = append(msgs, user("q"))
	for i := range n {
		m := message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{
			{ID: "c" + strings.Repeat("x", i), Name: "grep", Arguments: json.RawMessage(`{"p":"foo"}`)},
		}}
		msgs = append(msgs, m, message.Message{
			Role: message.RoleTool, ToolCallID: m.ToolCalls[0].ID, Content: "no match",
		})
	}
	return msgs
}

func TestLoopDetection_BreaksRepeatedIdenticalCall(t *testing.T) {
	t.Parallel()

	msgs := repeatedCall(3)
	h := hist(msgs...)

	st := middleware.NewState(middleware.StateInit{History: h})
	// 最后一条 assistant 就是第三次重复
	last := msgs[len(msgs)-2]
	st.ModelOutput = &model.Response{Message: last, StopReason: model.StopReasonToolCalls}

	ld := builtin.NewLoopDetection(builtin.LoopDetectionOptions{Threshold: 3, Window: 6})
	if err := ld.AfterModel(context.Background(), st); err != nil {
		t.Fatalf("AfterModel() error = %v", err)
	}

	if len(st.ModelOutput.Message.ToolCalls) != 0 {
		t.Fatal("the repeated tool call was not broken")
	}
	if !strings.Contains(st.ModelOutput.Message.Content, "not making progress") {
		t.Errorf("content = %q, want an explanation", st.ModelOutput.Message.Content)
	}
	if !strings.Contains(st.ModelOutput.Message.Content, "grep") {
		t.Errorf("content = %q, want it to name the repeated call", st.ModelOutput.Message.Content)
	}
}

func TestLoopDetection_AllowsRepetitionBelowThreshold(t *testing.T) {
	t.Parallel()

	msgs := repeatedCall(2)
	h := hist(msgs...)

	st := middleware.NewState(middleware.StateInit{History: h})
	st.ModelOutput = &model.Response{Message: msgs[len(msgs)-2], StopReason: model.StopReasonToolCalls}

	ld := builtin.NewLoopDetection(builtin.LoopDetectionOptions{Threshold: 3, Window: 6})
	if err := ld.AfterModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if len(st.ModelOutput.Message.ToolCalls) == 0 {
		t.Fatal("two identical calls were broken; the threshold is three")
	}
}

// 同一个工具用不同参数调用多次是正常的探索行为。
func TestLoopDetection_DifferentArgumentsAreNotALoop(t *testing.T) {
	t.Parallel()

	var msgs []message.Message
	msgs = append(msgs, user("q"))
	for i := range 5 {
		id := "c" + strings.Repeat("x", i)
		m := message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{
			{ID: id, Name: "grep", Arguments: json.RawMessage(`{"p":"` + strings.Repeat("a", i+1) + `"}`)},
		}}
		msgs = append(msgs, m, message.Message{Role: message.RoleTool, ToolCallID: id, Content: "x"})
	}

	st := middleware.NewState(middleware.StateInit{History: hist(msgs...)})
	st.ModelOutput = &model.Response{Message: msgs[len(msgs)-2], StopReason: model.StopReasonToolCalls}

	ld := builtin.NewLoopDetection(builtin.LoopDetectionOptions{Threshold: 3, Window: 10})
	if err := ld.AfterModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if len(st.ModelOutput.Message.ToolCalls) == 0 {
		t.Fatal("distinct arguments were treated as a loop; exploration would be blocked")
	}
}

// 判定必须无状态：中间件实例在服务端被多个会话共享。
func TestLoopDetection_IsStatelessAcrossHistories(t *testing.T) {
	t.Parallel()

	ld := builtin.NewLoopDetection(builtin.LoopDetectionOptions{Threshold: 3, Window: 6})

	// 先让一个会话触发检测
	loopy := repeatedCall(3)
	st1 := middleware.NewState(middleware.StateInit{History: hist(loopy...)})
	st1.ModelOutput = &model.Response{Message: loopy[len(loopy)-2], StopReason: model.StopReasonToolCalls}
	if err := ld.AfterModel(context.Background(), st1); err != nil {
		t.Fatal(err)
	}

	// 另一个干净会话不得被前一个的计数污染
	clean := repeatedCall(1)
	st2 := middleware.NewState(middleware.StateInit{History: hist(clean...)})
	st2.ModelOutput = &model.Response{Message: clean[len(clean)-2], StopReason: model.StopReasonToolCalls}
	if err := ld.AfterModel(context.Background(), st2); err != nil {
		t.Fatal(err)
	}
	if len(st2.ModelOutput.Message.ToolCalls) == 0 {
		t.Fatal("a clean session was broken by another session's counters")
	}
}

func TestLoopDetection_WindowLimitsLookback(t *testing.T) {
	t.Parallel()

	// 三次重复，但中间插入足够多的其他 assistant 轮把它们推出窗口
	var msgs []message.Message
	msgs = append(msgs, user("q"))
	for i := range 3 {
		id := "r" + strings.Repeat("x", i)
		msgs = append(msgs, message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{
			{ID: id, Name: "grep", Arguments: json.RawMessage(`{"p":"foo"}`)},
		}}, message.Message{Role: message.RoleTool, ToolCallID: id, Content: "x"})
	}
	for range 5 {
		msgs = append(msgs, message.Message{Role: message.RoleAssistant, Content: "thinking"})
	}
	last := message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{
		{ID: "final", Name: "grep", Arguments: json.RawMessage(`{"p":"foo"}`)},
	}}
	msgs = append(msgs, last)

	st := middleware.NewState(middleware.StateInit{History: hist(msgs...)})
	st.ModelOutput = &model.Response{Message: last, StopReason: model.StopReasonToolCalls}

	ld := builtin.NewLoopDetection(builtin.LoopDetectionOptions{Threshold: 3, Window: 4})
	if err := ld.AfterModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if len(st.ModelOutput.Message.ToolCalls) == 0 {
		t.Fatal("repetitions outside the window were counted")
	}
}

func TestLoopDetection_GradeIsListener(t *testing.T) {
	t.Parallel()

	ld := builtin.NewLoopDetection(builtin.LoopDetectionOptions{})
	if got := ld.Grade(); got != middleware.GradeListener {
		t.Fatalf("Grade() = %v, want GradeListener", got)
	}
}

func TestLoopDetection_NilOutput(t *testing.T) {
	t.Parallel()

	ld := builtin.NewLoopDetection(builtin.LoopDetectionOptions{})
	st := middleware.NewState(middleware.StateInit{History: message.NewHistory()})
	if err := ld.AfterModel(context.Background(), st); err != nil {
		t.Fatalf("AfterModel() with no output error = %v", err)
	}
}

func dumpRoles(msgs []message.Message) string {
	var sb strings.Builder
	for i, m := range msgs {
		sb.WriteString(strings.TrimSpace(string(m.Role)))
		if len(m.ToolCalls) > 0 {
			sb.WriteString("+calls")
		}
		if m.ToolCallID != "" {
			sb.WriteString("(" + m.ToolCallID + ")")
		}
		if i < len(msgs)-1 {
			sb.WriteString(" ")
		}
	}
	return sb.String()
}
