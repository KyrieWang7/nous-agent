// Package remote implements the versioned HTTP sandbox provider used by
// remote and Kubernetes-backed sandbox services.
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
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

const maxResponseBytes = 16 << 20

type Options struct {
	BaseURL     string
	Headers     map[string]string
	VirtualRoot string
	ExecTimeout time.Duration
	Client      *http.Client
}

type Provider struct {
	opts Options
}

func NewProvider(opts Options) (*Provider, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("remote sandbox: base URL must be absolute HTTP(S)")
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
	return &Provider{opts: opts}, nil
}

func (p *Provider) Acquire(ctx context.Context, key string) (sandbox.Handle, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("remote sandbox: key is required")
	}
	var response struct {
		ID   string `json:"id"`
		Root string `json:"root"`
	}
	if err := p.call(ctx, http.MethodPost, "/v1/sandboxes/"+url.PathEscape(key)+"/acquire", map[string]any{}, &response); err != nil {
		return nil, err
	}
	if response.ID == "" {
		return nil, errors.New("remote sandbox: acquire returned no id")
	}
	if response.Root == "" {
		response.Root = p.opts.VirtualRoot
	}
	h := &handle{provider: p, id: response.ID, root: response.Root}
	h.fs = &remoteFS{handle: h}
	return h, nil
}

func (p *Provider) Release(ctx context.Context, key string) error {
	return p.call(ctx, http.MethodDelete, "/v1/sandboxes/"+url.PathEscape(key), nil, nil)
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
	request, err := http.NewRequestWithContext(ctx, method, p.opts.BaseURL+endpoint, body)
	if err != nil {
		return err
	}
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range p.opts.Headers {
		request.Header.Set(name, value)
	}
	response, err := p.opts.Client.Do(request)
	if err != nil {
		return fmt.Errorf("remote sandbox: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxResponseBytes {
		return errors.New("remote sandbox: response exceeds limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusNotFound {
			return sandbox.ErrNotFound
		}
		return fmt.Errorf("remote sandbox: HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(raw)))
	}
	if responseBody != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, responseBody); err != nil {
			return fmt.Errorf("remote sandbox: decoding response: %w", err)
		}
	}
	return nil
}

type handle struct {
	provider *Provider
	id       string
	root     string
	fs       *remoteFS
}

func (h *handle) ID() string     { return h.id }
func (h *handle) Root() string   { return h.root }
func (h *handle) FS() sandbox.FS { return h.fs }

func (h *handle) Exec(ctx context.Context, command sandbox.Command) (*sandbox.ExecResult, error) {
	if strings.TrimSpace(command.Line) == "" {
		return nil, errors.New("remote sandbox: command is required")
	}
	if command.Timeout <= 0 {
		command.Timeout = h.provider.opts.ExecTimeout
	}
	var response struct {
		Stdout    string `json:"stdout"`
		Stderr    string `json:"stderr"`
		ExitCode  int    `json:"exit_code"`
		TimedOut  bool   `json:"timed_out"`
		Truncated bool   `json:"truncated"`
	}
	err := h.provider.call(ctx, http.MethodPost, h.endpoint("/exec"), map[string]any{
		"line": command.Line, "work_dir": command.WorkDir, "timeout_ms": command.Timeout.Milliseconds(), "env": command.Env,
	}, &response)
	return &sandbox.ExecResult{Stdout: response.Stdout, Stderr: response.Stderr, ExitCode: response.ExitCode, TimedOut: response.TimedOut, Truncated: response.Truncated}, err
}

func (h *handle) endpoint(suffix string) string {
	return "/v1/sandboxes/" + url.PathEscape(h.id) + suffix
}

type remoteFS struct{ handle *handle }

func (f *remoteFS) Resolve(virtual string) (string, error) {
	root := path.Clean(f.handle.root)
	clean := path.Clean(virtual)
	if !strings.HasPrefix(clean, "/") {
		clean = path.Join(root, clean)
	}
	if clean != root && !strings.HasPrefix(clean, root+"/") {
		return "", sandbox.ErrEscape
	}
	return clean, nil
}

func (f *remoteFS) ReadFile(ctx context.Context, filename string) ([]byte, error) {
	resolved, err := f.Resolve(filename)
	if err != nil {
		return nil, err
	}
	var response struct {
		Data string `json:"data_base64"`
	}
	if err := f.handle.provider.call(ctx, http.MethodPost, f.handle.endpoint("/fs/read"), map[string]any{"path": resolved}, &response); err != nil {
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
	resolved, err := f.Resolve(filename)
	if err != nil {
		return err
	}
	return f.handle.provider.call(ctx, http.MethodPost, f.handle.endpoint("/fs/write"), map[string]any{"path": resolved, "data_base64": base64.StdEncoding.EncodeToString(data)}, nil)
}

func (f *remoteFS) List(ctx context.Context, filename string) ([]sandbox.Entry, error) {
	resolved, err := f.Resolve(filename)
	if err != nil {
		return nil, err
	}
	var response struct {
		Entries []remoteEntry `json:"entries"`
	}
	err = f.handle.provider.call(ctx, http.MethodPost, f.handle.endpoint("/fs/list"), map[string]any{"path": resolved}, &response)
	entries := make([]sandbox.Entry, 0, len(response.Entries))
	for _, entry := range response.Entries {
		entries = append(entries, entry.sandboxEntry())
	}
	return entries, err
}

func (f *remoteFS) Stat(ctx context.Context, filename string) (sandbox.Entry, error) {
	resolved, err := f.Resolve(filename)
	if err != nil {
		return sandbox.Entry{}, err
	}
	var response remoteEntry
	err = f.handle.provider.call(ctx, http.MethodPost, f.handle.endpoint("/fs/stat"), map[string]any{"path": resolved}, &response)
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
