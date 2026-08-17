package handlers

import (
	"context"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/hooks"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// NameHook 是 hook 生命周期处理器的名字，供锚点引用。
const NameHook = "hook"

// Hook 把 Hook 治理层接到工具调用两侧。
type Hook struct {
	runner *hooks.Runner
}

// NewHook 返回 hook 生命周期处理器。
func NewHook(runner *hooks.Runner) *Hook {
	return &Hook{runner: runner}
}

// Name 实现 lifecycle.Lifecycle。
func (h *Hook) Name() string { return NameHook }

// Grade 实现 lifecycle.Graded。
//
// GradeAbort 只覆盖"生命周期处理器本身返回 error"，而它不会：单个 hook 的执行失败
// 由 Runner 吞掉并告警（hook 是治理增强，不是授权源）。
func (h *Hook) Grade() lifecycle.Grade { return lifecycle.GradeAbort }

// BeforeTool 实现 lifecycle.BeforeTool。
func (h *Hook) BeforeTool(ctx context.Context, st *lifecycle.State) (tool.Decision, error) {
	if st.ToolCall == nil || !h.runner.Has(hooks.EventPreToolUse) {
		return tool.Decision{}, nil
	}

	res := h.runner.Run(ctx, hooks.Payload{
		Event:    hooks.EventPreToolUse,
		ToolName: st.ToolCall.Name,
		ToolArgs: st.ToolCall.Args,
		ThreadID: st.ThreadID,
		RunID:    st.RunID,
	})

	return tool.Decision{
		Deny:   res.Deny,
		Reason: res.Message,
		Args:   res.UpdatedArgs,
	}, nil
}

// AfterTool 实现 lifecycle.AfterTool。
//
// 事件按结果分派：失败走 post_tool_use_failure，成功走 post_tool_use。
// 混成一个事件会让"只在失败时告警"这种最常见的 hook 写不出来。
func (h *Hook) AfterTool(ctx context.Context, st *lifecycle.State) error {
	if st.ToolCall == nil || st.ToolResult == nil {
		return nil
	}

	isError := st.ToolResult.IsError || st.ToolExecErr != nil
	event := hooks.EventPostToolUse
	if isError {
		event = hooks.EventPostToolUseFailure
	}
	if !h.runner.Has(event) {
		return nil
	}

	res := h.runner.Run(ctx, hooks.Payload{
		Event:    event,
		ToolName: st.ToolCall.Name,
		ToolArgs: st.ToolCall.Args,
		ToolOut:  st.ToolResult.Content,
		IsError:  isError,
		ThreadID: st.ThreadID,
		RunID:    st.RunID,
	})

	if res.Message == "" {
		return nil
	}

	// 反馈追加到结果而不是替换：hook 的意见是对工具输出的补充，
	// 覆盖掉原输出会让模型丢失它真正需要的信息。
	updated := *st.ToolResult
	updated.Content = st.ToolResult.Content + "\n\n[Hook feedback] " + res.Message
	if res.Deny {
		updated.IsError = true
	}
	st.ToolResult = &updated
	return nil
}
