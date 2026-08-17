package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/guardrail"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
)

const NameGuardrailInput = "guardrailInput"
const NameGuardrailOutput = "guardrailOutput"
const defaultBlockedResponse = "I cannot help with that request. I can help with a safer alternative."

type GuardrailInput struct{ evaluator *guardrail.Evaluator }

func NewGuardrailInput(e *guardrail.Evaluator) *GuardrailInput { return &GuardrailInput{evaluator: e} }
func (GuardrailInput) Name() string                            { return NameGuardrailInput }
func (g *GuardrailInput) BeforeModel(ctx context.Context, st *lifecycle.State) error {
	if g.evaluator == nil {
		return nil
	}
	if st.History == nil {
		return nil
	}
	text := lastUserPrompt(st.History.All())
	decision, err := g.evaluator.Evaluate(ctx, guardrail.DirectionInput, text)
	if err != nil {
		return err
	}
	st.RiskLevel = decisionRiskLevel(decision)
	st.GuardrailAction = string(decision.Action)
	if decision.Action != guardrail.ActionBlock {
		return nil
	}
	replacement := strings.TrimSpace(decision.Replacement)
	if replacement == "" {
		replacement = defaultBlockedResponse
	}
	msg := message.Message{Role: message.RoleAssistant, Content: replacement}
	st.History.Append(msg)
	st.ModelOutput = &model.Response{Message: msg, StopReason: model.StopReasonContentFilter}
	st.Directive = lifecycle.DirectiveStop
	return publishRuntime(ctx, st, runtime.EventGuardrailBlock, decision)
}

type GuardrailOutput struct{ evaluator *guardrail.Evaluator }

func NewGuardrailOutput(e *guardrail.Evaluator) *GuardrailOutput {
	return &GuardrailOutput{evaluator: e}
}
func (GuardrailOutput) Name() string { return NameGuardrailOutput }
func (g *GuardrailOutput) AfterModel(ctx context.Context, st *lifecycle.State) error {
	if g.evaluator == nil || st.ModelOutput == nil {
		return nil
	}
	decision, err := g.evaluator.Evaluate(ctx, guardrail.DirectionOutput, st.ModelOutput.Message.Content)
	if err != nil {
		return err
	}
	st.RiskLevel = decisionRiskLevel(decision)
	st.GuardrailAction = string(decision.Action)
	if decision.Action != guardrail.ActionBlock {
		return nil
	}
	replacement := strings.TrimSpace(decision.Replacement)
	if replacement == "" {
		replacement = defaultBlockedResponse
	}
	st.ModelOutput.Message.Content = replacement
	st.ModelOutput.Message.ToolCalls = nil
	if st.History != nil {
		st.History.ReplaceLastAssistant(st.ModelOutput.Message)
	}
	if err := publishRuntime(ctx, st, runtime.EventGuardrailBlock, decision); err != nil {
		return err
	}
	if st.Streamed && st.OriginalOutput != replacement {
		if err := publishRuntime(ctx, st, runtime.EventMessageReplace, runtime.MessageReplace{
			Content:   replacement,
			MessageID: fmt.Sprintf("%s:%d", st.RunID, st.Iteration),
			Reason:    decision.Reason,
		}); err != nil {
			return err
		}
	}
	return nil
}

func decisionRiskLevel(decision guardrail.Decision) string {
	if level := strings.TrimSpace(decision.RiskLevel); level != "" {
		return strings.ToLower(level)
	}
	return string(decision.Action)
}
