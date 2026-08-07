package subagent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/harness"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware/builtin"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

func TestDispatchUsesIndependentHistoryAndRestrictsTools(t *testing.T) {
	var gotAllowed []string
	factory := func(_ Definition, allowed []string) (*loop.Runner, error) {
		gotAllowed = append([]string(nil), allowed...)
		m := faux.New(faux.Text("child done"))
		h, err := harness.New(harness.Options{Model: m})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}
	m := NewManager(factory, nil, 0)
	_ = m.Register(Definition{Name: "explore", Description: "explore", AllowedTools: []string{"read", "write"}})
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "r", ThreadID: "t", AllowedTools: []string{"read"}})
	res, err := m.Dispatch(ctx, DispatchRequest{Agent: "explore", Prompt: "inspect", RestrictTools: []string{"read"}, ToolCallID: "tc"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "child done" || len(gotAllowed) != 1 || gotAllowed[0] != "read" {
		t.Fatalf("result=%#v allowed=%v", res, gotAllowed)
	}
	if _, err := m.Dispatch(ctx, DispatchRequest{Agent: "explore", Prompt: "x", RestrictTools: []string{"write"}}); err == nil {
		t.Fatal("widening restriction accepted")
	}
}

func TestDispatchAttributesUsageOnlyToSubagentBucket(t *testing.T) {
	factory := func(_ Definition, _ []string) (*loop.Runner, error) {
		m := faux.New(faux.Text("done").WithUsage(model.Usage{InputTokens: 3, OutputTokens: 2}))
		h, err := harness.New(harness.Options{Model: m, Middleware: []middleware.Middleware{builtin.NewTokenUsage()}})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}
	manager := NewManager(factory, nil, 0)
	_ = manager.Register(Definition{Name: "worker", Description: "worker"})
	journal := runtime.NewJournal(nil)
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "parent", ThreadID: "thread", Journal: journal})
	if _, err := manager.Dispatch(ctx, DispatchRequest{Agent: "worker", Prompt: "work"}); err != nil {
		t.Fatal(err)
	}
	totals := journal.Totals()
	if totals.SubagentTokens != 5 || totals.LeadTokens != 0 || totals.InputTokens != 3 || totals.OutputTokens != 2 {
		t.Fatalf("totals = %#v", totals)
	}
}

func TestManagerLimitsConcurrentDispatch(t *testing.T) {
	release := make(chan struct{})
	var factories atomic.Int32
	factory := func(_ Definition, _ []string) (*loop.Runner, error) {
		factories.Add(1)
		<-release
		h, err := harness.New(harness.Options{Model: faux.New(faux.Text("done"))})
		if err != nil {
			return nil, err
		}
		return h.Runner(), nil
	}
	m := NewManager(factory, nil, 0, 1)
	if err := m.Register(Definition{Name: "worker", Description: "worker"}); err != nil {
		t.Fatal(err)
	}
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "r", ThreadID: "t"})
	done := make(chan error, 2)
	go func() {
		_, err := m.Dispatch(ctx, DispatchRequest{Agent: "worker", Prompt: "one"})
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for factories.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	go func() {
		_, err := m.Dispatch(ctx, DispatchRequest{Agent: "worker", Prompt: "two"})
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if got := factories.Load(); got != 1 {
		t.Fatalf("factories started concurrently = %d", got)
	}
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
func TestBuiltinsAndTaskTool(t *testing.T) {
	if len(Builtins()) != 5 {
		t.Fatalf("builtins=%d", len(Builtins()))
	}
	m := NewManager(nil, nil, 0)
	if d := m.TaskTool(); d.Name != "task" {
		t.Fatalf("tool=%s", d.Name)
	}
}
