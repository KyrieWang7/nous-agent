package runtime

import (
	"context"
	"fmt"
	"sync"
)

// EventPublisher is the Runtime-owned canonical publication path. Audit and
// usage events are durable before live delivery; trace events use the bounded
// asynchronous persister.
type EventPublisher struct {
	bus       Bus
	store     EventStore
	persister *AsyncPersister
	sequences sync.Map
}

type eventSequence struct {
	mu          sync.Mutex
	initialized bool
	last        int64
}

func NewEventPublisher(bus Bus, store EventStore, opts PersisterOptions) (*EventPublisher, error) {
	if store == nil {
		return nil, fmt.Errorf("runtime: event publisher requires an event store")
	}
	if bus == nil {
		bus = NewMemoryBus(BusOptions{})
	}
	return &EventPublisher{bus: bus, store: store, persister: NewAsyncPersister(store, opts)}, nil
}

func (p *EventPublisher) Publish(ctx context.Context, event Event) (int64, error) {
	if p == nil || p.store == nil || p.bus == nil {
		return 0, fmt.Errorf("runtime: event publisher is not initialized")
	}
	value, _ := p.sequences.LoadOrStore(event.RunID, &eventSequence{})
	sequence := value.(*eventSequence)
	sequence.mu.Lock()
	defer sequence.mu.Unlock()
	if !sequence.initialized {
		if reader, ok := p.store.(EventSequenceReader); ok {
			last, err := reader.LastSeq(ctx, event.RunID)
			if err != nil {
				return 0, fmt.Errorf("reading durable event sequence: %w", err)
			}
			sequence.last = last
		}
		sequence.initialized = true
	}
	event.Seq = sequence.last + 1
	if event.Droppable() {
		event.Seq = p.bus.Publish(ctx, event)
		p.persister.Persist(ctx, event)
	} else {
		if err := p.persister.PersistDurable(ctx, event); err != nil {
			return 0, fmt.Errorf("persisting %s event: %w", event.Type, err)
		}
		event.Seq = p.bus.Publish(ctx, event)
	}
	sequence.last = event.Seq
	return event.Seq, nil
}

func (p *EventPublisher) Close() {
	if p != nil && p.persister != nil {
		p.persister.Close()
	}
}
