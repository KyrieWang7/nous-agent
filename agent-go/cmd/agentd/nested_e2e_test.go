//go:build docker

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	domain "github.com/KyrieWang7/nous-agent/agent-go/internal/model"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/repository"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/harness"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/migrations"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/delegation"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	runtimepostgres "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/postgres"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/scope"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/subagent"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func TestNestedSessionsPersistLineageAndCancelTheWholeTree(t *testing.T) {
	ctx := context.Background()
	url, pool := startAgentdPostgres(t)
	dir, _ := filepath.Abs("../../migrations")
	if err := migrations.Up(url, dir); err != nil {
		t.Fatal(err)
	}
	db := testGORM(t, pool)
	sessions, err := runtimepostgres.NewSubagentStore(db)
	if err != nil {
		t.Fatal(err)
	}
	store, err := repository.NewPostgresStore(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateThread(ctx, domain.Thread{ThreadID: "nested-e2e", Created: time.Now().UTC()}, true)
	if err != nil {
		t.Fatal(err)
	}
	profile := subagent.Definition{Name: "worker", Description: "nested worker", MaxTurns: 5}
	reachedLeaf := make(chan struct{}, 1)
	var manager *subagent.Manager
	manager = subagent.NewManagerWithPreparedFactory(func(_ subagent.Definition, allowed []string) (subagent.PreparedRunner, error) {
		h, err := harness.New(harness.Options{Sampler: sessionE2ESampler(func(ctx context.Context, st *lifecycle.State) (*model.Response, bool, error) {
			run, _ := runtime.RunContextFrom(ctx)
			cancelTree := strings.Contains(st.History.All()[0].Content, "cancel-tree")
			var call *message.ToolCall
			if run.AgentDepth < 3 {
				if st.Iteration == 0 {
					prompt := "nested work"
					if cancelTree {
						prompt = "cancel-tree"
					}
					args, _ := json.Marshal(map[string]any{"subagent_type": "worker", "description": "nested worker", "prompt": prompt, "background": true})
					call = &message.ToolCall{ID: fmt.Sprintf("nested-%d", run.AgentDepth), Name: "task", Arguments: args}
				} else if st.Iteration == 1 {
					args, _ := json.Marshal(map[string]any{"action": "wait", "task_id": fmt.Sprintf("nested-%d", run.AgentDepth), "timeout_seconds": 10})
					call = &message.ToolCall{ID: "wait", Name: "subagent_control", Arguments: args}
				}
			} else if cancelTree {
				reachedLeaf <- struct{}{}
				<-ctx.Done()
				return nil, false, ctx.Err()
			}
			if call != nil {
				return &model.Response{StopReason: model.StopReasonToolCalls, Message: message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{*call}}}, false, nil
			}
			return &model.Response{StopReason: model.StopReasonStop, Message: message.Message{Role: message.RoleAssistant, Content: "nested complete"}}, false, nil
		}), Tools: []tool.Definition{manager.TaskTool(), manager.ControlTool()}, Limits: loop.Limits{MaxIterations: 5}})
		if err != nil {
			return subagent.PreparedRunner{}, err
		}
		return subagent.PreparedRunner{Runner: h.Runner(), Release: h.Close}, nil
	}, sessions, time.Hour, 1)
	defer manager.Close()
	if err = manager.Register(profile); err != nil {
		t.Fatal(err)
	}
	caps := capability.NewRegistry()
	if err = capability.RegisterValue(caps, capability.Value{Definition: capability.Definition{Name: "agent.worker", Kind: capability.KindAgent, Scope: capability.ScopeRun}, Value: profile}); err != nil {
		t.Fatal(err)
	}
	for _, d := range []tool.Definition{manager.TaskTool(), manager.ControlTool()} {
		if err = capability.RegisterValue(caps, capability.Value{Definition: capability.Definition{Name: "tool." + d.Name, Kind: capability.KindTool, Scope: capability.ScopeRun}, Value: d}); err != nil {
			t.Fatal(err)
		}
	}
	view, err := capability.NewView(caps.Snapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := func(owner *scope.Scope, id string) context.Context {
		state, _ := runtime.NewRunStateMachine(id, "nested-e2e")
		_ = state.Start()
		return runtime.WithRunContext(owner.Context(), runtime.RunContext{RunID: id, ThreadID: "nested-e2e", ExecutionOwner: owner, ExecutionContext: owner.Context(), StateMachine: state, MaxRecursionDepth: 3, Capabilities: view, AllowedTools: []string{"task", "subagent_control"}, Budget: runtime.NewBudgetLedger(runtime.BudgetAmount{Subagents: 6})})
	}
	owner := scope.New(ctx)
	result, err := manager.Dispatch(parent(owner, "normal-root"), subagent.DispatchRequest{ToolCallID: "branch", SubagentType: "worker", Prompt: "normal work"})
	if disposeErr := owner.Dispose(); disposeErr != nil {
		t.Fatal(disposeErr)
	}
	if err != nil || result.Status != "completed" {
		t.Fatalf("nested result=%+v err=%v", result, err)
	}
	list, err := sessions.ListSessions(ctx, "nested-e2e")
	if err != nil || len(list) != 3 {
		t.Fatalf("sessions=%v err=%v", list, err)
	}
	byID := map[string]delegation.Session{}
	for _, s := range list {
		byID[s.ID] = s
		if s.Result.Status != "completed" {
			t.Fatalf("unsettled descendant=%+v", s.Result)
		}
	}
	roots := 0
	for _, s := range list {
		if s.ParentSessionID == "" {
			roots++
			continue
		}
		if _, ok := byID[s.ParentSessionID]; !ok {
			t.Fatalf("missing parent for %s", s.ID)
		}
	}
	if roots != 1 {
		t.Fatalf("root children=%d", roots)
	}
	// A fresh store reconstructs the same lineage independently of manager memory.
	restarted, err := runtimepostgres.NewSubagentStore(db)
	if err != nil {
		t.Fatal(err)
	}
	again, err := restarted.ListSessions(ctx, "nested-e2e")
	if err != nil || len(again) != 3 {
		t.Fatal("session tree did not survive store restart")
	}
	cancelledOwner := scope.New(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := manager.Dispatch(parent(cancelledOwner, "cancel-root"), subagent.DispatchRequest{ToolCallID: "branch", SubagentType: "worker", Prompt: "cancel-tree"})
		done <- err
	}()
	select {
	case <-reachedLeaf:
	case <-time.After(10 * time.Second):
		t.Fatal("nested leaf did not start")
	}
	if err = cancelledOwner.Dispose(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled tree returned success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("nested tree did not drain")
	}
	list, err = sessions.ListSessions(ctx, "nested-e2e")
	if err != nil || len(list) != 6 {
		t.Fatalf("sessions after cancel=%d err=%v", len(list), err)
	}
	n := 0
	for _, s := range list {
		if strings.HasPrefix(s.Result.RunID, "cancel-root:") {
			n++
			if s.Result.Status != "cancelled" {
				t.Fatalf("descendant did not cancel: %+v", s.Result)
			}
		}
	}
	if n != 3 {
		t.Fatalf("cancelled descendants=%d", n)
	}
}
