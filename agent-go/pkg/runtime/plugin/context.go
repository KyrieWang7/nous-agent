package plugin

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/scope"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

const (
	ToolService       = "tools"
	CapabilityService = "capabilities"
)

// Context is an Apply-time registration surface and a lifetime owner. Plugins
// declare services in Inject, never dependencies on other plugin names.
// Registrations are frozen before the generation is published; resources and
// workers remain owned by the scope until the generation drains and closes.
type Context struct {
	context.Context
	GenerationID string
	owner        *scope.Scope
	host         Host
	services     map[string]any
	mu           sync.Mutex
	sealed       bool
	provided     map[string]any
}

// Service resolves only a declared injection. The two registry services are
// accessed through RegisterTool/RegisterCapability rather than raw registries.
func (c *Context) Service(name string) (any, error) {
	value, ok := c.services[name]
	if !ok {
		return nil, fmt.Errorf("plugin: service %q was not injected", name)
	}
	return value, nil
}

// Provide stages a service. It becomes visible to other plugins only after
// Apply succeeds, so consumers never see a partially initialized provider.
func (c *Context) Provide(name string, value any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sealed {
		return errors.New("plugin: Apply has completed")
	}
	if name == "" || value == nil {
		return errors.New("plugin: service requires a name and value")
	}
	if _, ok := c.provided[name]; ok {
		return fmt.Errorf("plugin: service %q already provided", name)
	}
	c.provided[name] = value
	return nil
}

func (c *Context) RegisterTool(def tool.Definition) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sealed {
		return errors.New("plugin: Apply has completed")
	}
	if _, err := c.Service(ToolService); err != nil {
		return err
	}
	return c.owner.Own(func() (scope.Cleanup, error) {
		dispose, err := c.host.Tools.RegisterOwned(def)
		if err != nil {
			return nil, err
		}
		return func(context.Context) error { dispose(); return nil }, nil
	})
}

func (c *Context) RegisterCapability(entry capability.Entry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sealed {
		return errors.New("plugin: Apply has completed")
	}
	if _, err := c.Service(CapabilityService); err != nil {
		return err
	}
	return c.owner.Own(func() (scope.Cleanup, error) {
		dispose, err := c.host.Capabilities.RegisterOwned(entry)
		if err != nil {
			return nil, err
		}
		return func(context.Context) error { dispose(); return nil }, nil
	})
}

func (c *Context) OnDispose(cleanup scope.Cleanup) error { return c.owner.OnDispose(cleanup) }
func (c *Context) Go(work func(context.Context)) error   { return c.owner.Go(work) }

func (c *Context) seal() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sealed = true
	return c.provided
}
