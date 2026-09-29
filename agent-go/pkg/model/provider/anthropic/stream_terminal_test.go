package anthropic

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

func TestStreamRejectsTruncation(t *testing.T) {
	s := newStream(io.NopCloser(strings.NewReader("data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"partial\"}}\n\n")), "test")
	defer s.Close()
	var last model.StreamEvent
	for i := 0; i < 10; i++ {
		ev, ok := s.Next()
		if !ok {
			break
		}
		last = ev
	}
	resp, err := s.Result()
	if resp != nil || !errors.Is(err, io.ErrUnexpectedEOF) || !model.IsProviderUnavailable(err) {
		t.Fatalf("truncated stream returned response=%+v, error=%v", resp, err)
	}
	if last.Type != model.StreamError {
		t.Fatalf("last event=%+v", last)
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }
func (r failingReader) Close() error             { return nil }

func TestStreamReadFailureTerminatesOnce(t *testing.T) {
	cause := errors.New("connection reset")
	s := newStream(failingReader{cause}, "test")
	ev, ok := s.Next()
	if !ok || ev.Type != model.StreamError || !errors.Is(ev.Err, cause) {
		t.Fatalf("event=%+v, ok=%v", ev, ok)
	}
	if _, ok := s.Next(); ok {
		t.Fatal("read error repeated indefinitely")
	}
	if resp, err := s.Result(); resp != nil || !errors.Is(err, cause) {
		t.Fatalf("response=%+v, error=%v", resp, err)
	}
}

func TestStreamStopsAtMessageStop(t *testing.T) {
	s := newStream(io.NopCloser(strings.NewReader("data: {\"type\":\"message_stop\"}\n\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"must not appear\"}}\n\n")), "test")
	defer s.Close()
	if ev, ok := s.Next(); !ok || ev.Type != model.StreamDone {
		t.Fatalf("event=%+v", ev)
	}
	if _, ok := s.Next(); ok {
		t.Fatal("read content after message_stop")
	}
}
