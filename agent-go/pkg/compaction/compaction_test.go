package compaction_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/compaction"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
)

func msg(role message.Role, content string) message.Message {
	return message.Message{Role: role, Content: content}
}

func toolCallMsg(ids ...string) message.Message {
	m := message.Message{Role: message.RoleAssistant}
	for _, id := range ids {
		m.ToolCalls = append(m.ToolCalls, message.ToolCall{ID: id, Name: "ls"})
	}
	return m
}

func toolResultMsg(id string) message.Message {
	return message.Message{Role: message.RoleTool, ToolCallID: id, Content: "result " + id}
}

// --- FindCutPoint ---

func TestFindCutPoint_KeepsTail(t *testing.T) {
	t.Parallel()

	snapshot := []message.Message{
		msg(message.RoleUser, "1"), msg(message.RoleAssistant, "2"),
		msg(message.RoleUser, "3"), msg(message.RoleAssistant, "4"),
		msg(message.RoleUser, "5"), msg(message.RoleAssistant, "6"),
	}

	if got := compaction.FindCutPoint(snapshot, 2); got != 4 {
		t.Fatalf("FindCutPoint() = %d, want 4", got)
	}
}

func TestFindCutPoint_NothingToCompact(t *testing.T) {
	t.Parallel()

	snapshot := []message.Message{msg(message.RoleUser, "1"), msg(message.RoleAssistant, "2")}

	if got := compaction.FindCutPoint(snapshot, 10); got != 0 {
		t.Fatalf("FindCutPoint() = %d, want 0 when the keep window covers everything", got)
	}
}

// 半个工具事务会让下一次模型请求非法：tool_calls 没有对应的 tool 结果。
func TestFindCutPoint_NeverSplitsToolTransaction(t *testing.T) {
	t.Parallel()

	snapshot := []message.Message{
		msg(message.RoleUser, "q"),           // 0
		toolCallMsg("a", "b"),                // 1  事务起点
		toolResultMsg("a"),                   // 2
		toolResultMsg("b"),                   // 3
		msg(message.RoleAssistant, "answer"), // 4
	}

	// keep=2 会把切点落在下标 3，正好切断事务
	got := compaction.FindCutPoint(snapshot, 2)
	if got != 1 {
		t.Fatalf("FindCutPoint() = %d, want 1 (moved back to the transaction start)", got)
	}

	// 验证保留部分是自洽的：要么整个事务都在，要么都不在
	kept := snapshot[got:]
	var calls, results int
	for _, m := range kept {
		if m.StartsToolTransaction() {
			calls += len(m.ToolCalls)
		}
		if m.Role == message.RoleTool {
			results++
		}
	}
	if calls != results {
		t.Fatalf("kept slice has %d tool calls but %d results", calls, results)
	}
}

func TestFindCutPoint_ReturnsZeroWhenTransactionCoversEverything(t *testing.T) {
	t.Parallel()

	snapshot := []message.Message{
		toolCallMsg("a"), // 0
		toolResultMsg("a"),
		toolResultMsg("b"),
	}

	if got := compaction.FindCutPoint(snapshot, 1); got != 0 {
		t.Fatalf("FindCutPoint() = %d, want 0 when moving back leaves nothing to compact", got)
	}
}

// --- StripToolIO ---

func TestStripToolIO_RemovesToolMessagesAndCallMetadata(t *testing.T) {
	t.Parallel()

	in := []message.Message{
		msg(message.RoleUser, "question"),
		{Role: message.RoleAssistant, Content: "let me check", ToolCalls: []message.ToolCall{{ID: "a", Name: "ls"}},
			ReasoningContent: "thinking hard"},
		toolResultMsg("a"),
		msg(message.RoleAssistant, "answer"),
	}

	got := compaction.StripToolIO(in)

	if len(got) != 3 {
		t.Fatalf("StripToolIO() = %d messages, want 3 (the tool result must be dropped)", len(got))
	}
	if len(got[1].ToolCalls) != 0 {
		t.Error("tool call metadata was not stripped")
	}
	if got[1].ReasoningContent != "" {
		t.Error("reasoning content was not stripped")
	}
	if got[1].Content != "let me check" {
		t.Errorf("assistant text was modified: %q", got[1].Content)
	}
}

