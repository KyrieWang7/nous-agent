package capability_test

import (
	"context"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
)

func TestCapabilityViewCanOnlyRestrictParent(t *testing.T) {
	registry := capability.NewRegistry()
	for _, name := range []string{"model.default", "tool.read", "tool.write"} {
		kind := capability.KindTool
		if strings.HasPrefix(name, "model.") {
			kind = capability.KindModel
		}
		value := name
		if err := capability.RegisterValue(registry, capability.Value{
			Definition: capability.Definition{Name: name, Kind: kind}, Value: value,
		}); err != nil {
			t.Fatal(err)
		}
	}
	parent, err := capability.NewView(registry.Snapshot(), []string{"model.default", "tool.read"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := parent.Restrict([]string{"tool.read"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := child.Resolve(context.Background(), "model.default", capability.KindModel, capability.ResolveRequest{}); err == nil {
		t.Fatal("restricted capability remained resolvable")
	}
	if _, err := parent.Restrict([]string{"model.default", "tool.read", "tool.write"}); err == nil {
		t.Fatal("child view widened the parent allowlist")
	}
}
