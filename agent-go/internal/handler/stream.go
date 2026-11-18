package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

func (s *Handler) stream(w http.ResponseWriter, r *http.Request, rid string, after int64) {
	live, unsubscribe := s.app.Subscribe(rid)
	disconnectCtx := context.WithoutCancel(r.Context())
	defer func() {
		unsubscribe()
		_ = s.app.ClientDisconnected(disconnectCtx, rid)
	}()
	seen := after
	ended := false
	yield := func(e runtime.Event) bool {
		if e.Seq <= seen {
			return true
		}
		// Transcript events are the canonical model-facing persistence stream.
		// They must advance the SSE cursor, but are intentionally not exposed as
		// frontend wire events; the HTTP messages/values projections remain the
		// transport contract.
		if !clientVisibleRuntimeEvent(e) {
			seen = e.Seq
			return true
		}
		we, err := decodeWireEvent(e)
		if err != nil {
			s.app.Logger().Warn("invalid wire event", "error", err)
			return true
		}
		if err := writeSSE(w, e.Seq, we); err != nil {
			return false
		}
		seen = e.Seq
		if we.Event == "end" {
			ended = true
			return false
		}
		return true
	}
	_ = s.app.Replay(r.Context(), rid, after, yield)
	if !ended {
		for _, e := range s.app.Backlog(rid, seen) {
			if !yield(e) {
				break
			}
		}
	}
	if ended {
		return
	}
	ticker := time.NewTicker(s.app.Heartbeat())
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-live:
			if !ok {
				return
			}
			if !yield(e) {
				return
			}
		case <-ticker.C:
			_ = s.app.Replay(r.Context(), rid, seen, yield)
			if ended {
				return
			}
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}
