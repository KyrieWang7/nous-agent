// Package sandbox 提供工具执行的隔离边界。
//
// 本包只做隔离，不做审批也不做权限判定 —— 那是 pkg/permission 与中间件的职责
// （设计文档 §2 依赖纪律）。layercheck 断言 pkg/sandbox 不得 import pkg/permission。
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrEscape 表示路径越出了沙箱根目录。
var ErrEscape = errors.New("sandbox: path escapes the sandbox root")

// ErrNotFound 表示路径不存在。
var ErrNotFound = errors.New("sandbox: path not found")

// Entry 是一个目录项。
type Entry struct {
	Name  string
	IsDir bool
	Size  int64
	Mode  string
}

// Command 是一次命令执行请求。
type Command struct {
	// Line 是要执行的 shell 命令行。
	Line string

	// WorkDir 是相对沙箱根的工作目录，空表示根目录。
	WorkDir string

	// Timeout 是执行上限。<= 0 时用 Provider 的默认值。
	Timeout time.Duration

	// Env 是额外的环境变量，形如 KEY=VALUE。
	Env []string
}

// ExecResult 是一次命令执行的结果。
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int

	// TimedOut 表示命令因超时被杀。此时 ExitCode 无意义。
	TimedOut bool

	// Truncated 表示输出因超过上限被截断。
	Truncated bool
}

// FS 是沙箱内的文件操作。
//
// 路径参数一律是**虚拟路径**（模型看到的路径）。实现负责映射到宿主路径
// 并拦截越界访问。
type FS interface {
	// Resolve 把虚拟路径映射为宿主路径，越界返回 ErrEscape。
	Resolve(virtual string) (string, error)

	ReadFile(ctx context.Context, path string) ([]byte, error)
	WriteFile(ctx context.Context, path string, data []byte) error
	List(ctx context.Context, path string) ([]Entry, error)
	Stat(ctx context.Context, path string) (Entry, error)
}

// Handle 是一个已就绪的沙箱实例。
type Handle interface {
	// ID 是该实例的标识，用于日志与遥测。
	ID() string

	// Root 是沙箱根的虚拟路径。
	Root() string

	FS() FS
	Exec(ctx context.Context, cmd Command) (*ExecResult, error)
}

// Provider 按 key 提供沙箱实例。key 通常是 thread ID：
// 同一会话复用同一个沙箱，跨会话互相隔离。
type Provider interface {
	Acquire(ctx context.Context, key string) (Handle, error)
	Release(ctx context.Context, key string) error
}

// Lease 是对沙箱实例的惰性持有。
//
// lazy_init 的落点：SandboxMiddleware 在 BeforeAgent 建立 Lease，但真正的
// Acquire 延后到第一次工具调用。绝大多数回合根本不碰文件系统，
// 提前创建容器是纯浪费。
type Lease struct {
	provider Provider
	key      string

	once   sync.Once
	handle Handle
	err    error
}

// NewLease 返回绑定到 provider 与 key 的惰性租约。
func NewLease(p Provider, key string) *Lease {
	return &Lease{provider: p, key: key}
}

// Handle 首次调用时 Acquire，后续复用。并发安全。
//
// Acquire 失败会被记住：一个沙箱起不来时，后续每次工具调用都重试一遍
// 只会把回合拖到超时。
func (l *Lease) Handle(ctx context.Context) (Handle, error) {
	if l == nil {
		return nil, errors.New("sandbox: no lease available")
	}
	l.once.Do(func() {
		if l.provider == nil {
			l.err = errors.New("sandbox: lease has no provider")
			return
		}
		l.handle, l.err = l.provider.Acquire(ctx, l.key)
		if l.err != nil {
			l.err = fmt.Errorf("sandbox: acquiring %q: %w", l.key, l.err)
		}
	})
	return l.handle, l.err
}

// Acquired 报告底层沙箱是否已经真的创建过。
func (l *Lease) Acquired() bool {
	if l == nil {
		return false
	}
	return l.handle != nil
}

// Release 归还沙箱。未曾 Acquire 时是空操作。
func (l *Lease) Release(ctx context.Context) error {
	if l == nil || l.handle == nil {
		return nil
	}
	return l.provider.Release(ctx, l.key)
}

// contextKey 是 Lease 在 context 中的键。
type contextKey struct{}

// NewContext 把租约放进 context，供工具 Handler 取用。
//
// 走 context 而不是给每个工具注入依赖：工具是注册表里的全局单例，
// 而沙箱是按会话的。绑死在闭包里就得给每个会话建一份注册表。
func NewContext(ctx context.Context, l *Lease) context.Context {
	return context.WithValue(ctx, contextKey{}, l)
}

// FromContext 取出租约。没有时第二个返回值为 false。
func FromContext(ctx context.Context) (*Lease, bool) {
	l, ok := ctx.Value(contextKey{}).(*Lease)
	return l, ok && l != nil
}

// HandleFromContext 是取租约并解引用的便捷方法，供工具 Handler 使用。
func HandleFromContext(ctx context.Context) (Handle, error) {
	l, ok := FromContext(ctx)
	if !ok {
		return nil, errors.New("sandbox: no sandbox available for this call")
	}
	return l.Handle(ctx)
}
