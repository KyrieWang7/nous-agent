package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/service"
)

func (s *Handler) rawEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	page, err := s.app.RawEvents(r.Context(), r.PathValue("tid"), r.PathValue("rid"), queryInt64(r, "after", 0), queryInt(r, "limit", 200, 1, 1000))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
func (s *Handler) createRun(w http.ResponseWriter, r *http.Request) {
	var body runCreate
	if !decodeJSON(w, r, &body) {
		return
	}
	if _, removed := body.Config["configurable"]; removed {
		writeError(w, http.StatusBadRequest, errors.New("config.configurable is not supported; use flat config or context fields"))
		return
	}
	prompt, blocks, inputErr := inputFromMessages(body.Input)
	run, err := s.app.StartRun(r.Context(), r.PathValue("tid"), service.RunInput{
		AssistantID: body.AssistantID, Metadata: body.Metadata, OnDisconnect: body.OnDisconnect,
		Prompt: prompt, ContentBlocks: blocks, Config: body.Config, Context: body.Context, PreparationError: inputErr,
	})
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrThreadBusy) {
			status = http.StatusConflict
		}
		if errors.Is(err, service.ErrClosing) {
			status = http.StatusServiceUnavailable
		}
		writeError(w, status, err)
		return
	}
	streamHeaders(w)
	w.Header().Set("Content-Location", fmt.Sprintf("/api/v1/threads/%s/runs/%s/events", run.ThreadID, run.RunID))
	s.stream(w, r, run.RunID, 0)
}
func (s *Handler) listRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.app.ListRuns(r.Context(), r.PathValue("tid"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}
func (s *Handler) getRun(w http.ResponseWriter, r *http.Request) {
	run, _, err := s.app.GetRun(r.Context(), r.PathValue("tid"), r.PathValue("rid"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}
func (s *Handler) reconnectRun(w http.ResponseWriter, r *http.Request) {
	rid := r.PathValue("rid")
	_, orphaned, err := s.app.GetRun(r.Context(), r.PathValue("tid"), rid)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	after, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	streamHeaders(w)
	if orphaned {
		_ = writeSSE(w, 0, makeWireEvent("end", map[string]any{"status": "interrupted", "error": "run interrupted because its worker stopped"}))
		return
	}
	s.stream(w, r, rid, after)
}
func (s *Handler) cancelRun(w http.ResponseWriter, r *http.Request) {
	running, err := s.app.CancelRun(r.Context(), r.PathValue("tid"), r.PathValue("rid"))
	if err != nil {
		if errors.Is(err, service.ErrRunNotRegistered) {
			writeError(w, http.StatusConflict, err)
		} else {
			writeStoreError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "was_running": running})
}
func streamHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
}
