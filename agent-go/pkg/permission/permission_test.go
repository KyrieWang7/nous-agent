package permission_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/permission"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func def(name string, readOnly, sandboxed bool) tool.Definition {
	return tool.Definition{
		Name:       name,
		Group:      "test",
		Parameters: json.RawMessage(`{"type":"object"}`),
		Metadata: tool.Metadata{
			IsReadOnly:      readOnly,
			RequiresSandbox: sandboxed,
		},
		Handler: func(context.Context, tool.Call) (*tool.Result, error) {
			return &tool.Result{}, nil
		},
	}
}

var (
	readTool    = def("read_file", true, true)
	writeTool   = def("write_file", false, true)
	unsandboxed = def("http_post", false, false)
)

func TestAuthorize_ModeMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode Mode
		tool tool.Definition
		want bool
	}{
		{permission.ModeReadOnly, readTool, true},
		{permission.ModeReadOnly, writeTool, false},
		{permission.ModeReadOnly, unsandboxed, false},

		{permission.ModeWorkspaceWrite, readTool, true},
		{permission.ModeWorkspaceWrite, writeTool, true},
		{permission.ModeWorkspaceWrite, unsandboxed, false},

		{permission.ModeAllow, readTool, true},
		{permission.ModeAllow, writeTool, true},
		{permission.ModeAllow, unsandboxed, true},

		{permission.ModeDangerFullAccess, unsandboxed, true},
	}

	for _, tc := range tests {
		name := string(tc.mode) + "/" + tc.tool.Name
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := mustPolicy(t, permission.Config{Mode: tc.mode})
			got := p.Authorize(context.Background(), tc.tool, nil)

			if got.Allowed != tc.want {
				t.Fatalf("Authorize() allowed = %v, want %v (reason: %s)", got.Allowed, tc.want, got.Reason)
			}
			if !got.Allowed && got.Reason == "" {
				t.Error("a denial must carry a reason the model can act on")
			}
		})
	}
}

func TestNewPolicy_DefaultsToAllow(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, permission.Config{})
	if got := p.Mode("anything"); got != permission.ModeAllow {
		t.Fatalf("default mode = %q, want %q", got, permission.ModeAllow)
	}
}

// 拼错的 mode 若被当成默认值处理就是静默降级为放行，必须在构造期失败。
func TestNewPolicy_RejectsUnknownMode(t *testing.T) {
	t.Parallel()

	if _, err := permission.NewPolicy(permission.Config{Mode: "read-only"}); err == nil {
		t.Fatal("NewPolicy() accepted an unknown mode")
	}

	_, err := permission.NewPolicy(permission.Config{
		Mode:          permission.ModeAllow,
		ToolOverrides: map[string]permission.Mode{"bash": "yolo"},
	})
	if err == nil {
		t.Fatal("NewPolicy() accepted an unknown override mode")
	}
	if !strings.Contains(err.Error(), "bash") {
		t.Errorf("error = %q, want it to name the offending tool", err)
	}
}

func TestAuthorize_ToolOverrideWins(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, permission.Config{
		Mode:          permission.ModeReadOnly,
		ToolOverrides: map[string]permission.Mode{"write_file": permission.ModeAllow},
	})

	if got := p.Authorize(context.Background(), writeTool, nil); !got.Allowed {
		t.Fatalf("override did not take effect: %s", got.Reason)
	}
	// 未覆盖的工具仍按默认级别
	if got := p.Authorize(context.Background(), unsandboxed, nil); got.Allowed {
		t.Fatal("an unrelated tool was allowed by the override")
	}
}

// --- ModePrompt ---

func TestAuthorize_PromptAsksAndHonoursApproval(t *testing.T) {
	t.Parallel()

	pr := &fakePrompter{approve: true}
	p := mustPolicy(t, permission.Config{Mode: permission.ModePrompt, Prompter: pr})

	got := p.Authorize(context.Background(), writeTool, json.RawMessage(`{"path":"a"}`))
	if !got.Allowed {
		t.Fatalf("Authorize() denied an approved call: %s", got.Reason)
	}
	if pr.calls != 1 {
		t.Fatalf("prompter called %d times, want 1", pr.calls)
	}
	if pr.lastReq.ToolName != "write_file" {
		t.Errorf("prompter saw tool %q", pr.lastReq.ToolName)
	}
	if string(pr.lastReq.Args) != `{"path":"a"}` {
		t.Errorf("prompter saw args %q; the user must see what they are approving", pr.lastReq.Args)
	}
}

func TestAuthorize_PromptHonoursDecline(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, permission.Config{
		Mode:     permission.ModePrompt,
		Prompter: &fakePrompter{approve: false},
	})

	if got := p.Authorize(context.Background(), writeTool, nil); got.Allowed {
		t.Fatal("Authorize() allowed a declined call")
	}
}

