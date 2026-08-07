package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
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
	mw := NewSkillActivation(skills, tools, nil)
	h := message.NewHistory()
	h.Append(message.Message{Role: message.RoleUser, Content: "make reports"})
	st := middleware.NewState(middleware.StateInit{History: h})
	st.ToolSet = []string{"read", "write", "delayed"}
	st.ModelInput = &model.Request{Messages: h.All(), Tools: tools.Schemas(st.ToolSet)}
	if err := mw.BeforeModel(context.Background(), st); err != nil {
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
	if err := mw.BeforeModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if h.Len() != 2 {
		t.Fatalf("skill injected twice, history len=%d", h.Len())
	}
	if len(st.ToolSet) != 2 {
		t.Fatalf("narrowing disappeared on second run: %v", st.ToolSet)
	}
}
