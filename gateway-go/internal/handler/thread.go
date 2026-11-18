package handler

import (
	"errors"
	"net/http"

	domain "github.com/KyrieWang7/nous-agent/gateway-go/internal/model"
)

func (s *Handler) threads(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 50, 1, 100)
	offset := queryInt(r, "offset", 0, 0, 1_000_000)
	items, total, err := s.app.ListThreads(r.Context(), limit, offset, r.URL.Query().Get("include_archived") == "true")
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"threads": items, "total": total})
}

func (s *Handler) createThread(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		ModelName string `json:"model_name"`
	}
	if err := decode(r, &body); err != nil {
		problem(w, 400, err)
		return
	}
	if body.ID == "" {
		problem(w, 400, errors.New("id is required"))
		return
	}
	item, err := s.app.CreateThread(r.Context(), body.ID, body.Title, body.ModelName)
	if err != nil {
		problem(w, 503, err)
		return
	}
	writeJSON(w, 200, item)
}

func (s *Handler) updateThread(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title      *string `json:"title"`
		ModelName  *string `json:"model_name"`
		IsArchived *bool   `json:"is_archived"`
	}
	if err := decode(r, &body); err != nil {
		problem(w, 400, err)
		return
	}
	item, err := s.app.UpdateThread(r.Context(), r.PathValue("id"), domain.ThreadPatch{Title: body.Title, ModelName: body.ModelName, IsArchived: body.IsArchived})
	if errors.Is(err, domain.ErrNotFound) {
		problem(w, 404, err)
		return
	}
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, item)
}

func (s *Handler) deleteThread(w http.ResponseWriter, r *http.Request) {
	err := s.app.DeleteThread(r.Context(), r.PathValue("id"))
	if errors.Is(err, domain.ErrNotFound) {
		problem(w, 404, err)
		return
	}
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
