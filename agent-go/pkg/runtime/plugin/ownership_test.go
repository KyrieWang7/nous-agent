package plugin_test

import (
	"context"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func TestStaleDisposersCannotRemoveReplacementRegistrations(t *testing.T) {
	tools := tool.NewRegistry()
	stale, err := tools.RegisterOwned(echo("replace"))
	if err != nil {
		t.Fatal(err)
	}
	stale()
	replacement, err := tools.RegisterOwned(echo("replace"))
	if err != nil {
		t.Fatal(err)
	}
	stale()
	if !tools.Has("replace") {
		t.Fatal("stale tool disposer removed replacement")
	}
	replacement()
	if len(tools.Names()) != 0 {
		t.Fatal("tool index leaked")
	}
	caps := capability.NewRegistry()
	entry := capability.Entry{Definition: capability.Definition{Name: "memory.replace", Kind: capability.KindMemory}, Resolver: func(context.Context, capability.ResolveRequest) (any, error) { return "value", nil }}
	stale, err = caps.RegisterOwned(entry)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := caps.Snapshot()
	stale()
	replacement, err = caps.RegisterOwned(entry)
	if err != nil {
		t.Fatal(err)
	}
	stale()
	if !caps.Has(entry.Name) {
		t.Fatal("stale capability disposer removed replacement")
	}
	if _, err := snapshot.Get(entry.Name); err != nil {
		t.Fatal("disposal mutated generation snapshot")
	}
	replacement()
}