// 只有工具调用没有文本的 assistant 轮清空后会变成空消息，
// 摘要模型看到一串空回复无从下手。
func TestStripToolIO_AnnotatesToolOnlyTurns(t *testing.T) {
	t.Parallel()

	got := compaction.StripToolIO([]message.Message{toolCallMsg("a")})

	if len(got) != 1 {
		t.Fatalf("StripToolIO() = %d messages, want 1", len(got))
	}
	if strings.TrimSpace(got[0].Content) == "" {
		t.Fatal("a tool-only assistant turn became an empty message")
	}
}

func TestStripToolIO_DoesNotMutateInput(t *testing.T) {
	t.Parallel()

	in := []message.Message{
		{Role: message.RoleAssistant, Content: "x", ToolCalls: []message.ToolCall{{ID: "a", Name: "ls"}}},
	}
	compaction.StripToolIO(in)

	if len(in[0].ToolCalls) != 1 {
		t.Fatal("StripToolIO mutated its input")
	}
}

// --- MaybeCompact ---

func TestNew_RequiresSummariser(t *testing.T) {
	t.Parallel()

	if _, err := compaction.New(compaction.Config{TriggerTokens: 100}, nil); err == nil {
		t.Fatal("New() accepted a nil summariser; it would silently never compact")
	}
}

func TestMaybeCompact_BelowThresholdDoesNothing(t *testing.T) {
	t.Parallel()

	s := &fakeSummariser{out: "summary"}
	c := mustCompactor(t, compaction.Config{TriggerTokens: 1_000_000, KeepMessages: 2}, s)

	h := historyWith(20)
	compacted, err := c.MaybeCompact(context.Background(), h)
	if err != nil {
		t.Fatalf("MaybeCompact() error = %v", err)
	}
	if compacted {
		t.Fatal("compacted below the threshold")
	}
	if s.calls != 0 {
		t.Fatalf("summariser called %d times below the threshold", s.calls)
	}
}

func TestMaybeCompact_DisabledWhenTriggerIsZero(t *testing.T) {
	t.Parallel()

	s := &fakeSummariser{out: "summary"}
	c := mustCompactor(t, compaction.Config{KeepMessages: 2}, s)

	compacted, err := c.MaybeCompact(context.Background(), historyWith(500))
	if err != nil {
		t.Fatal(err)
	}
	if compacted {
		t.Fatal("compacted with TriggerTokens 0, which must disable compaction")
	}
}

