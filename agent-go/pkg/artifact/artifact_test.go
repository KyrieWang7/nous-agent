package artifact_test

import (
	"context"
	"errors"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/artifact"
)

func TestDirStore_PutGetRoundTrip(t *testing.T) {
	t.Parallel()

	s := artifact.NewDirStore(t.TempDir())
	ctx := context.Background()

	ref, err := s.Put(ctx, "run-1", []byte("large output"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if ref.Size != 12 {
		t.Errorf("Size = %d, want 12", ref.Size)
	}

	got, err := s.Get(ctx, ref.Key)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(got) != "large output" {
		t.Fatalf("Get() = %q", got)
	}
}

// 内容寻址让重复输出天然去重：同一条命令跑十次只占一份空间。
func TestDirStore_SameContentSameKey(t *testing.T) {
	t.Parallel()

	s := artifact.NewDirStore(t.TempDir())
	ctx := context.Background()

	a, _ := s.Put(ctx, "run-1", []byte("identical"))
	b, _ := s.Put(ctx, "run-1", []byte("identical"))

	if a.Key != b.Key {
		t.Fatalf("identical content produced different keys: %q vs %q", a.Key, b.Key)
	}
}

func TestDirStore_DifferentContentDifferentKey(t *testing.T) {
	t.Parallel()

	s := artifact.NewDirStore(t.TempDir())
	ctx := context.Background()

	a, _ := s.Put(ctx, "run-1", []byte("one"))
	b, _ := s.Put(ctx, "run-1", []byte("two"))

	if a.Key == b.Key {
		t.Fatal("different content produced the same key")
	}
}

func TestDirStore_MissingKeyIsNotFound(t *testing.T) {
	t.Parallel()

	s := artifact.NewDirStore(t.TempDir())
	if _, err := s.Get(context.Background(), "run-1/deadbeef"); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

// Key 会经过模型转录再传回来，可能被改写。不校验就等于把一个
// 任意文件读取接口交给了模型。
func TestDirStore_RejectsTraversalKeys(t *testing.T) {
	t.Parallel()

	s := artifact.NewDirStore(t.TempDir())
	ctx := context.Background()

	bad := []string{
		"../../etc/passwd",
		"run/../../../etc/passwd",
		"a/b/c/d",
		"",
		"   ",
		"ns/../x",
	}

	for _, key := range bad {
		t.Run("key="+key, func(t *testing.T) {
			if _, err := s.Get(ctx, key); !errors.Is(err, artifact.ErrNotFound) {
				t.Fatalf("Get(%q) error = %v, want ErrNotFound", key, err)
			}
		})
	}
}

func TestDirStore_NamespaceIsSanitised(t *testing.T) {
	t.Parallel()

	s := artifact.NewDirStore(t.TempDir())
	ctx := context.Background()

	// 带路径分隔符的命名空间不得造出目录层级
	ref, err := s.Put(ctx, "../escaped", []byte("x"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if _, err := s.Get(ctx, ref.Key); err != nil {
		t.Fatalf("Get() error = %v; the sanitised key must remain retrievable", err)
	}
}

func TestDirStore_ConcurrentPut(t *testing.T) {
	t.Parallel()

	s := artifact.NewDirStore(t.TempDir())
	ctx := context.Background()

	done := make(chan struct{})
	for range 20 {
		go func() {
			defer func() { done <- struct{}{} }()
			if _, err := s.Put(ctx, "run-1", []byte("same content")); err != nil {
				t.Errorf("Put() error = %v", err)
			}
		}()
	}
	for range 20 {
		<-done
	}
}
