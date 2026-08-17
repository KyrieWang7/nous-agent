package handlers

import (
	"context"
	"fmt"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
)

// NameToolErrorHandling 是工具错误处理生命周期处理器的名字。
const NameToolErrorHandling = "toolErrorHandling"

// ToolErrorHandling 把工具执行期的框架层错误转成模型可读的 error 结果。
//
// 执行器已经做了 panic recover 与结果兜底，这一层负责把错误**表述**成模型
// 能据以改换方案的样子：光把 Go 的 error 字符串丢回去，模型往往看不出
// 该怎么办。
type ToolErrorHandling struct{}

// NewToolErrorHandling 返回工具错误处理生命周期处理器。
func NewToolErrorHandling() *ToolErrorHandling { return &ToolErrorHandling{} }

// Name 实现 lifecycle.Lifecycle。
func (ToolErrorHandling) Name() string { return NameToolErrorHandling }

// AfterTool 实现 lifecycle.AfterTool。
func (ToolErrorHandling) AfterTool(_ context.Context, st *lifecycle.State) error {
	if st.ToolExecErr == nil || st.ToolCall == nil {
		return nil
	}

	// 结果已被标记为错误时不再重复包装：ToolOutputBudget 之类的生命周期处理器
	// 可能已经处理过，覆盖会丢掉它们的加工。
	if st.ToolResult != nil && st.ToolResult.IsError {
		return nil
	}

	updated := *st.ToolResult
	updated.IsError = true
	updated.Content = fmt.Sprintf(
		"Tool %q failed: %v\n\nThe tool did not run. Fix the arguments or use a different approach.",
		st.ToolCall.Name, st.ToolExecErr)
	st.ToolResult = &updated
	return nil
}
