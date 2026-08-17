// Package runtime 承载 Run 生命周期与事件通路。
//
// 内核只 Publish，不感知订阅者（设计文档 §2 依赖纪律、§12）。这一层解决的
// 不是"agent 怎么思考"，而是"一次 run 如何被观察、恢复、取消与计费"。
package runtime

import (
	"encoding/json"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
)

// EventType 是事件类型。与设计文档 §12.4 的清单一一对应。
type EventType string

const (
	EventRunStart           EventType = "run_start"
	EventRunStateChanged    EventType = "run_state_changed"
	EventBudgetChanged      EventType = "budget_changed"
	EventPlanModeChanged    EventType = "plan_mode_changed"
	EventStateValues        EventType = "state_values"
	EventMessage            EventType = "message"
	EventCustom             EventType = "custom"
	EventMessageStart       EventType = "message_start"
	EventContentDelta       EventType = "content_delta"
	EventMessageReplace     EventType = "message_replace"
	EventReasoningDelta     EventType = "reasoning_delta"
	EventToolStart          EventType = "tool_start"
	EventToolResult         EventType = "tool_result"
	EventApprovalRequested  EventType = "approval_requested"
	EventApprovalResolved   EventType = "approval_resolved"
	EventQuestionRequested  EventType = "question_requested"
	EventQuestionResolved   EventType = "question_resolved"
	EventCompactionStart    EventType = "compaction_started"
	EventCompactionComplete EventType = "compaction_completed"
	EventSkillActivated     EventType = "skill_activated"
	EventSubagentStart      EventType = "subagent_start"
	EventSubagentResult     EventType = "subagent_result"
	// EventSubagentProgress carries a child model's in-flight AI message. It is
	// projected to the frontend's task_running custom event, never to the
	// parent's messages stream.
	EventSubagentProgress EventType = "subagent_progress"
	EventGuardrailBlock   EventType = "guardrail_blocked"
	// Transcript events are canonical persistence events. Wire/UI events remain
	// separate projections and may be dropped or reshaped without changing the
	// transcript that a resumed agent sees.
	EventTranscriptAppend  EventType = "transcript_append"
	EventTranscriptReplace EventType = "transcript_replace"
	EventUsage             EventType = "usage"
	EventMessageStop       EventType = "message_stop"
	EventRunEnd            EventType = "run_end"
	EventError             EventType = "error"
)

// Category 决定事件在存储压力下的取舍。
type Category string

const (
	// CategoryTrace 是可丢弃的观测事件（逐 token 的 delta、工具进度）。
	CategoryTrace Category = "trace"

	// CategoryUsage 是用量与计费事件。
	CategoryUsage Category = "usage"

	// CategoryAudit 是不可丢弃的事件。
	//
	// message_replace 属于此类：它是流式的必要补丁 —— 已经流出的 token
	// 无法收回，护栏改写回复后必须让客户端整段替换。丢掉它等于把被拦截的
	// 文本留在用户屏幕上（设计文档 §12.4）。
	CategoryAudit Category = "audit"
)

// categoryOf 返回事件类型的默认分类。
func categoryOf(t EventType) Category {
	switch t {
	case EventMessageReplace, EventGuardrailBlock, EventTranscriptAppend, EventTranscriptReplace, EventToolStart, EventToolResult, EventSubagentStart, EventSubagentResult,
		EventApprovalRequested, EventApprovalResolved, EventQuestionRequested, EventQuestionResolved, EventCompactionStart, EventCompactionComplete,
		EventRunEnd, EventError, EventRunStart, EventRunStateChanged, EventBudgetChanged, EventPlanModeChanged, EventStateValues, EventMessage:
		return CategoryAudit
	case EventUsage:
		return CategoryUsage
	default:
		return CategoryTrace
	}
}

