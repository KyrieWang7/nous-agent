// Package compaction 实现单路径 LLM 摘要压缩。
//
// 只有一条压缩路径。nous-agent 曾并行跑两套压缩处理器，实践中互相干扰；
// 本实现不再保留重叠路径。
// （设计文档 §6.2）。
package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

// summaryHeading 是摘要消息的固定前缀，便于识别与跨压缩续接。
const summaryHeading = "## Summary"

// Config 配置压缩器。字段名与 config.yaml 的 summarization 段对齐。
type Config struct {
	// TriggerTokens 是触发压缩的转录 token 数。<= 0 时禁用压缩。
	TriggerTokens int

	// KeepMessages 是保留在摘要之后的尾部消息条数。<= 0 时用 10。
	KeepMessages int

	// MaxSummaryTokens 是摘要的输出上限。<= 0 时用 1024。
	MaxSummaryTokens int

	// MaxInputMessages 与 MaxInputChars 给摘要输入封顶。
	//
	// 不封顶时会出现"压缩比不压缩更贵"：一个 300k token 的转录，
	// 每轮都拿全量去生成摘要，摘要调用本身就烧穿预算。
	MaxInputMessages int
	MaxInputChars    int
}

func (c Config) withDefaults() Config {
	if c.KeepMessages <= 0 {
		c.KeepMessages = 10
	}
	if c.MaxSummaryTokens <= 0 {
		c.MaxSummaryTokens = 1024
	}
	if c.MaxInputMessages <= 0 {
		c.MaxInputMessages = 200
	}
	if c.MaxInputChars <= 0 {
		c.MaxInputChars = 120_000
	}
	return c
}

// Summariser 生成摘要。生产实现是一次 fast 层模型调用。
type Summariser interface {
	Summarise(ctx context.Context, msgs []message.Message, maxTokens int) (string, error)
}

// Compactor 按阈值压缩转录。
type Compactor struct {
	cfg        Config
	summariser Summariser

	// mu 串行化压缩：并发的两次压缩会各自基于同一份快照重写转录，
	// 后写的那次会覆盖前一次的结果并丢消息。
	mu sync.Mutex
}

// New 返回压缩器。summariser 为 nil 时返回 error ——
// 一个不会摘要的压缩器只会静默地什么都不做。
func New(cfg Config, s Summariser) (*Compactor, error) {
	if s == nil {
		return nil, errors.New("compaction: a summariser is required")
	}
	return &Compactor{cfg: cfg.withDefaults(), summariser: s}, nil
}

// ShouldCompact reports whether this history currently has a safe cut point
// above the configured threshold. The loop uses it only to expose the durable
// compacting phase; MaybeCompact remains the execution authority and checks
// the condition again while holding the compactor lock.
func (c *Compactor) ShouldCompact(h *message.History) bool {
	if c == nil || c.cfg.TriggerTokens <= 0 || h == nil || h.TokenCount() < c.cfg.TriggerTokens {
		return false
	}
	return FindCutPoint(h.All(), c.cfg.KeepMessages) > 0
}

// MaybeCompact 在超过阈值时压缩转录，返回是否发生了压缩。
//
// 实现 loop.Compactor。返回 error 只在摘要器彻底不可用且没有兜底时 ——
// 见 summarise 的降级说明。
func (c *Compactor) MaybeCompact(ctx context.Context, h *message.History) (bool, error) {
	if c.cfg.TriggerTokens <= 0 || h == nil {
		return false, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if h.TokenCount() < c.cfg.TriggerTokens {
		return false, nil
	}

	snapshot := h.All()
	cut := FindCutPoint(snapshot, c.cfg.KeepMessages)
	if cut <= 0 {
		// 没有安全的切点：整个转录就是一个大事务，或保留窗口已覆盖全部。
		// 压不了不是错误，下一轮 token 更多时切点可能就出现了。
		return false, nil
	}

	summary, err := c.summarise(ctx, snapshot[:cut])
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(summary) == "" {
		return false, nil
	}

	out := make([]message.Message, 0, 1+len(snapshot)-cut)
	out = append(out, message.Message{Role: message.RoleSystem, Content: summary})
	out = append(out, snapshot[cut:]...)
	h.Replace(out)

	return true, nil
}

// summarise 生成摘要，失败时回退到结构化占位摘要。
//
// 摘要模型不可用不该让长会话彻底不可用：占位摘要保留了消息计数与角色分布，
// 让模型知道"这里有过对话但细节已丢失"，比直接失败有用（设计文档 §3.2 步 3）。
func (c *Compactor) summarise(ctx context.Context, msgs []message.Message) (string, error) {
	input := c.capInput(StripToolIO(msgs))

	summary, err := c.summariser.Summarise(ctx, input, c.cfg.MaxSummaryTokens)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// 取消是外部意志，不该被占位摘要掩盖。
			return "", err
		}
		return placeholder(msgs), nil
	}

	summary = strings.TrimSpace(summary)
	if summary == "" {
		return "", nil
	}
	if !strings.HasPrefix(summary, summaryHeading) {
		summary = summaryHeading + "\n\n" + summary
	}
	return summary, nil
}

