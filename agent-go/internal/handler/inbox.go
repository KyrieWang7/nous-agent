package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/service"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/inbox"
)

type inputCreate struct {
	ID       string         `json:"id"`
	Delivery inbox.Delivery `json:"delivery"`
	Input    map[string]any `json:"input"`
	Config   map[string]any `json:"config,omitempty"`
	Context  map[string]any `json:"context,omitempty"`
}

func (s *Handler) enqueueInput(w http.ResponseWriter, r *http.Request) {
	var body inputCreate
	if !decodeJSON(w, r, &body) {
		return
	}
	if _, removed := body.Config["configurable"]; removed {
		writeError(w, 400, errors.New("config.configurable is not supported"))
		return
	}
	prompt, blocks, err := inputFromMessages(body.Input)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	e := inbox.Entry{ID: body.ID, ThreadID: r.PathValue("tid"), Delivery: body.Delivery, Input: inbox.Input{Prompt: prompt, ContentBlocks: blocks, Config: body.Config, Context: body.Context}}
	if err = e.Validate(); err != nil {
		writeError(w, 400, err)
		return
	}
	e, err = s.app.EnqueueInput(r.Context(), e)
	if err != nil {
		writeInputError(w, err)
		return
	}
	writeJSON(w, 202, e)
}
func (s *Handler) listInputs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	entries, err := s.app.ListInputs(r.Context(), r.PathValue("tid"), queryInt64(r, "after", 0), queryInt(r, "limit", 100, 1, 200))
	if err != nil {
		writeInputError(w, err)
		return
	}
	writeJSON(w, 200, entries)
}
func (s *Handler) getInput(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	e, err := s.app.GetInput(r.Context(), r.PathValue("tid"), r.PathValue("mid"))
	if err != nil {
		writeInputError(w, err)
		return
	}
	writeJSON(w, 200, e)
}
func (s *Handler) cancelInput(w http.ResponseWriter, r *http.Request) {
	e, err := s.app.CancelInput(r.Context(), r.PathValue("tid"), r.PathValue("mid"))
	if err != nil {
		writeInputError(w, err)
		return
	}
	writeJSON(w, 200, e)
}
func (s *Handler) changeInput(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prompt      *string   `json:"prompt"`
		TargetRunID string    `json:"target_run_id"`
		UpdatedAt   time.Time `json:"updated_at"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.UpdatedAt.IsZero() || (body.Prompt == nil) == (body.TargetRunID == "") || (body.Prompt != nil && strings.TrimSpace(*body.Prompt) == "") {
		writeError(w, http.StatusBadRequest, errors.New("updated_at and either a non-empty prompt or target_run_id are required"))
		return
	}
	entry, err := s.app.ChangeInput(r.Context(), r.PathValue("tid"), r.PathValue("mid"), inbox.Change{
		Prompt: body.Prompt, TargetRunID: body.TargetRunID, UpdatedAt: body.UpdatedAt,
	})
	if err != nil {
		writeInputError(w, err)
		return
	}
	writeJSON(w, 200, entry)
}

func writeInputError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, inbox.ErrNotFound):
		writeError(w, 404, err)
	case errors.Is(err, inbox.ErrConflict), errors.Is(err, inbox.ErrAdmitted), errors.Is(err, inbox.ErrChanged), errors.Is(err, inbox.ErrNoActiveRun), errors.Is(err, inbox.ErrTextOnly):
		writeError(w, 409, err)
	case errors.Is(err, inbox.ErrUnavailable), errors.Is(err, service.ErrClosing):
		writeError(w, 503, err)
	default:
		writeStoreError(w, err)
	}
}
