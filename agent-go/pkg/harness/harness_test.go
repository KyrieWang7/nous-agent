package harness_test

import (
	"context"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/harness"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	runtimeplugin "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/plugin"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

type runtimeToolPlugin struct{}

func (runtimeToolPlugin) Name() string           { return "test.runtime-tool" }
func (runtimeToolPlugin) Dependencies() []string { return nil }
func (runtimeToolPlugin) Start(_ context.Context, host runtimeplugin.Host) error {
	return host.Tools.Register(tool.Definition{
		Name:        "plugin_echo",
		Group:       "test",
		Description: "registered during runtime plugin startup",
		Handler: func(context.Context, tool.Call) (*tool.Result, error) {
			return &tool.Result{Content: "ok"}, nil
		},
	})
}
func (runtimeToolPlugin) Stop(context.Context) error { return nil }

func TestHarnessPublishesStaticDomainCapabilities(t *testing.T) {
	provider := struct{ Name string }{Name: "thread-memory"}
	h, err := harness.New(harness.Options{
		Model: faux.New(faux.Text("ok")),
		CapabilityValues: []capability.Value{{
			Definition: capability.Definition{Name: "memory.thread", Kind: capability.KindMemory, Scope: capability.ScopeThread},
			Value:      provider,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	got, err := h.ResolveCapability(context.Background(), capability.KindMemory, "memory.thread", capability.ResolveRequest{ThreadID: "thread-1"})
	if err != nil || got != provider {
		t.Fatalf("resolved capability = %#v, err = %v", got, err)
	}
}

func TestHarnessFreezesToolsRegisteredByRuntimePlugins(t *testing.T) {
	h, err := harness.New(harness.Options{
		Model:   faux.New(faux.Text("ok")),
		Plugins: []runtimeplugin.Plugin{runtimeToolPlugin{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()

	resolved, err := h.ResolveCapability(context.Background(), capability.KindTool, "tool.plugin_echo", capability.ResolveRequest{RunID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	definition, ok := resolved.(tool.Definition)
	if !ok || definition.Name != "plugin_echo" {
		t.Fatalf("resolved plugin tool = %#v", resolved)
	}
}
