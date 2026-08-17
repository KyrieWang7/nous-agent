package handlers

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

const ClarificationToolName = "ask_clarification"

type Clarification struct{}

func NewClarification() *Clarification { return &Clarification{} }
func (Clarification) Name() string     { return lifecycle.TerminalName }
func (Clarification) BeforeTool(_ context.Context, st *lifecycle.State) (tool.Decision, error) {
	if st.ToolCall == nil || st.ToolCall.Name != ClarificationToolName {
		return tool.Decision{}, nil
	}
	var args struct {
		Question string `json:"question"`
	}
	_ = json.Unmarshal(st.ToolCall.Args, &args)
	question := strings.TrimSpace(args.Question)
	if question == "" {
		question = "Please clarify the missing information before I continue."
	}
	return tool.Decision{EndTurn: true, Reason: question}, nil
}
func ClarificationTool() tool.Definition {
	return tool.Definition{Name: ClarificationToolName, Group: "interaction", Description: "Ask the user one focused question when required information is missing, then end this turn.", Parameters: json.RawMessage(`{"type":"object","properties":{"question":{"type":"string"}},"required":["question"]}`), Metadata: tool.Metadata{IsReadOnly: true}, Handler: func(context.Context, tool.Call) (*tool.Result, error) {
		return &tool.Result{Content: "clarification requested"}, nil
	}}
}
