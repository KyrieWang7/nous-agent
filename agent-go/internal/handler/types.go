package handler

import (
	"encoding/json"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
)

type runCreate struct {
	AssistantID  string         `json:"assistant_id"`
	Input        map[string]any `json:"input"`
	Config       map[string]any `json:"config"`
	Context      map[string]any `json:"context"`
	Metadata     map[string]any `json:"metadata"`
	OnDisconnect string         `json:"on_disconnect"`
}

type wireMessage struct {
	ID               string              `json:"id"`
	Type             string              `json:"type"`
	Content          any                 `json:"content"`
	Name             string              `json:"name,omitempty"`
	ToolCallID       string              `json:"tool_call_id,omitempty"`
	ToolCalls        []wireToolCall      `json:"tool_calls,omitempty"`
	ToolCallChunks   []wireToolCallChunk `json:"tool_call_chunks,omitempty"`
	AdditionalKwargs map[string]any      `json:"additional_kwargs"`
	ResponseMetadata map[string]any      `json:"response_metadata"`
	UsageMetadata    map[string]int      `json:"usage_metadata,omitempty"`
}

type wireToolCallChunk struct {
	Index int    `json:"index"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Args  string `json:"args,omitempty"`
	Type  string `json:"type"`
}

type wireToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
	Type string          `json:"type"`
}

func toWireMessage(m message.Message) wireMessage {
	t := string(m.Role)
	switch m.Role {
	case message.RoleUser:
		t = "human"
	case message.RoleAssistant:
		t = "ai"
	}
	w := wireMessage{
		ID:               model.NewID(),
		Type:             t,
		Content:          wireContent(m),
		Name:             m.Name,
		ToolCallID:       m.ToolCallID,
		AdditionalKwargs: map[string]any{},
		ResponseMetadata: map[string]any{},
	}
	if m.AdditionalKwargs != nil {
		for key, value := range m.AdditionalKwargs {
			w.AdditionalKwargs[key] = value
		}
	}
	if m.ReasoningContent != "" {
		w.AdditionalKwargs["reasoning_content"] = m.ReasoningContent
	}
	for _, tc := range m.ToolCalls {
		w.ToolCalls = append(w.ToolCalls, wireToolCall{ID: tc.ID, Name: tc.Name, Args: tc.Arguments, Type: "tool_call"})
	}
	return w
}

func wireContent(m message.Message) any {
	if len(m.ContentBlocks) == 0 {
		return m.Content
	}

	blocks := make([]map[string]any, 0, len(m.ContentBlocks)+1)
	if m.Content != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
	}
	for _, block := range m.ContentBlocks {
		switch block.Type {
		case "image":
			mimeType := block.MimeType
			if mimeType == "" {
				mimeType = "image/png"
			}
			blocks = append(blocks, map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					"url": "data:" + mimeType + ";base64," + block.Data,
				},
			})
		default:
			blocks = append(blocks, map[string]any{"type": "text", "text": block.Text})
		}
	}
	return blocks
}
