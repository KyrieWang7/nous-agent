package handlers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/skill"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func TestSkillActivationInjectsOnceButAlwaysNarrows(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "reports")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: reports\ndescription: Use for reports requests\nallowed-tools: [read, delayed]\n---\nReport instructions"), 0o600)
	skills, err := skill.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	tools := tool.NewRegistry()
	handler := func(context.Context, tool.Call) (*tool.Result, error) { return &tool.Result{}, nil }
	for _, d := range []tool.Definition{{Name: "read", Group: "g", Parameters: json.RawMessage(`{}`), Handler: handler}, {Name: "write", Group: "g", Parameters: json.RawMessage(`{}`), Handler: handler}, {Name: "delayed", Group: "g", Parameters: json.RawMessage(`{}`), Deferred: true, Handler: handler}} {
		if err := tools.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	mw := NewSkillActivation("skill.registry", tools, nil)
	capabilities := capability.NewRegistry()
	if err := capability.RegisterValue(capabilities, capability.Value{Definition: capability.Definition{Name: "skill.registry", Kind: capability.KindSkill}, Value: skills}); err != nil {
		t.Fatal(err)
	}
	view, err := capability.NewView(capabilities.Snapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "run-1", ThreadID: "thread-1", Capabilities: view})
	h := message.NewHistory()
	h.Append(message.Message{Role: message.RoleUser, Content: "make reports"})
	st := lifecycle.NewState(lifecycle.StateInit{History: h})
	st.ToolSet = []string{"read", "write", "delayed"}
	st.ModelInput = &model.Request{Messages: h.All(), Tools: tools.Schemas(st.ToolSet)}
	if err := mw.BeforeModel(ctx, st); err != nil {
		t.Fatal(err)
	}
	if h.Len() != 2 {
		t.Fatalf("history len=%d", h.Len())
	}
	if len(st.ToolSet) != 2 || len(st.DisclosedTools) != 1 {
		t.Fatalf("allow=%v disclosed=%v", st.ToolSet, st.DisclosedTools)
	}
	st.ToolSet = []string{"read", "write", "delayed"}
	st.ModelInput = &model.Request{Messages: h.All(), Tools: tools.Schemas(st.ToolSet)}
	if err := mw.BeforeModel(ctx, st); err != nil {
		t.Fatal(err)
	}
	if h.Len() != 2 {
		t.Fatalf("skill injected twice, history len=%d", h.Len())
	}
	if len(st.ToolSet) != 2 {
		t.Fatalf("narrowing disappeared on second run: %v", st.ToolSet)
	}
}
