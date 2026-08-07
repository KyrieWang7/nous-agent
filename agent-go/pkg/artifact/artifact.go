// Package artifact 存放被外部化的大块内容（超大工具输出、生成的文件）。
//
// 模型只看到预览与引用句柄，完整内容留在 Store 里。这样一次
// `find /` 的输出不会炸掉上下文，而需要细节时仍能取回。
package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ErrNotFound 表示引用不存在。
var ErrNotFound = errors.New("artifact: not found")

// Ref 是一个外部化内容的引用。
type Ref struct {
	// Key 是取回内容用的标识。
	Key string

	// Size 是完整内容的字节数。
	Size int64
}

// Store 存取外部化内容。
type Store interface {
	// Put 写入内容并返回引用。同样的内容多次写入应返回同一个 Key。
	Put(ctx context.Context, namespace string, data []byte) (Ref, error)

	// Get 按 Key 取回内容。
	Get(ctx context.Context, key string) ([]byte, error)
}

// DirStore 把内容写到本地目录，按内容哈希命名。
//
// 内容寻址（而非随机 Key）让重复输出天然去重：同一条命令跑十次
// 只占一份空间，而不是十份。
type DirStore struct {
	root string

	mu sync.Mutex
}

// NewDirStore 返回以 root 为根的存储。
func NewDirStore(root string) *DirStore {
	return &DirStore{root: root}
}

// Put 实现 Store。
func (d *DirStore) Put(_ context.Context, namespace string, data []byte) (Ref, error) {
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])

	key := digest
	if ns := sanitise(namespace); ns != "" {
		key = ns + "/" + digest
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	path := filepath.Join(d.root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return Ref{}, fmt.Errorf("artifact: creating directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return Ref{}, fmt.Errorf("artifact: writing %q: %w", key, err)
	}

	return Ref{Key: key, Size: int64(len(data))}, nil
}

// Get 实现 Store。
func (d *DirStore) Get(_ context.Context, key string) ([]byte, error) {
	clean := filepath.FromSlash(sanitiseKey(key))
	if clean == "" {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, key)
	}

	path := filepath.Join(d.root, clean)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %q", ErrNotFound, key)
		}
		return nil, fmt.Errorf("artifact: reading %q: %w", key, err)
	}
	return data, nil
}

// sanitise 把命名空间压成安全的单层目录名。
func sanitise(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// sanitiseKey 校验 Key 只含我们生成的形状，拦住路径穿越。
//
// Key 来自模型转录，可能被模型改写后再传回来。不校验就等于把
// 一个任意文件读取接口交给了模型。
func sanitiseKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" || strings.Contains(key, "..") {
		return ""
	}
	parts := strings.Split(key, "/")
	if len(parts) > 2 {
		return ""
	}
	for _, p := range parts {
		if p == "" || sanitise(p) != p {
			return ""
		}
	}
	return key
}
