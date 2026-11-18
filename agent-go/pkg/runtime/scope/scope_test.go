package scope_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/scope"
)

func TestDisposeCancelsWorkersDrainsChildrenAndKeepsCleanupErrors(t *testing.T) {
	root := scope.New(context.Background())
	child, err := root.Child()
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	cleanupErr := errors.New("cleanup")
	_ = child.OnDispose(func(ctx context.Context) error {
		if ctx.Err() != nil {
			t.Error("cleanup inherited cancellation")
		}
		order = append(order, "child")
		return cleanupErr
	})
	canceled := make(chan struct{})
	release := make(chan struct{})
	_ = child.Go(func(ctx context.Context) { <-ctx.Done(); close(canceled); <-release })
	_ = root.OnDispose(func(context.Context) error { order = append(order, "root"); return nil })
	done := make(chan error, 1)
	go func() { done <- root.Dispose() }()
	<-canceled
	select {
	case <-done:
		t.Fatal("disposal did not drain work")
	default:
	}
	close(release)
	if err := <-done; !errors.Is(err, cleanupErr) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"root", "child"}) {
		t.Fatal(order)
	}
	if err := root.Dispose(); !errors.Is(err, cleanupErr) {
		t.Fatal("lost shared disposal result")
	}
	if _, err := root.Child(); !errors.Is(err, scope.ErrDisposed) {
		t.Fatal(err)
	}
	if err := child.Go(func(context.Context) { t.Error("work started after disposal") }); !errors.Is(err, scope.ErrDisposed) {
		t.Fatal(err)
	}
}

func TestPanickingCleanupStillClosesOtherEffects(t *testing.T) {
	owner := scope.New(context.Background())
	cleaned := false
	_ = owner.OnDispose(func(context.Context) error { cleaned = true; return nil })
	_ = owner.OnDispose(func(context.Context) error { panic("broken disposer") })
	if err := owner.Dispose(); err == nil {
		t.Fatal("cleanup panic disappeared")
	}
	if !cleaned {
		t.Fatal("sibling cleanup was skipped")
	}
	if err := owner.Dispose(); err == nil {
		t.Fatal("racing disposal lost the cleanup failure")
	}
}
