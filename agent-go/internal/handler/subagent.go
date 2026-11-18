package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/service"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/delegation"
)

func subagentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, delegation.ErrNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, delegation.ErrMessageConflict), errors.Is(err, delegation.ErrBusy), errors.Is(err, service.ErrThreadBusy):
		writeError(w, http.StatusConflict, err)
	case errors.Is(err, service.ErrSubagentsUnavailable), errors.Is(err, service.ErrClosing):
		writeError(w, http.StatusServiceUnavailable, err)
	default:
		writeStoreError(w, err)
	}
}
func (s *Handler) listSubagents(w http.ResponseWriter, r *http.Request) {
	view, err := s.app.ListSubagents(r.Context(), r.PathValue("tid"))
	if err != nil {
		subagentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Handler) subagentInputs(w http.ResponseWriter, r *http.Request) {
	inputs, err := s.app.SubagentInputs(r.Context(), r.PathValue("tid"), r.PathValue("sid"))
	if err != nil {
		subagentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inputs)
}
func (s *Handler) sendSubagentInput(w http.ResponseWriter, r *http.Request) {
	var input delegation.InboxMessage
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.ID) == "" || strings.TrimSpace(input.Content) == "" {
		writeError(w, http.StatusBadRequest, errors.New("id and content are required"))
		return
	}
	input, err := s.app.SendSubagentInput(r.Context(), r.PathValue("tid"), r.PathValue("sid"), input)
	if err != nil {
		subagentError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, input)
}
func (s *Handler) getSubagent(w http.ResponseWriter, r *http.Request) {
	view, err := s.app.GetSubagent(r.Context(), r.PathValue("tid"), r.PathValue("sid"))
	if err != nil {
		subagentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
func (s *Handler) cancelSubagent(w http.ResponseWriter, r *http.Request) {
	view, err := s.app.CancelSubagent(r.Context(), r.PathValue("tid"), r.PathValue("sid"))
	if err != nil {
		subagentError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}
func (s *Handler) waitSubagent(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(queryInt(r, "timeout_seconds", 20, 1, 30))*time.Second)
	defer cancel()
	view, err := s.app.WaitSubagent(ctx, r.PathValue("tid"), r.PathValue("sid"))
	if errors.Is(err, context.DeadlineExceeded) {
		writeJSON(w, http.StatusAccepted, view)
		return
	}
	if err != nil {
		subagentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
func (s *Handler) continueSubagent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prompt string `json:"prompt"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Prompt) == "" {
		writeError(w, http.StatusBadRequest, errors.New("prompt is required"))
		return
	}
	run, err := s.app.ContinueSubagent(r.Context(), r.PathValue("tid"), r.PathValue("sid"), body.Prompt)
	if err != nil {
		subagentError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}
