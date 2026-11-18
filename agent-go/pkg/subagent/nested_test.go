package subagent

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/harness"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func TestNestedDispatchDoesNotDeadlockWithOneSiblingPermit(t *testing.T) {
	var manager *Manager
	var prepared, released atomic.Int32
	manager = NewManagerWithPreparedFactory(func(_ Definition, _ []string) (PreparedRunner, error) {
		prepared.Add(1)
		h, err := harness.New(harness.Options{Sampler: sampleFunc(func(ctx context.Context, st *lifecycle.State) (*model.Response, bool, error) {
			run, _ := runtime.RunContextFrom(ctx)
			if run.AgentDepth < run.MaxRecursionDepth && len(st.History.All()) == 1 {
				return &model.Response{StopReason: model.StopReasonToolCalls, Message: message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: fmt.Sprintf("nested-%d", run.AgentDepth), Name: "task", Arguments: []byte(`{"subagent_type":"worker","description":"nested worker","prompt":"focused work"}`)}}}}, false, nil
			}
			return &model.Response{StopReason: model.StopReasonStop, Message: message.Message{Role: message.RoleAssistant, Content: fmt.Sprintf("done at depth %d", run.AgentDepth)}}, false, nil
		}), Tools: []tool.Definition{manager.TaskTool(), manager.ControlTool()}})
		if err != nil {
			return PreparedRunner{}, err
		}
		return PreparedRunner{Runner: h.Runner(), Release: func() error { released.Add(1); return h.Close() }}, nil
	}, nil, time.Hour, 1)
	defer manager.Close()
	registerWorker(t, manager)
	registry := capability.NewRegistry()
	for _, definition := range []tool.Definition{manager.TaskTool(), manager.ControlTool()} {
		if err := capability.RegisterValue(registry, capability.Value{Definition: capability.Definition{Name: "tool." + definition.Name, Kind: capability.KindTool, Scope: capability.ScopeRun}, Value: definition}); err != nil {
			t.Fatal(err)
		}
	}
	if err := capability.RegisterValue(registry, capability.Value{Definition: capability.Definition{Name: "agent.worker", Kind: capability.KindAgent, Scope: capability.ScopeRun}, Value: Definition{Name: "worker", Description: "worker"}}); err != nil {
		t.Fatal(err)
	}
	view, err := capability.NewView(registry.Snapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = testRunContext(ctx, runtime.RunContext{RunID: "root", ThreadID: "nested-thread", MaxRecursionDepth: 3, Capabilities: view, AllowedTools: []string{"task", "subagent_control"}, Budget: runtime.NewBudgetLedger(runtime.BudgetAmount{Subagents: 6})})
	results := make(chan error, 2)
	for _, id := range []string{"branch-a", "branch-b"} {
		go func(id string) {
			result, err := manager.Dispatch(ctx, DispatchRequest{ToolCallID: id, SubagentType: "worker", Prompt: "start branch"})
			if err == nil && result.Status != "completed" {
				err = fmt.Errorf("unexpected result: %+v", result)
			}
			results <- err
		}(id)
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if prepared.Load() != 6 || released.Load() != 6 {
		t.Fatalf("prepared=%d released=%d; descendants failed or were not drained", prepared.Load(), released.Load())
	}
	manager.slotMu.Lock()
	defer manager.slotMu.Unlock()
	if len(manager.slots) != 0 {
		t.Fatalf("leaked parent concurrency pools: %d", len(manager.slots))
	}
}
