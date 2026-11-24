package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/middleware"
)

func TestWebRoutes(t *testing.T) {
	frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "frontend %s", r.URL.RequestURI())
	}))
	defer frontend.Close()
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Nous-Manager-Token") != "test-gateway-token" {
			t.Error("manager request lacks the gateway token")
		}
		if strings.HasPrefix(r.URL.Path, "/api/media/") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		if r.URL.Path == "/api/ideas/" {
			http.Redirect(w, r, "/api/ideas?"+r.URL.RawQuery, http.StatusTemporaryRedirect)
			return
		}
		fmt.Fprintf(w, "manager %s", r.URL.RequestURI())
	}))
	defer manager.Close()

	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "api %s", r.URL.RequestURI())
	})
	frontendProxy, managerProxy, err := middleware.NewWebUpstreams(middleware.WebUpstreams{FrontendURL: frontend.URL, ManagerURL: manager.URL,
		ManagerToken: "test-gateway-token", PublicOrigin: "https://nous.example"})
	if err != nil {
		t.Fatal(err)
	}

	handler := WithWeb(api, frontendProxy, managerProxy)

	for _, test := range []struct {
		method string
		path   string
		origin string
		status int
		body   string
	}{
		{"GET", "/", "", 200, "frontend /"},
		{"GET", "/_next/static/chunk.js", "", 200, "frontend /_next/static/chunk.js"},
		{"GET", "/api/models", "", 200, "api /api/models"},
		{"POST", "/api/agent/threads", "", 200, "frontend /api/agent/threads"},
		{"GET", "/health", "", 200, "api /health"},
		{"GET", "/creative", "", 308, ""},
		{"GET", "/workspace/creative?from=old", "", 308, ""},
		{"GET", "/creative/", "", 200, "manager /"},
		{"GET", "/creative/assets/index.js", "", 200, "manager /assets/index.js"},
		{"GET", "/creative/api/trends?limit=3", "", 200, "manager /api/trends?limit=3"},
		{"GET", "/creative/api/ideas/?keep=1", "", 307, ""},
		{"POST", "/creative/api/schedule", "https://nous.example", 200, "manager /api/schedule"},
		{"POST", "/creative/api/schedule", "https://elsewhere.example", 403, ""},
		{"GET", "/creative/api/media/example.html", "", 200, "manager /api/media/example.html"},
	} {
		t.Run(test.method+" "+test.path+" "+test.origin, func(t *testing.T) {
			req := httptest.NewRequest(test.method, "https://nous.example"+test.path, nil)
			if test.origin != "" {
				req.Header.Set("Origin", test.origin)
			}
			req.Header.Set("X-Nous-Manager-Token", "untrusted-client-value")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != test.status || (test.body != "" && res.Body.String() != test.body) {
				t.Fatalf("response = %d %q, want %d %q", res.Code, res.Body.String(), test.status, test.body)
			}
			if test.status == 308 {
				want := "/creative/"
				if req.URL.RawQuery != "" {
					want += "?" + req.URL.RawQuery
				}
				if got := res.Header().Get("Location"); got != want {
					t.Fatalf("Location = %q, want %q", got, want)
				}
			}
			if test.status == 307 && res.Header().Get("Location") != "/creative/api/ideas?keep=1" {
				t.Fatalf("Manager redirect Location = %q", res.Header().Get("Location"))
			}
			if strings.Contains(test.path, "/api/media/") && res.Header().Get("Content-Security-Policy") != "sandbox allow-scripts" {
				t.Fatal("active media was not isolated")
			}
		})
	}
}
