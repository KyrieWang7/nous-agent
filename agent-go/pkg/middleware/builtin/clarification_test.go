package builtin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/permission"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func TestClarificationEndsTurnWithoutSuspension(t *testing.T) {
	h := message.NewHistory()
	st := middleware.NewState(middleware.StateInit{History: h})
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
	policy, err := permission.NewPolicy(permission.Config{Mode: permission.ModeWorkspaceWrite})
	if err != nil {
		t.Fatal(err)
	}
	chain, err := middleware.NewChain([]middleware.Middleware{
		NewPermission(policy, registry),
		NewClarification(),
	}, middleware.ChainOptions{})
	if err != nil {
		t.Fatal(err)
	}

	history := message.NewHistory()
	state := middleware.NewState(middleware.StateInit{History: history})
	decision, err := chain.ToolInterceptor(state).BeforeTool(context.Background(), tool.Call{
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
