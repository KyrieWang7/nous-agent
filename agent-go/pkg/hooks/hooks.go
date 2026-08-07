// Package hooks 是 Hook 治理层：在工具调用与 subagent 生命周期上挂用户定义的检查。
//
// 与权限的区别：权限是授权源，hook 是治理增强。因此二者的失败语义相反 ——
// 权限判不出来就拒绝（fail-closed），hook 跑不起来则放行并告警（fail-open）。
// 把 hook 做成 fail-closed 会让一个写坏的钩子脚本瘫掉整个 agent。
package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
)

// Event 是 hook 事件类型。与 nous-agent 的 5 个事件一一对应。
type Event string

const (
	EventPreToolUse         Event = "pre_tool_use"
	EventPostToolUse        Event = "post_tool_use"
	EventPostToolUseFailure Event = "post_tool_use_failure"
	EventSubagentStart      Event = "subagent_start"
	EventSubagentEnd        Event = "subagent_end"
)

// Events 返回全部事件，供配置校验与错误信息使用。
func Events() []Event {
	return []Event{
		EventPreToolUse, EventPostToolUse, EventPostToolUseFailure,
		EventSubagentStart, EventSubagentEnd,
	}
}

// Valid 报告 e 是否是已知事件。
func (e Event) Valid() bool {
	return slices.Contains(Events(), e)
}

// Payload 是传给 hook 的数据。外部 hook 收到它的 JSON 形式（stdin）。
type Payload struct {
	Event    Event           `json:"event"`
	ToolName string          `json:"tool_name,omitempty"`
	ToolArgs json.RawMessage `json:"tool_args,omitempty"`
	ToolOut  string          `json:"tool_output,omitempty"`
	IsError  bool            `json:"is_error,omitempty"`
	ThreadID string          `json:"thread_id,omitempty"`
	RunID    string          `json:"run_id,omitempty"`
	Subagent string          `json:"subagent,omitempty"`
}

// Outcome 是一个 hook 的判定。
type Outcome struct {
	// Deny 表示阻止本次操作。
	Deny bool

	// Message 是给模型看的说明。Deny 时作为拒绝理由，否则作为附加反馈。
	Message string

	// UpdatedArgs 非 nil 时替换工具入参。仅 pre_tool_use 有效。
	UpdatedArgs json.RawMessage
}

// Hook 是一个可执行的钩子。
type Hook interface {
	// Name 用于日志与错误信息。
	Name() string

	// Events 是该 hook 关心的事件。
	Events() []Event

	// Matches 报告该 hook 是否作用于这个工具名。空匹配器匹配全部。
	Matches(toolName string) bool

	Run(ctx context.Context, p Payload) (Outcome, error)
}

// Result 是一次事件分发的汇总。
type Result struct {
	Deny        bool
	Message     string
	UpdatedArgs json.RawMessage

	// DeniedBy 是做出拒绝判定的 hook 名，用于审计。
	DeniedBy string
}

// Runner 按事件分发 hook。
type Runner struct {
	mu    sync.RWMutex
	byEvt map[Event][]Hook

	// onError 在 hook 执行失败时被调用。为 nil 时静默。
	onError func(hookName string, err error)
}

// NewRunner 返回分发器。hooks 里声明了未知事件的会返回 error。
func NewRunner(hooks []Hook, onError func(string, error)) (*Runner, error) {
	r := &Runner{byEvt: make(map[Event][]Hook), onError: onError}

	for _, h := range hooks {
		if h == nil {
			return nil, fmt.Errorf("hooks: nil hook in configuration")
		}
		if h.Name() == "" {
			return nil, fmt.Errorf("hooks: hook has an empty name")
		}
		evts := h.Events()
		if len(evts) == 0 {
			return nil, fmt.Errorf("hooks: %q declares no events", h.Name())
		}
		for _, e := range evts {
			if !e.Valid() {
				return nil, fmt.Errorf("hooks: %q declares unknown event %q; valid events: %v", h.Name(), e, Events())
			}
			r.byEvt[e] = append(r.byEvt[e], h)
		}
	}
	return r, nil
}

// Has 报告某事件是否有 hook 注册。
func (r *Runner) Has(e Event) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byEvt[e]) > 0
}

// Run 顺序执行该事件下匹配的 hook。
//
// 语义：
//   - 第一个 Deny 短路后续 hook —— 已经拒绝了，再问下去只会让"谁的理由被展示"
//     变成注册顺序的偶然产物。
//   - 入参改写累积传递：后一个 hook 看到的是前一个改写后的值。
//   - 单个 hook 执行失败**不阻断**：记录后继续。hook 是治理增强而非授权源，
//     一个写坏的脚本不该瘫掉 agent。
func (r *Runner) Run(ctx context.Context, p Payload) Result {
	r.mu.RLock()
	hooks := slices.Clone(r.byEvt[p.Event])
	r.mu.RUnlock()

	var res Result
	for _, h := range hooks {
		if err := ctx.Err(); err != nil {
			return res
		}
		if p.ToolName != "" && !h.Matches(p.ToolName) {
			continue
		}

		out, err := h.Run(ctx, p)
		if err != nil {
			if r.onError != nil {
				r.onError(h.Name(), err)
			}
			continue
		}

		if out.UpdatedArgs != nil {
			res.UpdatedArgs = out.UpdatedArgs
			p.ToolArgs = out.UpdatedArgs
		}
		if out.Deny {
			res.Deny = true
			res.DeniedBy = h.Name()
			if out.Message != "" {
				res.Message = out.Message
			}
			return res
		}
		if out.Message != "" {
			res.Message = appendMessage(res.Message, out.Message)
		}
	}
	return res
}

func appendMessage(existing, add string) string {
	if existing == "" {
		return add
	}
	return existing + "\n" + add
}
