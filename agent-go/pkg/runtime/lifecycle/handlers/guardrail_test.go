package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/guardrail"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
)

func blocker(t *testing.T) *guardrail.Evaluator {
	t.Helper()
	e, err := guardrail.New(guardrail.ProviderFunc(func(context.Context, guardrail.Direction, string) (guardrail.Decision, error) {
		return guardrail.Decision{Action: guardrail.ActionBlock, Replacement: "blocked", RiskLevel: "high"}, nil
	}), true)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func TestGuardrailInputStopsBeforeModelWithAssistantMessage(t *testing.T) {
	h := message.NewHistory()
	h.Append(message.Message{Role: message.RoleUser, Content: "bad"})
	st := lifecycle.NewState(lifecycle.StateInit{History: h})
	if err := NewGuardrailInput(blocker(t)).BeforeModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if st.Directive != lifecycle.DirectiveStop || st.ModelOutput.Message.Content != "blocked" || h.All()[1].Role != message.RoleAssistant {
		t.Fatalf("state=%#v history=%#v", st, h.All())
	}
}
func TestGuardrailOutputRewritesTranscript(t *testing.T) {
	h := message.NewHistory()
	h.Append(message.Message{Role: message.RoleAssistant, Content: "unsafe"})
	st := lifecycle.NewState(lifecycle.StateInit{History: h})
	st.ModelOutput = &model.Response{Message: message.Message{Role: message.RoleAssistant, Content: "unsafe"}}
	if err := NewGuardrailOutput(blocker(t)).AfterModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if st.ModelOutput.Message.Content != "blocked" || h.All()[0].Content != "blocked" {
		t.Fatalf("output=%#v history=%#v", st.ModelOutput, h.All())
	}
}

func TestGuardrailUsesActionWhenProviderOmitsRiskLevel(t *testing.T) {
	evaluator, err := guardrail.New(guardrail.ProviderFunc(func(context.Context, guardrail.Direction, string) (guardrail.Decision, error) {
		return guardrail.Decision{Action: guardrail.ActionBlock, Replacement: "blocked"}, nil
	}), true)
	if err != nil {
		t.Fatal(err)
	}
	st := lifecycle.NewState(lifecycle.StateInit{History: message.NewHistory()})
	st.ModelOutput = &model.Response{Message: message.Message{Role: message.RoleAssistant, Content: "unsafe"}}
	if err := NewGuardrailOutput(evaluator).AfterModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if st.RiskLevel != "block" {
		t.Fatalf("risk level = %q, want block", st.RiskLevel)
	}
}

func TestGuardrailOutputPublishesReplacementAfterStreaming(t *testing.T) {
	h := message.NewHistory()
	h.Append(message.Message{Role: message.RoleAssistant, Content: "unsafe"})
	st := lifecycle.NewState(lifecycle.StateInit{History: h, RunID: "run-1", ThreadID: "thread-1"})
	st.ModelOutput = &model.Response{Message: message.Message{Role: message.RoleAssistant, Content: "unsafe"}}
	st.Streamed = true
	st.OriginalOutput = "unsafe"
	var events []runtime.Event
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{
		RunID: "run-1", ThreadID: "thread-1",
		Publish: func(_ context.Context, event runtime.Event) (int64, error) {
			events = append(events, event)
			return int64(len(events)), nil
		},
	})
	if err := NewGuardrailOutput(blocker(t)).AfterModel(ctx, st); err != nil {
		t.Fatal(err)
	}
	if st.RiskLevel != "high" {
		t.Fatalf("risk level = %q", st.RiskLevel)
	}
	if _, exists := st.Value("risk_level"); exists {
		t.Fatal("risk level leaked into thread Values")
	}
	if len(events) != 2 || events[0].Type != runtime.EventGuardrailBlock || events[1].Type != runtime.EventMessageReplace {
		t.Fatalf("events = %#v", events)
	}
	var replacement runtime.MessageReplace
	if err := json.Unmarshal(events[1].Data, &replacement); err != nil {
		t.Fatal(err)
	}
	if replacement.MessageID != "run-1:0" {
		t.Fatalf("replacement message_id = %q", replacement.MessageID)
	}
}
