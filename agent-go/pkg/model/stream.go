package model

// StreamEventType 是流式事件类型。
type StreamEventType string

const (
	StreamStart         StreamEventType = "start"
	StreamTextDelta     StreamEventType = "text_delta"
	StreamThinkingDelta StreamEventType = "thinking_delta"
	StreamToolCallDelta StreamEventType = "toolcall_delta"
	StreamDone          StreamEventType = "done"
	StreamError         StreamEventType = "error"
)

// StreamEvent 是流式响应中的一个事件。
type StreamEvent struct {
	Type StreamEventType

	// Delta 是本次增量的文本。仅 TextDelta / ThinkingDelta 有值。
	Delta string

	// ToolCallIndex 与 ToolCallDelta 用于增量拼装工具调用。
	ToolCallIndex  int
	ToolCallID     string
	ToolCallName   string
	ArgumentsDelta string

	Err error
}

// StreamReader 逐个产出流式事件。
//
// 契约：Next 返回 false 后必须调用 Result 取最终响应；无论是否读完，
// 调用方都必须 Close。实现不得在 Next 中 panic —— 错误编码为
// StreamError 事件，或由 Result 返回。
type StreamReader interface {
	// Next 返回下一个事件。第二个返回值为 false 表示流已结束。
	Next() (StreamEvent, bool)

	// Result 返回最终聚合的响应。可在流结束后调用。
	Result() (*Response, error)

	// Close 释放底层连接。可重复调用。
	Close() error
}
