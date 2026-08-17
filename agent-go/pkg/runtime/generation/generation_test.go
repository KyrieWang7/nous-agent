package generation_test

import (
	"context"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/generation"
)

func TestManagerPinsGenerationAcrossInstall(t *testing.T) {
	r := capability.NewRegistry()
	if err := r.Register(capability.Entry{Definition: capability.Definition{Name: "model.default", Kind: capability.KindModel}, Resolver: func(context.Context, capability.ResolveRequest) (any, error) { return "model", nil }}); err != nil {
		t.Fatal(err)
	}
	g1, err := generation.New("g1", r)
	if err != nil {
		t.Fatal(err)
	}
	m, err := generation.NewManager(g1)
	if err != nil {
		t.Fatal(err)
	}
	l, err := m.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	g2, err := generation.New("g2", capability.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Install(g2); err != nil {
		t.Fatal(err)
	}
	if l.Generation().ID() != "g1" {
		t.Fatalf("lease generation = %q, want g1", l.Generation().ID())
	}
	if got := m.Stats(); got.CurrentID != "g2" || got.Retired != 1 {
		t.Fatalf("stats before release = %+v", got)
	}
	l.Release()
	if got := m.Stats(); got.Retired != 0 {
		t.Fatalf("retired generation was retained after its last lease: %+v", got)
	}
	if _, err := m.Acquire(); err != nil {
		t.Fatal(err)
	}
}
