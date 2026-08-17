package memory

import (
	"context"
	"fmt"
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
	scope := Scope{UserID: "u", ProjectID: "p", ThreadID: "t"}
	if err := m.Extract(context.Background(), scope, []message.Message{{Role: message.RoleUser, Content: "I use Go"}}); err != nil {
		t.Fatal(err)
	}
	prompt, err := m.Prompt(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "uses Go") || strings.Contains(prompt, "tea") {
		t.Fatalf("prompt=%q", prompt)
	}
}

func TestMemoryStoreIsolatesUserProjectAndThreadScopes(t *testing.T) {
	store := NewMemoryStore()
	scopes := []Scope{
		{UserID: "u1", ProjectID: "p1", ThreadID: "t1"},
		{UserID: "u2", ProjectID: "p1", ThreadID: "t1"},
		{UserID: "u1", ProjectID: "p2", ThreadID: "t1"},
		{UserID: "u1", ProjectID: "p1", ThreadID: "t2"},
	}
	for i, scope := range scopes {
		if err := store.Add(context.Background(), Fact{
			UserID: scope.UserID, ProjectID: scope.ProjectID, ThreadID: scope.ThreadID,
			Text: fmt.Sprintf("fact-%d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i, scope := range scopes {
		facts, err := store.List(context.Background(), scope, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(facts) != 1 || facts[0].Text != fmt.Sprintf("fact-%d", i) {
			t.Fatalf("scope %#v returned %#v", scope, facts)
		}
	}
}
