package builtin

import (
	"context"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
)

// NameDanglingToolCall 是悬空工具调用修复中间件的名字。
const NameDanglingToolCall = "danglingToolCall"

// DanglingToolCall 为没有结果的 tool_call 补占位结果。
//
// 转录里出现悬空 tool_call 的原因很多：用户中途取消、进程崩溃后恢复、
// 工具执行被治理中间件中断。而供应商一律拒绝"有 tool_calls 却缺 tool 结果"
// 的请求 —— 不修的话这个会话从此再也发不出任何请求，只能整个丢弃。
type DanglingToolCall struct{}

// NewDanglingToolCall 返回悬空工具调用修复中间件。
func NewDanglingToolCall() *DanglingToolCall { return &DanglingToolCall{} }

// Name 实现 middleware.Middleware。
func (DanglingToolCall) Name() string { return NameDanglingToolCall }

// BeforeModel 实现 middleware.BeforeModel。
func (DanglingToolCall) BeforeModel(_ context.Context, st *middleware.State) error {
	if st.History == nil {
		return nil
	}

	snapshot := st.History.All()
	repaired, changed := repairDangling(snapshot)
	if !changed {
		return nil
	}

	// 用 Replace 而不是 Append：占位结果必须紧跟在对应的 assistant 之后，
	// 追加到末尾仍然是非法请求。
	st.History.Replace(repaired)
	return nil
}

// repairDangling 返回补齐后的转录与是否有改动。
func repairDangling(msgs []message.Message) ([]message.Message, bool) {
	spans := message.ToolTransactionSpans(msgs)
	if len(spans) == 0 {
		return msgs, false
	}

	// 逐事务检查缺哪些 tool_call_id，从后往前插入以免打乱前面的下标。
	type insertion struct {
		at   int
		msgs []message.Message
	}
	var inserts []insertion

	for _, span := range spans {
		call := msgs[span.Start]

		seen := make(map[string]struct{}, span.End-span.Start-1)
		for i := span.Start + 1; i < span.End; i++ {
			seen[msgs[i].ToolCallID] = struct{}{}
		}

		var missing []message.Message
		for _, tc := range call.ToolCalls {
			if _, ok := seen[tc.ID]; ok {
				continue
			}
			missing = append(missing, message.Message{
				Role:       message.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    "[This tool call did not complete and produced no result.]",
				IsError:    true,
			})
		}
		if len(missing) > 0 {
			inserts = append(inserts, insertion{at: span.End, msgs: missing})
		}
	}

	if len(inserts) == 0 {
		return msgs, false
	}

	out := message.CloneAll(msgs)
	for i := len(inserts) - 1; i >= 0; i-- {
		ins := inserts[i]
		out = append(out[:ins.at], append(ins.msgs, out[ins.at:]...)...)
	}
	return out, true
}
