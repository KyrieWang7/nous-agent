package builtin

import (
	"context"
	"slices"
	"strconv"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

// NameSafetyFinishReason 是安全终止处理中间件的名字。
const NameSafetyFinishReason = "safetyFinishReason"

// SafetyFinishReason 在响应被截断或安全终止时清空其 tool_calls。
//
// 供应商在 content_filter 或输出上限处截断时，已经发出的 tool_call 参数可能
// 是残缺的 JSON。执行一个参数被截断的 `rm` 或 `write_file` 是真实的破坏风险 ——
// 而流式解析器往往能把残缺 JSON "抢救"成看起来合法的对象，让问题更隐蔽。
//
// 注册顺序：必须排在 LoopDetection **之后**。AfterModel 逆序执行，
// 于是本中间件先观察原始输出、清掉不安全的调用，LoopDetection 再对
// 清理后的消息计数（设计文档 §4.4）。
type SafetyFinishReason struct {
	reasons []string
}

// NewSafetyFinishReason 返回安全终止处理中间件。
//
// reasons 为空时使用默认集合：content_filter 与 length。
func NewSafetyFinishReason(reasons ...string) *SafetyFinishReason {
	if len(reasons) == 0 {
		reasons = []string{model.StopReasonContentFilter, model.StopReasonLength}
	}
	return &SafetyFinishReason{reasons: reasons}
}

// Name 实现 middleware.Middleware。
func (SafetyFinishReason) Name() string { return NameSafetyFinishReason }

// Grade 实现 middleware.Graded。
func (SafetyFinishReason) Grade() middleware.Grade { return middleware.GradeAbort }

// AfterModel 实现 middleware.AfterModel。
func (s *SafetyFinishReason) AfterModel(_ context.Context, st *middleware.State) error {
	if st.ModelOutput == nil || len(st.ModelOutput.Message.ToolCalls) == 0 {
		return nil
	}
	if !slices.Contains(s.reasons, st.ModelOutput.StopReason) {
		return nil
	}

	dropped := len(st.ModelOutput.Message.ToolCalls)
	st.ModelOutput.Message.ToolCalls = nil

	note := explainDroppedCalls(st.ModelOutput.StopReason, dropped)
	if st.ModelOutput.Message.Content == "" {
		st.ModelOutput.Message.Content = note
	} else {
		st.ModelOutput.Message.Content += "\n\n" + note
	}

	// 转录里的那条 assistant 也必须改：只改 ModelOutput 的话，
	// 带残缺 tool_calls 的消息会留在历史里，下一轮请求依然非法。
	if st.History != nil {
		st.History.ReplaceLastAssistant(st.ModelOutput.Message)
	}
	return nil
}

func explainDroppedCalls(stopReason string, n int) string {
	switch stopReason {
	case model.StopReasonLength:
		return pluralise(n) + " discarded because the response hit the output token limit and " +
			"their arguments may be truncated. Re-issue them with complete arguments."
	default:
		return pluralise(n) + " discarded because the provider terminated the response for safety reasons."
	}
}

func pluralise(n int) string {
	if n == 1 {
		return "1 tool call was"
	}
	return strconv.Itoa(n) + " tool calls were"
}
