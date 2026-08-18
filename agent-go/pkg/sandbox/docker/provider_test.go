//go:build docker

package docker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

func TestCleanVirtualCustomRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		value       string
		virtualRoot string
		want        string
		wantEscape  bool
	}{
		{name: "empty", value: "", virtualRoot: "/mnt/user-data"},
		{name: "relative", value: "workspace/a.txt", virtualRoot: "/mnt/user-data", want: "workspace/a.txt"},
		{name: "relative cleaned", value: "workspace/../outputs/a.txt", virtualRoot: "/mnt/user-data", want: "outputs/a.txt"},
		{name: "absolute root", value: "/mnt/user-data", virtualRoot: "/mnt/user-data"},
		{name: "absolute child", value: "/mnt/user-data/outputs/a.txt", virtualRoot: "/mnt/user-data", want: "outputs/a.txt"},
		{name: "slash root", value: "/etc/passwd", virtualRoot: "/", want: "etc/passwd"},
		{name: "relative parent", value: "../outside", virtualRoot: "/mnt/user-data", wantEscape: true},
		{name: "relative cleaned parent", value: "a/../../outside", virtualRoot: "/mnt/user-data", wantEscape: true},
		{name: "outside absolute", value: "/etc/passwd", virtualRoot: "/mnt/user-data", wantEscape: true},
		{name: "absolute traversal", value: "/mnt/user-data/../../etc/passwd", virtualRoot: "/mnt/user-data", wantEscape: true},
		{name: "shared prefix", value: "/mnt/user-data-other/a.txt", virtualRoot: "/mnt/user-data", wantEscape: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := cleanVirtual(tt.value, tt.virtualRoot)
			if tt.wantEscape {
				if !errors.Is(err, sandbox.ErrEscape) {
					t.Fatalf("cleanVirtual(%q, %q) error = %v, want ErrEscape", tt.value, tt.virtualRoot, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("cleanVirtual(%q, %q) = %q, %v; want %q, nil", tt.value, tt.virtualRoot, got, err, tt.want)
			}
		})
	}
}

func TestHostFSResolveCustomVirtualRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	fsys := &hostFS{root: root, virtualRoot: "/mnt/user-data"}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalRoot, "outputs", "report.md")
	for _, value := range []string{"outputs/report.md", "/mnt/user-data/outputs/report.md"} {
		got, err := fsys.Resolve(value)
		if err != nil || got != want {
			t.Fatalf("Resolve(%q) = %q, %v; want %q, nil", value, got, err, want)
		}
	}
}

func TestHostFSWriteCreatesSharedWorkspacePermissions(t *testing.T) {
	root := t.TempDir()
	f := &hostFS{root: root, virtualRoot: "/mnt/user-data", uid: os.Geteuid(), gid: os.Getegid()}
	if err := f.WriteFile(context.Background(), "/mnt/user-data/outputs/nested/report.md", []byte("report")); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{filepath.Join(root, "outputs"), filepath.Join(root, "outputs", "nested")} {
		info, err := os.Stat(directory)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o770 {
			t.Fatalf("directory %s mode = %o, want 770", directory, info.Mode().Perm())
		}
	}
	info, err := os.Stat(filepath.Join(root, "outputs", "nested", "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf("file mode = %o, want 660", info.Mode().Perm())
	}
}

func TestHostFSResolveRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	fsys := &hostFS{root: root, virtualRoot: "/mnt/user-data"}
	if _, err := fsys.Resolve("/mnt/user-data/escape/secret.txt"); !errors.Is(err, sandbox.ErrEscape) {
		t.Fatalf("Resolve() error = %v, want ErrEscape", err)
	}
}

func TestHostFSResolveAllowsInternalSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	fsys := &hostFS{root: root, virtualRoot: "/mnt/user-data"}
	got, err := fsys.Resolve("/mnt/user-data/link/report.md")
	want := filepath.Join(canonicalRoot, "real", "report.md")
	if err != nil || got != want {
		t.Fatalf("Resolve() = %q, %v; want %q, nil", got, err, want)
	}
}

func TestHostFSReadFileRejectsOversizedFileBeforeReading(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	file := filepath.Join(root, "large.bin")
	if err := os.WriteFile(file, nil, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(file, sandbox.MaxReadFileBytes+1); err != nil {
		t.Fatal(err)
	}

	fsys := &hostFS{root: root, virtualRoot: "/workspace"}
	_, err := fsys.ReadFile(context.Background(), "large.bin")
	if err == nil || !strings.Contains(err.Error(), "over the") {
		t.Fatalf("ReadFile() error = %v, want size-limit error", err)
	}
}

func TestHostFSListWithLimitReportsTruncation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	fsys := &hostFS{root: root, virtualRoot: "/workspace"}
	entries, truncated, err := sandbox.ListWithLimit(context.Background(), fsys, ".", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || !truncated {
		t.Fatalf("ListWithLimit() = %d entries, truncated=%t; want 2, true", len(entries), truncated)
	}
}

func TestProviderNormalizesVirtualRoot(t *testing.T) {
	t.Parallel()

	provider := NewProvider(Options{VirtualRoot: "/mnt/user-data/../user-data/"})
	if provider.opts.VirtualRoot != "/mnt/user-data" {
		t.Fatalf("VirtualRoot = %q, want /mnt/user-data", provider.opts.VirtualRoot)
	}
	if got := NewProvider(Options{VirtualRoot: "  "}).opts.VirtualRoot; got != "/workspace" {
		t.Fatalf("blank VirtualRoot = %q, want /workspace", got)
	}
}

func TestProviderRejectsRelativeVirtualRoot(t *testing.T) {
	t.Parallel()

	provider := NewProvider(Options{VirtualRoot: "workspace"})
	_, err := provider.Acquire(context.Background(), "thread")
	if err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("Acquire() error = %v, want absolute-root error", err)
	}
}

func TestProviderAcquireExecAndReuse(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	provider := NewProvider(Options{BaseDir: t.TempDir(), Image: "alpine:3.20", VirtualRoot: "/mnt/user-data"})
	t.Cleanup(func() { _ = provider.Release(context.Background(), "docker-test") })
	first, err := provider.Acquire(ctx, "docker-test")
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.Acquire(ctx, "docker-test")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() != second.ID() {
		t.Fatalf("thread sandbox was not reused: %q != %q", first.ID(), second.ID())
	}
	if first.Root() != "/mnt/user-data" {
		t.Fatalf("Root() = %q, want /mnt/user-data", first.Root())
	}
	if err := first.FS().WriteFile(ctx, "/mnt/user-data/nested/message.txt", []byte("docker-ok")); err != nil {
		t.Fatal(err)
	}
	result, err := first.Exec(ctx, sandbox.Command{WorkDir: "/mnt/user-data/nested", Line: "pwd; cat message.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "/mnt/user-data/nested\ndocker-ok" {
		t.Fatalf("exec result = %#v", result)
	}
}
