// Package assembly builds the capability/plugin/generation spine used by a
// Harness. It intentionally does not own the Agent Loop.
package assembly

import (
	"context"
	"errors"
	"fmt"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/generation"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/plugin"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// Options configures one runtime assembly.
type Options struct {
	GenerationID string
	Capabilities *capability.Registry
	Plugins      []plugin.Plugin
	Tools        *tool.Registry
}

// Runtime is an assembled, immutable capability generation plus its plugin
// lifecycle owner.
type Runtime struct {
	Generation *generation.Generation
	Plugins    *plugin.Manager
}

// New validates and starts plugins before publishing a generation snapshot.
func New(ctx context.Context, opts Options) (*Runtime, error) {
	registry := opts.Capabilities
	if registry == nil {
		registry = capability.NewRegistry()
	}
	id := opts.GenerationID
	if id == "" {
		return nil, errors.New("assembly: generation id must not be empty")
	}
	plugins, err := plugin.NewManager(opts.Plugins)
	if err != nil {
		return nil, err
	}
	if err := plugins.Start(ctx, plugin.Host{Capabilities: registry, Tools: opts.Tools, GenerationID: id}); err != nil {
		return nil, err
	}
	gen, err := generation.New(id, registry)
	if err != nil {
		_ = plugins.Stop(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("assembly: creating generation: %w", err)
	}
	return &Runtime{Generation: gen, Plugins: plugins}, nil
}

// Close stops runtime plugins. It is safe to call more than once.
func (r *Runtime) Close(ctx context.Context) error {
	if r == nil || r.Plugins == nil {
		return nil
	}
	return r.Plugins.Stop(ctx)
}
