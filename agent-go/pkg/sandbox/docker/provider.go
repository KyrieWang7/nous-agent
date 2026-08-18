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
	Image   string
	BaseDir string
	// HostBaseDir is the same bind mount as BaseDir, expressed in the Docker
	// daemon's filesystem namespace. It is only needed when sandboxd itself
	// runs in a container against the host daemon.
	HostBaseDir     string
	VirtualRoot     string
	Shell           string
	ExecTimeout     time.Duration
	MaxOutputBytes  int
	Memory          string
	CPUs            string
	PIDsLimit       int
	User            string
	ContainerPrefix string
}
type Provider struct {
	opts    Options
	mu      sync.Mutex
	handles map[string]*handle
}

func (p *Provider) Enforcement() map[string]bool {
	return map[string]bool{"isolated": true, "network_none": true, "non_root": true, "read_only_root": true, "resource_limits": true}
}

func (p *Provider) Verify(ctx context.Context) error {
	if _, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").Output(); err != nil {
		return fmt.Errorf("docker sandbox: daemon verification failed: %w", err)
	}
	return nil
}

// Reconcile removes only containers owned by sandboxd. Since controller leases
// are intentionally in-memory, all such containers are orphaned after restart.
func (p *Provider) Reconcile(ctx context.Context) error {
	if err := p.prepareBaseDir(); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, "docker", "ps", "-aq",
		"--filter", "label=io.nous-agent.sandbox=true",
		"--filter", "label=io.nous-agent.managed-by=sandboxd").CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker sandbox: list managed containers: %w: %s", err, out)
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return nil
	}
	out, err = exec.CommandContext(ctx, "docker", append([]string{"rm", "-f"}, ids...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker sandbox: remove orphaned containers: %w: %s", err, out)
	}
	return nil
}

func (p *Provider) prepareBaseDir() error {
	uid, gid, err := p.userIDs()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.opts.BaseDir, 0o2770); err != nil {
		return fmt.Errorf("docker sandbox: create shared workspace root: %w", err)
	}
	if err := os.Chown(p.opts.BaseDir, uid, gid); err != nil {
		return fmt.Errorf("docker sandbox: chown shared workspace root: %w", err)
	}
	if err := os.Chmod(p.opts.BaseDir, 0o2770); err != nil {
		return fmt.Errorf("docker sandbox: chmod shared workspace root: %w", err)
	}
	return nil
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
	if opts.Memory == "" {
		opts.Memory = "512m"
	}
	if opts.CPUs == "" {
		opts.CPUs = "1"
	}
	if opts.PIDsLimit <= 0 {
		opts.PIDsLimit = 128
	}
	if opts.User == "" {
		opts.User = "65532:65532"
	}
	if opts.ContainerPrefix == "" {
		opts.ContainerPrefix = "nous-sandbox-"
	}
	return &Provider{opts: opts, handles: map[string]*handle{}}
}
func (p *Provider) Acquire(ctx context.Context, key string) (sandbox.Handle, error) {
	return p.AcquireWorkspace(ctx, key, key)
}

func (p *Provider) AcquireWorkspace(ctx context.Context, key, workspaceKey string) (sandbox.Handle, error) {
	if key == "" || workspaceKey == "" {
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
	workspaceSafe := sanitize(workspaceKey)
	root := filepath.Join(p.opts.BaseDir, workspaceSafe)
	hostRoot := root
	if p.opts.HostBaseDir != "" {
		hostRoot = filepath.Join(filepath.Clean(p.opts.HostBaseDir), workspaceSafe)
	}
	if err := os.MkdirAll(root, 0o2770); err != nil {
		return nil, err
	}
	uid, gid, err := p.userIDs()
	if err != nil {
		return nil, err
	}
	// The controller container runs as root and must hand the bind mount to
	// the non-root workload user. Host-side development processes (notably
	// macOS) cannot chown their temp directories; Docker will still enforce
	// the configured container user in that case.
	if os.Geteuid() == 0 {
		if err := os.Chown(root, uid, gid); err != nil {
			return nil, fmt.Errorf("docker sandbox: chown workspace: %w", err)
		}
	}
	if err := os.Chmod(root, 0o2770); err != nil {
		return nil, fmt.Errorf("docker sandbox: chmod workspace: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("docker sandbox: resolve root: %w", err)
	}
	name := p.opts.ContainerPrefix + safe
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
	cmd := exec.CommandContext(ctx, "docker", "run", "-d", "--name", name,
		"--label", "io.nous-agent.sandbox=true", "--label", "io.nous-agent.managed-by=sandboxd",
		"--network", "none", "--read-only", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges", "--user", p.opts.User,
		"--memory", p.opts.Memory, "--cpus", p.opts.CPUs,
		"--pids-limit", strconv.Itoa(p.opts.PIDsLimit), "--ulimit", "nofile=1024:1024",
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=64m", "--tmpfs", "/run:rw,noexec,nosuid,nodev,size=8m",
		"-v", hostRoot+":"+p.opts.VirtualRoot+":rw", p.opts.Image, "sleep", "infinity")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker sandbox: start: %w: %s", err, out)
	}
	h := &handle{name: name, root: root, opts: p.opts}
	h.fs = &hostFS{root: root, virtualRoot: p.opts.VirtualRoot, uid: uid, gid: gid}
	p.handles[key] = h
	return h, nil
}

func (p *Provider) userIDs() (int, int, error) {
	parts := strings.SplitN(p.opts.User, ":", 2)
	uid, err := strconv.Atoi(parts[0])
	gid := uid
	if len(parts) == 2 {
		gid, err = strconv.Atoi(parts[1])
	}
	if err != nil {
		return 0, 0, fmt.Errorf("docker sandbox: invalid user %q", p.opts.User)
	}
	return uid, gid, nil
}
func (p *Provider) Release(ctx context.Context, key string) error {
	p.mu.Lock()
	h := p.handles[key]
	p.mu.Unlock()
	if h == nil {
		return nil
	}
	out, err := exec.CommandContext(ctx, "docker", "rm", "-f", h.name).CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(out)), "no such container") {
			p.forget(key, h)
			return nil
		}
		return fmt.Errorf("docker sandbox: remove: %w: %s", err, out)
	}
	p.forget(key, h)
	return nil
}

func (p *Provider) forget(key string, h *handle) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.handles[key] == h {
		delete(p.handles, key)
	}
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
	uid         int
	gid         int
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
	if err := f.ensureDir(filepath.Dir(p)); err != nil {
		return err
	}
	if err := os.WriteFile(p, data, 0o660); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(p, f.uid, f.gid); err != nil {
			return err
		}
	}
	if err := os.Chmod(p, 0o660); err != nil {
		return err
	}
	return nil
}

func (f *hostFS) ensureDir(target string) error {
	root, err := filepath.EvalSymlinks(f.root)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return sandbox.ErrEscape
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if err := os.Mkdir(current, 0o2770); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if os.Geteuid() == 0 {
			if err := os.Chown(current, f.uid, f.gid); err != nil {
				return err
			}
		}
		if err := os.Chmod(current, 0o2770); err != nil {
			return err
		}
	}
	return nil
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
