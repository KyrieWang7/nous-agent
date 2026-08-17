// Package replay projects canonical runtime events into model-facing state.
package replay

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

// Projector reconstructs a transcript from canonical transcript events. UI
// deltas, usage and tool-progress events are deliberately ignored.
type Projector struct {
	mu      sync.Mutex
	h       *message.History
	lastSeq int64
}

// NewProjector returns a projector with an empty history.
func NewProjector() *Projector { return &Projector{h: message.NewHistory()} }

// NewProjectorFromSnapshot seeds a projector from a disposable checkpoint.
// Callers must continue replaying the canonical event stream after LastSeq.
func NewProjectorFromSnapshot(snapshot Snapshot) *Projector {
	history := message.NewHistory()
	history.Load(snapshot.Messages)
	return &Projector{h: history, lastSeq: snapshot.LastSeq}
}

// Apply applies one event. Duplicate or older events are idempotently ignored;
// a sequence gap is not an error because a projector may intentionally filter
// non-canonical UI events from the same stream.
func (p *Projector) Apply(event runtime.Event) error {
	if p == nil {
		return errors.New("replay: projector is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if event.Seq > 0 && event.Seq <= p.lastSeq {
		return nil
	}
	switch event.Type {
	case runtime.EventTranscriptAppend:
		var payload runtime.TranscriptAppend
		if err := decode(event, &payload); err != nil {
			return err
		}
		p.h.AppendAll(payload.Messages)
	case runtime.EventTranscriptReplace:
		var payload runtime.TranscriptReplace
		if err := decode(event, &payload); err != nil {
			return err
		}
		p.h.Load(payload.Messages)
	default:
		if event.Seq > p.lastSeq {
			p.lastSeq = event.Seq
		}
		return nil
	}
	if event.Seq > p.lastSeq {
		p.lastSeq = event.Seq
	}
	return nil
}

// ApplyAll applies events in the supplied order.
func (p *Projector) ApplyAll(events []runtime.Event) error {
	for _, event := range events {
		if err := p.Apply(event); err != nil {
			return err
		}
	}
	return nil
}

// History returns a defensive snapshot of the projected history.
func (p *Projector) History() *message.History {
	if p == nil {
		return message.NewHistory()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := message.NewHistory()
	out.Load(p.h.All())
	return out
}

// LastSeq returns the highest event sequence observed by the projector.
func (p *Projector) LastSeq() int64 {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastSeq
}

func decode(event runtime.Event, dst any) error {
	if len(event.Data) == 0 {
		return fmt.Errorf("replay: event %s seq %d has empty payload", event.Type, event.Seq)
	}
	if err := json.Unmarshal(event.Data, dst); err != nil {
		return fmt.Errorf("replay: decoding %s seq %d: %w", event.Type, event.Seq, err)
	}
	return nil
}
