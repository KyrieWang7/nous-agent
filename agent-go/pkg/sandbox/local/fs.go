package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

// localFS 把虚拟路径映射到宿主路径，并拦截一切越界访问。
type localFS struct {
	hostRoot    string // 已经过 EvalSymlinks 的绝对路径
	virtualRoot string
}

// Resolve 实现 sandbox.FS。
//
// 三道检查，缺一不可：
//  1. 绝对路径必须落在 virtualRoot 下。否则 "/etc/passwd" 会被静默重映射到
//     沙箱内的同名路径，让模型以为自己读到了真的系统文件。
//  2. 词法检查：Clean 之后必须仍在 hostRoot 下，拦住 "../.." 这类。
//  3. 符号链接检查：对最深的已存在祖先做 EvalSymlinks 后重新检查。
//     只做词法检查的实现会被"沙箱内的符号链接指向 /etc"直接穿透。
func (l *localFS) Resolve(virtual string) (string, error) {
	rel, err := l.toRelative(virtual)
	if err != nil {
		return "", err
	}

	clean := filepath.Clean(filepath.Join(l.hostRoot, rel))
	if !underRoot(clean, l.hostRoot) {
		return "", fmt.Errorf("%w: %q", sandbox.ErrEscape, virtual)
	}

	resolved, err := l.resolveSymlinks(clean)
	if err != nil {
		return "", err
	}
	if !underRoot(resolved, l.hostRoot) {
		return "", fmt.Errorf("%w: %q resolves outside the sandbox via a symlink", sandbox.ErrEscape, virtual)
	}
	return resolved, nil
}

// toRelative 把虚拟路径转成相对沙箱根的路径。
func (l *localFS) toRelative(virtual string) (string, error) {
	p := strings.TrimSpace(virtual)
	if p == "" {
		return ".", nil
	}
	p = filepath.ToSlash(p)

	if !strings.HasPrefix(p, "/") {
		return p, nil // 相对路径按相对沙箱根解释
	}

	vroot := strings.TrimSuffix(filepath.ToSlash(l.virtualRoot), "/")
	if vroot == "" {
		return strings.TrimPrefix(p, "/"), nil
	}

	switch {
	case p == vroot:
		return ".", nil
	case strings.HasPrefix(p, vroot+"/"):
		return strings.TrimPrefix(p, vroot+"/"), nil
	default:
		return "", fmt.Errorf("%w: absolute path %q is outside %s", sandbox.ErrEscape, virtual, vroot)
	}
}

// resolveSymlinks 对 p 最深的已存在祖先做 EvalSymlinks，再拼回其余部分。
//
// 直接对 p 调 EvalSymlinks 在"写一个还不存在的文件"时会失败，
// 所以必须从存在的部分开始。
func (l *localFS) resolveSymlinks(p string) (string, error) {
	existing := p
	var missing []string

	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			// 一直退到文件系统根都不存在：不该发生，因为 hostRoot 一定存在。
			return "", fmt.Errorf("sandbox: cannot resolve %q", p)
		}
		missing = append([]string{filepath.Base(existing)}, missing...)
		existing = parent
	}

	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", fmt.Errorf("sandbox: resolving %q: %w", p, err)
	}
	if len(missing) == 0 {
		return resolved, nil
	}
	return filepath.Join(append([]string{resolved}, missing...)...), nil
}

// underRoot 报告 p 是否等于 root 或位于其下。
//
// 用路径分隔符比较而不是纯字符串前缀：否则 "/tmp/sandbox-evil" 会被当成
// "/tmp/sandbox" 的子目录。
func underRoot(p, root string) bool {
	if p == root {
		return true
	}
	return strings.HasPrefix(p, root+string(filepath.Separator))
}

// ReadFile 实现 sandbox.FS。
func (l *localFS) ReadFile(ctx context.Context, path string) ([]byte, error) {
	host, err := l.Resolve(path)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(host)
	if err != nil {
		return nil, wrapStat(path, err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, wrapStat(path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("sandbox: %q is a directory", path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("sandbox: %q is not a regular file", path)
	}
	if info.Size() > sandbox.MaxReadFileBytes {
		return nil, fmt.Errorf("sandbox: %q is %d bytes, over the %d byte limit", path, info.Size(), sandbox.MaxReadFileBytes)
	}

	data, err := sandbox.ReadAllLimited(ctx, file, sandbox.MaxReadFileBytes)
	if err != nil {
		if errors.Is(err, sandbox.ErrReadLimit) {
			return nil, fmt.Errorf("sandbox: %q grew over the %d byte limit while being read: %w", path, sandbox.MaxReadFileBytes, err)
		}
		return nil, fmt.Errorf("sandbox: reading %q: %w", path, err)
	}
	return data, nil
}

// WriteFile 实现 sandbox.FS。父目录不存在时自动创建。
func (l *localFS) WriteFile(_ context.Context, path string, data []byte) error {
	host, err := l.Resolve(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(host), 0o750); err != nil {
		return fmt.Errorf("sandbox: creating parent of %q: %w", path, err)
	}
	if err := os.WriteFile(host, data, 0o640); err != nil {
		return fmt.Errorf("sandbox: writing %q: %w", path, err)
	}
	return nil
}

// List 实现 sandbox.FS。
func (l *localFS) List(ctx context.Context, path string) ([]sandbox.Entry, error) {
	entries, _, err := l.list(ctx, path, 0)
	return entries, err
}

func (l *localFS) ListLimit(ctx context.Context, path string, limit int) ([]sandbox.Entry, bool, error) {
	if limit <= 0 {
		return nil, false, errors.New("sandbox: list limit must be positive")
	}
	return l.list(ctx, path, limit)
}

func (l *localFS) list(ctx context.Context, path string, limit int) ([]sandbox.Entry, bool, error) {
	host, err := l.Resolve(path)
	if err != nil {
		return nil, false, err
	}

	dir, err := os.Open(host)
	if err != nil {
		return nil, false, wrapStat(path, err)
	}
	defer func() { _ = dir.Close() }()
	readCount := -1
	if limit > 0 {
		readCount = limit + 1
	}
	entries, err := dir.ReadDir(readCount)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, wrapStat(path, err)
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
		entry := sandbox.Entry{Name: e.Name(), IsDir: e.IsDir()}
		if info, err := e.Info(); err == nil {
			entry.Size = info.Size()
			entry.Mode = info.Mode().String()
		}
		out = append(out, entry)
	}
	return out, truncated, nil
}

// Stat 实现 sandbox.FS。
func (l *localFS) Stat(_ context.Context, path string) (sandbox.Entry, error) {
	host, err := l.Resolve(path)
	if err != nil {
		return sandbox.Entry{}, err
	}

	info, err := os.Stat(host)
	if err != nil {
		return sandbox.Entry{}, wrapStat(path, err)
	}
	return sandbox.Entry{
		Name:  info.Name(),
		IsDir: info.IsDir(),
		Size:  info.Size(),
		Mode:  info.Mode().String(),
	}, nil
}

func wrapStat(path string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %q", sandbox.ErrNotFound, path)
	}
	return fmt.Errorf("sandbox: accessing %q: %w", path, err)
}
