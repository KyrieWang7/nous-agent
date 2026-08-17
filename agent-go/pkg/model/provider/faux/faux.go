// Package faux 提供脚本化的确定性 model.Model 实现，供离线测试使用。
//
// 这是硬要求（设计文档 §18）：loop、chain、subagent 的全部测试都靠它，
// 不碰真实 API、不花钱、不需要 key。默认 make check 因此可以完全离线。
//
// 本包只在测试中使用，但放在 pkg 下而非 _test 文件里，
// 因为跨包测试（pkg/loop、pkg/harness、test/）都要引用它。
package faux

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

// ErrScriptExhausted 表示脚本里的回合已用尽而调用方还在继续采样。
// 这通常意味着被测循环比预期多跑了一轮 —— 是有用的失败信号，不要静默补默认回合。
var ErrScriptExhausted = errors.New("faux: script exhausted")

// Turn 是脚本里的一个回合：一次模型调用的预设结果。
type Turn struct {
	content    string
	thinking   string
	toolCalls  []message.ToolCall
	stopReason string
	usage      model.Usage
	err        error
}

// Text 返回一个纯文本回合，StopReason 为 stop。
func Text(content string) Turn {
	return Turn{content: content, stopReason: model.StopReasonStop}
}

// Thinking 返回带思维链的文本回合。
func Thinking(reasoning, content string) Turn {
	return Turn{content: content, thinking: reasoning, stopReason: model.StopReasonStop}
}

// ToolCall 返回请求单个工具的回合，StopReason 为 tool_calls。
func ToolCall(name, argsJSON string) Turn {
	return ToolCalls(Call{Name: name, Args: argsJSON})
}

// Call 描述脚本里的一次工具调用。
type Call struct {
	ID   string
	Name string
	Args string
}

// ToolCalls 返回请求多个工具的回合，用于测试并发分段。
func ToolCalls(calls ...Call) Turn {
	tcs := make([]message.ToolCall, len(calls))
	for i, c := range calls {
		id := c.ID
		if id == "" {
			id = fmt.Sprintf("faux-call-%d", i+1)
		}
		tcs[i] = message.ToolCall{ID: id, Name: c.Name, Arguments: []byte(c.Args)}
	}
	return Turn{toolCalls: tcs, stopReason: model.StopReasonToolCalls}
}

// Fail 返回一个直接返回 err 的回合，用于测试降级与重试。
func Fail(err error) Turn {
	return Turn{err: err, stopReason: model.StopReasonError}
}

// Truncated 返回一个被输出上限截断的回合（StopReason 为 length），
// 用于测试 SafetyFinishReason 与截断工具参数的处理。
func Truncated(content string, calls ...Call) Turn {
	t := ToolCalls(calls...)
	t.content = content
	t.stopReason = model.StopReasonLength
	return t
}

// Filtered 返回一个被供应商安全终止的回合（StopReason 为 content_filter）。
func Filtered(content string, calls ...Call) Turn {
	t := ToolCalls(calls...)
	t.content = content
	t.stopReason = model.StopReasonContentFilter
	return t
}

// WithUsage 设置该回合上报的 token 用量。
func (t Turn) WithUsage(u model.Usage) Turn {
	t.usage = u
	return t
}

// WithStopReason 覆盖该回合的停止原因。
func (t Turn) WithStopReason(reason string) Turn {
	t.stopReason = reason
	return t
}

// Model 是脚本化的 model.Model 实现。并发安全。
type Model struct {
	mu       sync.Mutex
	turns    []Turn
	cursor   int
	requests []model.Request
	callSeq  int

	info      model.Info
	chunkSize int
}

// New 返回按 turns 顺序应答的 Model。
func New(turns ...Turn) *Model {
	return &Model{
		turns: turns,
		info: model.Info{
			Name:            "faux",
			ContextLength:   200_000,
			MaxOutputTokens: 8192,
			SupportsTools:   true,
		},
		chunkSize: 4,
	}
}

// WithInfo 覆盖 Info()，用于测试依赖模型能力的 lifecycle handler（如 ViewImage）。
func (m *Model) WithInfo(info model.Info) *Model {
	m.info = info
	return m
}

// Info 实现 model.Model。
func (m *Model) Info() model.Info { return m.info }

// Requests 返回按调用顺序记录的全部请求，供断言"内核投递了什么"。
func (m *Model) Requests() []model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]model.Request, len(m.requests))
	copy(out, m.requests)
	return out
}

// LastRequest 返回最近一次请求。没有请求时第二个返回值为 false。
func (m *Model) LastRequest() (model.Request, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		return model.Request{}, false
	}
	return m.requests[len(m.requests)-1], true
}

