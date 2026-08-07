//go:build docker

// Package docker provides process isolation through Docker containers.
package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

type Options struct {
	Image          string
	BaseDir        string
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
	name := "nous-agent-" + safe
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
	cmd := exec.CommandContext(ctx, "docker", "run", "-d", "--name", name, "--network", "none", "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m", "-v", root+":/workspace:rw", p.opts.Image, "sleep", "infinity")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker sandbox: start: %w: %s", err, out)
	}
	h := &handle{name: name, root: root, opts: p.opts}
	h.fs = &hostFS{root: root}
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
func (h *handle) Root() string   { return "/workspace" }
func (h *handle) FS() sandbox.FS { return h.fs }
func (h *handle) Exec(ctx context.Context, c sandbox.Command) (*sandbox.ExecResult, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = h.opts.ExecTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	work := "/workspace"
	if c.WorkDir != "" {
		clean, err := cleanVirtual(c.WorkDir)
		if err != nil {
			return nil, err
		}
		work = "/workspace/" + clean
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

type hostFS struct{ root string }

func (f *hostFS) Resolve(path string) (string, error) {
	clean, err := cleanVirtual(path)
	if err != nil {
		return "", err
	}
	resolved := filepath.Join(f.root, clean)
	rel, err := filepath.Rel(f.root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", sandbox.ErrEscape
	}
	return resolved, nil
}
func (f *hostFS) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := f.Resolve(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := f.Resolve(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]sandbox.Entry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, sandbox.Entry{Name: e.Name(), IsDir: e.IsDir(), Size: info.Size(), Mode: info.Mode().String()})
	}
	return out, nil
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
func cleanVirtual(path string) (string, error) {
	path = strings.TrimPrefix(filepath.Clean(path), string(filepath.Separator))
	if path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return "", sandbox.ErrEscape
	}
	if path == "." {
		return "", nil
	}
	return path, nil
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
