// Package capability defines the runtime capability registry.
//
// Capabilities are the stable discovery boundary for agents, models, tools,
// sandboxes, memory, skills and MCP providers. Domain packages keep their
// strongly typed interfaces; this package only owns names, kinds and runtime
// resolution so the kernel does not need to know how a capability is built.
package capability

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// Kind identifies the capability domain.
type Kind string

const (
	KindAgent   Kind = "agent"
	KindModel   Kind = "model"
	KindTool    Kind = "tool"
	KindSandbox Kind = "sandbox"
	KindMemory  Kind = "memory"
	KindSkill   Kind = "skill"
	KindMCP     Kind = "mcp"
	KindPolicy  Kind = "policy"
	// KindInteraction is a human collaboration channel. It is deliberately
	// distinct from policy: answering a question never grants permissions.
	KindInteraction Kind = "interaction"
)

// Scope describes the narrowest lifecycle a capability may depend on.
type Scope string

const (
	ScopeGlobal Scope = "global"
	ScopeThread Scope = "thread"
	ScopeRun    Scope = "run"
)

// Definition is the stable, model- and API-visible part of a capability.
type Definition struct {
	Name        string            `json:"name"`
	Kind        Kind              `json:"kind"`
	Description string            `json:"description,omitempty"`
	Scope       Scope             `json:"scope,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// ResolveRequest carries trusted runtime identity to a provider.
type ResolveRequest struct {
	GenerationID string
	ThreadID     string
	RunID        string
	Values       map[string]any
}

// Resolver builds or returns the concrete capability for one run.
type Resolver func(context.Context, ResolveRequest) (any, error)

// Entry combines a capability definition with its provider.
type Entry struct {
	Definition
	Resolver Resolver
}

// Value is a convenient adapter for an already-constructed provider. Domain
// packages keep their typed interfaces; the runtime only needs the capability
// definition and an opaque value at this boundary.
type Value struct {
	Definition
	Value any
}

// RegisterValue registers a static capability value with defensive nil
// validation. Per-run providers should use Entry.Resolver instead.
func RegisterValue(r *Registry, value Value) error {
	if value.Value == nil {
		return fmt.Errorf("capability %q: value must not be nil", value.Name)
	}
	provider := value.Value
	return r.Register(Entry{Definition: value.Definition, Resolver: func(context.Context, ResolveRequest) (any, error) {
		return provider, nil
	}})
}

// Registry is a concurrency-safe mutable registry used during runtime
// assembly. A Generation takes an immutable Snapshot before a run is exposed.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]Entry
}

// NewRegistry returns an empty capability registry.
func NewRegistry() *Registry { return &Registry{entries: make(map[string]Entry)} }

// Register adds one capability. Names are globally unique across kinds so a
// typo cannot silently resolve a different domain's capability.
func (r *Registry) Register(entry Entry) error {
	if r == nil {
		return errors.New("capability: registry is nil")
	}
	if entry.Name == "" {
		return errors.New("capability: name must not be empty")
	}
	if entry.Kind == "" {
		return fmt.Errorf("capability %q: kind must not be empty", entry.Name)
	}
	if entry.Resolver == nil {
		return fmt.Errorf("capability %q: resolver must not be nil", entry.Name)
	}
	if entry.Scope == "" {
		entry.Scope = ScopeGlobal
	}
	if entry.Scope != ScopeGlobal && entry.Scope != ScopeThread && entry.Scope != ScopeRun {
		return fmt.Errorf("capability %q: unknown scope %q", entry.Name, entry.Scope)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[entry.Name]; exists {
		return fmt.Errorf("capability %q: already registered", entry.Name)
	}
	entry.Metadata = cloneMetadata(entry.Metadata)
	r.entries[entry.Name] = entry
	return nil
}

// Get returns a definition without invoking its resolver.
func (r *Registry) Get(name string) (Entry, error) {
	if r == nil {
		return Entry{}, errors.New("capability: registry is nil")
	}
	r.mu.RLock()
	entry, ok := r.entries[name]
	r.mu.RUnlock()
	if !ok {
		return Entry{}, fmt.Errorf("capability: unknown %q; registered: %v", name, r.Names())
	}
	entry.Metadata = cloneMetadata(entry.Metadata)
	return entry, nil
}

// Has reports whether a capability name is registered.
func (r *Registry) Has(name string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	_, ok := r.entries[name]
	r.mu.RUnlock()
	return ok
}

// Resolve invokes a capability resolver after validating its expected kind.
func (r *Registry) Resolve(ctx context.Context, name string, expected Kind, req ResolveRequest) (any, error) {
	entry, err := r.Get(name)
	if err != nil {
		return nil, err
	}
	if expected != "" && entry.Kind != expected {
		return nil, fmt.Errorf("capability %q: kind is %q, want %q", name, entry.Kind, expected)
	}
	if err := validateResolveScope(entry.Definition, req); err != nil {
		return nil, err
	}
	if req.Values != nil {
		req.Values = cloneValues(req.Values)
	}
	value, err := entry.Resolver(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("capability %q: resolve: %w", name, err)
	}
	if value == nil {
		return nil, fmt.Errorf("capability %q: resolver returned nil", name)
	}
	return value, nil
}

// Names returns all names in stable order.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.entries))
	for name := range r.entries {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// NamesByKind returns capability names for one domain in stable order.
func (r *Registry) NamesByKind(kind Kind) []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []string
	for name, entry := range r.entries {
		if entry.Kind == kind {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// Snapshot returns an immutable copy suitable for a runtime generation.
func (r *Registry) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries := make(map[string]Entry, len(r.entries))
	for name, entry := range r.entries {
		entry.Metadata = cloneMetadata(entry.Metadata)
		entries[name] = entry
	}
	return Snapshot{entries: entries}
}

// Snapshot is an immutable capability view. Entry values contain function
// references but no mutable registry map.
type Snapshot struct{ entries map[string]Entry }

// Empty reports whether the snapshot contains no capabilities.
func (s Snapshot) Empty() bool { return len(s.entries) == 0 }

// Names returns stable names from the snapshot.
func (s Snapshot) Names() []string {
	out := make([]string, 0, len(s.entries))
	for name := range s.entries {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// Get returns an entry from the snapshot.
func (s Snapshot) Get(name string) (Entry, error) {
	entry, ok := s.entries[name]
	if !ok {
		return Entry{}, fmt.Errorf("capability: unknown %q; generation contains: %v", name, s.Names())
	}
	entry.Metadata = cloneMetadata(entry.Metadata)
	return entry, nil
}

// Resolve invokes a resolver from the immutable snapshot.
func (s Snapshot) Resolve(ctx context.Context, name string, expected Kind, req ResolveRequest) (any, error) {
	entry, err := s.Get(name)
	if err != nil {
		return nil, err
	}
	if expected != "" && entry.Kind != expected {
		return nil, fmt.Errorf("capability %q: kind is %q, want %q", name, entry.Kind, expected)
	}
	if err := validateResolveScope(entry.Definition, req); err != nil {
		return nil, err
	}
	if req.Values != nil {
		req.Values = cloneValues(req.Values)
	}
	value, err := entry.Resolver(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("capability %q: resolve: %w", name, err)
	}
	if value == nil {
		return nil, fmt.Errorf("capability %q: resolver returned nil", name)
	}
	return value, nil
}

func validateResolveScope(definition Definition, req ResolveRequest) error {
	switch definition.Scope {
	case ScopeThread:
		if req.ThreadID == "" {
			return fmt.Errorf("capability %q: thread scope requires thread id", definition.Name)
		}
	case ScopeRun:
		if req.RunID == "" {
			return fmt.Errorf("capability %q: run scope requires run id", definition.Name)
		}
	}
	return nil
}

func cloneMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneValues(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
