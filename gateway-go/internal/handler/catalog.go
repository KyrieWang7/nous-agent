package handler

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	domain "github.com/KyrieWang7/nous-agent/gateway-go/internal/model"
)

func (s *Handler) models(w http.ResponseWriter, _ *http.Request) {
	models, err := s.app.Models()
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"models": models})
}

func (s *Handler) model(w http.ResponseWriter, r *http.Request) {
	item, err := s.app.Model(r.PathValue("name"))
	if errors.Is(err, domain.ErrModelNotFound) {
		problem(w, 404, err)
		return
	}
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, item)
}

func (s *Handler) getMCP(w http.ResponseWriter, _ *http.Request) {
	ext, err := s.app.Extensions()
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"mcp_servers": ext.MCPServers})
}

func (s *Handler) putMCP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MCPServers map[string]domain.MCPServer `json:"mcp_servers"`
	}
	if err := decode(r, &body); err != nil {
		problem(w, 400, err)
		return
	}
	ext, err := s.app.SaveMCP(body.MCPServers)
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"mcp_servers": ext.MCPServers})
}

func (s *Handler) skills(w http.ResponseWriter, _ *http.Request) {
	items, err := s.app.Skills()
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"skills": items})
}

func (s *Handler) skill(w http.ResponseWriter, r *http.Request) {
	item, err := s.app.Skill(r.PathValue("name"))
	if errors.Is(err, os.ErrNotExist) {
		problem(w, 404, err)
		return
	}
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, item)
}

func (s *Handler) putSkill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &body); err != nil {
		problem(w, 400, err)
		return
	}
	item, err := s.app.SetSkillEnabled(r.PathValue("name"), body.Enabled)
	if errors.Is(err, os.ErrNotExist) {
		problem(w, 404, err)
		return
	}
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, item)
}

func (s *Handler) installSkill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ThreadID string `json:"thread_id"`
		Path     string `json:"path"`
	}
	if err := decode(r, &body); err != nil {
		problem(w, 400, err)
		return
	}
	name, err := s.app.InstallSkill(body.ThreadID, body.Path)
	if err != nil {
		problem(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "skill_name": name, "message": fmt.Sprintf("Skill %q installed successfully", name)})
}
