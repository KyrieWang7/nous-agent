package router

import (
	"net/http"
	"strings"
)

const managerPrefix = "/creative"

// WithWeb mounts the Manager and product frontends at the same origin.
func WithWeb(api, frontendProxy, managerProxy http.Handler) http.Handler {
	if managerProxy != nil {
		managerProxy = http.StripPrefix(managerPrefix, managerProxy)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == managerPrefix || path == "/workspace/creative" || path == "/workspace/creative/" {
			destination := managerPrefix + "/"
			if r.URL.RawQuery != "" {
				destination += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, destination, http.StatusPermanentRedirect)
			return
		}
		if strings.HasPrefix(path, managerPrefix+"/") {
			if managerProxy == nil {
				http.Error(w, "Manager is not configured", http.StatusServiceUnavailable)
				return
			}
			managerProxy.ServeHTTP(w, r)
			return
		}
		if frontendProxy != nil && path != "/health" &&
			((path != "/api" && !strings.HasPrefix(path, "/api/")) || (path == "/api/agent" || strings.HasPrefix(path, "/api/agent/"))) {
			frontendProxy.ServeHTTP(w, r)
			return
		}
		api.ServeHTTP(w, r)
	})
}
