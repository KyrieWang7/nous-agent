// Package message 定义对话消息类型与转录（History）。
//
// 转录是 agent 回合的唯一状态（设计文档 §3.3）。本包不含任何编排逻辑，
// 也不依赖 pkg/loop —— 依赖纪律由 make layer-check 断言。
package message

import "encoding/json"

// Role 是消息角色。取值与 OpenAI chat completions 对齐，便于供应商适配。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall 是模型请求的一次工具调用。
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ContentBlock 承载多模态内容。Type 取 text / image / thinking。
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Data     string `json:"data,omitempty"`
}

// Message 是转录中的一条消息。
//
// Content 与 ContentBlocks 并存：纯文本走 Content（绝大多数情况），
// 多模态走 ContentBlocks。供应商适配层负责二者的合并投递。
type Message struct {
	Role             Role           `json:"role"`
	Content          string         `json:"content,omitempty"`
	ContentBlocks    []ContentBlock `json:"content_blocks,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	Name             string         `json:"name,omitempty"`
	IsError          bool           `json:"is_error,omitempty"`
	// AdditionalKwargs carries provider/UI metadata which must survive the
	// transcript round trip.  The subagent tool uses this for its terminal
	// status contract; other adapters may add orthogonal keys.
	AdditionalKwargs map[string]any `json:"additional_kwargs,omitempty"`
}

// Clone 返回 m 的深拷贝。
//
// 深拷贝是必需的：压缩（History.Replace）与护栏（ReplaceLastAssistant）会改写转录，
// 若共享底层数组，改写会波及调用方持有的快照。nil slice 保持 nil，
// 以免在序列化时把 nil 变成 []。
func Clone(m Message) Message {
	out := m

	if m.ToolCalls != nil {
		out.ToolCalls = make([]ToolCall, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			out.ToolCalls[i] = ToolCall{ID: tc.ID, Name: tc.Name}
			if tc.Arguments != nil {
				out.ToolCalls[i].Arguments = append(json.RawMessage(nil), tc.Arguments...)
			}
		}
	}

	if m.ContentBlocks != nil {
		out.ContentBlocks = make([]ContentBlock, len(m.ContentBlocks))
		copy(out.ContentBlocks, m.ContentBlocks)
	}
	if m.AdditionalKwargs != nil {
		out.AdditionalKwargs = cloneAdditionalKwargs(m.AdditionalKwargs)
	}

	return out
}

func cloneAdditionalKwargs(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		switch nested := value.(type) {
		case map[string]any:
			out[key] = cloneAdditionalKwargs(nested)
		case []any:
			items := make([]any, len(nested))
			for i, item := range nested {
				if m, ok := item.(map[string]any); ok {
					items[i] = cloneAdditionalKwargs(m)
				} else {
					items[i] = item
				}
			}
			out[key] = items
		default:
			out[key] = value
		}
	}
	return out
}

// CloneAll 返回 msgs 的深拷贝切片。
func CloneAll(msgs []Message) []Message {
	if msgs == nil {
		return nil
	}
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		out[i] = Clone(m)
	}
	return out
}

// StartsToolTransaction 报告 m 是否开启一个工具事务，
// 即 m 是带 tool_calls 的 assistant 消息。
func (m Message) StartsToolTransaction() bool {
	return m.Role == RoleAssistant && len(m.ToolCalls) > 0
}

// Span 是转录中的半开区间 [Start, End)。
type Span struct {
	Start int
	End   int
}

// ToolTransactionSpans 返回转录中所有工具事务的区间。
//
// 一个事务从带 tool_calls 的 assistant 消息起，延伸到紧随其后的连续 tool 消息止。
// 压缩与裁剪都必须避免把切点落在事务中间 —— 半个事务会让下一次模型请求非法
// （tool_calls 没有对应的 tool 结果）。参见设计文档 §6.2。
func ToolTransactionSpans(msgs []Message) []Span {
	if len(msgs) == 0 {
		return nil
	}

	var spans []Span
	for i := 0; i < len(msgs); i++ {
		if !msgs[i].StartsToolTransaction() {
			continue
		}
		end := i + 1
		for end < len(msgs) && msgs[end].Role == RoleTool {
			end++
		}
		spans = append(spans, Span{Start: i, End: end})
		i = end - 1
	}
	return spans
}
