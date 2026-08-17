// Package e2e 是跨包的端到端测试。
//
// 全部用 faux provider：离线、无 API key、可重复。
package e2e_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/harness"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func lsTool(log *[]string) tool.Definition {
	return tool.Definition{
		Name:        "ls",
		Group:       "file:read",
		Description: "list files",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		Metadata:    tool.Metadata{IsReadOnly: true, IsConcurrencySafe: true},
		Handler: func(_ context.Context, c tool.Call) (*tool.Result, error) {
			*log = append(*log, string(c.Args))
			return &tool.Result{Content: "README.md\ngo.mod"}, nil
		},
	}
}

// M0 的验收标准：带工具调用的完整回合，全离线。
func TestM0_FullTurnWithToolCall(t *testing.T) {
	t.Parallel()

	var toolArgs []string

	h, err := harness.New(harness.Options{
		Model: faux.New(
			faux.ToolCall("ls", `{"path":"."}`),
			faux.Text("The repo has a README and a go.mod."),
		),
		Tools:  []tool.Definition{lsTool(&toolArgs)},
		Limits: loop.Limits{MaxIterations: 5},
	})
	if err != nil {
		t.Fatalf("harness.New() error = %v", err)
	}

	hist := message.NewHistory()
	res, err := h.Runner().Run(context.Background(), loop.Request{
		ThreadID:     "thread-1",
		RunID:        "run-1",
		SystemPrompt: "You are a helpful assistant.",
		History:      hist,
		Prompt:       "what is in this repo?",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if res.Output != "The repo has a README and a go.mod." {
		t.Errorf("Output = %q", res.Output)
	}
	if res.Iterations != 2 {
		t.Errorf("Iterations = %d, want 2", res.Iterations)
	}
	if len(toolArgs) != 1 || toolArgs[0] != `{"path":"."}` {
		t.Errorf("tool received args %v", toolArgs)
	}

	msgs := hist.All()
	if len(msgs) != 4 {
		t.Fatalf("transcript has %d messages, want 4:\n%s", len(msgs), dump(msgs))
	}
	if msgs[2].Role != message.RoleTool || msgs[2].ToolCallID != msgs[1].ToolCalls[0].ID {
		t.Errorf("tool result is not paired with its call:\n%s", dump(msgs))
	}
}

// Lifecycle dispatcher、工具执行、内核三者协作：权限式拒绝让工具不执行且模型能改换方案。
func TestM0_DenyingLifecycleHandlerPreventsExecutionAndModelRecovers(t *testing.T) {
	t.Parallel()

	var toolArgs []string

	h, err := harness.New(harness.Options{
		Model: faux.New(
			faux.ToolCall("ls", `{"path":"/etc"}`),
			faux.Text("I cannot read that path, so here is what I can tell you."),
		),
		Tools:             []tool.Definition{lsTool(&toolArgs)},
		LifecycleHandlers: []lifecycle.Handler{denyOutsideWorkspace{}},
		Limits:            loop.Limits{MaxIterations: 5},
	})
	if err != nil {
		t.Fatalf("harness.New() error = %v", err)
	}

	hist := message.NewHistory()
	res, err := h.Runner().Run(context.Background(), loop.Request{
		ThreadID: "t", RunID: "r", History: hist, Prompt: "read /etc",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(toolArgs) != 0 {
		t.Fatalf("tool executed despite the deny decision: %v", toolArgs)
	}
	if !strings.Contains(res.Output, "cannot read") {
		t.Errorf("Output = %q", res.Output)
	}

	// 被拒绝的调用仍必须留下一条 tool 结果，否则 tool_calls 悬空、下一次请求非法
	var toolMsgs int
	for _, m := range hist.All() {
		if m.Role == message.RoleTool {
			toolMsgs++
			if !m.IsError {
				t.Error("the denied call's tool message should be marked as an error")
			}
		}
	}
	if toolMsgs != 1 {
		t.Fatalf("transcript has %d tool messages, want 1:\n%s", toolMsgs, dump(hist.All()))
	}
}

// 声明了却没进入生命周期序列的处理器必须让装配失败。
func TestM0_DeclaredButMissingLifecycleHandlerFailsAssembly(t *testing.T) {
	t.Parallel()

	_, err := harness.New(harness.Options{
		Model:            faux.New(faux.Text("x")),
		DeclaredHandlers: []string{"guardrailOutput"},
	})
	if err == nil {
		t.Fatal("harness.New() succeeded with a declared-but-absent lifecycle handler")
	}
	if !strings.Contains(err.Error(), "guardrailOutput") {
		t.Errorf("error = %q, want it to name the missing lifecycle handler", err)
	}
}

func TestM0_ExtensionsAreAnchoredIntoLifecycle(t *testing.T) {
	t.Parallel()

	h, err := harness.New(harness.Options{
		Model:             faux.New(faux.Text("x")),
		LifecycleHandlers: []lifecycle.Handler{named{"first"}, named{lifecycle.TerminalName}},
		Extensions:        []lifecycle.Handler{anchoredAfter{name: "injected", after: "first"}},
	})
	if err != nil {
		t.Fatalf("harness.New() error = %v", err)
	}

	got := strings.Join(h.Lifecycle().Names(), ",")
	want := "first,injected," + lifecycle.TerminalName
	if got != want {
		t.Fatalf("lifecycle = %s, want %s", got, want)
	}
}

func TestM0_ConflictingAnchorsFailAssembly(t *testing.T) {
	t.Parallel()

	_, err := harness.New(harness.Options{
		Model:             faux.New(faux.Text("x")),
		LifecycleHandlers: []lifecycle.Handler{named{"first"}},
		Extensions: []lifecycle.Handler{
			anchoredAfter{name: "a", after: "first"},
			anchoredAfter{name: "b", after: "first"},
		},
	})
	if err == nil {
		t.Fatal("harness.New() succeeded with conflicting anchors")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("error = %q, want it to report the conflict", err)
	}
}

// --- helpers ---

func dump(msgs []message.Message) string {
	var sb strings.Builder
	for i, m := range msgs {
		sb.WriteString(string(rune('0' + i)))
		sb.WriteString(". ")
		sb.WriteString(string(m.Role))
		if len(m.ToolCalls) > 0 {
			sb.WriteString(" +toolcalls")
		}
		if m.ToolCallID != "" {
			sb.WriteString(" for=" + m.ToolCallID)
		}
		sb.WriteString(" | ")
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

type denyOutsideWorkspace struct{}

func (denyOutsideWorkspace) Name() string { return "permission" }

func (denyOutsideWorkspace) BeforeTool(_ context.Context, st *lifecycle.State) (tool.Decision, error) {
	if st.ToolCall != nil && strings.Contains(string(st.ToolCall.Args), "/etc") {
		return tool.Decision{Deny: true, Reason: "path outside the workspace"}, nil
	}
	return tool.Decision{}, nil
}

type named struct{ n string }

func (n named) Name() string { return n.n }

type anchoredAfter struct {
	name  string
	after string
}

func (a anchoredAfter) Name() string { return a.name }
func (a anchoredAfter) Anchor() lifecycle.Anchor {
	return lifecycle.Anchor{After: a.after}
}
