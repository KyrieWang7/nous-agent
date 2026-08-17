// Package plugin provides explicit Runtime plugin lifecycle management.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// Host is the narrow surface exposed to plugins during startup.
type Host struct {
	Capabilities *capability.Registry
	Tools        *tool.Registry
	GenerationID string
}

// Plugin is a runtime assembly unit. Plugins register capabilities or
// observers during Start and release all owned resources during Stop.
type Plugin interface {
	Name() string
	Dependencies() []string
	Start(context.Context, Host) error
	Stop(context.Context) error
}

// Manager validates, starts and stops a dependency-ordered plugin set.
type Manager struct {
	mu      sync.Mutex
	plugins map[string]Plugin
	order   []Plugin
	started bool
}

// NewManager validates plugin names and dependency references, but does not
// start anything until Start is called.
func NewManager(plugins []Plugin) (*Manager, error) {
	m := &Manager{plugins: make(map[string]Plugin, len(plugins))}
	for i, p := range plugins {
		if p == nil {
			return nil, fmt.Errorf("plugin: entry %d is nil", i)
		}
		name := p.Name()
		if name == "" {
			return nil, fmt.Errorf("plugin: entry %d has an empty name", i)
		}
		if _, exists := m.plugins[name]; exists {
			return nil, fmt.Errorf("plugin: %q is registered more than once", name)
		}
		m.plugins[name] = p
	}
	order, err := topoOrder(m.plugins)
	if err != nil {
		return nil, err
	}
	m.order = order
	return m, nil
}

// Names returns the resolved start order.
func (m *Manager) Names() []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.order))
	for _, p := range m.order {
		out = append(out, p.Name())
	}
	return out
}

// Start starts all plugins in dependency order. A partial start is rolled
// back in reverse order if a later plugin fails.
func (m *Manager) Start(ctx context.Context, host Host) error {
	if m == nil {
		return errors.New("plugin: manager is nil")
	}
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return errors.New("plugin: manager already started")
	}
	m.mu.Unlock()

	var started []Plugin
	for _, p := range m.order {
		if err := p.Start(ctx, host); err != nil {
			_ = stopReverse(context.WithoutCancel(ctx), started)
			return fmt.Errorf("plugin %q: start: %w", p.Name(), err)
		}
		started = append(started, p)
	}
	m.mu.Lock()
	m.started = true
	m.mu.Unlock()
	return nil
}

// Stop stops started plugins in reverse dependency order. It is idempotent.
func (m *Manager) Stop(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return nil
	}
	m.started = false
	m.mu.Unlock()
	return stopReverse(ctx, m.order)
}

func stopReverse(ctx context.Context, plugins []Plugin) error {
	var errs []error
	for i := len(plugins) - 1; i >= 0; i-- {
		if err := plugins[i].Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("plugin %q: stop: %w", plugins[i].Name(), err))
		}
	}
	return errors.Join(errs...)
}

func topoOrder(plugins map[string]Plugin) ([]Plugin, error) {
	state := make(map[string]uint8, len(plugins))
	var order []Plugin
	var visit func(string) error
	visit = func(name string) error {
		switch state[name] {
		case 1:
			return fmt.Errorf("plugin: dependency cycle at %q", name)
		case 2:
			return nil
		}
		p, ok := plugins[name]
		if !ok {
			return fmt.Errorf("plugin: dependency %q is not registered", name)
		}
		state[name] = 1
		deps := slices.Clone(p.Dependencies())
		slices.Sort(deps)
		for _, dep := range deps {
			if dep == name {
				return fmt.Errorf("plugin: %q depends on itself", name)
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[name] = 2
		order = append(order, p)
		return nil
	}

	names := make([]string, 0, len(plugins))
	for name := range plugins {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return order, nil
}
