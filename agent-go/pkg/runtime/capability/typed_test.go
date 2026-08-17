package capability_test

import (
	"context"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
)

type typedProvider struct{ id string }

func (p typedProvider) ID() string { return p.id }

func TestResolveAsPreservesDomainTypeAndRejectsBadRegistration(t *testing.T) {
	type provider interface{ ID() string }

	registry := capability.NewRegistry()
	if err := capability.RegisterValue(registry, capability.Value{
		Definition: capability.Definition{Name: "memory.typed", Kind: capability.KindMemory},
		Value:      typedProvider{id: "typed"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := capability.ResolveAs[provider](context.Background(), registry.Snapshot(), "memory.typed", capability.KindMemory, capability.ResolveRequest{})
	if err != nil || got.ID() != "typed" {
		t.Fatalf("ResolveAs() = %#v, %v", got, err)
	}

	if err := capability.RegisterValue(registry, capability.Value{
		Definition: capability.Definition{Name: "memory.default", Kind: capability.KindMemory},
		Value:      struct{ Name string }{Name: "wrong type"},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = capability.ResolveAs[provider](context.Background(), registry.Snapshot(), "memory.default", capability.KindMemory, capability.ResolveRequest{})
	if err == nil || !strings.Contains(err.Error(), "requested domain type") {
		t.Fatalf("ResolveAs() error = %v, want domain type mismatch", err)
	}
}
