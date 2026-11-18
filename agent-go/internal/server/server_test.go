package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/model"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/repository"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/runmanager"
)

type agentFunc func(context.Context, runmanager.AgentRequest) (runmanager.AgentResult, error)

func (f agentFunc) Run(ctx context.Context, req runmanager.AgentRequest) (runmanager.AgentResult, error) {
	return f(ctx, req)
}

// Exercise the production composition, including middleware and ownership checks.
func TestServerCompositionAndRunOwnership(t *testing.T) {
	store := repository.NewMemoryStore()
	if err := store.CreateRun(context.Background(), model.Run{RunID: "run", ThreadID: "owner", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Options{Store: store, Agent: agentFunc(func(context.Context, runmanager.AgentRequest) (runmanager.AgentResult, error) {
		return runmanager.AgentResult{Output: "done"}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/ok", "", http.StatusOK},
		{http.MethodOptions, "/api/v1/threads", "", http.StatusNoContent},
		{http.MethodGet, "/api/v1/threads/other/runs/run", "", http.StatusNotFound},
		{http.MethodGet, "/api/v1/threads/other/runs/run/events", "", http.StatusNotFound},
		{http.MethodPost, "/api/v1/threads/other/runs/run/cancel", "", http.StatusNotFound},
		{http.MethodPost, "/api/v1/threads/owner/runs", `{"input":{"messages":[{"type":"human","content":"hello"}]}}`, http.StatusOK},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			srv.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if response.Header().Get("Access-Control-Allow-Origin") != "*" {
				t.Fatal("CORS middleware missing")
			}
			if tc.body != "" && !strings.Contains(response.Body.String(), "event: end\n") {
				t.Fatalf("run did not complete: %s", response.Body.String())
			}
		})
	}
}
