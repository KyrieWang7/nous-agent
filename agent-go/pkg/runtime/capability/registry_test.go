package capability_test

import (
	"context"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
)

func TestRegistryRegisterResolveAndSnapshot(t *testing.T) {
	r := capability.NewRegistry()
	if err := r.Register(capability.Entry{
		Definition: capability.Definition{Name: "model.default", Kind: capability.KindModel},
		Resolver: func(_ context.Context, req capability.ResolveRequest) (any, error) {
			return req.GenerationID, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(context.Background(), "model.default", capability.KindTool, capability.ResolveRequest{}); err == nil {
		t.Fatal("expected kind mismatch")
	}
	got, err := r.Resolve(context.Background(), "model.default", capability.KindModel, capability.ResolveRequest{GenerationID: "g1"})
	if err != nil || got != "g1" {
		t.Fatalf("resolve = %v, %v", got, err)
	}
	snap := r.Snapshot()
	if got := snap.Names(); len(got) != 1 || got[0] != "model.default" {
		t.Fatalf("snapshot names = %v", got)
	}
	if err := r.Register(capability.Entry{Definition: capability.Definition{Name: "tool.echo", Kind: capability.KindTool}, Resolver: func(context.Context, capability.ResolveRequest) (any, error) { return "echo", nil }}); err != nil {
		t.Fatal(err)
	}
	if len(snap.Names()) != 1 {
		t.Fatalf("snapshot changed after registry mutation: %v", snap.Names())
	}
}

func TestRegistryRejectsDuplicateAndInvalidEntries(t *testing.T) {
	r := capability.NewRegistry()
	entry := capability.Entry{Definition: capability.Definition{Name: "x", Kind: capability.KindTool}, Resolver: func(context.Context, capability.ResolveRequest) (any, error) { return true, nil }}
	if err := r.Register(entry); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(entry); err == nil {
		t.Fatal("expected duplicate error")
	}
	for _, bad := range []capability.Entry{{}, {Definition: capability.Definition{Name: "x", Kind: capability.KindTool}}} {
		if err := capability.NewRegistry().Register(bad); err == nil {
			t.Fatalf("expected invalid entry error for %#v", bad)
		}
	}
}

func TestRegisterValueAdaptsStaticDomainProvider(t *testing.T) {
	r := capability.NewRegistry()
	provider := struct{ Name string }{Name: "local-sandbox"}
	if err := capability.RegisterValue(r, capability.Value{
		Definition: capability.Definition{Name: "sandbox.local", Kind: capability.KindSandbox, Scope: capability.ScopeThread},
		Value:      provider,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(context.Background(), "sandbox.local", capability.KindSandbox, capability.ResolveRequest{}); err == nil {
		t.Fatal("thread-scoped capability resolved without thread identity")
	}
	got, err := r.Resolve(context.Background(), "sandbox.local", capability.KindSandbox, capability.ResolveRequest{ThreadID: "thread-1"})
	if err != nil || got != provider {
		t.Fatalf("resolved provider = %#v, err = %v", got, err)
	}
	if err := capability.RegisterValue(r, capability.Value{Definition: capability.Definition{Name: "memory.nil", Kind: capability.KindMemory}}); err == nil {
		t.Fatal("expected nil provider rejection")
	}
}
