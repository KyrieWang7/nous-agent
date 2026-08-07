package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/catalog"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/files"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/store"
)

type Server struct {
	catalog *catalog.Catalog
	files   *files.Manager
	store   *store.Store
	origins map[string]struct{}
	handler http.Handler
}

func New(catalog *catalog.Catalog, files *files.Manager, store *store.Store, origins []string) *Server {
	s := &Server{catalog: catalog, files: files, store: store, origins: make(map[string]struct{})}
	for _, origin := range origins {
		s.origins[origin] = struct{}{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /api/models", s.models)
	mux.HandleFunc("GET /api/models/{name}", s.model)
	mux.HandleFunc("GET /api/mcp/config", s.getMCP)
	mux.HandleFunc("PUT /api/mcp/config", s.putMCP)
	mux.HandleFunc("GET /api/skills", s.skills)
	mux.HandleFunc("GET /api/skills/{name}", s.skill)
	mux.HandleFunc("PUT /api/skills/{name}", s.putSkill)
	mux.HandleFunc("POST /api/skills/install", s.installSkill)
	mux.HandleFunc("GET /api/threads", s.threads)
	mux.HandleFunc("POST /api/threads", s.createThread)
	mux.HandleFunc("PATCH /api/threads/{id}", s.updateThread)
	mux.HandleFunc("DELETE /api/threads/{id}", s.deleteThread)
	mux.HandleFunc("POST /api/threads/{id}/uploads", s.upload)
	mux.HandleFunc("GET /api/threads/{id}/uploads/list", s.uploads)
	mux.HandleFunc("DELETE /api/threads/{id}/uploads/{name}", s.deleteUpload)
	mux.HandleFunc("GET /api/threads/{id}/artifacts/{path...}", s.artifact)
	mux.HandleFunc("GET /api/memory", s.memory)
	mux.HandleFunc("POST /api/memory/reload", s.memory)
	mux.HandleFunc("GET /api/memory/config", s.memoryConfig)
	mux.HandleFunc("GET /api/memory/status", s.memoryStatus)
	mux.HandleFunc("GET /api/swarm/teams", s.teams)
	mux.HandleFunc("GET /api/swarm/teams/{id}/members", s.members)
	mux.HandleFunc("GET /api/swarm/teams/{id}/stream", s.swarmStream)
	s.handler = s.middleware(mux)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if _, all := s.origins["*"]; all {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if _, allowed := s.origins[origin]; allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Last-Event-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Debug("gateway request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "healthy", "service": "nous-gateway-go", "database": s.store.Available()})
}

func (s *Server) models(w http.ResponseWriter, _ *http.Request) {
	models, err := s.catalog.Models()
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"models": models})
}

func (s *Server) model(w http.ResponseWriter, r *http.Request) {
	models, err := s.catalog.Models()
	if err != nil {
		problem(w, 500, err)
		return
	}
	for _, item := range models {
		if item.Name == r.PathValue("name") {
			writeJSON(w, 200, item)
			return
		}
	}
	problem(w, 404, errors.New("model not found"))
}

func (s *Server) getMCP(w http.ResponseWriter, _ *http.Request) {
	ext, err := s.catalog.Extensions()
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"mcp_servers": ext.MCPServers})
}

func (s *Server) putMCP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MCPServers map[string]catalog.MCPServer `json:"mcp_servers"`
	}
	if err := decode(r, &body); err != nil {
		problem(w, 400, err)
		return
	}
	ext, err := s.catalog.SaveMCP(body.MCPServers)
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"mcp_servers": ext.MCPServers})
}

func (s *Server) skills(w http.ResponseWriter, _ *http.Request) {
	items, err := s.catalog.Skills()
	if err != nil {
		problem(w, 500, err)
		return
	}
	for i := range items {
		items[i].Content = ""
	}
	writeJSON(w, 200, map[string]any{"skills": items})
}

func (s *Server) skill(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.Skill(r.PathValue("name"))
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

func (s *Server) putSkill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &body); err != nil {
		problem(w, 400, err)
		return
	}
	item, err := s.catalog.SetSkillEnabled(r.PathValue("name"), body.Enabled)
	if errors.Is(err, os.ErrNotExist) {
		problem(w, 404, err)
		return
	}
	if err != nil {
		problem(w, 500, err)
		return
	}
	item.Content = ""
	writeJSON(w, 200, item)
}

func (s *Server) installSkill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ThreadID string `json:"thread_id"`
		Path     string `json:"path"`
	}
	if err := decode(r, &body); err != nil {
		problem(w, 400, err)
		return
	}
	name, err := s.files.InstallSkill(body.ThreadID, body.Path)
	if err != nil {
		problem(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "skill_name": name, "message": fmt.Sprintf("Skill %q installed successfully", name)})
}

func (s *Server) threads(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 50, 1, 100)
	offset := queryInt(r, "offset", 0, 0, 1_000_000)
	items, total, err := s.store.ListThreads(r.Context(), limit, offset, r.URL.Query().Get("include_archived") == "true")
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"threads": items, "total": total})
}

func (s *Server) createThread(w http.ResponseWriter, r *http.Request) {
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
	item, err := s.store.CreateThread(r.Context(), body.ID, body.Title, body.ModelName)
	if err != nil {
		problem(w, 503, err)
		return
	}
	writeJSON(w, 200, item)
}

