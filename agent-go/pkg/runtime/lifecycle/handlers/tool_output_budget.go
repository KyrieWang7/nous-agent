package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/artifact"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// NameToolOutputBudget 是工具输出预算生命周期处理器的名字。
const NameToolOutputBudget = "toolOutputBudget"

// BudgetOptions 配置工具输出预算。字段名与 config.yaml 的 tool_output 段对齐。
type BudgetOptions struct {
	// ExternalizeMinChars 是触发外部化的输出长度。<= 0 时用 12000。
	ExternalizeMinChars int

	// PreviewHeadChars / PreviewTailChars 是外部化后保留的首尾预览长度。
	PreviewHeadChars int
	PreviewTailChars int

	// FallbackMaxChars 是存储不可用时的截断上限。
	FallbackMaxChars int

	// ExemptTools 列出豁免的工具名。read_file 这类"就是要读全文"的工具
	// 必须豁免，否则模型永远拿不到完整文件。
	ExemptTools []string

	// Store 存放被外部化的内容。为 nil 时退化为纯截断。
	Store artifact.Store

	Logger *slog.Logger
}

func (o BudgetOptions) withDefaults() BudgetOptions {
	if o.ExternalizeMinChars <= 0 {
		o.ExternalizeMinChars = 12_000
	}
	if o.PreviewHeadChars <= 0 {
		o.PreviewHeadChars = 2_000
	}
	if o.PreviewTailChars <= 0 {
		o.PreviewTailChars = 1_000
	}
	if o.FallbackMaxChars <= 0 {
		o.FallbackMaxChars = 30_000
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return o
}

// ToolOutputBudget 把超大工具输出外部化或截断。
//
// 一次 `find /` 的输出能有几百 MB，直接回灌会炸掉上下文；而模型无论如何
// 也读不完那么多。外部化保留了"需要时能取回细节"的可能。
type ToolOutputBudget struct {
	opts   BudgetOptions
	exempt map[string]struct{}
}

// NewToolOutputBudget 返回工具输出预算生命周期处理器。
func NewToolOutputBudget(opts BudgetOptions) *ToolOutputBudget {
	opts = opts.withDefaults()

	exempt := make(map[string]struct{}, len(opts.ExemptTools))
	for _, n := range opts.ExemptTools {
		exempt[n] = struct{}{}
	}
	return &ToolOutputBudget{opts: opts, exempt: exempt}
}

// Name 实现 lifecycle.Lifecycle。
func (b *ToolOutputBudget) Name() string { return NameToolOutputBudget }

// Grade 实现 lifecycle.Graded。
//
// 监听类：外部化失败时应当退化为截断而不是判死回合 —— 一个存储抖动
// 不该让已经跑成功的工具白跑。
func (b *ToolOutputBudget) Grade() lifecycle.Grade { return lifecycle.GradeListener }

// AfterTool 实现 lifecycle.AfterTool。
func (b *ToolOutputBudget) AfterTool(ctx context.Context, st *lifecycle.State) error {
	if st.ToolResult == nil || st.ToolCall == nil {
		return nil
	}
	if _, ok := b.exempt[st.ToolCall.Name]; ok {
		return nil
	}

	content := st.ToolResult.Content
	if len(content) < b.opts.ExternalizeMinChars {
		return nil
	}

	updated := *st.ToolResult

	if b.opts.Store != nil {
		ref, err := b.opts.Store.Put(ctx, st.RunID, []byte(content))
		if err != nil {
			// 存储不可用时退化为截断，不失败 —— 见 Grade 的说明。
			b.opts.Logger.WarnContext(ctx, "externalising tool output failed; truncating instead",
				"tool", st.ToolCall.Name, "error", err)
			updated.Content = b.truncate(content)
			st.ToolResult = &updated
			return nil
		}

		updated.Content = b.preview(content, ref)
		updated.Artifacts = append(updated.Artifacts, tool.Artifact{
			Kind: "file",
			Ref:  ref.Key,
			Size: ref.Size,
		})
		st.ToolResult = &updated
		return nil
	}

	updated.Content = b.truncate(content)
	st.ToolResult = &updated
	return nil
}

// preview 渲染外部化后的首尾预览。
func (b *ToolOutputBudget) preview(content string, ref artifact.Ref) string {
	head := head(content, b.opts.PreviewHeadChars)
	tail := tail(content, b.opts.PreviewTailChars)

	var sb strings.Builder
	sb.WriteString(head)
	sb.WriteString(fmt.Sprintf(
		"\n\n[output truncated: %d characters total, stored as %s]\n\n", len(content), ref.Key))
	sb.WriteString(tail)
	return sb.String()
}

// truncate 是存储不可用时的兜底：保留首尾并明确说明截断。
//
// 必须说明：静默截断会让模型基于半截输出做判断，比报错更糟。
func (b *ToolOutputBudget) truncate(content string) string {
	if len(content) <= b.opts.FallbackMaxChars {
		return content
	}

	half := b.opts.FallbackMaxChars / 2
	return head(content, half) +
		fmt.Sprintf("\n\n[output truncated: %d characters total, full content unavailable]\n\n", len(content)) +
		tail(content, half)
}

func head(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n]
}

func tail(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