// capInput 给摘要输入封顶，从尾部保留（近期消息信息量更大）。
func (c *Compactor) capInput(msgs []message.Message) []message.Message {
	if len(msgs) > c.cfg.MaxInputMessages {
		msgs = msgs[len(msgs)-c.cfg.MaxInputMessages:]
	}

	total := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		total += len(msgs[i].Content)
		if total > c.cfg.MaxInputChars {
			return msgs[i+1:]
		}
	}
	return msgs
}

// placeholder 是摘要器不可用时的结构化兜底。
func placeholder(msgs []message.Message) string {
	var user, assistant, toolCalls int
	for _, m := range msgs {
		switch m.Role {
		case message.RoleUser:
			user++
		case message.RoleAssistant:
			assistant++
			toolCalls += len(m.ToolCalls)
		}
	}

	return fmt.Sprintf(
		"%s\n\nThe earlier part of this conversation was compacted but the summariser was unavailable, "+
			"so its content is not recoverable here. It contained %d user messages, %d assistant replies "+
			"and %d tool calls. Ask the user to restate anything you need from it.",
		summaryHeading, user, assistant, toolCalls)
}

// FindCutPoint 返回压缩切点：snapshot[:cut] 被摘要，snapshot[cut:] 保留。
//
// 两条约束：
//   - 保留尾部 keep 条消息。
//   - 切点不落在工具事务中间。半个事务会让下一次模型请求非法
//     （tool_calls 没有对应的 tool 结果），所以切点必须前移到事务起点。
//
// 返回 0 表示没有可用切点。
func FindCutPoint(snapshot []message.Message, keep int) int {
	if len(snapshot) <= keep {
		return 0
	}

	cut := len(snapshot) - keep
	for _, span := range message.ToolTransactionSpans(snapshot) {
		if span.Start < cut && cut < span.End {
			cut = span.Start
			break
		}
	}

	// 首条 system 消息（通常是上一轮的摘要）单独摘要没有意义，
	// 但把它留在保留窗口外也不对 —— 它会作为输入参与新摘要，实现跨压缩续接。
	if cut <= 0 {
		return 0
	}
	return cut
}

// StripToolIO 为摘要输入剔除工具的输入输出。
//
// 工具 JSON 与原始输出占据了转录的绝大部分体积，却几乎不含需要长期记住的信息。
// 把它们喂给摘要模型只是在为噪声付钱。
func StripToolIO(msgs []message.Message) []message.Message {
	if len(msgs) == 0 {
		return nil
	}

	out := make([]message.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == message.RoleTool {
			continue
		}

		cloned := message.Clone(m)
		hadToolCalls := len(cloned.ToolCalls) > 0
		cloned.ToolCalls = nil
		cloned.ReasoningContent = ""

		// 只有工具调用、没有文本的 assistant 轮清空后会变成空消息。
		// 留一句说明，否则摘要模型看到的是一串没头没尾的空回复。
		if hadToolCalls && strings.TrimSpace(cloned.Content) == "" && len(cloned.ContentBlocks) == 0 {
			cloned.Content = "(called tools)"
		}
		out = append(out, cloned)
	}
	return out
}

// ModelSummariser 用一次模型调用生成摘要。
type ModelSummariser struct {
	model  model.Model
	prompt string
}

// NewModelSummariser 返回基于 m 的摘要器。
//
// m 应当是 fast 层模型：摘要是系统开销，用主模型做等于每次压缩都付一次
// 高价推理的钱。
func NewModelSummariser(m model.Model) *ModelSummariser {
	return &ModelSummariser{
		model: m,
		prompt: "You compress conversations for an AI agent's long-term context. " +
			"Summarise the conversation below. Keep decisions, constraints, file paths, " +
			"identifiers and unresolved questions. Do not include tool JSON or raw tool output. " +
			"Be concise and factual.",
	}
}

// Summarise 实现 Summariser。
func (s *ModelSummariser) Summarise(ctx context.Context, msgs []message.Message, maxTokens int) (string, error) {
	if s.model == nil {
		return "", errors.New("compaction: summariser has no model")
	}

	req := model.Request{
		System: s.prompt,
		Messages: append(msgs, message.Message{
			Role:    message.RoleUser,
			Content: "Compress the conversation above into a summary.",
		}),
		MaxTokens: maxTokens,
	}

	resp, err := s.model.Complete(ctx, req)
	if err != nil {
		return "", fmt.Errorf("compaction: summarising: %w", err)
	}
	if resp == nil {
		return "", errors.New("compaction: summariser returned no response")
	}
	return resp.Message.Content, nil
}
