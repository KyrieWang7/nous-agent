package replay

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

// StateProjector reconstructs the last durable RunSnapshot from lifecycle
// events. It deliberately ignores UI and usage events in the same stream.
type StateProjector struct {
	mu      sync.Mutex
	snap    runtime.RunSnapshot
	lastSeq int64
}

func NewStateProjector() *StateProjector { return &StateProjector{} }

func (p *StateProjector) Apply(event runtime.Event) error {
	if p == nil {
		return errors.New("replay: state projector is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if event.Seq > 0 && event.Seq <= p.lastSeq {
		return nil
	}
	if event.Type == runtime.EventRunStateChanged {
		var payload runtime.RunStateChanged
		if len(event.Data) == 0 {
			return fmt.Errorf("replay: state event seq %d has empty payload", event.Seq)
		}
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			return fmt.Errorf("replay: decoding state event seq %d: %w", event.Seq, err)
		}
		if payload.Snapshot.Version < p.snap.Version {
			return nil
		}
		p.snap = payload.Snapshot
	}
	if event.Seq > p.lastSeq {
		p.lastSeq = event.Seq
	}
	return nil
}

func (p *StateProjector) ApplyAll(events []runtime.Event) error {
	for _, event := range events {
		if err := p.Apply(event); err != nil {
			return err
		}
	}
	return nil
}

func (p *StateProjector) Snapshot() runtime.RunSnapshot {
	if p == nil {
		return runtime.RunSnapshot{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snap
}

func (p *StateProjector) LastSeq() int64 {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastSeq
}
