// Package openai 是 OpenAI 兼容端点的适配器。
//
// 覆盖 OpenAI 本体以及所有兼容它的端点（DeepSeek、MiniMax、通义千问、
// vLLM、Ollama 等）。供应商私有字段通过 ExtraBody 透传，例如 DeepSeek 的
// thinking.type —— 为每家开一个 provider 只会让同一份协议维护五遍。
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

// Name 是本 provider 在注册表中的名字。
const Name = "openai-compatible"

const defaultBaseURL = "https://api.openai.com/v1"

// Client 是 OpenAI 兼容模型。
type Client struct {
	http    *http.Client
	baseURL string
	apiKey  string
	info    model.Info

	modelID           string
	maxTokens         int
	temperature       *float64
	extraBody         map[string]any
	thinkingExtraBody map[string]any
}

// New 按配置构造客户端。
func New(cfg model.ProviderConfig) (model.Model, error) {
	if cfg.Model == "" {
		return nil, errors.New("openai: model is required")
	}

	base := strings.TrimSuffix(cfg.BaseURL, "/")
	if base == "" {
		base = defaultBaseURL
	}

	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}

	ctxLen := cfg.ContextLength
	if ctxLen <= 0 {
		ctxLen = 128_000
	}

	return &Client{
		http:    &http.Client{Timeout: timeout},
		baseURL: base,
		apiKey:  cfg.APIKey,
		modelID: cfg.Model,
		info: model.Info{
			Name:                    cfg.Name,
			ContextLength:           ctxLen,
			MaxOutputTokens:         cfg.MaxTokens,
			SupportsThinking:        cfg.SupportsThinking,
			SupportsReasoningEffort: cfg.SupportsReasoningEffort,
			SupportsVision:          cfg.SupportsVision,
			SupportsTools:           true,
		},
		maxTokens:         cfg.MaxTokens,
		temperature:       cfg.Temperature,
		extraBody:         cfg.ExtraBody,
		thinkingExtraBody: cfg.ThinkingExtraBody,
	}, nil
}

// Info 实现 model.Model。
func (c *Client) Info() model.Info { return c.info }

// Complete 实现 model.Model。
func (c *Client) Complete(ctx context.Context, req model.Request) (*model.Response, error) {
	body, err := c.buildBody(req, false)
	if err != nil {
		return nil, err
	}

	resp, err := c.post(ctx, body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if err := classify(resp); err != nil {
		return nil, err
	}

	var parsed chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("openai: decoding response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return nil, errors.New("openai: response contained no choices")
	}

	return parsed.toResponse(c.info.Name), nil
}

// Stream 实现 model.Model。
func (c *Client) Stream(ctx context.Context, req model.Request) (model.StreamReader, error) {
	body, err := c.buildBody(req, true)
	if err != nil {
		return nil, err
	}

	resp, err := c.post(ctx, body)
	if err != nil {
		return nil, err
	}

	if err := classify(resp); err != nil {
		_ = resp.Body.Close()
		return nil, err
	}

	return newSSEReader(resp.Body, c.info.Name), nil
}

func (c *Client) post(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		// 网络层失败按供应商不可用分类，让 router 走降级链。
		return nil, fmt.Errorf("%w: %w", model.ErrProviderUnavailable, err)
	}
	return resp, nil
}

// buildBody 组装请求体。
//
// ExtraBody 最后合并，且不允许覆盖 messages/tools/model 这些结构性字段 ——
// 配置里一个手滑的 extra_body 不该悄悄改掉整个请求的语义。
func (c *Client) buildBody(req model.Request, stream bool) ([]byte, error) {
	payload := map[string]any{
		"model":    c.modelID,
		"messages": toWireMessages(req.System, req.Messages),
	}

	if stream {
		payload["stream"] = true
		payload["stream_options"] = map[string]any{"include_usage": true}
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = c.maxTokens
	}
	if maxTokens > 0 {
		payload["max_tokens"] = maxTokens
	}

	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	} else if c.temperature != nil {
		payload["temperature"] = *c.temperature
	}

	if len(req.Tools) > 0 {
		payload["tools"] = toWireTools(req.Tools)
	}

	mergeExtraBody(payload, c.extraBody)
	if req.Thinking {
		mergeExtraBody(payload, c.thinkingExtraBody)
	}
	mergeExtraBody(payload, req.ExtraBody)

	out, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("openai: marshalling request: %w", err)
	}
	return out, nil
}

func mergeExtraBody(payload, extra map[string]any) {
	for k, v := range extra {
		if !isStructuralField(k) {
			payload[k] = v
		}
	}
}

func isStructuralField(k string) bool {
	switch k {
	case "model", "messages", "tools", "stream":
		return true
	default:
		return false
	}
}

