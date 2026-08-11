// Package model 定义单一的模型接口。
//
// 所有供应商适配到 Model 接口，不分叉运行时契约（设计文档 §2 依赖纪律、§11.1）。
// 分层、降级、熔断与错误分类恢复不在本包，归 pkg/modelrouter。
package model

import (
	"context"
	"encoding/json"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
)

// StopReason 是模型停止生成的原因。
const (
	StopReasonStop          = "stop"           // 正常收尾
	StopReasonToolCalls     = "tool_calls"     // 请求工具
	StopReasonLength        = "length"         // 撞到输出上限，内容可能被截断
	StopReasonContentFilter = "content_filter" // 供应商安全终止
	StopReasonError         = "error"
)

// ToolSchema 是投递给模型的工具声明。
//
// 本包不引用 pkg/tool：模型只需要名字、说明与参数 schema，
// 不需要 handler 与执行元数据。转换由 pkg/tool 提供。
type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Request 是一次模型调用的输入。
type Request struct {
	System      string
	Messages    []message.Message
	Tools       []ToolSchema
	MaxTokens   int
	Temperature *float64

	// Thinking 请求开启思维链。仅在 Info().SupportsThinking 为真时有效。
	Thinking bool

	// ExtraBody 透传供应商私有字段（如 DeepSeek 的 thinking.type）。
	// 这是配置里 when_thinking_enabled.extra_body 的落点。
	ExtraBody map[string]any

	// EnablePromptCache 请求供应商侧的 prompt 缓存。
	EnablePromptCache bool
}

// Usage 是一次模型调用的 token 用量。
type Usage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
}

// Add 返回 u 与 other 的和，不改写 u。
func (u Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens:       u.InputTokens + other.InputTokens,
		OutputTokens:      u.OutputTokens + other.OutputTokens,
		CachedInputTokens: u.CachedInputTokens + other.CachedInputTokens,
	}
}

// TotalTokens 返回输入与输出之和。
func (u Usage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens
}

// Response 是一次模型调用的输出。
type Response struct {
	Message    message.Message
	StopReason string
	Usage      Usage

	// CallID 唯一标识这次模型调用，供三桶用量归因去重（设计文档 §15）。
	// 重复回调（流式 done 与 Result 各报一次）不得重复计数。
	CallID string

	// ModelName 是实际服务本次请求的模型，可能因降级而与请求的不同。
	ModelName string
}

// Info 描述模型能力。中间件据此决定是否生效（如 ViewImage 只在 SupportsVision 时挂载）。
type Info struct {
	Name                    string
	ContextLength           int
	MaxOutputTokens         int
	SupportsThinking        bool
	SupportsReasoningEffort bool
	SupportsVision          bool
	SupportsTools           bool
}

// Model 是唯一的模型抽象。
type Model interface {
	Complete(ctx context.Context, req Request) (*Response, error)
	Stream(ctx context.Context, req Request) (StreamReader, error)
	Info() Info
}

// ProviderConfig 是构造一个 Model 实例所需的配置。
// 由 pkg/config 从 YAML 装填，内核不认识 YAML。
type ProviderConfig struct {
	Name             string
	Model            string
	BaseURL          string
	APIKey           string
	MaxTokens        int
	Temperature      *float64
	ContextLength    int
	SupportsThinking bool
	// SupportsReasoningEffort allows the per-run reasoning_effort option to be
	// forwarded in ExtraBody. Providers that do not implement it leave this false.
	SupportsReasoningEffort bool
	SupportsVision          bool
	ExtraBody               map[string]any
	// ThinkingExtraBody is merged only for requests whose Thinking flag is true.
	// It is interpreted by OpenAI-compatible providers; native providers ignore it.
	ThinkingExtraBody map[string]any
	Timeout           int // 秒
}
