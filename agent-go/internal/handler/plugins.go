package handler

import (
	"errors"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/plugincontrol"
	"net/http"
)

func (s *Handler) plugins(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	snapshot, err := s.app.Plugins(r.Context())
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, 200, snapshot)
}
func (s *Handler) changePlugins(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Action   string `json:"action"`
		Version  string `json:"version"`
		Enabled  *bool  `json:"enabled"`
		Revision string `json:"revision"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Action != "" {
		if body.Enabled != nil || body.Name == "" || (body.Action == "install" && body.Version == "") ||
			(body.Action == "uninstall" && body.Version != "") || (body.Action != "install" && body.Action != "uninstall") {
			writeError(w, 400, errors.New("invalid plugin package action"))
			return
		}
		s.applyPluginChange(w, r, plugincontrol.Change{Name: body.Name, PackageAction: body.Action, Version: body.Version, Revision: body.Revision})
		return
	}
	if body.Enabled == nil {
		writeError(w, 400, errors.New("enabled is required"))
		return
	}
	s.applyPluginChange(w, r, plugincontrol.Change{Name: body.Name, Enabled: *body.Enabled, Revision: body.Revision})
}
func (s *Handler) reloadPlugins(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Revision string `json:"revision"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	s.applyPluginChange(w, r, plugincontrol.Change{Reload: true, Revision: body.Revision})
}
func (s *Handler) applyPluginChange(w http.ResponseWriter, r *http.Request, change plugincontrol.Change) {
	snapshot, err := s.app.ChangePlugins(r.Context(), change)
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, 200, snapshot)
}
func writePluginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plugincontrol.ErrUnavailable):
		writeError(w, 503, err)
	case errors.Is(err, plugincontrol.ErrNotFound):
		writeError(w, 404, err)
	case errors.Is(err, plugincontrol.ErrConflict):
		writeError(w, 409, err)
	default:
		writeError(w, 500, err)
	}
}
