package local

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

// maxFileBytes 是单次 ReadFile 的上限。
//
// 没有上限时一个 2 GB 的文件会直接吃满内存，而模型无论如何读不完 ——
// 超限应当报错而不是静默截断，静默截断会让模型基于半个文件做判断。
const maxFileBytes = 8 << 20

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
func (l *localFS) ReadFile(_ context.Context, path string) ([]byte, error) {
	host, err := l.Resolve(path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(host)
	if err != nil {
		return nil, wrapStat(path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("sandbox: %q is a directory", path)
	}
	if info.Size() > maxFileBytes {
		return nil, fmt.Errorf("sandbox: %q is %d bytes, over the %d byte limit", path, info.Size(), maxFileBytes)
	}

	data, err := os.ReadFile(host)
	if err != nil {
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
func (l *localFS) List(_ context.Context, path string) ([]sandbox.Entry, error) {
	host, err := l.Resolve(path)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(host)
	if err != nil {
		return nil, wrapStat(path, err)
	}

	out := make([]sandbox.Entry, 0, len(entries))
	for _, e := range entries {
		entry := sandbox.Entry{Name: e.Name(), IsDir: e.IsDir()}
		if info, err := e.Info(); err == nil {
			entry.Size = info.Size()
			entry.Mode = info.Mode().String()
		}
		out = append(out, entry)
	}
	return out, nil
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