func TestMaybeCompact_ReplacesPrefixWithSummary(t *testing.T) {
	t.Parallel()

	s := &fakeSummariser{out: "the user asked about X"}
	c := mustCompactor(t, compaction.Config{TriggerTokens: 1, KeepMessages: 4}, s)

	h := historyWith(20)
	before := h.Len()

	compacted, err := c.MaybeCompact(context.Background(), h)
	if err != nil {
		t.Fatalf("MaybeCompact() error = %v", err)
	}
	if !compacted {
		t.Fatal("MaybeCompact() = false, want true above the threshold")
	}

	msgs := h.All()
	if len(msgs) >= before {
		t.Fatalf("transcript still has %d messages (was %d)", len(msgs), before)
	}
	if msgs[0].Role != message.RoleSystem {
		t.Fatalf("first message role = %q, want system", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "the user asked about X") {
		t.Errorf("summary content = %q", msgs[0].Content)
	}
	if !strings.HasPrefix(msgs[0].Content, "## Summary") {
		t.Errorf("summary must carry the standard heading: %q", msgs[0].Content)
	}
	if len(msgs) != 5 { // 1 摘要 + 4 保留
		t.Fatalf("transcript = %d messages, want 5", len(msgs))
	}
}

// 水位必须存活：压缩把转录缩短了，但"本回合新增"的计算不能受影响。
func TestMaybeCompact_PreservesWatermarkSemantics(t *testing.T) {
	t.Parallel()

	s := &fakeSummariser{out: "summary"}
	c := mustCompactor(t, compaction.Config{TriggerTokens: 1, KeepMessages: 2}, s)

	h := historyWith(20)
	wm := h.Watermark()
	h.Append(msg(message.RoleUser, "the new question"))

	if _, err := c.MaybeCompact(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	h.Append(msg(message.RoleAssistant, "the new answer"))

	got := h.Since(wm)
	var found int
	for _, m := range got {
		if strings.HasPrefix(m.Content, "the new ") {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("Since(watermark) = %d messages, missing this turn's exchange after compaction", len(got))
	}
}

// 摘要模型不可用不该让长会话彻底不可用。
func TestMaybeCompact_FallsBackToPlaceholderSummary(t *testing.T) {
	t.Parallel()

	s := &fakeSummariser{err: errors.New("fast model unreachable")}
	c := mustCompactor(t, compaction.Config{TriggerTokens: 1, KeepMessages: 2}, s)

	h := historyWith(20)
	compacted, err := c.MaybeCompact(context.Background(), h)
	if err != nil {
		t.Fatalf("MaybeCompact() error = %v; a summariser failure must degrade, not fail", err)
	}
	if !compacted {
		t.Fatal("MaybeCompact() = false; the placeholder summary should still compact")
	}

	summary := h.All()[0].Content
	if !strings.Contains(summary, "summariser was unavailable") {
		t.Errorf("placeholder must state what happened: %q", summary)
	}
	// 占位摘要要给出结构信息，让模型知道这里有过对话
	if !strings.Contains(summary, "user messages") {
		t.Errorf("placeholder should report the message counts: %q", summary)
	}
}

// 取消是外部意志，不该被占位摘要掩盖成"压缩成功"。
func TestMaybeCompact_CancellationIsNotMasked(t *testing.T) {
	t.Parallel()

	s := &fakeSummariser{err: context.Canceled}
	c := mustCompactor(t, compaction.Config{TriggerTokens: 1, KeepMessages: 2}, s)

	_, err := c.MaybeCompact(context.Background(), historyWith(20))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("MaybeCompact() error = %v, want context.Canceled", err)
	}
}

func TestMaybeCompact_EmptySummaryDoesNotReplace(t *testing.T) {
	t.Parallel()

	s := &fakeSummariser{out: "   "}
	c := mustCompactor(t, compaction.Config{TriggerTokens: 1, KeepMessages: 2}, s)

	h := historyWith(20)
	before := h.Len()

	compacted, err := c.MaybeCompact(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	if compacted {
		t.Fatal("an empty summary must not replace the transcript")
	}
	if h.Len() != before {
		t.Fatalf("transcript changed despite an empty summary: %d -> %d", before, h.Len())
	}
}

// 摘要输入不封顶会出现"压缩比不压缩更贵"。
func TestMaybeCompact_CapsSummaryInputByMessageCount(t *testing.T) {
	t.Parallel()

	s := &fakeSummariser{out: "summary"}
	c := mustCompactor(t, compaction.Config{
		TriggerTokens:    1,
		KeepMessages:     2,
		MaxInputMessages: 5,
	}, s)

	if _, err := c.MaybeCompact(context.Background(), historyWith(100)); err != nil {
		t.Fatal(err)
	}
	if got := len(s.lastInput); got > 5 {
		t.Fatalf("summariser received %d messages, want <= 5", got)
	}
}

func TestMaybeCompact_CapsSummaryInputByChars(t *testing.T) {
	t.Parallel()

	s := &fakeSummariser{out: "summary"}
	c := mustCompactor(t, compaction.Config{
		TriggerTokens: 1,
		KeepMessages:  2,
		MaxInputChars: 500,
	}, s)

	h := message.NewHistory()
	for i := range 50 {
		h.Append(msg(message.RoleUser, strings.Repeat("x", 200)+fmt.Sprint(i)))
	}

	if _, err := c.MaybeCompact(context.Background(), h); err != nil {
		t.Fatal(err)
	}

	var chars int
	for _, m := range s.lastInput {
		chars += len(m.Content)
	}
	if chars > 900 { // 500 上限 + 最后一条可能整条保留
		t.Fatalf("summariser received %d chars, want it capped near 500", chars)
	}
}

// 摘要输入必须已剔除工具 IO：工具 JSON 占体积却几乎不含需长期记住的信息。
func TestMaybeCompact_SummariserSeesStrippedInput(t *testing.T) {
	t.Parallel()

	s := &fakeSummariser{out: "summary"}
	c := mustCompactor(t, compaction.Config{TriggerTokens: 1, KeepMessages: 1}, s)

	h := message.NewHistory()
	for range 5 {
		h.Append(msg(message.RoleUser, "q"))
		h.Append(toolCallMsg("a"))
		h.Append(toolResultMsg("a"))
		h.Append(msg(message.RoleAssistant, "answer"))
	}

	if _, err := c.MaybeCompact(context.Background(), h); err != nil {
		t.Fatal(err)
	}

	for _, m := range s.lastInput {
		if m.Role == message.RoleTool {
			t.Fatal("summariser input still contains tool messages")
		}
		if len(m.ToolCalls) > 0 {
			t.Fatal("summariser input still contains tool call metadata")
		}
	}
}

// 上一轮的摘要必须参与新摘要，否则跨压缩会丢掉更早的历史。
func TestMaybeCompact_ChainsPreviousSummary(t *testing.T) {
	t.Parallel()

	s := &fakeSummariser{out: "round two"}
	c := mustCompactor(t, compaction.Config{TriggerTokens: 1, KeepMessages: 2}, s)

	h := message.NewHistory()
	h.Append(message.Message{Role: message.RoleSystem, Content: "## Summary\n\nround one facts"})
	for range 10 {
		h.Append(msg(message.RoleUser, "q"))
		h.Append(msg(message.RoleAssistant, "a"))
	}

	if _, err := c.MaybeCompact(context.Background(), h); err != nil {
		t.Fatal(err)
	}

	var sawPrevious bool
	for _, m := range s.lastInput {
		if strings.Contains(m.Content, "round one facts") {
			sawPrevious = true
		}
	}
	if !sawPrevious {
		t.Fatal("the previous summary was not fed into the new one; earlier history would be lost")
	}
}

// 并发压缩会各自基于同一份快照重写转录，后写的覆盖前一次并丢消息。
func TestMaybeCompact_IsSerialised(t *testing.T) {
	t.Parallel()

	s := &fakeSummariser{out: "summary"}
	c := mustCompactor(t, compaction.Config{TriggerTokens: 1, KeepMessages: 2}, s)
	h := historyWith(40)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.MaybeCompact(context.Background(), h); err != nil {
				t.Errorf("MaybeCompact() error = %v", err)
			}
		}()
	}
	wg.Wait()

	// 转录必须仍然自洽：不得出现连续两条摘要之外的结构损坏
	if h.Len() == 0 {
		t.Fatal("concurrent compaction emptied the transcript")
	}
}

func TestMaybeCompact_NilHistory(t *testing.T) {
	t.Parallel()

	c := mustCompactor(t, compaction.Config{TriggerTokens: 1}, &fakeSummariser{out: "x"})
	if _, err := c.MaybeCompact(context.Background(), nil); err != nil {
		t.Fatalf("MaybeCompact(nil) error = %v", err)
	}
}

// --- ModelSummariser ---

func TestModelSummariser_UsesTheModel(t *testing.T) {
	t.Parallel()

	fx := faux.New(faux.Text("compressed"))
	s := compaction.NewModelSummariser(fx)

	got, err := s.Summarise(context.Background(), []message.Message{msg(message.RoleUser, "hello")}, 256)
	if err != nil {
		t.Fatalf("Summarise() error = %v", err)
	}
	if got != "compressed" {
		t.Fatalf("Summarise() = %q", got)
	}

	req, _ := fx.LastRequest()
	if req.MaxTokens != 256 {
		t.Errorf("MaxTokens = %d, want 256", req.MaxTokens)
	}
	if req.System == "" {
		t.Error("the summariser must send a system prompt")
	}
}

func TestModelSummariser_PropagatesModelErrors(t *testing.T) {
	t.Parallel()

	s := compaction.NewModelSummariser(faux.New(faux.Fail(model.ErrProviderUnavailable)))

	if _, err := s.Summarise(context.Background(), nil, 100); !model.IsProviderUnavailable(err) {
		t.Fatalf("Summarise() error = %v, want the provider error", err)
	}
}

func TestModelSummariser_NilModel(t *testing.T) {
	t.Parallel()

	s := compaction.NewModelSummariser(nil)
	if _, err := s.Summarise(context.Background(), nil, 100); err == nil {
		t.Fatal("Summarise() with a nil model succeeded")
	}
}

// --- helpers ---

func mustCompactor(t *testing.T, cfg compaction.Config, s compaction.Summariser) *compaction.Compactor {
	t.Helper()
	c, err := compaction.New(cfg, s)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return c
}

func historyWith(n int) *message.History {
	h := message.NewHistory()
	for i := range n {
		role := message.RoleUser
		if i%2 == 1 {
			role = message.RoleAssistant
		}
		h.Append(msg(role, fmt.Sprintf("message %d with some content to make it weigh something", i)))
	}
	return h
}

type fakeSummariser struct {
	mu        sync.Mutex
	out       string
	err       error
	calls     int
	lastInput []message.Message
}

func (f *fakeSummariser) Summarise(_ context.Context, msgs []message.Message, _ int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastInput = msgs
	return f.out, f.err
}
