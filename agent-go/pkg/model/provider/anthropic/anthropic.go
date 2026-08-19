// Package anthropic adapts the native Anthropic Messages API.
package anthropic

import (
	"bufio"
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

const Name = "anthropic"

type Client struct {
	http                     *http.Client
	baseURL, apiKey, modelID string
	maxTokens                int
	temperature              *float64
	info                     model.Info
}

// Close releases pooled transport connections owned by this generation.
func (c *Client) Close() error {
	if c != nil && c.http != nil {
		c.http.CloseIdleConnections()
	}
	return nil
}

func New(cfg model.ProviderConfig) (model.Model, error) {
	if cfg.Model == "" {
		return nil, errors.New("anthropic: model is required")
	}
	base := strings.TrimSuffix(cfg.BaseURL, "/")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	max := cfg.MaxTokens
	if max <= 0 {
		max = 8192
	}
	ctxLen := cfg.ContextLength
	if ctxLen <= 0 {
		ctxLen = 200000
	}
	return &Client{
		http:        &http.Client{Timeout: timeout},
		baseURL:     base,
		apiKey:      cfg.APIKey,
		modelID:     cfg.Model,
		maxTokens:   max,
		temperature: cfg.Temperature,
		info: model.Info{
			Name:             cfg.Name,
			ContextLength:    ctxLen,
			MaxOutputTokens:  max,
			SupportsThinking: cfg.SupportsThinking,
			SupportsVision:   cfg.SupportsVision,
			SupportsTools:    true,
		},
	}, nil
}
func (c *Client) Info() model.Info { return c.info }
func (c *Client) Complete(ctx context.Context, req model.Request) (*model.Response, error) {
	resp, err := c.post(ctx, req, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := classify(resp); err != nil {
		return nil, err
	}
	var wire response
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return nil, err
	}
	return wire.toModel(c.info.Name), nil
}
func (c *Client) Stream(ctx context.Context, req model.Request) (model.StreamReader, error) {
	resp, err := c.post(ctx, req, true)
	if err != nil {
		return nil, err
	}
	if err := classify(resp); err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	return newStream(resp.Body, c.info.Name), nil
}
func (c *Client) post(ctx context.Context, req model.Request, stream bool) (*http.Response, error) {
	payload := map[string]any{"model": c.modelID, "max_tokens": c.maxTokens, "messages": wireMessages(req.Messages), "stream": stream}
	temperature := req.Temperature
	if temperature == nil {
		temperature = c.temperature
	}
	if temperature != nil {
		payload["temperature"] = *temperature
	}
	if req.System != "" {
		if req.EnablePromptCache {
			payload["system"] = []any{map[string]any{"type": "text", "text": req.System, "cache_control": map[string]string{"type": "ephemeral"}}}
		} else {
			payload["system"] = req.System
		}
	}
	if len(req.Tools) > 0 {
		var tools []any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Parameters})
		}
		payload["tools"] = tools
	}
	if req.Thinking {
		payload["thinking"] = map[string]any{"type": "enabled", "budget_tokens": max(c.maxTokens/2, 1024)}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	request.Header.Set("content-type", "application/json")
	request.Header.Set("anthropic-version", "2023-06-01")
	if c.apiKey != "" {
		request.Header.Set("x-api-key", c.apiKey)
	}
	resp, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", model.ErrProviderUnavailable, err)
	}
	return resp, nil
}
func classify(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	detail := string(raw)
	switch resp.StatusCode {
	case 401, 403:
		return fmt.Errorf("%w: %s", model.ErrAuthFailed, detail)
	case 429:
		return fmt.Errorf("%w: %s", model.ErrRateLimited, detail)
	case 400:
		if strings.Contains(strings.ToLower(detail), "too long") || strings.Contains(strings.ToLower(detail), "context") {
			return fmt.Errorf("%w: %s", model.ErrContextOverflow, detail)
		}
		return fmt.Errorf("%w: %s", model.ErrInvalidRequest, detail)
	default:
		if resp.StatusCode >= 500 {
			return fmt.Errorf("%w: %s", model.ErrProviderUnavailable, detail)
		}
		return fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, detail)
	}
}
func wireMessages(msgs []message.Message) []any {
	out := make([]any, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == message.RoleSystem {
			continue
		}
		role := string(m.Role)
		if role == "tool" {
			out = append(out, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content, "is_error": m.IsError}}})
			continue
		}
		var content []any
		if m.Content != "" {
			content = append(content, map[string]any{"type": "text", "text": m.Content})
		}
		for _, tc := range m.ToolCalls {
			var input any
			_ = json.Unmarshal(tc.Arguments, &input)
			content = append(content, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": input})
		}
		out = append(out, map[string]any{"role": role, "content": content})
	}
	return out
}

