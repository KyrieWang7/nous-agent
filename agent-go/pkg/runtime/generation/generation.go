// Package generation owns immutable runtime capability views.
package generation

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
)

// Generation is an immutable set of capabilities assembled for a group of
// runs. A run must keep using its acquired generation until it finishes.
type Generation struct {
	id           string
	createdAt    time.Time
	capabilities capability.Snapshot
}

// New creates a generation from a registry snapshot.
func New(id string, registry *capability.Registry) (*Generation, error) {
	if id == "" {
		return nil, errors.New("generation: id must not be empty")
	}
	if registry == nil {
		return nil, errors.New("generation: capability registry is nil")
	}
	return &Generation{id: id, createdAt: time.Now().UTC(), capabilities: registry.Snapshot()}, nil
}

// ID returns the stable generation identifier.
func (g *Generation) ID() string {
	if g == nil {
		return ""
	}
	return g.id
}

// CreatedAt returns the generation creation time.
func (g *Generation) CreatedAt() time.Time {
	if g == nil {
		return time.Time{}
	}
	return g.createdAt
}

// Capabilities returns the immutable capability view.
func (g *Generation) Capabilities() capability.Snapshot {
	if g == nil {
		return capability.Snapshot{}
	}
	return g.capabilities
}

// Lease keeps a generation live until Release. Production reload keeps the
// complete assembly (models, plugins and external clients) behind the same
// ownership rule and closes a retired assembly after its final run releases.
type Lease struct {
	manager *Manager
	gen     *Generation
	once    sync.Once
}

// Generation returns the leased generation.
func (l *Lease) Generation() *Generation {
	if l == nil {
		return nil
	}
	return l.gen
}

// Release releases one reference. It is idempotent.
func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if l.manager != nil {
			l.manager.release(l.gen)
		}
	})
}

type generationSlot struct {
	gen     *Generation
	refs    int
	retired bool
}

// Manager owns the current generation and keeps retired generations alive
// while active runs still hold leases.
type Manager struct {
	mu      sync.Mutex
	current *generationSlot
	retired []*generationSlot
	closed  bool
}

// ManagerStats is a point-in-time ownership view for reload telemetry and
// lifecycle tests.
type ManagerStats struct {
	CurrentID   string
	CurrentRefs int
	Retired     int
}

// NewManager returns a manager with an initial generation.
func NewManager(initial *Generation) (*Manager, error) {
	if initial == nil {
		return nil, errors.New("generation: initial generation is nil")
	}
	return &Manager{current: &generationSlot{gen: initial}}, nil
}

// Acquire pins the current generation for one run.
func (m *Manager) Acquire() (*Lease, error) {
	if m == nil {
		return nil, errors.New("generation: manager is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.current == nil {
		return nil, errors.New("generation: manager is closed")
	}
	m.current.refs++
	return &Lease{manager: m, gen: m.current.gen}, nil
}

// Install makes gen current. Existing leases continue to use the old one.
func (m *Manager) Install(gen *Generation) error {
	if m == nil {
		return errors.New("generation: manager is nil")
	}
	if gen == nil {
		return errors.New("generation: generation is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("generation: manager is closed")
	}
	if m.current != nil && m.current.gen.ID() == gen.ID() {
		return fmt.Errorf("generation: %q is already current", gen.ID())
	}
	if m.current != nil {
		m.current.retired = true
		if m.current.refs > 0 {
			m.retired = append(m.retired, m.current)
		}
	}
	m.current = &generationSlot{gen: gen}
	return nil
}

// Stats returns current references and retained retired generations.
func (m *Manager) Stats() ManagerStats {
	if m == nil {
		return ManagerStats{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := ManagerStats{Retired: len(m.retired)}
	if m.current != nil {
		stats.CurrentID = m.current.gen.ID()
		stats.CurrentRefs = m.current.refs
	}
	return stats
}

// Close prevents new leases. Existing leases remain valid until released.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.closed = true
	if m.current != nil {
		m.current.retired = true
	}
	m.mu.Unlock()
}

func (m *Manager) release(gen *Generation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil && m.current.gen == gen {
		if m.current.refs > 0 {
			m.current.refs--
		}
		return
	}
	for i, slot := range m.retired {
		if slot.gen != gen {
			continue
		}
		if slot.refs > 0 {
			slot.refs--
		}
		if slot.refs == 0 {
			copy(m.retired[i:], m.retired[i+1:])
			m.retired[len(m.retired)-1] = nil
			m.retired = m.retired[:len(m.retired)-1]
		}
		return
	}
}
