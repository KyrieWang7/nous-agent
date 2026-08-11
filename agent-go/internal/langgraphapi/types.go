package langgraphapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

// Agent is the narrow bridge between the LangGraph wire adapter and the
// harness. Implementations own model/tool execution; the adapter owns HTTP,
// run lifecycle, history persistence and SSE framing.
type Agent interface {
	Run(context.Context, AgentRequest) (AgentResult, error)
}

// RunPreparer lets a dynamic assembly pin one coherent generation for a run.
// Agent, tool disclosure, and pricing must come from the same generation.
type RunPreparer interface {
	PrepareRun() (PreparedRun, error)
}

type PreparedRun struct {
	Agent        Agent
	AllowedTools []string
	Pricer       *runtime.Pricer
	Release      func()
}

type AgentRequest struct {
	RunID         string
	ThreadID      string
	AssistantID   string
	Prompt        string
	ContentBlocks []message.ContentBlock
	History       []message.Message
	Config        map[string]any
	Context       map[string]any
}

type AgentResult struct {
	Messages []message.Message
	// Transcript is the complete active history after compaction. It is nil for
	// append-only turns; persistence must use it verbatim when Compacted is true.
	Transcript []message.Message
	Output     string
	Iterations int
	Usage      Usage
	Compacted  bool
	Streamed   bool
	Values     map[string]any
	RiskLevel  string
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type Thread struct {
	ThreadID string         `json:"thread_id"`
	Created  time.Time      `json:"created_at"`
	Updated  time.Time      `json:"updated_at"`
	Metadata map[string]any `json:"metadata"`
	Status   string         `json:"status"`
	Values   map[string]any `json:"values"`
	Config   map[string]any `json:"config"`
}

type Run struct {
	RunID        string         `json:"run_id"`
	ThreadID     string         `json:"thread_id"`
	AssistantID  string         `json:"assistant_id"`
	Status       string         `json:"status"`
	Created      time.Time      `json:"created_at"`
	Completed    *time.Time     `json:"completed_at,omitempty"`
	Metadata     map[string]any `json:"metadata"`
	OnDisconnect string         `json:"on_disconnect"`
	RiskLevel    string         `json:"risk_level,omitempty"`
}

type RunUpdate struct {
	Status    string
	RiskLevel string
}

type RunCompletion struct {
	RunID             string
	ThreadID          string
	Status            string
	Iterations        int
	LLMCalls          int
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
	LeadTokens        int
	SubagentTokens    int
	MiddlewareTokens  int
	CostMicros        int64
	Duration          time.Duration
	CompletedAt       time.Time
}

type runCreate struct {
	AssistantID    string         `json:"assistant_id"`
	Input          map[string]any `json:"input"`
	Config         map[string]any `json:"config"`
	Context        map[string]any `json:"context"`
	Command        map[string]any `json:"command"`
	Metadata       map[string]any `json:"metadata"`
	StreamMode     []string       `json:"stream_mode"`
	StreamSubgraph bool           `json:"stream_subgraphs"`
	OnDisconnect   string         `json:"on_disconnect"`
}

type wireMessage struct {
	ID               string         `json:"id"`
	Type             string         `json:"type"`
	Content          any            `json:"content"`
	Name             string         `json:"name,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	ToolCalls        []wireToolCall `json:"tool_calls,omitempty"`
	AdditionalKwargs map[string]any `json:"additional_kwargs"`
	ResponseMetadata map[string]any `json:"response_metadata"`
	UsageMetadata    map[string]int `json:"usage_metadata,omitempty"`
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
		ID:               newID(),
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
	// Agent implementations that predate the structured task contract may
	// still return only the legacy result prefix. Normalize at the wire edge as
	// a final compatibility guard; the transcript boundary performs the same
	// normalization before persistence.
	if m.Role == message.RoleTool && m.Name == "task" {
		for key, value := range message.StampSubagentStatus(m).AdditionalKwargs {
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
