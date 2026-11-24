package router

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/KyrieWang7/nous-agent/manager/manager_service/api/v1"
	handler "github.com/KyrieWang7/nous-agent/manager/manager_service/internal/handler/ideas"
	"github.com/KyrieWang7/nous-agent/manager/manager_service/internal/middleware"
	repository "github.com/KyrieWang7/nous-agent/manager/manager_service/internal/repository/ideas"
	service "github.com/KyrieWang7/nous-agent/manager/manager_service/internal/service/ideas"
	"github.com/KyrieWang7/nous-agent/platform-go/postgres"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

func TestIdeasHTTPOnPostgres(t *testing.T) {
	database := os.Getenv("MANAGER_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("MANAGER_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := postgres.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	schema := "manager_test_" + hex.EncodeToString(suffix[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(clean, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop scratch schema: %v", err)
		}
	}()

	dsn, err := url.Parse(database)
	if err != nil {
		t.Fatal(err)
	}
	query := dsn.Query()
	query.Set("search_path", schema)
	dsn.RawQuery = query.Encode()
	db, err := postgres.Open(ctx, dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	path := filepath.Join(t.TempDir(), "_ideas.json")
	seed := []byte(`[{"id":"legacy-idea","title":"旧选题","note":"旧备注","source":"历史","status":"done","created":1700000000}]`)
	if err := os.WriteFile(path, seed, 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := repository.Migrate(ctx, db.ORM, path); err != nil {
			t.Fatal(err)
		}
	}
	engine := New(handler.New(service.New(repository.New(db.ORM)), zap.NewNop()),
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("legacy"))
		}), middleware.TrustedGateway("test-token"))

	request := func(method, path, payload string, token bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(payload))
		r.Header.Set("Content-Type", "application/json")
		if token {
			r.Header.Set(middleware.TokenHeader, "test-token")
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		return w
	}
	if response := request("GET", "/api/ideas", "", false); response.Code != 403 {
		t.Fatalf("unauthorized status = %d", response.Code)
	}
	if response := request("POST", "/api/ideas", "{}", true); response.Code != 422 {
		t.Fatalf("invalid request status = %d", response.Code)
	}
	root := request("GET", "/", "", true)
	if root.Code != 200 || root.Body.String() != "legacy" {
		t.Fatalf("legacy route = %d %q", root.Code, root.Body.String())
	}
	if res := request("POST", "/api/ideas/?keep=1", "{}", true); res.Code != 307 || res.Header().Get("Location") != "/api/ideas?keep=1" {
		t.Fatalf("redirect = %d %q", res.Code, res.Header().Get("Location"))
	}
	if res := request("PATCH", "/api/ideas/id", "{}", true); res.Code != 405 {
		t.Fatalf("unsupported method = %d", res.Code)
	}
	if res := request("GET", "/api/ideas/id/extra", "", true); res.Code != 404 {
		t.Fatalf("unknown idea route = %d", res.Code)
	}

	created := request("POST", "/api/ideas", `{"title":" 新选题 ","note":"备注","status":"unknown"}`, true)
	var item v1.Idea
	if created.Code != 200 || json.Unmarshal(created.Body.Bytes(), &item) != nil ||
		item.Title != "新选题" || item.Status != "pending" || len(item.ID) != 12 {
		t.Fatalf("create = %d %s", created.Code, created.Body)
	}
	updated := request("PUT", "/api/ideas/"+item.ID, `{"title":" ","status":"doing"}`, true)
	if updated.Code != 200 || json.Unmarshal(updated.Body.Bytes(), &item) != nil || item.Title != "新选题" || item.Status != "doing" || item.Note != "" {
		t.Fatalf("update = %d %s", updated.Code, updated.Body)
	}

	list := request("GET", "/api/ideas", "", true)
	var rows []v1.Idea
	if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &rows) != nil || len(rows) != 2 ||
		rows[0].ID != item.ID || rows[1].ID != "legacy-idea" || rows[1].Created != 1700000000 {
		t.Fatalf("list = %d %s", list.Code, list.Body)
	}
	if deleted := request("DELETE", "/api/ideas/"+item.ID, "", true); deleted.Code != 200 {
		t.Fatalf("delete = %d %s", deleted.Code, deleted.Body)
	}
	if missing := request("DELETE", "/api/ideas/"+item.ID, "", true); missing.Code != 404 {
		t.Fatalf("missing delete = %d %s", missing.Code, missing.Body)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(seed) {
		t.Fatal("migration changed the legacy data file")
	}
}
