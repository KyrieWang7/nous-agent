package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/permission"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func TestClarificationEndsTurnWithoutSuspension(t *testing.T) {
	h := message.NewHistory()
	st := lifecycle.NewState(lifecycle.StateInit{History: h})
	call := tool.Call{ID: "c1", Name: ClarificationToolName, Args: json.RawMessage(`{"question":"Which environment?"}`)}
	st.ToolCall = &call
	decision, err := NewClarification().BeforeTool(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.EndTurn || decision.Reason != "Which environment?" {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestClarificationPassesWorkspacePermissionBeforeEndingTurn(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(ClarificationTool()); err != nil {
		t.Fatal(err)
	}
	policy, err := permission.NewPolicy(permission.Config{Preset: permission.PresetWorkspaceWrite})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := lifecycle.NewDispatcher([]lifecycle.Handler{
		NewPermission("policy.tools", registry),
		NewClarification(),
	}, lifecycle.DispatcherOptions{})
	if err != nil {
		t.Fatal(err)
	}

	history := message.NewHistory()
	state := lifecycle.NewState(lifecycle.StateInit{History: history})
	capabilities := capability.NewRegistry()
	if err := capability.RegisterValue(capabilities, capability.Value{Definition: capability.Definition{Name: "policy.tools", Kind: capability.KindPolicy, Scope: capability.ScopeRun}, Value: policy}); err != nil {
		t.Fatal(err)
	}
	view, err := capability.NewView(capabilities.Snapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "run-1", ThreadID: "thread-1", Capabilities: view})
	decision, err := dispatcher.ToolInterceptor(state).BeforeTool(ctx, tool.Call{
		ID:   "c1",
		Name: ClarificationToolName,
		Args: json.RawMessage(`{"question":"Which environment?"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Deny || !decision.EndTurn || decision.Reason != "Which environment?" {
		t.Fatalf("decision = %#v, want an allowed end-turn clarification", decision)
	}
}
