// Package aio integrates the existing agent-infra/AIO sandbox service.
package aio

import (
	"bytes"
	"context"
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

type Options struct {
	ProvisionerURL string
	Headers        map[string]string
	VirtualRoot    string
	ExecTimeout    time.Duration
	Client         *http.Client
}
type Provider struct {
	opts    Options
	mu      sync.Mutex
	handles map[string]*handle
}

func NewProvider(opts Options) (*Provider, error) {
	if strings.TrimSpace(opts.ProvisionerURL) == "" {
		return nil, errors.New("aio sandbox: provisioner URL is required")
	}
	u, err := url.Parse(strings.TrimRight(opts.ProvisionerURL, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("aio sandbox: provisioner URL must be absolute")
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
	return &Provider{opts: opts, handles: map[string]*handle{}}, nil
}
func (p *Provider) Acquire(ctx context.Context, threadID string) (sandbox.Handle, error) {
	if threadID == "" {
		return nil, errors.New("aio sandbox: thread id is required")
	}
	p.mu.Lock()
	if h := p.handles[threadID]; h != nil {
		p.mu.Unlock()
		return h, nil
	}
	p.mu.Unlock()
	var out struct {
		ID  string `json:"sandbox_id"`
		URL string `json:"sandbox_url"`
	}
	if err := p.call(ctx, http.MethodPost, "/api/sandboxes", map[string]string{"sandbox_id": threadID, "thread_id": threadID}, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		out.ID = threadID
	}
	if out.URL == "" {
		return nil, errors.New("aio sandbox: provisioner returned no sandbox_url")
	}
	h := &handle{provider: p, id: out.ID, base: strings.TrimRight(out.URL, "/"), root: p.opts.VirtualRoot}
	h.fs = &remoteFS{handle: h}
	p.mu.Lock()
	p.handles[threadID] = h
	p.mu.Unlock()
	return h, nil
}
func (p *Provider) Release(ctx context.Context, threadID string) error {
	p.mu.Lock()
	h := p.handles[threadID]
	delete(p.handles, threadID)
	p.mu.Unlock()
	if h == nil {
		return nil
	}
	return p.call(ctx, http.MethodDelete, "/api/sandboxes/"+url.PathEscape(h.id), nil, nil)
}
func (p *Provider) call(ctx context.Context, method, endpoint string, input, output any) error {
	var body io.Reader
	if input != nil {
		raw, e := json.Marshal(input)
		if e != nil {
			return e
		}
		body = bytes.NewReader(raw)
	}
	req, e := http.NewRequestWithContext(ctx, method, p.opts.ProvisionerURL+endpoint, body)
	if e != nil {
		return e
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range p.opts.Headers {
		req.Header.Set(k, v)
	}
	resp, e := p.opts.Client.Do(req)
	if e != nil {
		return fmt.Errorf("aio sandbox: %w", e)
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if e != nil {
		return e
	}
	if len(raw) > maxResponseBytes {
		return errors.New("aio sandbox: response exceeds limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("aio sandbox: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if output != nil && len(raw) > 0 {
		if e = json.Unmarshal(raw, output); e != nil {
			return fmt.Errorf("aio sandbox: decode response: %w", e)
		}
	}
	return nil
}

type handle struct {
	provider       *Provider
	id, base, root string
	fs             *remoteFS
}

func (h *handle) ID() string     { return h.id }
func (h *handle) Root() string   { return h.root }
func (h *handle) FS() sandbox.FS { return h.fs }
func (h *handle) Exec(ctx context.Context, c sandbox.Command) (*sandbox.ExecResult, error) {
	mode, e := sandbox.ModeFromContext(ctx)
	if e != nil {
		return nil, e
	}
	if mode == sandbox.ModeReadOnly {
		return nil, errors.New("aio sandbox: exec requires workspace-write or danger-full-access")
	}
	if c.Timeout <= 0 {
		c.Timeout = h.provider.opts.ExecTimeout
	}
	var out struct {
		Output   string `json:"output"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exit_code"`
		TimedOut bool   `json:"timed_out"`
	}
	body := map[string]any{"command": c.Line}
	if c.WorkDir != "" {
		body["workdir"] = c.WorkDir
	}
	if e = h.provider.callBase(ctx, h.base, http.MethodPost, "/api/execute", body, &out); e != nil {
		return nil, e
	}
	return &sandbox.ExecResult{Stdout: out.Output, Stderr: out.Stderr, ExitCode: out.ExitCode, TimedOut: out.TimedOut}, nil
}
func (h *handle) callBase(ctx context.Context, method, endpoint string, input, output any) error {
	return h.provider.callBase(ctx, h.base, method, endpoint, input, output)
}
func (p *Provider) callBase(ctx context.Context, base, method, endpoint string, input, output any) error {
	var body io.Reader
	if input != nil {
		raw, e := json.Marshal(input)
		if e != nil {
			return e
		}
		body = bytes.NewReader(raw)
	}
	req, e := http.NewRequestWithContext(ctx, method, base+endpoint, body)
	if e != nil {
		return e
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range p.opts.Headers {
		req.Header.Set(k, v)
	}
	resp, e := p.opts.Client.Do(req)
	if e != nil {
		return fmt.Errorf("aio sandbox: %w", e)
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if e != nil {
		return e
	}
	if len(raw) > maxResponseBytes {
		return errors.New("aio sandbox: response exceeds limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("aio sandbox: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if output != nil && len(raw) > 0 {
		if e = json.Unmarshal(raw, output); e != nil {
			return fmt.Errorf("aio sandbox: decode response: %w", e)
		}
	}
	return nil
}

type remoteFS struct{ handle *handle }

func (f *remoteFS) Resolve(v string) (string, error) {
	root, clean := path.Clean(f.handle.root), path.Clean(v)
	if !strings.HasPrefix(clean, "/") {
		clean = path.Join(root, clean)
	}
	if clean != root && !strings.HasPrefix(clean, root+"/") {
		return "", sandbox.ErrEscape
	}
	return clean, nil
}
func (f *remoteFS) ReadFile(ctx context.Context, name string) ([]byte, error) {
	resolved, e := f.Resolve(name)
	if e != nil {
		return nil, e
	}
	var out struct {
		Content string `json:"content"`
	}
	if e = f.handle.callBase(ctx, http.MethodPost, "/api/read_file", map[string]string{"path": resolved}, &out); e != nil {
		return nil, e
	}
	data := []byte(out.Content)
	if int64(len(data)) > sandbox.MaxReadFileBytes {
		return nil, sandbox.ErrReadLimit
	}
	return data, nil
}
func (f *remoteFS) WriteFile(ctx context.Context, name string, data []byte) error {
	mode, e := sandbox.ModeFromContext(ctx)
	if e != nil {
		return e
	}
	if mode == sandbox.ModeReadOnly {
		return errors.New("aio sandbox: write requires workspace-write or danger-full-access")
	}
	resolved, e := f.Resolve(name)
	if e != nil {
		return e
	}
	return f.handle.callBase(ctx, http.MethodPost, "/api/write_file", map[string]string{"path": resolved, "content": string(data)}, nil)
}
func (f *remoteFS) List(ctx context.Context, name string) ([]sandbox.Entry, error) {
	entries, _, e := f.ListLimit(ctx, name, 1000)
	return entries, e
}
func (f *remoteFS) ListLimit(ctx context.Context, name string, limit int) ([]sandbox.Entry, bool, error) {
	if limit <= 0 {
		return nil, false, errors.New("aio sandbox: list limit must be positive")
	}
	resolved, e := f.Resolve(name)
	if e != nil {
		return nil, false, e
	}
	var out struct {
		Entries []string `json:"entries"`
	}
	if e = f.handle.callBase(ctx, http.MethodPost, "/api/list_dir", map[string]any{"path": resolved, "max_depth": 1}, &out); e != nil {
		return nil, false, e
	}
	truncated := len(out.Entries) > limit
	if truncated {
		out.Entries = out.Entries[:limit]
	}
	result := make([]sandbox.Entry, 0, len(out.Entries))
	for _, name := range out.Entries {
		clean := strings.TrimSpace(name)
		if clean == "" {
			continue
		}
		isDir := strings.HasSuffix(clean, "/")
		clean = strings.TrimSuffix(clean, "/")
		result = append(result, sandbox.Entry{Name: path.Base(clean), IsDir: isDir, Mode: ""})
	}
	return result, truncated, nil
}
func (f *remoteFS) Stat(ctx context.Context, name string) (sandbox.Entry, error) {
	entries, e := f.List(ctx, path.Dir(name))
	if e != nil {
		return sandbox.Entry{}, e
	}
	base := path.Base(name)
	for _, entry := range entries {
		if entry.Name == base {
			return entry, nil
		}
	}
	return sandbox.Entry{}, sandbox.ErrNotFound
}