// Event 是事件流中的一条事件。
type Event struct {
	// Seq 是该 run 内单调递增的序号，从 1 起。
	//
	// 它同时是 Last-Event-ID 的游标与 Redis Stream 的 entry ID。用 harness
	// 序号而不是 Redis 自动 ID，才能让 Last-Event-ID 成为真正的 seek ——
	// 从流头 XRANGE 再按游标过滤的实现，游标一旦越过第一页就永远返回空
	// （设计文档 §12.2）。
	Seq int64 `json:"seq"`

	RunID    string    `json:"run_id"`
	ThreadID string    `json:"thread_id,omitempty"`
	Type     EventType `json:"type"`
	Category Category  `json:"category"`
	// IdempotencyKey links retries and projections to one logical fact.
	// Persistence implementations may use it to collapse duplicate writes.
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	// Data 是事件负载。
	Data json.RawMessage `json:"data,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

// Droppable 报告该事件在缓冲压力下是否可被丢弃。
func (e Event) Droppable() bool {
	return e.Category == CategoryTrace
}

// NewEvent 构造一条事件，Seq 由 EventBus 在发布时赋值。
func NewEvent(runID, threadID string, t EventType, data any) (Event, error) {
	e := Event{
		RunID:     runID,
		ThreadID:  threadID,
		Type:      t,
		Category:  categoryOf(t),
		CreatedAt: time.Now().UTC(),
	}

	if data == nil {
		return e, nil
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return Event{}, err
	}
	e.Data = payload
	return e, nil
}

// MustEvent 是 NewEvent 的便捷版本，序列化失败时把错误写进负载。
//
// 事件发布不该因为一个负载序列化失败就中断回合 —— 那是观测通路的问题，
// 不该影响业务。
func MustEvent(runID, threadID string, t EventType, data any) Event {
	e, err := NewEvent(runID, threadID, t, data)
	if err != nil {
		fallback, _ := json.Marshal(map[string]string{
			"marshal_error": err.Error(),
		})
		return Event{
			RunID:     runID,
			ThreadID:  threadID,
			Type:      t,
			Category:  categoryOf(t),
			Data:      fallback,
			CreatedAt: time.Now().UTC(),
		}
	}
	return e
}

// ContentDelta 是 content_delta 的负载。
type ContentDelta struct {
	Delta     string `json:"delta"`
	MessageID string `json:"message_id,omitempty"`
}

// ReasoningDelta is a private reasoning stream fragment.
type ReasoningDelta struct {
	Delta     string `json:"delta"`
	MessageID string `json:"message_id,omitempty"`
}

type PlanModeChanged struct {
	Active bool `json:"active"`
	Mode   Mode `json:"mode"`
}

// MessageReplace 是 message_replace 的负载：让客户端整段替换已渲染的文本。
type MessageReplace struct {
	Content   string `json:"content"`
	MessageID string `json:"message_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// ToolStart 是 tool_start 的负载。
type ToolStart struct {
	ToolCallID string          `json:"tool_call_id"`
	Name       string          `json:"name"`
	Args       json.RawMessage `json:"args,omitempty"`
}

// ToolResult 是 tool_result 的负载。
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

// TranscriptAppend is the canonical transcript delta for a completed run.
// Messages are kept in one event so persistence can atomically append the
// complete tool transaction produced by the run.
type TranscriptAppend struct {
	Messages []message.Message `json:"messages"`
}

// TranscriptReplace is emitted when compaction rewrites the full transcript.
type TranscriptReplace struct {
	Messages []message.Message `json:"messages"`
}

// SubagentProgress is the trusted child-stream envelope. Message deliberately
// uses the canonical transcript type; the HTTP adapter converts it to the
// client message projection and adds the task_id.
type SubagentProgress struct {
	TaskID       string          `json:"task_id"`
	MessageID    string          `json:"message_id"`
	MessageIndex int             `json:"message_index"`
	Message      message.Message `json:"message"`
}

// RunEnd 是 run_end 的负载。
type RunEnd struct {
	Status     string `json:"status"`
	Output     string `json:"output,omitempty"`
	Iterations int    `json:"iterations"`
	RiskLevel  string `json:"risk_level"`
	Error      string `json:"error,omitempty"`
}

// RunStateChanged is the canonical lifecycle projection used to recover a
// state machine after a process restart.
type RunStateChanged struct {
	Snapshot RunSnapshot `json:"snapshot"`
}

// UsageReport 是 usage 事件的负载。
type UsageReport struct {
	InputTokens     int   `json:"input_tokens"`
	OutputTokens    int   `json:"output_tokens"`
	LeadTokens      int   `json:"lead_tokens"`
	SubagentTokens  int   `json:"subagent_tokens"`
	AuxiliaryTokens int   `json:"auxiliary_tokens"`
	CostMicros      int64 `json:"cost_micros"`
}
