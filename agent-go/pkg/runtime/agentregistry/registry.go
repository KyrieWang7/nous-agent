// Package agentregistry provides the typed definition registry for named
// agents. Concrete runner construction remains owned by the harness/runtime.
package agentregistry

import (
	"errors"
	"fmt"
	"slices"
	"sync"
)

// Definition describes a named agent profile without binding it to a loop
// implementation. Tool lists and limits can only be narrowed at dispatch.
type Definition struct {
	Name         string
	Description  string
	SystemPrompt string
	ModelName    string
	AllowedTools []string
	MaxTurns     int
}

// Registry stores immutable-by-convention named agent definitions.
type Registry struct {
	mu   sync.RWMutex
	defs map[string]Definition
}

// New returns an empty registry.
func New() *Registry { return &Registry{defs: make(map[string]Definition)} }

// Register adds one definition and rejects duplicate names.
func (r *Registry) Register(def Definition) error {
	if r == nil {
		return errors.New("agentregistry: registry is nil")
	}
	if def.Name == "" {
		return errors.New("agentregistry: name must not be empty")
	}
	if def.Description == "" {
		return fmt.Errorf("agentregistry: %q description must not be empty", def.Name)
	}
	if def.MaxTurns < 0 {
		return fmt.Errorf("agentregistry: %q max turns must not be negative", def.Name)
	}
	def.AllowedTools = slices.Clone(def.AllowedTools)
	slices.Sort(def.AllowedTools)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.defs[def.Name]; ok {
		return fmt.Errorf("agentregistry: %q already registered", def.Name)
	}
	r.defs[def.Name] = def
	return nil
}

// Get returns a defensive copy.
func (r *Registry) Get(name string) (Definition, error) {
	if r == nil {
		return Definition{}, errors.New("agentregistry: registry is nil")
	}
	r.mu.RLock()
	def, ok := r.defs[name]
	r.mu.RUnlock()
	if !ok {
		return Definition{}, fmt.Errorf("agentregistry: unknown agent %q; registered: %v", name, r.Names())
	}
	def.AllowedTools = slices.Clone(def.AllowedTools)
	return def, nil
}

// Names returns registered names in stable order.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.defs))
	for name := range r.defs {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}
