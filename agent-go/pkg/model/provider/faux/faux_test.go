package faux_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
)

func TestComplete_ReturnsScriptedTurnsInOrder(t *testing.T) {
	t.Parallel()

	m := faux.New(
		faux.Text("first"),
		faux.Text("second"),
	)

	ctx := context.Background()
	first, err := m.Complete(ctx, model.Request{})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if first.Message.Content != "first" {
		t.Fatalf("first content = %q, want %q", first.Message.Content, "first")
	}

	second, err := m.Complete(ctx, model.Request{})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if second.Message.Content != "second" {
		t.Fatalf("second content = %q, want %q", second.Message.Content, "second")
	}
}

func TestComplete_ExhaustedScriptIsAnError(t *testing.T) {
	t.Parallel()

	m := faux.New(faux.Text("only"))
	ctx := context.Background()

	if _, err := m.Complete(ctx, model.Request{}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	_, err := m.Complete(ctx, model.Request{})
	if !errors.Is(err, faux.ErrScriptExhausted) {
		t.Fatalf("Complete() after exhaustion error = %v, want ErrScriptExhausted", err)
	}
}

func TestComplete_AssignsUniqueCallIDs(t *testing.T) {
	t.Parallel()

	m := faux.New(faux.Text("a"), faux.Text("b"))
	ctx := context.Background()

	first, _ := m.Complete(ctx, model.Request{})
	second, _ := m.Complete(ctx, model.Request{})

	if first.CallID == "" || second.CallID == "" {
		t.Fatal("CallID must be set; usage attribution dedupes on it")
	}
	if first.CallID == second.CallID {
		t.Fatalf("CallIDs collide: %q", first.CallID)
	}
}

func TestComplete_RecordsRequests(t *testing.T) {
	t.Parallel()

	m := faux.New(faux.Text("ok"))
	req := model.Request{
		System:   "you are a test",
		Messages: []message.Message{{Role: message.RoleUser, Content: "hi"}},
		Tools:    []model.ToolSchema{{Name: "ls"}},
	}

	if _, err := m.Complete(context.Background(), req); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	got := m.Requests()
	if len(got) != 1 {
		t.Fatalf("Requests() = %d, want 1", len(got))
	}
	if got[0].System != "you are a test" {
		t.Errorf("recorded System = %q", got[0].System)
	}
	if len(got[0].Tools) != 1 || got[0].Tools[0].Name != "ls" {
		t.Errorf("recorded Tools = %+v", got[0].Tools)
	}
}

func TestToolCall_TurnCarriesToolCallsAndStopReason(t *testing.T) {
	t.Parallel()

	m := faux.New(faux.ToolCall("ls", `{"path":"/"}`))

	resp, err := m.Complete(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if resp.StopReason != model.StopReasonToolCalls {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, model.StopReasonToolCalls)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v, want 1", resp.Message.ToolCalls)
	}
	tc := resp.Message.ToolCalls[0]
	if tc.Name != "ls" || string(tc.Arguments) != `{"path":"/"}` {
		t.Errorf("tool call = %+v", tc)
	}
	if tc.ID == "" {
		t.Error("tool call ID must be set")
	}
}

func TestFail_TurnReturnsClassifiedError(t *testing.T) {
	t.Parallel()

	m := faux.New(
		faux.Fail(model.ErrRateLimited),
		faux.Text("recovered"),
	)
	ctx := context.Background()

	_, err := m.Complete(ctx, model.Request{})
	if !model.IsRateLimited(err) {
		t.Fatalf("Complete() error = %v, want rate-limited", err)
	}

	// 失败的一轮已消耗，下一次调用取下一轮
	resp, err := m.Complete(ctx, model.Request{})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if resp.Message.Content != "recovered" {
		t.Fatalf("content = %q, want %q", resp.Message.Content, "recovered")
	}
}

func TestStream_EmitsDeltasThatConcatenateToTheFullContent(t *testing.T) {
	t.Parallel()

	m := faux.New(faux.Text("hello world"))

	r, err := m.Stream(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() { _ = r.Close() }()

	var sb strings.Builder
	var sawStart, sawDone bool
	for {
		ev, ok := r.Next()
		if !ok {
			break
		}
		switch ev.Type {
		case model.StreamStart:
			sawStart = true
		case model.StreamTextDelta:
			sb.WriteString(ev.Delta)
		case model.StreamDone:
			sawDone = true
		}
	}

	if !sawStart || !sawDone {
		t.Errorf("sawStart = %v, sawDone = %v; both required", sawStart, sawDone)
	}
	if sb.String() != "hello world" {
		t.Fatalf("concatenated deltas = %q, want %q", sb.String(), "hello world")
	}
}

func TestStream_ResultMatchesComplete(t *testing.T) {
	t.Parallel()

	turn := faux.Text("same answer")

	streaming := faux.New(turn)
	r, err := streaming.Stream(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() { _ = r.Close() }()
	for {
		if _, ok := r.Next(); !ok {
			break
		}
	}
	streamed, err := r.Result()
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}

	blocking := faux.New(turn)
	completed, err := blocking.Complete(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if streamed.Message.Content != completed.Message.Content {
		t.Fatalf("stream content %q != complete content %q",
			streamed.Message.Content, completed.Message.Content)
	}
	if streamed.StopReason != completed.StopReason {
		t.Fatalf("stream stop %q != complete stop %q", streamed.StopReason, completed.StopReason)
	}
}

func TestStream_ToolCallDeltasAssembleIntoToolCalls(t *testing.T) {
	t.Parallel()

	m := faux.New(faux.ToolCall("grep", `{"pattern":"x"}`))

	r, err := m.Stream(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() { _ = r.Close() }()

	var args strings.Builder
	var sawToolDelta bool
	for {
		ev, ok := r.Next()
		if !ok {
			break
		}
		if ev.Type == model.StreamToolCallDelta {
			sawToolDelta = true
			args.WriteString(ev.ArgumentsDelta)
		}
	}
	if !sawToolDelta {
		t.Fatal("no toolcall_delta events emitted")
	}
	if args.String() != `{"pattern":"x"}` {
		t.Fatalf("assembled args = %q", args.String())
	}

	resp, err := r.Result()
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("Result ToolCalls = %+v, want 1", resp.Message.ToolCalls)
	}
}

func TestStream_ErrorTurnSurfacesAsEventAndResult(t *testing.T) {
	t.Parallel()

	m := faux.New(faux.Fail(model.ErrProviderUnavailable))

	r, err := m.Stream(context.Background(), model.Request{})
	if err != nil {
		// 允许实现选择在 Stream 就返回错误
		if !model.IsProviderUnavailable(err) {
			t.Fatalf("Stream() error = %v, want provider-unavailable", err)
		}
		return
	}
	defer func() { _ = r.Close() }()

	var sawError bool
	for {
		ev, ok := r.Next()
		if !ok {
			break
		}
		if ev.Type == model.StreamError {
			sawError = true
			if !model.IsProviderUnavailable(ev.Err) {
				t.Errorf("event error = %v, want provider-unavailable", ev.Err)
			}
		}
	}
	if !sawError {
		t.Error("no error event emitted")
	}
	if _, err := r.Result(); !model.IsProviderUnavailable(err) {
		t.Errorf("Result() error = %v, want provider-unavailable", err)
	}
}

func TestStream_CloseIsIdempotent(t *testing.T) {
	t.Parallel()

	m := faux.New(faux.Text("x"))
	r, err := m.Stream(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestStream_RespectsContextCancellation(t *testing.T) {
	t.Parallel()

	m := faux.New(faux.Text("a long answer with many chunks"))
	ctx, cancel := context.WithCancel(context.Background())

	r, err := m.Stream(ctx, model.Request{})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() { _ = r.Close() }()

	// 读一个事件后取消，剩余事件必须停止
	if _, ok := r.Next(); !ok {
		t.Fatal("expected at least one event")
	}
	cancel()

	for {
		if _, ok := r.Next(); !ok {
			break
		}
	}
	if _, err := r.Result(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Result() error = %v, want context.Canceled", err)
	}
}

func TestInfo_IsConfigurable(t *testing.T) {
	t.Parallel()

	m := faux.New(faux.Text("x")).WithInfo(model.Info{
		Name:           "faux-vision",
		ContextLength:  1000,
		SupportsVision: true,
	})

	got := m.Info()
	if got.Name != "faux-vision" || !got.SupportsVision || got.ContextLength != 1000 {
		t.Fatalf("Info() = %+v", got)
	}
}

func TestUsage_IsReportedPerTurn(t *testing.T) {
	t.Parallel()

	m := faux.New(faux.Text("x").WithUsage(model.Usage{InputTokens: 11, OutputTokens: 7}))

	resp, err := m.Complete(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 7 {
		t.Fatalf("Usage = %+v", resp.Usage)
	}
}

func TestConcurrentComplete_IsSafe(t *testing.T) {
	t.Parallel()

	turns := make([]faux.Turn, 50)
	for i := range turns {
		turns[i] = faux.Text("x")
	}
	m := faux.New(turns...)

	done := make(chan struct{})
	for range 50 {
		go func() {
			defer func() { done <- struct{}{} }()
			_, _ = m.Complete(context.Background(), model.Request{})
		}()
	}
	for range 50 {
		<-done
	}

	if got := len(m.Requests()); got != 50 {
		t.Fatalf("Requests() = %d, want 50", got)
	}
}

func TestJSONArgumentsAreValid(t *testing.T) {
	t.Parallel()

	m := faux.New(faux.ToolCall("ls", `{"path":"/tmp"}`))
	resp, _ := m.Complete(context.Background(), model.Request{})

	var v map[string]any
	if err := json.Unmarshal(resp.Message.ToolCalls[0].Arguments, &v); err != nil {
		t.Fatalf("tool call arguments are not valid JSON: %v", err)
	}
}
