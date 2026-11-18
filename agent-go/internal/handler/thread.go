package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/model"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/service"
	"github.com/google/uuid"
)

func (s *Handler) forkRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ThreadID string `json:"thread_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	destination, err := uuid.Parse(body.ThreadID)
	if err != nil || destination == uuid.Nil || destination.String() == r.PathValue("tid") {
		writeError(w, 400, errors.New("a distinct destination thread UUID is required"))
		return
	}
	thread, err := s.app.ForkRun(r.Context(), r.PathValue("tid"), r.PathValue("rid"), destination.String())
	switch {
	case err == nil:
		writeJSON(w, 201, thread)
	case errors.Is(err, service.ErrForkUnavailable), errors.Is(err, model.ErrForkConflict):
		writeError(w, 409, err)
	default:
		writeStoreError(w, err)
	}
}

func (s *Handler) createThread(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ThreadID string         `json:"thread_id"`
		Metadata map[string]any `json:"metadata"`
		IfExists string         `json:"if_exists"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	t, err := s.app.CreateThread(r.Context(), body.ThreadID, body.Metadata, body.IfExists == "do_nothing")
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, t)
}

func (s *Handler) searchThreads(w http.ResponseWriter, r *http.Request) {
	ts, err := s.app.SearchThreads(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, ts)
}

func (s *Handler) getThread(w http.ResponseWriter, r *http.Request) {
	t, err := s.app.GetThread(r.Context(), r.PathValue("tid"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, t)
}

func (s *Handler) patchThread(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Metadata map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	t, err := s.app.UpdateThread(r.Context(), r.PathValue("tid"), body.Metadata, nil)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, t)
}

func (s *Handler) deleteThread(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteThread(r.Context(), r.PathValue("tid")); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Handler) state(ctx context.Context, tid string) (map[string]any, error) {
	state, err := s.app.State(ctx, tid)
	if err != nil {
		return nil, err
	}
	vals := make(map[string]any, len(state.Thread.Values)+1)
	for k, v := range state.Thread.Values {
		vals[k] = v
	}
	vals["messages"] = wireMessages(state.History)
	result := map[string]any{"values": vals, "metadata": state.Thread.Metadata, "created_at": state.Thread.Updated}
	if state.HasCompletion {
		c := state.Completion
		result["token_usage"] = tokenUsagePayload(c.InputTokens, c.OutputTokens, c.CachedInputTokens, c.LeadTokens, c.SubagentTokens, c.AuxiliaryTokens)
	}
	return result, nil
}
func (s *Handler) getState(w http.ResponseWriter, r *http.Request) {
	v, err := s.state(r.Context(), r.PathValue("tid"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Handler) postState(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Values map[string]any `json:"values"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	_, err := s.app.UpdateThread(r.Context(), r.PathValue("tid"), nil, body.Values)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.getState(w, r)
}

func (s *Handler) getHistory(w http.ResponseWriter, r *http.Request) {
	v, err := s.state(r.Context(), r.PathValue("tid"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, []any{v})
}
