package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
)

func TestExtractThresholdAndPromptBudget(t *testing.T) {
	store := NewMemoryStore()
	m, err := New(faux.New(faux.Text(`[{"fact":"uses Go","confidence":0.95},{"fact":"maybe likes tea","confidence":0.2}]`)), store, Options{ConfidenceThreshold: .7, InjectionTokens: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Extract(context.Background(), "t", []message.Message{{Role: message.RoleUser, Content: "I use Go"}}); err != nil {
		t.Fatal(err)
	}
	prompt, err := m.Prompt(context.Background(), "t")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "uses Go") || strings.Contains(prompt, "tea") {
		t.Fatalf("prompt=%q", prompt)
	}
}
