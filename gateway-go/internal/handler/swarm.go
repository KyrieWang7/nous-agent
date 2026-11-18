package handler

import (
	"errors"
	"net/http"
	"strconv"

	domain "github.com/KyrieWang7/nous-agent/gateway-go/internal/model"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/service"
)

func (s *Handler) teams(w http.ResponseWriter, r *http.Request) {
	items, err := s.swarm.Teams(r.Context(), r.URL.Query().Get("thread_id"))
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"teams": items})
}

func (s *Handler) members(w http.ResponseWriter, r *http.Request) {
	items, err := s.swarm.Members(r.Context(), r.PathValue("id"))
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"members": items})
}

func (s *Handler) swarmStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		problem(w, 500, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	after, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	_ = service.StreamSwarm(r.Context(), s.swarm, r.PathValue("id"), after, service.StreamOptions{PollInterval: s.swarmPollInterval, HeartbeatInterval: s.swarmHeartbeatInterval}, func(event domain.StreamEvent) error {
		if err := sse(w, event.Name, event.ID, event.Data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
}
