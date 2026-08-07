// Package local 是基于宿主文件系统与 shell 的沙箱实现。
//
// 隔离手段是路径限制：全部文件访问经 pathmap 映射并做逃逸检查。
// 这不是强隔离（命令本身仍以宿主用户身份运行），适用于单用户本地场景。
// 需要真隔离时换 docker 或 remote provider，接口不变。
package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

// Options 配置 local provider。
type Options struct {
	// BaseDir 是所有沙箱根目录的父目录。空时用 os.TempDir()/nous-agent-go。
	BaseDir string

	// VirtualRoot 是模型看到的根路径。空时用 "/workspace"。
	VirtualRoot string

	// Shell 是执行命令用的 shell。空时用 /bin/sh。
	Shell string

	// ExecTimeout 是命令的默认执行上限。<= 0 时用 30 秒。
	ExecTimeout time.Duration

	// MaxOutputBytes 是单次命令 stdout/stderr 各自的上限。<= 0 时用 256 KiB。
	//
	// 上限是必需的：一条 `find /` 能产出几百 MB，不截断会直接吃满内存，
	// 而模型无论如何也读不完那么多。
	MaxOutputBytes int
}

// execWaitDelay 是命令被取消后等待其子进程收口的上限。
const execWaitDelay = time.Second

func (o Options) withDefaults() Options {
	if o.BaseDir == "" {
		o.BaseDir = filepath.Join(os.TempDir(), "nous-agent-go")
	}
	if o.VirtualRoot == "" {
		o.VirtualRoot = "/workspace"
	}
	if o.Shell == "" {
		o.Shell = "/bin/sh"
	}
	if o.ExecTimeout <= 0 {
		o.ExecTimeout = 30 * time.Second
	}
	if o.MaxOutputBytes <= 0 {
		o.MaxOutputBytes = 256 << 10
	}
	return o
}

// Provider 按 key 在 BaseDir 下创建独立目录作为沙箱根。
type Provider struct {
	opts Options

	mu      sync.Mutex
	handles map[string]*handle
}

// NewProvider 返回 local provider。
func NewProvider(opts Options) *Provider {
	return &Provider{
		opts:    opts.withDefaults(),
		handles: make(map[string]*handle),
	}
}

// Acquire 实现 sandbox.Provider。同一 key 复用同一实例。
func (p *Provider) Acquire(_ context.Context, key string) (sandbox.Handle, error) {
	if key == "" {
		return nil, errors.New("local: sandbox key must not be empty")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if h, ok := p.handles[key]; ok {
		return h, nil
	}

	hostRoot := filepath.Join(p.opts.BaseDir, sanitiseKey(key))
	if err := os.MkdirAll(hostRoot, 0o750); err != nil {
		return nil, fmt.Errorf("local: creating sandbox root: %w", err)
	}
	// EvalSymlinks 让根本身也是规范路径：否则 macOS 上 /var → /private/var
	// 会让每一次逃逸检查都误判为越界。
	resolved, err := filepath.EvalSymlinks(hostRoot)
	if err != nil {
		return nil, fmt.Errorf("local: resolving sandbox root: %w", err)
	}

	h := &handle{
		id:       key,
		hostRoot: resolved,
		opts:     p.opts,
	}
	h.fs = &localFS{hostRoot: resolved, virtualRoot: p.opts.VirtualRoot}
	p.handles[key] = h
	return h, nil
}

// Release 实现 sandbox.Provider。
//
// 刻意不删除目录：沙箱内可能有用户产出的文件（报告、下载、生成的代码）。
// 删除是运维决定，不是运行时决定。
func (p *Provider) Release(_ context.Context, key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.handles, key)
	return nil
}

// sanitiseKey 把 key 变成安全的单层目录名。
func sanitiseKey(key string) string {
	out := make([]rune, 0, len(key))
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "default"
	}
	return string(out)
}

type handle struct {
	id       string
	hostRoot string
	opts     Options
	fs       *localFS
}

func (h *handle) ID() string     { return h.id }
func (h *handle) Root() string   { return h.opts.VirtualRoot }
func (h *handle) FS() sandbox.FS { return h.fs }

// Exec 在沙箱根内执行命令。
func (h *handle) Exec(ctx context.Context, cmd sandbox.Command) (*sandbox.ExecResult, error) {
	if cmd.Line == "" {
		return nil, errors.New("local: empty command")
	}

	workDir := h.hostRoot
	if cmd.WorkDir != "" {
		resolved, err := h.fs.Resolve(cmd.WorkDir)
		if err != nil {
			return nil, err
		}
		workDir = resolved
	}

	timeout := cmd.Timeout
	if timeout <= 0 {
		timeout = h.opts.ExecTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := exec.CommandContext(execCtx, h.opts.Shell, "-c", cmd.Line)
	c.Dir = workDir
	c.Env = append(os.Environ(), cmd.Env...)

	// 超时杀掉 shell 后，它 fork 出的后台子进程仍持有 stdout 管道的写端，
	// 而 Run 要等管道关闭才返回 —— `sleep 5 &` 这类命令会让超时形同不存在。
	// WaitDelay 让取消后至多再等这么久就强制收口。
	c.WaitDelay = execWaitDelay

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	runErr := c.Run()

	res := &sandbox.ExecResult{
		TimedOut: errors.Is(execCtx.Err(), context.DeadlineExceeded),
	}
	res.Stdout, res.Truncated = capBytes(stdout.Bytes(), h.opts.MaxOutputBytes)
	out, truncated := capBytes(stderr.Bytes(), h.opts.MaxOutputBytes)
	res.Stderr = out
	res.Truncated = res.Truncated || truncated

	switch {
	case res.TimedOut:
		return res, nil
	case runErr == nil:
		res.ExitCode = 0
		return res, nil
	default:
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			// 非零退出是命令的正常结果，不是框架错误：交给模型判断。
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		// shell 起不来这类才是真错误。
		return nil, fmt.Errorf("local: running command: %w", runErr)
	}
}

func capBytes(b []byte, limit int) (string, bool) {
	if len(b) <= limit {
		return string(b), false
	}
	return string(b[:limit]), true
}