func (s *Server) updateThread(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title      *string `json:"title"`
		ModelName  *string `json:"model_name"`
		IsArchived *bool   `json:"is_archived"`
	}
	if err := decode(r, &body); err != nil {
		problem(w, 400, err)
		return
	}
	item, err := s.store.UpdateThread(r.Context(), r.PathValue("id"), store.ThreadPatch{Title: body.Title, ModelName: body.ModelName, IsArchived: body.IsArchived})
	if errors.Is(err, store.ErrNotFound) {
		problem(w, 404, err)
		return
	}
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, item)
}

func (s *Server) deleteThread(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteThread(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		problem(w, 404, err)
		return
	}
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		problem(w, 400, err)
		return
	}
	items, err := s.files.SaveUploads(r.Context(), r.PathValue("id"), r.MultipartForm.File["files"])
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "files": items, "message": fmt.Sprintf("Successfully uploaded %d file(s)", len(items))})
}

func (s *Server) uploads(w http.ResponseWriter, r *http.Request) {
	items, err := s.files.ListUploads(r.PathValue("id"))
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"files": items, "count": len(items)})
}

func (s *Server) deleteUpload(w http.ResponseWriter, r *http.Request) {
	err := s.files.DeleteUpload(r.PathValue("id"), r.PathValue("name"))
	if errors.Is(err, os.ErrNotExist) {
		problem(w, 404, err)
		return
	}
	if err != nil {
		problem(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "Deleted " + r.PathValue("name")})
}

func (s *Server) artifact(w http.ResponseWriter, r *http.Request) {
	path, err := s.files.Artifact(r.PathValue("id"), r.PathValue("path"))
	if err != nil {
		problem(w, 403, err)
		return
	}
	stat, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		problem(w, 404, err)
		return
	}
	if err != nil {
		problem(w, 500, err)
		return
	}
	if stat.IsDir() {
		problem(w, 400, errors.New("path is not a file"))
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if r.URL.Query().Get("download") == "true" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(path)))
	} else {
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filepath.Base(path)))
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeFile(w, r, path)
}

type memoryResponse struct {
	Version     string         `json:"version"`
	LastUpdated string         `json:"lastUpdated"`
	User        map[string]any `json:"user"`
	History     map[string]any `json:"history"`
	Facts       []store.Fact   `json:"facts"`
}

func (s *Server) memory(w http.ResponseWriter, r *http.Request) {
	facts, err := s.store.Facts(r.Context())
	if err != nil {
		problem(w, 500, err)
		return
	}
	last := ""
	if len(facts) > 0 {
		last = facts[0].CreatedAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, 200, memoryResponse{Version: "1.0", LastUpdated: last, User: emptyUser(), History: emptyHistory(), Facts: facts})
}

func (s *Server) memoryConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, memoryConfiguration())
}
func (s *Server) memoryStatus(w http.ResponseWriter, r *http.Request) {
	facts, _ := s.store.Facts(r.Context())
	writeJSON(w, 200, map[string]any{"config": memoryConfiguration(), "data": memoryResponse{Version: "1.0", User: emptyUser(), History: emptyHistory(), Facts: facts}})
}

func (s *Server) teams(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.Teams(r.Context(), r.URL.Query().Get("thread_id"))
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"teams": items})
}
func (s *Server) members(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.Members(r.Context(), r.PathValue("id"))
	if err != nil {
		problem(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"members": items})
}
func (s *Server) swarmStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		problem(w, 500, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	members, err := s.store.Members(r.Context(), r.PathValue("id"))
	if err != nil {
		sse(w, "error", 0, map[string]string{"error": err.Error()})
		flusher.Flush()
		return
	}
	sse(w, "team_update", 0, members)
	flusher.Flush()
	after := int64(0)
	if value := r.Header.Get("Last-Event-ID"); value != "" {
		after, _ = strconv.ParseInt(value, 10, 64)
	}
	ticker := time.NewTicker(time.Second)
	heartbeat := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			messages, err := s.store.Messages(r.Context(), r.PathValue("id"), after, 100)
			if err != nil {
				sse(w, "error", 0, map[string]string{"error": err.Error()})
				flusher.Flush()
				continue
			}
			for _, message := range messages {
				after = message.ID
				sse(w, "message", message.ID, message)
			}
			if len(messages) > 0 {
				flusher.Flush()
			}
		case now := <-heartbeat.C:
			sse(w, "heartbeat", 0, map[string]string{"timestamp": now.UTC().Format(time.RFC3339)})
			flusher.Flush()
		}
	}
}

func memoryConfiguration() map[string]any {
	return map[string]any{"enabled": true, "storage_path": "postgresql", "debounce_seconds": 0, "max_facts": 50, "fact_confidence_threshold": 0.7, "injection_enabled": true, "max_injection_tokens": 1000}
}
func section() map[string]string { return map[string]string{"summary": "", "updatedAt": ""} }
func emptyUser() map[string]any {
	return map[string]any{"workContext": section(), "personalContext": section(), "topOfMind": section()}
}
func emptyHistory() map[string]any {
	return map[string]any{"recentMonths": section(), "earlierContext": section(), "longTermBackground": section()}
}

func decode(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func problem(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"detail": err.Error()})
}
func queryInt(r *http.Request, key string, fallback, min, max int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return fallback
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
func sse(w http.ResponseWriter, event string, id int64, value any) {
	raw, _ := json.Marshal(value)
	if id > 0 {
		fmt.Fprintf(w, "id: %d\n", id)
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
}

func Shutdown(ctx context.Context, server *http.Server) error { return server.Shutdown(ctx) }
