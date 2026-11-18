package subagent

import (
	"context"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

type parentSlots struct {
	permits chan struct{}
	refs    int
}

// A parent's fan-out cannot consume the permits needed by its descendants.
// Each parent has a bounded sibling pool; trusted depth and the root's shared
// dispatch budget bound the whole tree. Waiting callers retain their pool ref.
func (m *Manager) acquireParentSlot(ctx context.Context, parent runtime.RunContext) (func(), error) {
	key := parent.ThreadID + "\x00" + parent.RunID
	m.slotMu.Lock()
	slots := m.slots[key]
	if slots == nil {
		slots = &parentSlots{permits: make(chan struct{}, m.concurrency)}
		m.slots[key] = slots
	}
	slots.refs++
	m.slotMu.Unlock()
	drop := func() {
		m.slotMu.Lock()
		slots.refs--
		if slots.refs == 0 {
			delete(m.slots, key)
		}
		m.slotMu.Unlock()
	}
	select {
	case slots.permits <- struct{}{}:
		return func() { <-slots.permits; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}