// 只读工具不该打扰用户：prompt 模式的意图是拦住有副作用的操作。
func TestAuthorize_PromptDoesNotAskForReadOnlyTools(t *testing.T) {
	t.Parallel()

	pr := &fakePrompter{approve: false}
	p := mustPolicy(t, permission.Config{Mode: permission.ModePrompt, Prompter: pr})

	if got := p.Authorize(context.Background(), readTool, nil); !got.Allowed {
		t.Fatalf("a read-only tool was denied in prompt mode: %s", got.Reason)
	}
	if pr.calls != 0 {
		t.Fatalf("prompter called %d times for a read-only tool, want 0", pr.calls)
	}
}

// 征求确认失败不能替用户点同意。
func TestAuthorize_PrompterErrorIsDenial(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, permission.Config{
		Mode:     permission.ModePrompt,
		Prompter: &fakePrompter{err: errors.New("ui disconnected")},
	})

	got := p.Authorize(context.Background(), writeTool, nil)
	if got.Allowed {
		t.Fatal("a prompter failure must not be treated as approval")
	}
	if !strings.Contains(got.Reason, "ui disconnected") {
		t.Errorf("reason = %q, want it to carry the cause", got.Reason)
	}
}

// 没人能回答的确认请求只能是拒绝。
func TestAuthorize_PromptWithoutPrompterIsDenial(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, permission.Config{Mode: permission.ModePrompt})

	if got := p.Authorize(context.Background(), writeTool, nil); got.Allowed {
		t.Fatal("prompt mode without a prompter must deny")
	}
}

func TestAuthorize_PromptTimesOutAsDenial(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, permission.Config{
		Mode:          permission.ModePrompt,
		Prompter:      &blockingPrompter{},
		PromptTimeout: 20 * time.Millisecond,
	})

	start := time.Now()
	got := p.Authorize(context.Background(), writeTool, nil)

	if got.Allowed {
		t.Fatal("a confirmation timeout must be treated as a denial")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Authorize() took %v; the prompt timeout did not fire", elapsed)
	}
}

// --- AllowedTools ---

func TestAllowedTools_FiltersByMode(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	for _, d := range []tool.Definition{readTool, writeTool, unsandboxed} {
		if err := r.Register(d); err != nil {
			t.Fatal(err)
		}
	}

	p := mustPolicy(t, permission.Config{Mode: permission.ModeReadOnly})
	got := p.AllowedTools(r, []string{"read_file", "write_file", "http_post"})

	if len(got) != 1 || got[0] != "read_file" {
		t.Fatalf("AllowedTools() = %v, want [read_file]", got)
	}
}

// prompt 模式下工具应当可见：披露与调用是两件事，
// 否则模型永远不知道有这个能力，也就永远不会去请求确认。
func TestAllowedTools_PromptModeKeepsToolsVisible(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	if err := r.Register(writeTool); err != nil {
		t.Fatal(err)
	}

	p := mustPolicy(t, permission.Config{Mode: permission.ModePrompt})
	got := p.AllowedTools(r, []string{"write_file"})

	if len(got) != 1 {
		t.Fatalf("AllowedTools() = %v, want the tool to stay visible in prompt mode", got)
	}
}

func TestAllowedTools_SkipsUnregisteredNames(t *testing.T) {
	t.Parallel()

	r := tool.NewRegistry()
	if err := r.Register(readTool); err != nil {
		t.Fatal(err)
	}

	p := mustPolicy(t, permission.Config{Mode: permission.ModeAllow})
	got := p.AllowedTools(r, []string{"read_file", "ghost"})

	if len(got) != 1 || got[0] != "read_file" {
		t.Fatalf("AllowedTools() = %v, want [read_file]", got)
	}
}

// --- ParseMode ---

func TestParseMode(t *testing.T) {
	t.Parallel()

	if got, err := permission.ParseMode(" READ_ONLY "); err != nil || got != permission.ModeReadOnly {
		t.Fatalf("ParseMode() = %q, %v", got, err)
	}
	if _, err := permission.ParseMode("nope"); err == nil {
		t.Fatal("ParseMode() accepted an unknown mode")
	}
}

// --- helpers ---

type Mode = permission.Mode

func mustPolicy(t *testing.T, cfg permission.Config) *permission.Policy {
	t.Helper()
	p, err := permission.NewPolicy(cfg)
	if err != nil {
		t.Fatalf("NewPolicy() error = %v", err)
	}
	return p
}

type fakePrompter struct {
	approve bool
	err     error
	calls   int
	lastReq permission.ConfirmRequest
}

func (f *fakePrompter) Confirm(_ context.Context, req permission.ConfirmRequest) (bool, error) {
	f.calls++
	f.lastReq = req
	return f.approve, f.err
}

type blockingPrompter struct{}

func (blockingPrompter) Confirm(ctx context.Context, _ permission.ConfirmRequest) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}
