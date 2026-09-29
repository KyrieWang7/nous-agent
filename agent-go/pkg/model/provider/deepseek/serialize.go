package deepseek

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"strings"
)

type block struct {
	Type      string            `json:"type"`
	Text      string            `json:"text,omitempty"`
	Thinking  string            `json:"thinking,omitempty"`
	Signature string            `json:"signature,omitempty"`
	ID        string            `json:"id,omitempty"`
	Name      string            `json:"name,omitempty"`
	Input     json.RawMessage   `json:"input,omitempty"`
	ToolUseID string            `json:"tool_use_id,omitempty"`
	Content   []block           `json:"content,omitempty"`
	IsError   bool              `json:"is_error,omitempty"`
	Source    map[string]string `json:"source,omitempty"`
}
type turn struct {
	Role    string  `json:"role"`
	Content []block `json:"content"`
}
type replay struct {
	Model  string  `json:"model"`
	Digest string  `json:"digest"`
	Blocks []block `json:"blocks"`
}

func digest(m message.Message) string {
	m.AdditionalKwargs = nil
	b, _ := json.Marshal(m)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func (c *Client) serialize(ctx context.Context, req model.Request) ([]byte, error) {
	var msgs []turn
	system := []string{}
	if req.System != "" {
		system = append(system, req.System)
	}
	pending := map[string]bool{}
	imageBytes := 0
	for _, m := range req.Messages {
		if m.Role == message.RoleSystem {
			if m.Content != "" {
				system = append(system, m.Content)
			}
			for _, b := range m.ContentBlocks {
				if b.Type != "text" {
					return nil, fmt.Errorf("%w: unsupported system block %q", model.ErrInvalidRequest, b.Type)
				}
				if b.Text != "" {
					system = append(system, b.Text)
				}
			}
			continue
		}
		var blocks []block
		if m.Role == message.RoleAssistant {
			if len(pending) > 0 {
				return nil, fmt.Errorf("%w: unresolved tool calls", model.ErrInvalidRequest)
			}
			if v := m.AdditionalKwargs["deepseek_replay"]; v != nil {
				raw, _ := json.Marshal(v)
				var rp replay
				if json.Unmarshal(raw, &rp) == nil && rp.Model == c.modelID && rp.Digest == digest(m) {
					blocks = rp.Blocks
				}
			}
			if blocks == nil {
				if m.ReasoningContent != "" {
					blocks = append(blocks, block{Type: "thinking", Thinking: m.ReasoningContent})
				}
				if m.Content != "" {
					blocks = append(blocks, block{Type: "text", Text: m.Content})
				}
				for _, b := range m.ContentBlocks {
					switch b.Type {
					case "text":
						if b.Text != "" {
							blocks = append(blocks, block{Type: "text", Text: b.Text})
						}
					case "thinking":
						if b.Text != "" {
							blocks = append(blocks, block{Type: "thinking", Thinking: b.Text})
						}
					default:
						return nil, fmt.Errorf("%w: unsupported assistant block %q", model.ErrInvalidRequest, b.Type)
					}
				}
				for _, tc := range m.ToolCalls {
					var args map[string]json.RawMessage
					raw := tc.Arguments
					if json.Unmarshal(raw, &args) != nil || args == nil {
						raw = json.RawMessage(`{}`)
					}
					blocks = append(blocks, block{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: raw})
				}
			}
			for _, tc := range m.ToolCalls {
				if tc.ID == "" || pending[tc.ID] {
					return nil, fmt.Errorf("%w: duplicate or empty tool id", model.ErrInvalidRequest)
				}
				pending[tc.ID] = true
			}
		} else {
			if m.Role != message.RoleTool && len(pending) > 0 {
				return nil, fmt.Errorf("%w: tools require immediate results", model.ErrInvalidRequest)
			}
			if m.Content != "" {
				blocks = append(blocks, block{Type: "text", Text: m.Content})
			}
			for _, b := range m.ContentBlocks {
				switch b.Type {
				case "text":
					if b.Text != "" {
						blocks = append(blocks, block{Type: "text", Text: b.Text})
					}
				case "image":
					media := b.MimeType
					if media == "" {
						media = "image/png"
					}
					if media != "image/png" && media != "image/jpeg" && media != "image/webp" && media != "image/gif" {
						return nil, fmt.Errorf("%w: unsupported image media type %q", model.ErrInvalidRequest, media)
					}
					if len(b.Data) > 128*1024*1024*4/3+4 {
						return nil, fmt.Errorf("%w: image exceeds 128 MiB", model.ErrInvalidRequest)
					}
					data, err := base64.StdEncoding.DecodeString(b.Data)
					if err != nil {
						return nil, fmt.Errorf("%w: invalid image base64", model.ErrInvalidRequest)
					}
					source := map[string]string{"type": "base64", "media_type": media, "data": b.Data}
					if c.useFiles {
						id, err := c.imageFile(ctx, data, media)
						if err != nil {
							return nil, err
						}
						source = map[string]string{"type": "file", "file_id": id}
					} else {
						imageBytes += len(b.Data)
						if imageBytes > 20*1024*1024 {
							return nil, fmt.Errorf("%w: inline images exceed 20 MiB; enable use_files", model.ErrInvalidRequest)
						}
					}
					blocks = append(blocks, block{Type: "image", Source: source})
				default:
					return nil, fmt.Errorf("%w: unsupported content block %q", model.ErrInvalidRequest, b.Type)
				}
			}
			if m.Role == message.RoleTool {
				if !pending[m.ToolCallID] {
					return nil, fmt.Errorf("%w: tool result has no matching call", model.ErrInvalidRequest)
				}
				delete(pending, m.ToolCallID)
				if len(blocks) == 0 {
					blocks = []block{}
				}
				blocks = []block{{Type: "tool_result", ToolUseID: m.ToolCallID, Content: blocks, IsError: m.IsError}}
			}
		}
		role := string(m.Role)
		if m.Role == message.RoleTool {
			role = "user"
		}
		if role != "user" && role != "assistant" {
			return nil, fmt.Errorf("%w: unsupported role %q", model.ErrInvalidRequest, role)
		}
		if len(blocks) == 0 {
			continue
		}
		if len(msgs) > 0 && msgs[len(msgs)-1].Role == role {
			msgs[len(msgs)-1].Content = append(msgs[len(msgs)-1].Content, blocks...)
		} else {
			msgs = append(msgs, turn{Role: role, Content: blocks})
		}
	}
	if len(pending) > 0 {
		return nil, fmt.Errorf("%w: history ends with unresolved tools", model.ErrInvalidRequest)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("%w: Messages requires non-empty history", model.ErrInvalidRequest)
	}
	output := req.MaxTokens
	if output <= 0 {
		output = c.info.MaxOutputTokens
	}
	thinking := "disabled"
	if req.Thinking {
		thinking = "enabled"
	}
	payload := map[string]any{"model": c.modelID, "stream": true, "max_tokens": output, "messages": msgs, "thinking": map[string]string{"type": thinking}}
	if req.Thinking {
		effort := "high"
		if v, ok := req.ExtraBody["reasoning_effort"]; ok {
			var valid bool
			effort, valid = v.(string)
			if !valid {
				return nil, fmt.Errorf("%w: reasoning_effort must be a string", model.ErrInvalidRequest)
			}
		}
		if effort != "low" && effort != "high" && effort != "max" {
			return nil, fmt.Errorf("%w: unsupported reasoning effort %q", model.ErrInvalidRequest, effort)
		}
		payload["output_config"] = map[string]string{"effort": effort}
	}
	if len(system) > 0 {
		payload["system"] = strings.Join(system, "\n\n")
	}
	temperature := req.Temperature
	if temperature == nil {
		temperature = c.temperature
	}
	if temperature != nil {
		payload["temperature"] = *temperature
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := t.Parameters
			if len(params) == 0 {
				params = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": params})
		}
		payload["tools"] = tools
	}
	return json.Marshal(payload)
}

// Required empty protocol fields must survive omitempty on optional block data.
func (b block) MarshalJSON() ([]byte, error) {
	type plain block
	switch b.Type {
	case "text":
		return json.Marshal(struct {
			plain
			Text string `json:"text"`
		}{plain(b), b.Text})
	case "thinking":
		return json.Marshal(struct {
			plain
			Thinking string `json:"thinking"`
		}{plain(b), b.Thinking})
	case "tool_result":
		content := b.Content
		if content == nil {
			content = []block{}
		}
		return json.Marshal(struct {
			plain
			Content []block `json:"content"`
		}{plain(b), content})
	default:
		return json.Marshal(plain(b))
	}
}
