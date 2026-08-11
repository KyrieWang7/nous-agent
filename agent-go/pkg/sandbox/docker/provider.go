//go:build docker

// Package docker provides process isolation through Docker containers.
package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

type Options struct {
	Image          string
	BaseDir        string
	VirtualRoot    string
	Shell          string
	ExecTimeout    time.Duration
	MaxOutputBytes int
}
type Provider struct {
	opts    Options
	mu      sync.Mutex
	handles map[string]*handle
}

func NewProvider(opts Options) *Provider {
	if opts.Image == "" {
		opts.Image = "alpine:3.20"
	}
	if opts.BaseDir == "" {
		opts.BaseDir = filepath.Join(os.TempDir(), "nous-agent-docker")
	}
	opts.VirtualRoot = strings.TrimSpace(opts.VirtualRoot)
	if opts.VirtualRoot == "" {
		opts.VirtualRoot = "/workspace"
	} else {
		opts.VirtualRoot = path.Clean(opts.VirtualRoot)
	}
	if opts.Shell == "" {
		opts.Shell = "/bin/sh"
	}
	if opts.ExecTimeout <= 0 {
		opts.ExecTimeout = 30 * time.Second
	}
	if opts.MaxOutputBytes <= 0 {
		opts.MaxOutputBytes = 256 << 10
	}
	return &Provider{opts: opts, handles: map[string]*handle{}}
}
func (p *Provider) Acquire(ctx context.Context, key string) (sandbox.Handle, error) {
	if key == "" {
		return nil, errors.New("docker sandbox: key is empty")
	}
	if !path.IsAbs(p.opts.VirtualRoot) {
		return nil, fmt.Errorf("docker sandbox: virtual root must be absolute: %q", p.opts.VirtualRoot)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if h := p.handles[key]; h != nil {
		return h, nil
	}
	safe := sanitize(key)
	root := filepath.Join(p.opts.BaseDir, safe)
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("docker sandbox: resolve root: %w", err)
	}
	name := "nous-agent-" + safe
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
	cmd := exec.CommandContext(ctx, "docker", "run", "-d", "--name", name, "--network", "none", "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m", "-v", root+":"+p.opts.VirtualRoot+":rw", p.opts.Image, "sleep", "infinity")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker sandbox: start: %w: %s", err, out)
	}
	h := &handle{name: name, root: root, opts: p.opts}
	h.fs = &hostFS{root: root, virtualRoot: p.opts.VirtualRoot}
	p.handles[key] = h
	return h, nil
}
func (p *Provider) Release(ctx context.Context, key string) error {
	p.mu.Lock()
	h := p.handles[key]
	delete(p.handles, key)
	p.mu.Unlock()
	if h == nil {
		return nil
	}
	out, err := exec.CommandContext(ctx, "docker", "rm", "-f", h.name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker sandbox: remove: %w: %s", err, out)
	}
	return nil
}

type handle struct {
	name, root string
	opts       Options
	fs         *hostFS
}

func (h *handle) ID() string     { return h.name }
func (h *handle) Root() string   { return h.opts.VirtualRoot }
func (h *handle) FS() sandbox.FS { return h.fs }
func (h *handle) Exec(ctx context.Context, c sandbox.Command) (*sandbox.ExecResult, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = h.opts.ExecTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	work := h.opts.VirtualRoot
	if c.WorkDir != "" {
		clean, err := cleanVirtual(c.WorkDir, h.opts.VirtualRoot)
		if err != nil {
			return nil, err
		}
		work = path.Join(h.opts.VirtualRoot, clean)
	}
	args := []string{"exec", "-w", work}
	for _, env := range c.Env {
		args = append(args, "-e", env)
	}
	args = append(args, h.name, h.opts.Shell, "-c", c.Line)
	cmd := exec.CommandContext(runCtx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := &sandbox.ExecResult{}
	res.Stdout, res.Truncated = capOutput(stdout.Bytes(), h.opts.MaxOutputBytes)
	var trunc bool
	res.Stderr, trunc = capOutput(stderr.Bytes(), h.opts.MaxOutputBytes)
	res.Truncated = res.Truncated || trunc
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
		return res, nil
	}
	if err == nil {
		return res, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		res.ExitCode = exit.ExitCode()
		return res, nil
	}
	return nil, err
}

type hostFS struct {
	root        string
	virtualRoot string
}

