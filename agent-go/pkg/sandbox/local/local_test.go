package local_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox/local"
)

func newHandle(t *testing.T) sandbox.Handle {
	t.Helper()

	p := local.NewProvider(local.Options{BaseDir: t.TempDir()})
	h, err := p.Acquire(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	return h
}

// --- 逃逸检查（安全边界，最重要的一组）---

func TestResolve_RejectsEscapes(t *testing.T) {
	t.Parallel()

	h := newHandle(t)
	fsys := h.FS()

	escapes := []string{
		"../outside",
		"../../etc/passwd",
		"a/b/../../../outside",
		"/etc/passwd",
		"/workspace/../../etc/passwd",
		"/var/log/system.log",
	}

	for _, p := range escapes {
		t.Run(p, func(t *testing.T) {
			if _, err := fsys.Resolve(p); !errors.Is(err, sandbox.ErrEscape) {
				t.Fatalf("Resolve(%q) error = %v, want ErrEscape", p, err)
			}
		})
	}
}

func TestResolve_AcceptsInsidePaths(t *testing.T) {
	t.Parallel()

	h := newHandle(t)
	fsys := h.FS()

	inside := []string{
		"file.txt",
		"a/b/c.txt",
		"./file.txt",
		"a/../b.txt",
		"/workspace/file.txt",
		"/workspace",
		"",
	}

	for _, p := range inside {
		t.Run("path="+p, func(t *testing.T) {
			if _, err := fsys.Resolve(p); err != nil {
				t.Fatalf("Resolve(%q) error = %v, want nil", p, err)
			}
		})
	}
}

// 只做词法检查的实现会被这一条穿透：沙箱内建一个指向 /etc 的符号链接，
// 然后经它读取外部文件。
func TestResolve_RejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	p := local.NewProvider(local.Options{BaseDir: base})
	h, err := p.Acquire(context.Background(), "t1")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	hostRoot, err := h.FS().Resolve("")
	if err != nil {
		t.Fatalf("Resolve(root) error = %v", err)
	}

	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("classified"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(hostRoot, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := h.FS().Resolve("escape/secret.txt"); !errors.Is(err, sandbox.ErrEscape) {
		t.Fatalf("Resolve() error = %v, want ErrEscape via the symlink", err)
	}
	if _, err := h.FS().ReadFile(context.Background(), "escape/secret.txt"); !errors.Is(err, sandbox.ErrEscape) {
		t.Fatalf("ReadFile() error = %v, want ErrEscape", err)
	}
}

// 沙箱内部的符号链接是合法的，不能一律拒绝。
func TestResolve_AllowsInternalSymlink(t *testing.T) {
	t.Parallel()

	h := newHandle(t)
	ctx := context.Background()

	if err := h.FS().WriteFile(ctx, "real/data.txt", []byte("hello")); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	hostRoot, _ := h.FS().Resolve("")
	if err := os.Symlink(filepath.Join(hostRoot, "real"), filepath.Join(hostRoot, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := h.FS().ReadFile(ctx, "link/data.txt")
	if err != nil {
		t.Fatalf("ReadFile() through an internal symlink error = %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("ReadFile() = %q", got)
	}
}

// 兄弟目录名以沙箱根为前缀时不得被当成沙箱内部（字符串前缀比较的经典漏洞）。
func TestResolve_SiblingWithSharedPrefixIsOutside(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	p := local.NewProvider(local.Options{BaseDir: base})
	h, err := p.Acquire(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}

	hostRoot, _ := h.FS().Resolve("")
	evil := hostRoot + "-evil"
	if err := os.MkdirAll(evil, 0o750); err != nil {
		t.Fatal(err)
	}

	// 通过 .. 绕到兄弟目录
	rel := "../" + filepath.Base(evil) + "/x.txt"
	if _, err := h.FS().Resolve(rel); !errors.Is(err, sandbox.ErrEscape) {
		t.Fatalf("Resolve(%q) error = %v, want ErrEscape", rel, err)
	}
}

// --- 文件操作 ---

func TestFS_WriteReadList(t *testing.T) {
	t.Parallel()

	h := newHandle(t)
	ctx := context.Background()
	fsys := h.FS()

	if err := fsys.WriteFile(ctx, "dir/a.txt", []byte("content-a")); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := fsys.WriteFile(ctx, "dir/b.txt", []byte("content-b")); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := fsys.ReadFile(ctx, "dir/a.txt")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != "content-a" {
		t.Fatalf("ReadFile() = %q", got)
	}

	entries, err := fsys.List(ctx, "dir")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List() = %d entries, want 2", len(entries))
	}
}

func TestFS_ReadMissingFileIsNotFound(t *testing.T) {
	t.Parallel()

	h := newHandle(t)
	_, err := h.FS().ReadFile(context.Background(), "nope.txt")
	if !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("ReadFile() error = %v, want ErrNotFound", err)
	}
}

func TestFS_ReadDirectoryIsAnError(t *testing.T) {
	t.Parallel()

	h := newHandle(t)
	ctx := context.Background()
	if err := h.FS().WriteFile(ctx, "d/x.txt", []byte("x")); err != nil {
		t.Fatal(err)
	}

	if _, err := h.FS().ReadFile(ctx, "d"); err == nil {
		t.Fatal("ReadFile() on a directory succeeded, want an error")
	}
}

func TestFS_StatReportsKind(t *testing.T) {
	t.Parallel()

	h := newHandle(t)
	ctx := context.Background()
	if err := h.FS().WriteFile(ctx, "d/x.txt", []byte("xyz")); err != nil {
		t.Fatal(err)
	}

	file, err := h.FS().Stat(ctx, "d/x.txt")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if file.IsDir || file.Size != 3 {
		t.Fatalf("Stat(file) = %+v", file)
	}

	dir, err := h.FS().Stat(ctx, "d")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if !dir.IsDir {
		t.Fatalf("Stat(dir) = %+v", dir)
	}
}

// --- Exec ---

func TestExec_CapturesStdoutAndExitCode(t *testing.T) {
	t.Parallel()

	h := newHandle(t)
	res, err := h.Exec(context.Background(), sandbox.Command{Line: "echo hello"})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "hello" {
		t.Fatalf("Stdout = %q", res.Stdout)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d", res.ExitCode)
	}
}

// 非零退出是命令的正常结果，不是框架错误：交给模型判断怎么办。
func TestExec_NonZeroExitIsNotAnError(t *testing.T) {
	t.Parallel()

	h := newHandle(t)
	res, err := h.Exec(context.Background(), sandbox.Command{Line: "exit 3"})
	if err != nil {
		t.Fatalf("Exec() error = %v; a non-zero exit must not be a framework error", err)
	}
	if res.ExitCode != 3 {
		t.Fatalf("ExitCode = %d, want 3", res.ExitCode)
	}
}

func TestExec_RunsInsideTheSandboxRoot(t *testing.T) {
	t.Parallel()

	h := newHandle(t)
	ctx := context.Background()
	if err := h.FS().WriteFile(ctx, "marker.txt", []byte("x")); err != nil {
		t.Fatal(err)
	}

	res, err := h.Exec(ctx, sandbox.Command{Line: "ls"})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if !strings.Contains(res.Stdout, "marker.txt") {
		t.Fatalf("Exec() did not run in the sandbox root; stdout = %q", res.Stdout)
	}
}

func TestExec_TimesOut(t *testing.T) {
	t.Parallel()

	h := newHandle(t)
	start := time.Now()
	res, err := h.Exec(context.Background(), sandbox.Command{
		Line:    "sleep 5",
		Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if !res.TimedOut {
		t.Fatal("TimedOut = false, want true")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Exec() took %v; the timeout did not kill the command", elapsed)
	}
}

// 后台子进程会继承 stdout 管道的写端，而 Run 要等管道关闭才返回。
// 不设 WaitDelay 的实现会在这里挂到子进程自己结束，超时形同不存在。
func TestExec_TimesOutEvenWhenChildHoldsThePipe(t *testing.T) {
	t.Parallel()

	h := newHandle(t)
	start := time.Now()
	res, err := h.Exec(context.Background(), sandbox.Command{
		Line:    "sleep 10 & echo started; wait",
		Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if !res.TimedOut {
		t.Fatal("TimedOut = false, want true")
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("Exec() took %v; a background child kept the pipe open and defeated the timeout", elapsed)
	}
}

// 输出上限是必需的：一条 find / 能产出几百 MB，不截断会吃满内存。
func TestExec_TruncatesLargeOutput(t *testing.T) {
	t.Parallel()

	p := local.NewProvider(local.Options{BaseDir: t.TempDir(), MaxOutputBytes: 64})
	h, err := p.Acquire(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}

	res, err := h.Exec(context.Background(), sandbox.Command{Line: "printf 'x%.0s' $(seq 1 5000)"})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if !res.Truncated {
		t.Fatal("Truncated = false, want true")
	}
	if len(res.Stdout) > 64 {
		t.Fatalf("Stdout = %d bytes, want <= 64", len(res.Stdout))
	}
}

func TestExec_RejectsWorkDirEscape(t *testing.T) {
	t.Parallel()

	h := newHandle(t)
	_, err := h.Exec(context.Background(), sandbox.Command{Line: "pwd", WorkDir: "../.."})
	if !errors.Is(err, sandbox.ErrEscape) {
		t.Fatalf("Exec() error = %v, want ErrEscape", err)
	}
}

func TestExec_EmptyCommandIsAnError(t *testing.T) {
	t.Parallel()

	h := newHandle(t)
	if _, err := h.Exec(context.Background(), sandbox.Command{}); err == nil {
		t.Fatal("Exec() with an empty command line succeeded")
	}
}

// --- Provider ---

func TestProvider_SameKeyReusesHandle(t *testing.T) {
	t.Parallel()

	p := local.NewProvider(local.Options{BaseDir: t.TempDir()})
	ctx := context.Background()

	a, err := p.Acquire(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Acquire(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}

	if a.ID() != b.ID() {
		t.Fatalf("same key produced different handles: %q vs %q", a.ID(), b.ID())
	}

	rootA, _ := a.FS().Resolve("")
	rootB, _ := b.FS().Resolve("")
	if rootA != rootB {
		t.Fatalf("same key produced different roots: %q vs %q", rootA, rootB)
	}
}

func TestProvider_DifferentKeysAreIsolated(t *testing.T) {
	t.Parallel()

	p := local.NewProvider(local.Options{BaseDir: t.TempDir()})
	ctx := context.Background()

	a, _ := p.Acquire(ctx, "thread-a")
	b, _ := p.Acquire(ctx, "thread-b")

	if err := a.FS().WriteFile(ctx, "secret.txt", []byte("a's data")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.FS().ReadFile(ctx, "secret.txt"); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("thread-b could see thread-a's file: err = %v", err)
	}
}

func TestProvider_EmptyKeyIsRejected(t *testing.T) {
	t.Parallel()

	p := local.NewProvider(local.Options{BaseDir: t.TempDir()})
	if _, err := p.Acquire(context.Background(), ""); err == nil {
		t.Fatal("Acquire(\"\") succeeded, want an error")
	}
}

// key 里的路径分隔符不得造出目录层级，否则 key="../x" 就能越出 BaseDir。
func TestProvider_KeyCannotTraverse(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	p := local.NewProvider(local.Options{BaseDir: base})

	h, err := p.Acquire(context.Background(), "../escaped")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	root, _ := h.FS().Resolve("")

	resolvedBase, _ := filepath.EvalSymlinks(base)
	if !strings.HasPrefix(root, resolvedBase) {
		t.Fatalf("sandbox root %q escaped the base dir %q", root, resolvedBase)
	}
}

func TestProvider_ConcurrentAcquire(t *testing.T) {
	t.Parallel()

	p := local.NewProvider(local.Options{BaseDir: t.TempDir()})
	ctx := context.Background()

	var wg sync.WaitGroup
	roots := make([]string, 20)
	for i := range roots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := p.Acquire(ctx, "shared")
			if err != nil {
				t.Errorf("Acquire() error = %v", err)
				return
			}
			roots[i], _ = h.FS().Resolve("")
		}()
	}
	wg.Wait()

	for i := 1; i < len(roots); i++ {
		if roots[i] != roots[0] {
			t.Fatalf("concurrent Acquire produced different roots: %q vs %q", roots[i], roots[0])
		}
	}
}

// --- Lease（惰性创建）---

func TestLease_DoesNotAcquireUntilUsed(t *testing.T) {
	t.Parallel()

	p := &countingProvider{inner: local.NewProvider(local.Options{BaseDir: t.TempDir()})}
	lease := sandbox.NewLease(p, "t1")

	if p.acquires != 0 {
		t.Fatalf("Acquire called %d times before use, want 0", p.acquires)
	}
	if lease.Acquired() {
		t.Fatal("Acquired() = true before first use")
	}

	if _, err := lease.Handle(context.Background()); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if p.acquires != 1 {
		t.Fatalf("Acquire called %d times, want 1", p.acquires)
	}
	if !lease.Acquired() {
		t.Fatal("Acquired() = false after first use")
	}
}

func TestLease_HandleIsCached(t *testing.T) {
	t.Parallel()

	p := &countingProvider{inner: local.NewProvider(local.Options{BaseDir: t.TempDir()})}
	lease := sandbox.NewLease(p, "t1")
	ctx := context.Background()

	for range 5 {
		if _, err := lease.Handle(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if p.acquires != 1 {
		t.Fatalf("Acquire called %d times, want 1", p.acquires)
	}
}

// 沙箱起不来时不该每次工具调用都重试一遍 —— 那只会把回合拖到超时。
func TestLease_RemembersAcquireFailure(t *testing.T) {
	t.Parallel()

	p := &failingProvider{}
	lease := sandbox.NewLease(p, "t1")
	ctx := context.Background()

	for range 3 {
		if _, err := lease.Handle(ctx); err == nil {
			t.Fatal("Handle() error = nil, want the acquire failure")
		}
	}
	if p.calls != 1 {
		t.Fatalf("Acquire called %d times, want 1 (the failure must be remembered)", p.calls)
	}
}

func TestLease_ReleaseWithoutAcquireIsNoop(t *testing.T) {
	t.Parallel()

	p := &countingProvider{inner: local.NewProvider(local.Options{BaseDir: t.TempDir()})}
	lease := sandbox.NewLease(p, "t1")

	if err := lease.Release(context.Background()); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if p.releases != 0 {
		t.Fatalf("Release called %d times on an unused lease, want 0", p.releases)
	}
}

func TestLease_NilIsSafe(t *testing.T) {
	t.Parallel()

	var lease *sandbox.Lease
	if _, err := lease.Handle(context.Background()); err == nil {
		t.Fatal("Handle() on a nil lease succeeded")
	}
	if lease.Acquired() {
		t.Fatal("Acquired() = true on a nil lease")
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatalf("Release() on a nil lease error = %v", err)
	}
}

func TestLease_ConcurrentHandleAcquiresOnce(t *testing.T) {
	t.Parallel()

	p := &countingProvider{inner: local.NewProvider(local.Options{BaseDir: t.TempDir()})}
	lease := sandbox.NewLease(p, "t1")
	ctx := context.Background()

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := lease.Handle(ctx); err != nil {
				t.Errorf("Handle() error = %v", err)
			}
		}()
	}
	wg.Wait()

	if p.acquires != 1 {
		t.Fatalf("Acquire called %d times under concurrency, want 1", p.acquires)
	}
}

// --- context 传递 ---

func TestContext_RoundTrip(t *testing.T) {
	t.Parallel()

	lease := sandbox.NewLease(local.NewProvider(local.Options{BaseDir: t.TempDir()}), "t1")
	ctx := sandbox.NewContext(context.Background(), lease)

	got, ok := sandbox.FromContext(ctx)
	if !ok || got != lease {
		t.Fatalf("FromContext() = %v, %v", got, ok)
	}

	h, err := sandbox.HandleFromContext(ctx)
	if err != nil {
		t.Fatalf("HandleFromContext() error = %v", err)
	}
	if h == nil {
		t.Fatal("HandleFromContext() returned a nil handle")
	}
}

func TestContext_MissingLease(t *testing.T) {
	t.Parallel()

	if _, ok := sandbox.FromContext(context.Background()); ok {
		t.Fatal("FromContext() found a lease in a bare context")
	}
	if _, err := sandbox.HandleFromContext(context.Background()); err == nil {
		t.Fatal("HandleFromContext() succeeded without a lease")
	}
}

// --- helpers ---

type countingProvider struct {
	inner    sandbox.Provider
	acquires int
	releases int
}

func (c *countingProvider) Acquire(ctx context.Context, key string) (sandbox.Handle, error) {
	c.acquires++
	return c.inner.Acquire(ctx, key)
}

func (c *countingProvider) Release(ctx context.Context, key string) error {
	c.releases++
	return c.inner.Release(ctx, key)
}

type failingProvider struct{ calls int }

func (f *failingProvider) Acquire(context.Context, string) (sandbox.Handle, error) {
	f.calls++
	return nil, errors.New("docker daemon unreachable")
}

func (f *failingProvider) Release(context.Context, string) error { return nil }
