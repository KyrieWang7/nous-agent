// Package sandboxapi exposes the authenticated Remote Sandbox API v2.
package sandboxapi

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox/controller"
)

const maxRequestBytes = 16 << 20

type Server struct {
	manager *controller.Manager
	token   string
}

func New(manager *controller.Manager, token string) (*Server, error) {
	if manager == nil {
		return nil, errors.New("sandbox API: manager is required")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("sandbox API: bearer token is required")
	}
	return &Server{manager: manager, token: token}, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/ok" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v2/capabilities" {
		writeJSON(w, http.StatusOK, map[string]any{"api_version": "v2", "enforcement": s.manager.Enforcement()})
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/v2/sandboxes/acquire" {
		s.acquire(w, r)
		return
	}
	const prefix = "/v2/sandboxes/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	remainder := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.Split(remainder, "/")
	id, err := url.PathUnescape(parts[0])
	if err != nil || id == "" {
		writeError(w, http.StatusBadRequest, "invalid sandbox id")
		return
	}
	suffix := ""
	if len(parts) > 1 {
		suffix = "/" + strings.Join(parts[1:], "/")
	}
	s.dispatchSandbox(w, r, id, suffix)
}

func (s *Server) authorized(r *http.Request) bool {
	want := "Bearer " + s.token
	got := r.Header.Get("Authorization")
	return len(got) == len(want) && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (s *Server) acquire(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TenantID string `json:"tenant_id"`
		ThreadID string `json:"thread_id"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	lease, err := s.manager.Acquire(r.Context(), strings.TrimSpace(body.TenantID), strings.TrimSpace(body.ThreadID))
	if err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lease)
}

func (s *Server) dispatchSandbox(w http.ResponseWriter, r *http.Request, id, suffix string) {
	if suffix == "" {
		leaseID := r.URL.Query().Get("lease_id")
		switch r.Method {
		case http.MethodGet:
			lease, err := s.manager.Status(id, leaseID)
			if err != nil {
				writeManagerError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, lease)
		case http.MethodDelete:
			if err := s.manager.Release(r.Context(), id, leaseID); err != nil {
				writeManagerError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body operationRequest
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	if suffix == "/heartbeat" {
		lease, err := s.manager.Status(id, body.LeaseID)
		if err != nil {
			writeManagerError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, lease)
		return
	}
	handle, _, err := s.manager.Use(id, body.LeaseID)
	if err != nil {
		writeManagerError(w, err)
		return
	}
	mode, err := parseMode(body.SandboxMode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := sandbox.WithMode(r.Context(), mode)
	switch suffix {
	case "/exec":
		s.exec(w, ctx, handle, body)
	case "/fs/read":
		data, err := handle.FS().ReadFile(ctx, body.Path)
		if err != nil {
			writeSandboxError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"data_base64": base64.StdEncoding.EncodeToString(data)})
	case "/fs/write":
		if mode == sandbox.ModeReadOnly {
			writeError(w, http.StatusForbidden, "read-only sandbox mode denies writes")
			return
		}
		data, err := base64.StdEncoding.DecodeString(body.DataBase64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid data_base64")
			return
		}
		if err := handle.FS().WriteFile(ctx, body.Path, data); err != nil {
			writeSandboxError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "/fs/list":
		if body.Limit <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be positive")
			return
		}
		entries, _, err := handle.FS().ListLimit(ctx, body.Path, body.Limit)
		if err != nil {
			writeSandboxError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
	case "/fs/stat":
		entry, err := handle.FS().Stat(ctx, body.Path)
		if err != nil {
			writeSandboxError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, entry)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

type operationRequest struct {
	LeaseID     string   `json:"lease_id"`
	SandboxMode string   `json:"sandbox_mode"`
	Line        string   `json:"line"`
	WorkDir     string   `json:"work_dir"`
	TimeoutMS   int64    `json:"timeout_ms"`
	Env         []string `json:"env"`
	Path        string   `json:"path"`
	DataBase64  string   `json:"data_base64"`
	Limit       int      `json:"limit"`
}

func (s *Server) exec(w http.ResponseWriter, ctx context.Context, handle sandbox.Handle, body operationRequest) {
	if strings.TrimSpace(body.Line) == "" {
		writeError(w, http.StatusBadRequest, "line is required")
		return
	}
	if body.TimeoutMS < 0 {
		writeError(w, http.StatusBadRequest, "timeout_ms cannot be negative")
		return
	}
	result, err := handle.Exec(ctx, sandbox.Command{Line: body.Line, WorkDir: body.WorkDir, Timeout: time.Duration(body.TimeoutMS) * time.Millisecond, Env: body.Env})
	if err != nil {
		writeSandboxError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func parseMode(value string) (sandbox.Mode, error) {
	mode := sandbox.Mode(value)
	switch mode {
	case sandbox.ModeReadOnly, sandbox.ModeWorkspaceWrite, sandbox.ModeDangerFullAccess:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid sandbox_mode %q", value)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return errors.New("multiple JSON values")
	}
	return nil
}

func writeManagerError(w http.ResponseWriter, err error) {
	if errors.Is(err, controller.ErrLease) {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

func writeSandboxError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, sandbox.ErrNotFound) {
		status = http.StatusNotFound
	}
	writeError(w, status, err.Error())
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