type response struct {
	ID         string `json:"id"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
	} `json:"content"`
	Usage usage `json:"usage"`
}

type usage struct {
	Input         int `json:"input_tokens"`
	Output        int `json:"output_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
}

// Anthropic reports uncached, cache-created, and cache-read input separately.
// The harness contract keeps InputTokens as the complete input count and
// CachedInputTokens as the cache-read subset, matching OpenAI's semantics.
func (u usage) toModel() model.Usage {
	return model.Usage{
		InputTokens:       u.Input + u.CacheCreation + u.CacheRead,
		OutputTokens:      u.Output,
		CachedInputTokens: u.CacheRead,
	}
}

func (r response) toModel(name string) *model.Response {
	msg := message.Message{Role: message.RoleAssistant}
	for _, b := range r.Content {
		switch b.Type {
		case "text":
			msg.Content += b.Text
		case "thinking":
			msg.ReasoningContent += b.Thinking
		case "tool_use":
			msg.ToolCalls = append(msg.ToolCalls, message.ToolCall{ID: b.ID, Name: b.Name, Arguments: b.Input})
		}
	}
	return &model.Response{Message: msg, StopReason: mapStop(r.StopReason), Usage: r.Usage.toModel(), CallID: r.ID, ModelName: r.Model}
}
func mapStop(v string) string {
	switch v {
	case "end_turn", "stop_sequence":
		return model.StopReasonStop
	case "tool_use":
		return model.StopReasonToolCalls
	case "max_tokens":
		return model.StopReasonLength
	case "refusal":
		return model.StopReasonContentFilter
	default:
		return v
	}
}

type stream struct {
	body           io.ReadCloser
	scanner        *bufio.Scanner
	name           string
	text, thinking strings.Builder
	calls          map[int]*message.ToolCall
	response       model.Response
	done           bool
	err            error
}

func newStream(body io.ReadCloser, name string) *stream {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	return &stream{body: body, scanner: scanner, name: name, calls: map[int]*message.ToolCall{}}
}
func (s *stream) Next() (model.StreamEvent, bool) {
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		var ev struct {
			Type    string   `json:"type"`
			Index   int      `json:"index"`
			Message response `json:"message"`
			Delta   struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Content struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Usage struct {
				Output int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(data)), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "message_start":
			s.response.CallID = ev.Message.ID
			s.response.ModelName = ev.Message.Model
			s.response.Usage = ev.Message.Usage.toModel()
			return model.StreamEvent{Type: model.StreamStart}, true
		case "content_block_start":
			if ev.Content.Type == "tool_use" {
				s.calls[ev.Index] = &message.ToolCall{ID: ev.Content.ID, Name: ev.Content.Name}
			}
		case "content_block_delta":
			if ev.Delta.Text != "" {
				s.text.WriteString(ev.Delta.Text)
				return model.StreamEvent{Type: model.StreamTextDelta, Delta: ev.Delta.Text}, true
			}
			if ev.Delta.Thinking != "" {
				s.thinking.WriteString(ev.Delta.Thinking)
				return model.StreamEvent{Type: model.StreamThinkingDelta, Delta: ev.Delta.Thinking}, true
			}
			if ev.Delta.PartialJSON != "" {
				tc := s.calls[ev.Index]
				if tc != nil {
					tc.Arguments = append(tc.Arguments, ev.Delta.PartialJSON...)
					return model.StreamEvent{Type: model.StreamToolCallDelta, ToolCallIndex: ev.Index, ToolCallID: tc.ID, ToolCallName: tc.Name, ArgumentsDelta: ev.Delta.PartialJSON}, true
				}
			}
		case "message_delta":
			s.response.StopReason = mapStop(ev.Delta.StopReason)
			s.response.Usage.OutputTokens = ev.Usage.Output
		case "message_stop":
			s.done = true
			return model.StreamEvent{Type: model.StreamDone}, true
		}
	}
	if err := s.scanner.Err(); err != nil {
		s.err = err
		return model.StreamEvent{Type: model.StreamError, Err: err}, true
	}
	s.done = true
	return model.StreamEvent{}, false
}
func (s *stream) Result() (*model.Response, error) {
	for !s.done {
		if _, ok := s.Next(); !ok {
			break
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	s.response.Message = message.Message{Role: message.RoleAssistant, Content: s.text.String(), ReasoningContent: s.thinking.String()}
	for i := 0; i < len(s.calls); i++ {
		if tc := s.calls[i]; tc != nil {
			s.response.Message.ToolCalls = append(s.response.Message.ToolCalls, *tc)
		}
	}
	if s.response.ModelName == "" {
		s.response.ModelName = s.name
	}
	return &s.response, nil
}
func (s *stream) Close() error { return s.body.Close() }
