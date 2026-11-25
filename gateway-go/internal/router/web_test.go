package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/middleware"
)

func TestCreativeWebRoutes(t *testing.T) {
	public := "http://127.0.0.1:7777"
	canvas := "http://127.0.0.1:3000"
	frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("open_ai_canvas_session"); err == nil {
			t.Error("forwarded shared session to the frontend")
		}
		fmt.Fprintf(w, "frontend %s", r.URL.RequestURI())
	}))
	defer frontend.Close()
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("unrelated"); err == nil {
			t.Error("forwarded unrelated cookie to authentication")
		}
		switch r.URL.Path {
		case "/api/auth/nous-session":
			cookie, err := r.Cookie("open_ai_canvas_session")
			if err != nil {
				t.Error("authentication called without a session")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			switch cookie.Value {
			case "unavailable":
				w.WriteHeader(http.StatusInternalServerError)
			case "admin", "user":
				fmt.Fprintf(w, `{"code":0,"data":{"user":{"id":"test","role":%q,"status":"active"}}}`, cookie.Value)
			default:
				w.WriteHeader(http.StatusUnauthorized)
			}
		case "/api/auth/logout":
			http.SetCookie(w, &http.Cookie{Name: "open_ai_canvas_session", MaxAge: -1, Path: "/", HttpOnly: true})
			fmt.Fprint(w, `{"code":0}`)
		default:
			t.Errorf("unexpected authentication route: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer identity.Close()

	canvasWeb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-Host") != "127.0.0.1:7777" || r.Header.Get("X-Forwarded-Proto") != "http" {
			t.Error("creative request lost its public origin")
		}
		switch r.URL.Path {
		case "/api/ideas":
			http.NotFound(w, r)
		case "/api/auth/callback":
			http.Redirect(w, r, "/login?next=%2Fcanvas%2Fone", http.StatusFound)
		case "/api/external":
			http.Redirect(w, r, "https://provider.example/authorize", http.StatusFound)
		default:
			fmt.Fprintf(w, "creative %s", r.URL.RequestURI())
		}
	}))
	defer canvasWeb.Close()

	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "api %s", r.URL.RequestURI())
	})
	web, creative, err := middleware.NewWebUpstreams(middleware.WebUpstreams{
		FrontendURL: frontend.URL, CreativeURL: identity.URL,
		CreativeWebURL: canvasWeb.URL, PublicOrigin: public,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := WithWeb(api, web, creative)

	for _, tc := range []struct {
		method, path, session, origin string
		status                        int
		body, location                string
	}{
		{"GET", "/", "", "", 200, "frontend /", ""},
		{"GET", "/health", "", "", 200, "api /health", ""},
		{"GET", "/creative", "", "", 303, "", "/creative/"},
		{"GET", "/workspace/creative?ref=one", "", "", 303, "", "/creative/?ref=one"},
		{"GET", "/creative/", "", "", 200, "creative /", ""},
		{"GET", "/creative/canvas/one", "user", "", 200, "creative /canvas/one", ""},
		{"GET", "/creative/api/ideas", "", "", 404, "", ""},
		{"GET", "/creative/api/auth/nous-session", "user", "", 404, "", ""},
		{"GET", "/creative/api/auth/callback", "", "", 302, "", "/creative/login?next=%2Fcanvas%2Fone"},
		{"GET", "/creative/api/external", "", "", 302, "", "https://provider.example/authorize"},
		{"POST", "/creative/api/auth/login", "", canvas, 403, "", ""},
		{"POST", "/creative/api/auth/login", "", public, 200, "creative /api/auth/login", ""},
		{"GET", "/login", "", "", 303, "", "/creative/login?next=%2Fnous%2Freturn"},
		{"GET", "/register?ref=one", "", "", 303, "", "/creative/register?next=%2Fnous%2Freturn&ref=one"},
		{"GET", "/workspace/chats/new", "", "", 303, "", "/login"},
		{"GET", "/api/agent/threads", "", "", 401, "", ""},
		{"GET", "/api/agent/threads", "user", "", 403, "", ""},
		{"GET", "/api/agent/threads", "admin", "", 200, "frontend /api/agent/threads", ""},
		{"GET", "/api/models", "admin", "", 200, "api /api/models", ""},
		{"GET", "/api/models", "expired", "", 401, "", ""},
		{"GET", "/api/models", "unavailable", "", 503, "", ""},
		{"POST", "/api/agent/threads", "admin", canvas, 403, "", ""},
		{"POST", "/api/agent/threads", "admin", public, 200, "frontend /api/agent/threads", ""},
		{"GET", "/api/auth/session", "user", "", 200, `"role":"user"`, ""},
		{"GET", "/api/auth/session", "", "", 200, `"user":null`, ""},
		{"POST", "/api/auth/logout", "admin", public, 204, "", ""},
	} {
		t.Run(tc.method+" "+tc.path+"/"+tc.session+"/"+tc.origin, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, public+tc.path, nil)
			req.AddCookie(&http.Cookie{Name: "unrelated", Value: "test"})
			if tc.session != "" {
				req.AddCookie(&http.Cookie{Name: "open_ai_canvas_session", Value: tc.session})
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			req.Header.Set("X-Nous-Role", "admin")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.body) || rec.Header().Get("Location") != tc.location {
				t.Fatalf("got HTTP %d, body %q, Location %q", rec.Code, rec.Body.String(), rec.Header().Get("Location"))
			}
			if tc.path == "/api/auth/logout" {
				cookies := rec.Result().Cookies()
				if len(cookies) != 1 || cookies[0].Name != "open_ai_canvas_session" || cookies[0].MaxAge != -1 {
					t.Fatalf("shared session was not cleared: %v", cookies)
				}
			}
		})
	}
}

func TestStandaloneWebHasNoSharedAuth(t *testing.T) {
	handler := WithWeb(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }), nil, nil)
	for path, status := range map[string]int{"/api/models": 200, "/creative/": 503, "/api/auth/session": 200} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != status {
			t.Errorf("%s: HTTP %d, want %d", path, rec.Code, status)
		}
	}
}
