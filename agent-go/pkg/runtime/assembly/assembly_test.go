package assembly_test

import (
	"context"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/assembly"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/plugin"
)

type registeringPlugin struct{}

func (registeringPlugin) Name() string           { return "registering" }
func (registeringPlugin) Dependencies() []string { return nil }
func (registeringPlugin) Start(_ context.Context, host plugin.Host) error {
	return host.Capabilities.Register(capability.Entry{
		Definition: capability.Definition{Name: "tool.echo", Kind: capability.KindTool},
		Resolver:   func(context.Context, capability.ResolveRequest) (any, error) { return "echo", nil },
	})
}
func (registeringPlugin) Stop(context.Context) error { return nil }

func TestNewPublishesPluginCapabilitiesInGeneration(t *testing.T) {
	r, err := assembly.New(context.Background(), assembly.Options{GenerationID: "g1", Plugins: []plugin.Plugin{registeringPlugin{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(context.Background()) }()
	if _, err := r.Generation.Capabilities().Get("tool.echo"); err != nil {
		t.Fatal(err)
	}
}
