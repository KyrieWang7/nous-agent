// Package remote implements the fail-closed Remote Sandbox API v2 client.
package remote

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

const maxResponseBytes = 16 << 20

var errInvalidLease = errors.New("remote sandbox: invalid or expired lease")

type Options struct {
	BaseURL, TenantID, VirtualRoot string
	Headers                        map[string]string
	ExecTimeout                    time.Duration
	Client                         *http.Client
}

type Provider struct {
	opts   Options
	mu     sync.Mutex
	leases map[string]*handle
}

func NewProvider(opts Options) (*Provider, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("remote sandbox: base URL must be absolute HTTP(S)")
	}
	if strings.TrimSpace(opts.TenantID) == "" {
		return nil, errors.New("remote sandbox: tenant id is required")
	}
	if opts.VirtualRoot == "" {
		opts.VirtualRoot = "/mnt/user-data"
	}
	if opts.ExecTimeout <= 0 {
		opts.ExecTimeout = 30 * time.Second
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 2 * opts.ExecTimeout}
	}
	opts.BaseURL = strings.TrimRight(opts.BaseURL, "/")
	return &Provider{opts: opts, leases: make(map[string]*handle)}, nil
}

type acquireResponse struct {
	ID        string    `json:"id"`
	LeaseID   string    `json:"lease_id"`
	Root      string    `json:"root"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (p *Provider) Acquire(ctx context.Context, threadID string) (sandbox.Handle, error) {
	if strings.TrimSpace(threadID) == "" {
		return nil, errors.New("remote sandbox: thread id is required")
	}
	p.mu.Lock()
	if existing := p.leases[threadID]; existing != nil {
		p.mu.Unlock()
		err := p.heartbeat(ctx, existing)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, errInvalidLease) && !errors.Is(err, sandbox.ErrNotFound) {
			return nil, err
		}
		p.forget(threadID, existing)
	} else {
		p.mu.Unlock()
	}
	var response acquireResponse
	if err := p.call(ctx, http.MethodPost, "/v2/sandboxes/acquire", map[string]any{"tenant_id": p.opts.TenantID, "thread_id": threadID}, &response); err != nil {
		return nil, err
	}
	if response.ID == "" || response.LeaseID == "" {
		return nil, errors.New("remote sandbox: acquire returned incomplete lease")
	}
	if response.Root == "" {
		response.Root = p.opts.VirtualRoot
	}
	h := &handle{provider: p, id: response.ID, leaseID: response.LeaseID, root: response.Root, threadID: threadID}
	h.fs = &remoteFS{handle: h}
	p.mu.Lock()
	if existing := p.leases[threadID]; existing != nil {
		p.mu.Unlock()
		return existing, nil
	}
	p.leases[threadID] = h
	p.mu.Unlock()
	return h, nil
}

func (p *Provider) Release(ctx context.Context, threadID string) error {
	p.mu.Lock()
	h := p.leases[threadID]
	p.mu.Unlock()
	if h == nil {
		return nil
	}
	endpoint := "/v2/sandboxes/" + url.PathEscape(h.id) + "?lease_id=" + url.QueryEscape(h.leaseID)
	err := p.call(ctx, http.MethodDelete, endpoint, nil, nil)
	if err != nil && !errors.Is(err, errInvalidLease) && !errors.Is(err, sandbox.ErrNotFound) {
		return err
	}
	p.forget(threadID, h)
	return nil
}

func (p *Provider) heartbeat(ctx context.Context, h *handle) error {
	endpoint := "/v2/sandboxes/" + url.PathEscape(h.id) + "/heartbeat"
	return p.call(ctx, http.MethodPost, endpoint, map[string]string{"lease_id": h.leaseID}, nil)
}

func (p *Provider) forget(threadID string, h *handle) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.leases[threadID] == h {
		delete(p.leases, threadID)
	}
}

func (p *Provider) call(ctx context.Context, method, endpoint string, requestBody, responseBody any) error {
	var body io.Reader
	if requestBody != nil {
		raw, err := json.Marshal(requestBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.opts.BaseURL+endpoint, body)
	if err != nil {
		return err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range p.opts.Headers {
		req.Header.Set(name, value)
	}
	resp, err := p.opts.Client.Do(req)
	if err != nil {
		return fmt.Errorf("remote sandbox: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxResponseBytes {
		return errors.New("remote sandbox: response exceeds limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusNotFound {
			return sandbox.ErrNotFound
		}
		if resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: %s", errInvalidLease, strings.TrimSpace(string(raw)))
		}
		return fmt.Errorf("remote sandbox: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if responseBody != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, responseBody); err != nil {
			return fmt.Errorf("remote sandbox: decoding response: %w", err)
		}
	}
	return nil
}

type handle struct {
	provider                    *Provider
	id, leaseID, root, threadID string
	fs                          *remoteFS
}

func (h *handle) ID() string     { return h.id }
func (h *handle) Root() string   { return h.root }
func (h *handle) FS() sandbox.FS { return h.fs }
func (h *handle) body(ctx context.Context, fields map[string]any) (map[string]any, error) {
	mode, err := sandbox.ModeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if fields == nil {
		fields = make(map[string]any)
	}
	fields["lease_id"], fields["sandbox_mode"] = h.leaseID, string(mode)
	return fields, nil
}
func (h *handle) Exec(ctx context.Context, command sandbox.Command) (*sandbox.ExecResult, error) {
	if strings.TrimSpace(command.Line) == "" {
		return nil, errors.New("remote sandbox: command is required")
	}
	if command.Timeout <= 0 {
		command.Timeout = h.provider.opts.ExecTimeout
	}
	body, err := h.body(ctx, map[string]any{"line": command.Line, "work_dir": command.WorkDir, "timeout_ms": command.Timeout.Milliseconds(), "env": command.Env})
	if err != nil {
		return nil, err
	}
	var response sandbox.ExecResult
	err = h.provider.call(ctx, http.MethodPost, h.endpoint("/exec"), body, &response)
	return &response, err
}
func (h *handle) endpoint(suffix string) string {
	return "/v2/sandboxes/" + url.PathEscape(h.id) + suffix
}

type remoteFS struct{ handle *handle }

func (f *remoteFS) Resolve(virtual string) (string, error) {
	root, clean := path.Clean(f.handle.root), path.Clean(virtual)
	if !strings.HasPrefix(clean, "/") {
		clean = path.Join(root, clean)
	}
	if clean != root && !strings.HasPrefix(clean, root+"/") {
		return "", sandbox.ErrEscape
	}
	return clean, nil
}
func (f *remoteFS) request(ctx context.Context, endpoint, filename string, fields map[string]any, response any) error {
	resolved, err := f.Resolve(filename)
	if err != nil {
		return err
	}
	if fields == nil {
		fields = make(map[string]any)
	}
	fields["path"] = resolved
	body, err := f.handle.body(ctx, fields)
	if err != nil {
		return err
	}
	return f.handle.provider.call(ctx, http.MethodPost, f.handle.endpoint(endpoint), body, response)
}
func (f *remoteFS) ReadFile(ctx context.Context, filename string) ([]byte, error) {
	var response struct {
		Data string `json:"data_base64"`
	}
	if err := f.request(ctx, "/fs/read", filename, nil, &response); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(response.Data)
	if err != nil {
		return nil, fmt.Errorf("remote sandbox: decoding file: %w", err)
	}
	if int64(len(data)) > sandbox.MaxReadFileBytes {
		return nil, sandbox.ErrReadLimit
	}
	return data, nil
}
func (f *remoteFS) WriteFile(ctx context.Context, filename string, data []byte) error {
	return f.request(ctx, "/fs/write", filename, map[string]any{"data_base64": base64.StdEncoding.EncodeToString(data)}, nil)
}
func (f *remoteFS) List(ctx context.Context, filename string) ([]sandbox.Entry, error) {
	entries, _, err := f.list(ctx, filename, 0)
	return entries, err
}
func (f *remoteFS) ListLimit(ctx context.Context, filename string, limit int) ([]sandbox.Entry, bool, error) {
	if limit <= 0 {
		return nil, false, errors.New("remote sandbox: list limit must be positive")
	}
	return f.list(ctx, filename, limit)
}
func (f *remoteFS) list(ctx context.Context, filename string, limit int) ([]sandbox.Entry, bool, error) {
	requestLimit := limit
	if requestLimit > 0 {
		requestLimit++
	}
	var response struct {
		Entries []remoteEntry `json:"entries"`
	}
	if err := f.request(ctx, "/fs/list", filename, map[string]any{"limit": requestLimit}, &response); err != nil {
		return nil, false, err
	}
	truncated := limit > 0 && len(response.Entries) > limit
	if truncated {
		response.Entries = response.Entries[:limit]
	}
	entries := make([]sandbox.Entry, 0, len(response.Entries))
	for _, entry := range response.Entries {
		entries = append(entries, entry.sandboxEntry())
	}
	return entries, truncated, nil
}
func (f *remoteFS) Stat(ctx context.Context, filename string) (sandbox.Entry, error) {
	var response remoteEntry
	err := f.request(ctx, "/fs/stat", filename, nil, &response)
	return response.sandboxEntry(), err
}

type remoteEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
	Mode  string `json:"mode"`
}

func (e remoteEntry) sandboxEntry() sandbox.Entry {
	return sandbox.Entry{Name: e.Name, IsDir: e.IsDir, Size: e.Size, Mode: e.Mode}
}