func (f *hostFS) Resolve(path string) (string, error) {
	clean, err := cleanVirtual(path, f.virtualRoot)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(f.root)
	if err != nil {
		return "", fmt.Errorf("docker sandbox: resolve root: %w", err)
	}
	resolved := filepath.Clean(filepath.Join(root, filepath.FromSlash(clean)))
	if !underRoot(resolved, root) {
		return "", sandbox.ErrEscape
	}
	resolved, err = resolveSymlinks(resolved)
	if err != nil {
		return "", err
	}
	if !underRoot(resolved, root) {
		return "", sandbox.ErrEscape
	}
	return resolved, nil
}

func resolveSymlinks(value string) (string, error) {
	existing := value
	var missing []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("docker sandbox: cannot resolve %q", value)
		}
		missing = append([]string{filepath.Base(existing)}, missing...)
		existing = parent
	}

	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	if len(missing) == 0 {
		return resolved, nil
	}
	return filepath.Join(append([]string{resolved}, missing...)...), nil
}

func underRoot(value, root string) bool {
	return value == root || strings.HasPrefix(value, root+string(filepath.Separator))
}
func (f *hostFS) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := f.Resolve(path)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("docker sandbox: %q is a directory", path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("docker sandbox: %q is not a regular file", path)
	}
	if info.Size() > sandbox.MaxReadFileBytes {
		return nil, fmt.Errorf("docker sandbox: %q is %d bytes, over the %d byte limit", path, info.Size(), sandbox.MaxReadFileBytes)
	}
	data, err := sandbox.ReadAllLimited(ctx, file, sandbox.MaxReadFileBytes)
	if errors.Is(err, sandbox.ErrReadLimit) {
		return nil, fmt.Errorf("docker sandbox: %q grew over the %d byte limit while being read: %w", path, sandbox.MaxReadFileBytes, err)
	}
	return data, err
}
func (f *hostFS) WriteFile(ctx context.Context, path string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := f.Resolve(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o640)
}
func (f *hostFS) List(ctx context.Context, path string) ([]sandbox.Entry, error) {
	entries, _, err := f.list(ctx, path, 0)
	return entries, err
}
func (f *hostFS) ListLimit(ctx context.Context, path string, limit int) ([]sandbox.Entry, bool, error) {
	if limit <= 0 {
		return nil, false, errors.New("docker sandbox: list limit must be positive")
	}
	return f.list(ctx, path, limit)
}
func (f *hostFS) list(ctx context.Context, path string, limit int) ([]sandbox.Entry, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	p, err := f.Resolve(path)
	if err != nil {
		return nil, false, err
	}
	dir, err := os.Open(p)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = dir.Close() }()
	readCount := -1
	if limit > 0 {
		readCount = limit + 1
	}
	entries, err := dir.ReadDir(readCount)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	truncated := limit > 0 && len(entries) > limit
	if truncated {
		entries = entries[:limit]
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	out := make([]sandbox.Entry, 0, len(entries))
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		info, err := e.Info()
		if err != nil {
			return nil, false, err
		}
		out = append(out, sandbox.Entry{Name: e.Name(), IsDir: e.IsDir(), Size: info.Size(), Mode: info.Mode().String()})
	}
	return out, truncated, nil
}
func (f *hostFS) Stat(ctx context.Context, path string) (sandbox.Entry, error) {
	if err := ctx.Err(); err != nil {
		return sandbox.Entry{}, err
	}
	p, err := f.Resolve(path)
	if err != nil {
		return sandbox.Entry{}, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return sandbox.Entry{}, err
	}
	return sandbox.Entry{Name: info.Name(), IsDir: info.IsDir(), Size: info.Size(), Mode: info.Mode().String()}, nil
}
func cleanVirtual(value, virtualRoot string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	cleaned := path.Clean(value)
	if path.IsAbs(cleaned) {
		root := path.Clean(virtualRoot)
		switch {
		case cleaned == root:
			return "", nil
		case root == "/":
			cleaned = strings.TrimPrefix(cleaned, "/")
		case strings.HasPrefix(cleaned, root+"/"):
			cleaned = strings.TrimPrefix(cleaned, root+"/")
		default:
			return "", sandbox.ErrEscape
		}
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", sandbox.ErrEscape
	}
	if cleaned == "." {
		return "", nil
	}
	return cleaned, nil
}
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return b.String()
}
func capOutput(raw []byte, limit int) (string, bool) {
	if len(raw) <= limit {
		return string(raw), false
	}
	return string(raw[:limit]), true
}