// classify 把 HTTP 状态码映射到 model 包的分类哨兵。
//
// 分类必须在这里做：router 的恢复策略（重发/退避/降级）依赖它，
// 而只有适配层知道每家供应商怎么表达"上下文超限"。
func classify(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	detail := strings.TrimSpace(string(body))

	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", model.ErrRateLimited, detail)

	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s", model.ErrAuthFailed, detail)

	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		// 上下文超限在 OpenAI 兼容端点上是 400，只能靠错误体识别。
		// 认错的代价是白重试一次；不认的代价是长会话彻底卡死。
		if looksLikeContextOverflow(detail) {
			return fmt.Errorf("%w: %s", model.ErrContextOverflow, detail)
		}
		return fmt.Errorf("%w: %s", model.ErrInvalidRequest, detail)

	case http.StatusRequestEntityTooLarge:
		return fmt.Errorf("%w: %s", model.ErrContextOverflow, detail)

	default:
		if resp.StatusCode >= 500 {
			return fmt.Errorf("%w: status %d: %s", model.ErrProviderUnavailable, resp.StatusCode, detail)
		}
		return fmt.Errorf("openai: unexpected status %d: %s", resp.StatusCode, detail)
	}
}

func looksLikeContextOverflow(detail string) bool {
	lower := strings.ToLower(detail)
	needles := []string{
		"context length",
		"context_length_exceeded",
		"maximum context",
		"too many tokens",
		"reduce the length",
		"input is too long",
		"prompt is too long",
	}
	for _, n := range needles {
		if strings.Contains(lower, n) {
			return true
		}
	}
	return false
}

// --- wire 类型 ---

type wireMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

type wireToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func toWireMessages(system string, msgs []message.Message) []wireMessage {
	out := make([]wireMessage, 0, len(msgs)+1)

	if strings.TrimSpace(system) != "" {
		out = append(out, wireMessage{Role: "system", Content: system})
	}

	for _, m := range msgs {
		w := wireMessage{Role: string(m.Role), ToolCallID: m.ToolCallID, Name: m.Name}

		if len(m.ContentBlocks) > 0 {
			w.Content = toWireBlocks(m)
		} else if m.Content != "" {
			w.Content = m.Content
		}

		for _, tc := range m.ToolCalls {
			args := string(tc.Arguments)
			if args == "" {
				args = "{}"
			}
			w.ToolCalls = append(w.ToolCalls, wireToolCall{
				ID:       tc.ID,
				Type:     "function",
				Function: wireFunction{Name: tc.Name, Arguments: args},
			})
		}

		// 带 tool_calls 的 assistant 消息内容可以为空，但字段必须存在，
		// 部分兼容端点会拒绝缺少 content 键的消息。
		if w.Content == nil && len(w.ToolCalls) > 0 {
			w.Content = ""
		}

		out = append(out, w)
	}
	return out
}

func toWireBlocks(m message.Message) []map[string]any {
	blocks := make([]map[string]any, 0, len(m.ContentBlocks)+1)

	if m.Content != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
	}
	for _, b := range m.ContentBlocks {
		switch b.Type {
		case "image":
			mime := b.MimeType
			if mime == "" {
				mime = "image/png"
			}
			blocks = append(blocks, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": "data:" + mime + ";base64," + b.Data},
			})
		default:
			blocks = append(blocks, map[string]any{"type": "text", "text": b.Text})
		}
	}
	return blocks
}

func toWireTools(tools []model.ToolSchema) []map[string]any {
	out := make([]map[string]any, len(tools))
	for i, t := range tools {
		params := t.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  params,
			},
		}
	}
	return out
}

type chatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content          string         `json:"content"`
			ReasoningContent string         `json:"reasoning_content"`
			ToolCalls        []wireToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

func (r chatResponse) toResponse(providerName string) *model.Response {
	choice := r.Choices[0]

	msg := message.Message{
		Role:             message.RoleAssistant,
		Content:          choice.Message.Content,
		ReasoningContent: choice.Message.ReasoningContent,
	}
	for _, tc := range choice.Message.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, message.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}

	callID := r.ID
	if callID == "" {
		callID = providerName + "-response"
	}

	modelName := r.Model
	if modelName == "" {
		modelName = providerName
	}

	return &model.Response{
		Message:    msg,
		StopReason: mapFinishReason(choice.FinishReason),
		Usage: model.Usage{
			InputTokens:       r.Usage.PromptTokens,
			OutputTokens:      r.Usage.CompletionTokens,
			CachedInputTokens: r.Usage.PromptTokensDetails.CachedTokens,
		},
		CallID:    callID,
		ModelName: modelName,
	}
}

// mapFinishReason 把供应商的 finish_reason 映射到内核的 StopReason。
func mapFinishReason(reason string) string {
	switch reason {
	case "tool_calls", "function_call":
		return model.StopReasonToolCalls
	case "length", "max_tokens":
		return model.StopReasonLength
	case "content_filter":
		return model.StopReasonContentFilter
	case "stop", "end_turn", "":
		return model.StopReasonStop
	default:
		return reason
	}
}
