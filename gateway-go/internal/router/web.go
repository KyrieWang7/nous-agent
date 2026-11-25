package router

import (
	"net/http"
	"strings"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/middleware"
)

// WithWeb serves the two workbenches and shared login on one origin.
func WithWeb(api, frontendProxy http.Handler, creative *middleware.CreativeWeb) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		isAPI := path == "/api" || strings.HasPrefix(path, "/api/")
		switch path {
		case middleware.CreativePrefix, "/workspace/creative", "/workspace/creative/":
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			} else if creative == nil {
				http.Error(w, "Creative workbench is not configured", http.StatusServiceUnavailable)
			} else {
				target := middleware.CreativePrefix + "/"
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusSeeOther)
			}
			return
		case "/api/auth/session":
			if creative == nil {
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"enabled":false,"user":null}`))
			} else {
				creative.Session(w, r)
			}
			return
		case "/api/auth/logout":
			if creative != nil {
				creative.Logout(w, r)
				return
			}
		case "/login", "/register", "/forgot-password":
			if creative != nil && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
				creative.Login(w, r)
				return
			}
		}
		if strings.HasPrefix(path, middleware.CreativePrefix+"/") {
			if creative == nil {
				http.Error(w, "Creative workbench is not configured", http.StatusServiceUnavailable)
			} else {
				creative.ServeHTTP(w, r)
			}
			return
		}
		workspace := path == "/workspace" || strings.HasPrefix(path, "/workspace/") ||
			path == "/drama" || strings.HasPrefix(path, "/drama/") ||
			path == "/drama-api" || strings.HasPrefix(path, "/drama-api/")
		if creative != nil && (isAPI || workspace) && !creative.AuthorizeWorkspace(w, r, isAPI) {
			return
		}
		if frontendProxy != nil && path != "/health" &&
			(!isAPI || path == "/api/agent" || strings.HasPrefix(path, "/api/agent/")) {
			frontendProxy.ServeHTTP(w, r)
			return
		}
		api.ServeHTTP(w, r)
	})
}
