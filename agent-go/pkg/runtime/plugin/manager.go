// Package plugin implements DSH-style service injection and scoped Apply effects
// for one immutable Go runtime generation.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/scope"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// Host is available only to the composition root. Plugins receive scoped ports.
type Host struct {
	Capabilities *capability.Registry
	Tools        *tool.Registry
	GenerationID string
}

// Plugin follows the DSH name/inject/apply contract. Apply registers effects;
// cleanup is attached immediately through Context.OnDispose, including during
// setup that can fail. There is no separate, manually paired Stop method.
type Plugin interface {
	Name() string
	Inject() []string
	Apply(*Context) error
}

// Optional keeps a plugin pending when its injected services are unavailable.
// Required plugins instead fail startup after all owned effects are rolled back.
type Optional struct{ Plugin }

func (Optional) IsOptional() bool { return true }

type entry struct {
	plugin   Plugin
	name     string
	inject   []string
	optional bool
}

// Manager owns a complete generation. A changed plugin config is assembled as
// a new manager before publication; the old manager closes after its run leases
// drain. In-place service replacement is deliberately not exposed here.
type Manager struct {
	mu        sync.Mutex
	entries   []entry
	names     []string
	pending   map[string][]string
	started   bool
	closed    bool
	startDone chan struct{}
	cancel    context.CancelFunc
	owner     *scope.Scope
	stopOnce  sync.Once
	stopErr   error
}

func NewManager(plugins []Plugin) (*Manager, error) {
	m := &Manager{pending: make(map[string][]string)}
	seen := make(map[string]bool)
	for i, p := range plugins {
		if p == nil {
			return nil, fmt.Errorf("plugin: entry %d is nil", i)
		}
		name := p.Name()
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("plugin: entry %d has an empty name", i)
		}
		if seen[name] {
			return nil, fmt.Errorf("plugin: %q is registered more than once", name)
		}
		seen[name] = true
		deps := slices.Clone(p.Inject())
		for _, dep := range deps {
			if strings.TrimSpace(dep) == "" {
				return nil, fmt.Errorf("plugin %q: empty injected service", name)
			}
		}
		optional, ok := p.(interface{ IsOptional() bool })
		m.entries = append(m.entries, entry{plugin: p, name: name, inject: deps, optional: ok && optional.IsOptional()})
	}
	return m, nil
}

// Names returns the successfully activated plugin order.
func (m *Manager) Names() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.names)
}

// Pending reports optional plugins waiting for services, with defensive copies.
func (m *Manager) Pending() map[string][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string][]string, len(m.pending))
	for name, missing := range m.pending {
		result[name] = slices.Clone(missing)
	}
	return result
}

func (m *Manager) Start(ctx context.Context, host Host) (err error) {
	m.mu.Lock()
	if m.started || m.closed {
		m.mu.Unlock()
		return errors.New("plugin: manager already started or closed")
	}
	m.started = true
	m.startDone = make(chan struct{})
	lifetime, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.owner = scope.New(lifetime)
	m.mu.Unlock()
	defer func() { //nolint:contextcheck // rollback uses the plugin generation owner, not a request lifetime
		if err != nil {
			err = errors.Join(err, m.dispose())
		}
		close(m.startDone)
	}()

	services := make(map[string]any)
	// Marker values prevent plugins bypassing ownership with raw registry writes.
	if host.Tools != nil {
		services[ToolService] = struct{}{}
	}
	if host.Capabilities != nil {
		services[CapabilityService] = struct{}{}
	}
	remaining := slices.Clone(m.entries)
	for len(remaining) > 0 {
		progress := false
		next := make([]entry, 0, len(remaining))
		for _, e := range remaining {
			if err := lifetime.Err(); err != nil {
				return err
			}
			missing := missingServices(e.inject, services)
			if len(missing) > 0 {
				next = append(next, e)
				continue
			}
			owner, err := m.owner.Child()
			if err != nil {
				return err
			}
			injected := make(map[string]any, len(e.inject))
			for _, name := range e.inject {
				injected[name] = services[name]
			}
			scoped := &Context{Context: owner.Context(), GenerationID: host.GenerationID, owner: owner, host: host, services: injected, provided: make(map[string]any)}
			applyErr := apply(e.plugin, scoped)
			provided := scoped.seal()
			if applyErr != nil {
				return fmt.Errorf("plugin %q: apply: %w", e.name, applyErr)
			}
			if err := lifetime.Err(); err != nil {
				return err
			}
			// Validate every new service before publishing any of them.
			for name := range provided {
				if _, exists := services[name]; exists {
					return fmt.Errorf("plugin %q: service %q already provided", e.name, name)
				}
			}
			for name, value := range provided {
				services[name] = value
			}
			m.mu.Lock()
			m.names = append(m.names, e.name)
			m.mu.Unlock()
			progress = true
		}
		remaining = next
		if !progress {
			break
		}
	}
	var unresolved []error
	m.mu.Lock()
	for _, e := range remaining {
		missing := missingServices(e.inject, services)
		m.pending[e.name] = missing
		if !e.optional {
			unresolved = append(unresolved, fmt.Errorf("plugin %q: missing services %v (or service dependency cycle)", e.name, missing))
		}
	}
	m.mu.Unlock()
	if err := errors.Join(unresolved...); err != nil {
		return err
	}
	if err := lifetime.Err(); err != nil {
		return err
	}
	return m.publishToolCatalog(host)
}

func missingServices(names []string, services map[string]any) []string {
	var missing []string
	for _, name := range names {
		if _, ok := services[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// publishToolCatalog freezes all tool contributions after service activation.
// Its effects share the generation owner and are removed before plugin cleanup.
func (m *Manager) publishToolCatalog(host Host) error {
	if host.Tools == nil || host.Capabilities == nil {
		return nil
	}
	for _, name := range host.Tools.Names() {
		capabilityName := "tool." + name
		if host.Capabilities.Has(capabilityName) {
			continue
		}
		definition, err := host.Tools.Get(name)
		if err != nil {
			return err
		}
		err = m.owner.Own(func() (scope.Cleanup, error) {
			dispose, err := host.Capabilities.RegisterOwned(capability.Entry{
				Definition: capability.Definition{Name: capabilityName, Kind: capability.KindTool, Description: definition.Description, Scope: capability.ScopeRun},
				Resolver:   func(context.Context, capability.ResolveRequest) (any, error) { return definition, nil },
			})
			if err != nil {
				return nil, err
			}
			return func(context.Context) error { dispose(); return nil }, nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Stop cancels setup if necessary, then waits for all effects and owned workers.
// The caller's canceled context cannot abandon cleanup halfway through.
func (m *Manager) Stop(context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.closed = true
	done, cancel := m.startDone, m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	return m.dispose() //nolint:contextcheck // disposal drains the captured generation owner
}

func (m *Manager) dispose() error {
	m.stopOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		owner := m.owner
		m.mu.Unlock()
		if owner != nil {
			m.stopErr = owner.Dispose()
		}
	})
	return m.stopErr
}

func apply(p Plugin, ctx *Context) (err error) {
	defer func() {
		if cause := recover(); cause != nil {
			err = fmt.Errorf("apply panic: %v", cause)
		}
	}()
	return p.Apply(ctx)
}
