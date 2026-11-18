package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/repository"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/repository/catalog"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/repository/files"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/service"
)

func TestProductionCompositionAndCORS(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("models:\n  - name: demo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := repository.Open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	app := New(service.Options{Catalog: catalog.New(config, filepath.Join(root, "extensions.json"), root), Files: files.New(root, root), Repository: store, Swarm: store}, []string{"https://allowed.example"})
	for _, tc := range []struct {
		method, path, origin string
		status               int
	}{
		{http.MethodGet, "/health", "https://allowed.example", http.StatusOK},
		{http.MethodOptions, "/api/models", "https://allowed.example", http.StatusNoContent},
		{http.MethodGet, "/api/models/demo", "https://allowed.example", http.StatusOK},
		{http.MethodGet, "/api/models/missing", "https://allowed.example", http.StatusNotFound},
		{http.MethodGet, "/api/models/demo", "https://blocked.example", http.StatusOK},
	} {
		t.Run(tc.method+tc.path+tc.origin, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, nil)
			request.Header.Set("Origin", tc.origin)
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			want := ""
			if strings.Contains(tc.origin, "allowed") {
				want = tc.origin
			}
			if got := response.Header().Get("Access-Control-Allow-Origin"); got != want {
				t.Fatalf("CORS origin=%q want=%q", got, want)
			}
		})
	}
}
