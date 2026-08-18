package runtime_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

func TestEventPublisherRecoversFromSequenceClaimedByAnotherProcess(t *testing.T) {
	store := &sequenceRaceStore{}
	publisher, err := runtime.NewEventPublisher(runtime.NewMemoryBus(runtime.BusOptions{}), store, runtime.PersisterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()

	event := runtime.MustEvent("run-1", "thread-1", runtime.EventRunEnd, runtime.RunEnd{Status: "success"})
	seq, err := publisher.Publish(context.Background(), event)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if seq != 2 {
		t.Fatalf("Publish() seq = %d, want 2 after competing writer claimed seq 1", seq)
	}
	events, err := store.Get(context.Background(), "run-1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Seq != 2 || events[1].Type != runtime.EventRunEnd {
		t.Fatalf("stored events = %+v, want competing event followed by run_end at seq 2", events)
	}
}

type sequenceRaceStore struct {
	mu       sync.Mutex
	events   []runtime.Event
	injected bool
}

func (s *sequenceRaceStore) PutBatch(_ context.Context, events []runtime.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.injected {
		s.injected = true
		competing := runtime.MustEvent(events[0].RunID, events[0].ThreadID, runtime.EventContentDelta, map[string]string{"source": "old-process"})
		competing.Seq = events[0].Seq
		s.events = append(s.events, competing)
		return errors.New("sequence already claimed")
	}
	s.events = append(s.events, events...)
	return nil
}

func (s *sequenceRaceStore) Get(_ context.Context, runID string, afterSeq int64, limit int) ([]runtime.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []runtime.Event
	for _, event := range s.events {
		if event.RunID == runID && event.Seq > afterSeq {
			result = append(result, event)
			if limit > 0 && len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func (s *sequenceRaceStore) LastSeq(_ context.Context, runID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var last int64
	for _, event := range s.events {
		if event.RunID == runID && event.Seq > last {
			last = event.Seq
		}
	}
	return last, nil
}
