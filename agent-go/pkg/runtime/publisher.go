package runtime

import (
	"bytes"
	"context"
	"fmt"
	"sync"
)

const durableSequenceRetryLimit = 8

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
	if event.Droppable() {
		event.Seq = sequence.last + 1
		event.Seq = p.bus.Publish(ctx, event)
		p.persister.Persist(ctx, event)
		sequence.last = event.Seq
		return event.Seq, nil
	}

	for attempt := 0; attempt < durableSequenceRetryLimit; attempt++ {
		event.Seq = sequence.last + 1
		if err := p.persister.PersistDurable(ctx, event); err == nil {
			event.Seq = p.bus.Publish(ctx, event)
			sequence.last = event.Seq
			return event.Seq, nil
		} else if stored, ok := p.storedEvent(ctx, event); ok {
			// A batched write can report a conflict from an earlier trace event
			// even though this audit event was committed successfully.
			event.Seq = p.bus.Publish(ctx, stored)
			sequence.last = event.Seq
			return event.Seq, nil
		} else if reader, ok := p.store.(EventSequenceReader); ok {
			last, readErr := reader.LastSeq(ctx, event.RunID)
			if readErr != nil {
				return 0, fmt.Errorf("persisting %s event: %w (reading durable event sequence: %v)", event.Type, err, readErr)
			}
			if last < event.Seq {
				return 0, fmt.Errorf("persisting %s event: %w", event.Type, err)
			}
			sequence.last = last
			continue
		} else {
			return 0, fmt.Errorf("persisting %s event: %w", event.Type, err)
		}
	}
	return 0, fmt.Errorf("persisting %s event: sequence contention did not settle after %d attempts", event.Type, durableSequenceRetryLimit)
}

func (p *EventPublisher) storedEvent(ctx context.Context, expected Event) (Event, bool) {
	events, err := p.store.Get(ctx, expected.RunID, expected.Seq-1, 1)
	if err != nil || len(events) != 1 {
		return Event{}, false
	}
	stored := events[0]
	return stored, stored.Seq == expected.Seq && stored.RunID == expected.RunID &&
		stored.ThreadID == expected.ThreadID && stored.Type == expected.Type &&
		stored.Category == expected.Category && stored.IdempotencyKey == expected.IdempotencyKey &&
		bytes.Equal(stored.Data, expected.Data) && stored.CreatedAt.Equal(expected.CreatedAt)
}

func (p *EventPublisher) Close() {
	if p != nil && p.persister != nil {
		p.persister.Close()
	}
}
