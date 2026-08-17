package agentregistry_test

import (
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/agentregistry"
)

func TestRegistryDefensiveCopyAndStableNames(t *testing.T) {
	r := agentregistry.New()
	tools := []string{"write", "read"}
	if err := r.Register(agentregistry.Definition{Name: "general", Description: "general purpose", AllowedTools: tools, MaxTurns: 20}); err != nil {
		t.Fatal(err)
	}
	tools[0] = "mutated"
	got, err := r.Get("general")
	if err != nil {
		t.Fatal(err)
	}
	if got.AllowedTools[0] != "read" || got.AllowedTools[1] != "write" {
		t.Fatalf("tools = %v", got.AllowedTools)
	}
	if err := r.Register(agentregistry.Definition{Name: "general", Description: "duplicate"}); err == nil {
		t.Fatal("expected duplicate error")
	}
}
