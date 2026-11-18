package tool_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func TestRollingPoolReplenishesAndCommitsPolicyInModelOrder(t *testing.T) {
	registry := tool.NewRegistry()
	release := make(chan struct{})
	secondDone := make(chan struct{})
	thirdStarted := make(chan struct{})
	var prepared, committed []string
	for _, name := range []string{"a", "b", "c"} {
		mustRegister(t, registry, def(name, func(d *tool.Definition) {
			readOnly(d)
			d.Handler = func(ctx context.Context, _ tool.Call) (*tool.Result, error) {
				switch name {
				case "a":
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				case "b":
					close(secondDone)
				case "c":
					close(thirdStarted)
				}
				return &tool.Result{Content: name}, nil
			}
		}))
	}
	ic := &fakeInterceptor{before: func(_ context.Context, c tool.Call) (tool.Decision, error) {
		prepared = append(prepared, c.Name)
		return tool.Decision{}, nil
	}, after: func(_ context.Context, c tool.Call, r *tool.Result, _ error) (*tool.Result, error) {
		committed = append(committed, c.Name)
		return r, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	type result struct {
		batch tool.BatchResult
		err   error
	}
	done := make(chan result, 1)
	go func() {
		batch, err := tool.NewExecutor(registry, tool.ExecutorOptions{Concurrency: 2}).RunBatch(ctx, []tool.Call{call("1", "a"), call("2", "b"), call("3", "c")}, ic)
		done <- result{batch, err}
	}()
	select {
	case <-secondDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-thirdStarted:
	case <-ctx.Done():
		t.Fatal("pool did not replenish behind blocked earlier result")
	}
	close(release)
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if !reflect.DeepEqual(prepared, []string{"a", "b", "c"}) || !reflect.DeepEqual(committed, []string{"a", "b", "c"}) {
		t.Fatalf("prepare=%v commit=%v", prepared, committed)
	}
}

func TestUnstartedToolReclassificationCreatesExclusiveBarrier(t *testing.T) {
	registry := tool.NewRegistry()
	bStarted := make(chan struct{})
	releaseB := make(chan struct{})
	changed := make(chan struct{})
	cStarted := make(chan struct{})
	releaseC := make(chan struct{})
	dStarted := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wait := func(ch <-chan struct{}) error {
		select {
		case <-ch:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	mustRegister(t, registry, def("a", func(d *tool.Definition) {
		readOnly(d)
		d.Handler = func(context.Context, tool.Call) (*tool.Result, error) {
			return &tool.Result{Content: "a"}, wait(bStarted)
		}
	}))
	mustRegister(t, registry, def("b", func(d *tool.Definition) {
		readOnly(d)
		d.Handler = func(context.Context, tool.Call) (*tool.Result, error) {
			close(bStarted)
			return &tool.Result{Content: "b"}, wait(releaseB)
		}
	}))
	oldDispose, err := registry.RegisterOwned(def("c", readOnly))
	if err != nil {
		t.Fatal(err)
	}
	mustRegister(t, registry, def("d", func(d *tool.Definition) {
		readOnly(d)
		d.Handler = func(context.Context, tool.Call) (*tool.Result, error) { close(dStarted); return &tool.Result{}, nil }
	}))
	ic := &fakeInterceptor{after: func(_ context.Context, c tool.Call, r *tool.Result, _ error) (*tool.Result, error) {
		if c.Name == "a" {
			oldDispose()
			replacement := def("c", func(d *tool.Definition) {
				d.Handler = func(context.Context, tool.Call) (*tool.Result, error) {
					close(cStarted)
					return &tool.Result{Content: "exclusive"}, wait(releaseC)
				}
			})
			if err := registry.Register(replacement); err != nil {
				return nil, err
			}
			close(changed)
		}
		return r, nil
	}}
	done := make(chan error, 1)
	go func() {
		_, err := tool.NewExecutor(registry, tool.ExecutorOptions{Concurrency: 2}).Run(ctx, []tool.Call{call("1", "a"), call("2", "b"), call("3", "c"), call("4", "d")}, ic)
		done <- err
	}()
	if err := wait(changed); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cStarted:
		t.Fatal("exclusive replacement overlapped an earlier body")
	default:
	}
	close(releaseB)
	if err := wait(cStarted); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dStarted:
		t.Fatal("later call crossed exclusive barrier")
	default:
	}
	close(releaseC)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSharedExecutorKeepsStopDecisionOnItsOwnBatch(t *testing.T) {
	registry := tool.NewRegistry()
	started := make(chan struct{})
	release := make(chan struct{})
	mustRegister(t, registry, def("work", func(d *tool.Definition) {
		readOnly(d)
		d.Handler = func(ctx context.Context, _ tool.Call) (*tool.Result, error) {
			close(started)
			select {
			case <-release:
				return &tool.Result{Content: "done"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}))
	executor := tool.NewExecutor(registry, tool.ExecutorOptions{Concurrency: 2})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan tool.BatchResult, 1)
	go func() { batch, _ := executor.RunBatch(ctx, []tool.Call{call("normal", "work")}, nil); result <- batch }()
	<-started
	stopped, err := executor.RunBatch(ctx, []tool.Call{call("stop", "work")}, &fakeInterceptor{before: func(context.Context, tool.Call) (tool.Decision, error) { return tool.Decision{EndTurn: true}, nil }})
	if err != nil || !stopped.EndTurn {
		t.Fatalf("stop=%+v err=%v", stopped, err)
	}
	close(release)
	if normal := <-result; normal.EndTurn || len(normal.Outcomes) != 1 {
		t.Fatalf("normal batch inherited sibling stop: %+v", normal)
	}
}