// CallCount 返回已发生的模型调用次数。
func (m *Model) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

// next 取出下一个回合并记录请求。
func (m *Model) next(req model.Request) (Turn, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.requests = append(m.requests, req)

	if m.cursor >= len(m.turns) {
		return Turn{}, "", fmt.Errorf("%w after %d turns", ErrScriptExhausted, len(m.turns))
	}
	t := m.turns[m.cursor]
	m.cursor++
	m.callSeq++

	return t, fmt.Sprintf("faux-%d", m.callSeq), nil
}

// Complete 实现 model.Model。
func (m *Model) Complete(ctx context.Context, req model.Request) (*model.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	turn, callID, err := m.next(req)
	if err != nil {
		return nil, err
	}
	if turn.err != nil {
		return nil, turn.err
	}
	return m.respond(turn, callID), nil
}

func (m *Model) respond(turn Turn, callID string) *model.Response {
	return &model.Response{
		Message: message.Message{
			Role:             message.RoleAssistant,
			Content:          turn.content,
			ReasoningContent: turn.thinking,
			ToolCalls:        cloneToolCalls(turn.toolCalls),
		},
		StopReason: turn.stopReason,
		Usage:      turn.usage,
		CallID:     callID,
		ModelName:  m.info.Name,
	}
}

func cloneToolCalls(tcs []message.ToolCall) []message.ToolCall {
	if tcs == nil {
		return nil
	}
	out := make([]message.ToolCall, len(tcs))
	for i, tc := range tcs {
		out[i] = message.ToolCall{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: append([]byte(nil), tc.Arguments...),
		}
	}
	return out
}

// Stream 实现 model.Model。事件序列为
// start → thinking_delta* → text_delta* → toolcall_delta* → done|error。
func (m *Model) Stream(ctx context.Context, req model.Request) (model.StreamReader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	turn, callID, err := m.next(req)
	if err != nil {
		return nil, err
	}

	return &reader{
		ctx:    ctx,
		events: buildEvents(turn, m.chunkSize),
		final:  m.respond(turn, callID),
		err:    turn.err,
	}, nil
}

func buildEvents(turn Turn, chunk int) []model.StreamEvent {
	events := []model.StreamEvent{{Type: model.StreamStart}}

	for _, part := range split(turn.thinking, chunk) {
		events = append(events, model.StreamEvent{Type: model.StreamThinkingDelta, Delta: part})
	}
	for _, part := range split(turn.content, chunk) {
		events = append(events, model.StreamEvent{Type: model.StreamTextDelta, Delta: part})
	}
	for i, tc := range turn.toolCalls {
		for _, part := range split(string(tc.Arguments), chunk) {
			events = append(events, model.StreamEvent{
				Type:           model.StreamToolCallDelta,
				ToolCallIndex:  i,
				ToolCallID:     tc.ID,
				ToolCallName:   tc.Name,
				ArgumentsDelta: part,
			})
		}
	}

	if turn.err != nil {
		events = append(events, model.StreamEvent{Type: model.StreamError, Err: turn.err})
	} else {
		events = append(events, model.StreamEvent{Type: model.StreamDone})
	}
	return events
}

func split(s string, chunk int) []string {
	if s == "" {
		return nil
	}
	if chunk <= 0 {
		chunk = 4
	}

	var out []string
	runes := []rune(s)
	for i := 0; i < len(runes); i += chunk {
		end := min(i+chunk, len(runes))
		out = append(out, string(runes[i:end]))
	}
	return out
}

// reader 是 faux 的 model.StreamReader 实现。
type reader struct {
	ctx    context.Context
	events []model.StreamEvent
	pos    int
	final  *model.Response
	err    error

	cancelled bool
	closed    bool
}

// Next 实现 model.StreamReader。
func (r *reader) Next() (model.StreamEvent, bool) {
	if r.closed {
		return model.StreamEvent{}, false
	}
	if err := r.ctx.Err(); err != nil {
		// 上下文取消后停止产出。Result 会返回 ctx 错误，
		// 模拟真实供应商在连接中断时的行为。
		r.cancelled = true
		return model.StreamEvent{}, false
	}
	if r.pos >= len(r.events) {
		return model.StreamEvent{}, false
	}

	ev := r.events[r.pos]
	r.pos++
	return ev, true
}

// Result 实现 model.StreamReader。
func (r *reader) Result() (*model.Response, error) {
	if r.cancelled {
		return nil, r.ctx.Err()
	}
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	if r.err != nil {
		return nil, r.err
	}
	return r.final, nil
}

// Close 实现 model.StreamReader。可重复调用。
func (r *reader) Close() error {
	r.closed = true
	return nil
}
