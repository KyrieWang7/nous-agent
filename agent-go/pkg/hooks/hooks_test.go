package hooks_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/hooks"
)

func fn(name string, evt hooks.Event, out hooks.Outcome, err error) *hooks.FuncHook {
	return &hooks.FuncHook{
		HookName: name,
		OnEvents: []hooks.Event{evt},
		Fn: func(context.Context, hooks.Payload) (hooks.Outcome, error) {
			return out, err
		},
	}
}

func mustRunner(t *testing.T, hs ...hooks.Hook) *hooks.Runner {
	t.Helper()
	r, err := hooks.NewRunner(hs, nil)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	return r
}

// --- 配置校验 ---

func TestNewRunner_RejectsInvalidHooks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hook hooks.Hook
		want string
	}{
		{
			name: "unknown event",
			hook: &hooks.FuncHook{HookName: "h", OnEvents: []hooks.Event{"on_vibes"}},
			want: "on_vibes",
		},
		{
			name: "no events",
			hook: &hooks.FuncHook{HookName: "h"},
			want: "no events",
		},
		{
			name: "empty name",
			hook: &hooks.FuncHook{OnEvents: []hooks.Event{hooks.EventPreToolUse}},
			want: "name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := hooks.NewRunner([]hooks.Hook{tc.hook}, nil)
			if err == nil {
				t.Fatalf("NewRunner() succeeded, want error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestNewRunner_RejectsNilHook(t *testing.T) {
	t.Parallel()

	if _, err := hooks.NewRunner([]hooks.Hook{nil}, nil); err == nil {
		t.Fatal("NewRunner() accepted a nil hook")
	}
}

func TestRunner_HasOnlyReportsRegisteredEvents(t *testing.T) {
	t.Parallel()

	r := mustRunner(t, fn("h", hooks.EventPreToolUse, hooks.Outcome{}, nil))

	if !r.Has(hooks.EventPreToolUse) {
		t.Error("Has(pre_tool_use) = false")
	}
	if r.Has(hooks.EventPostToolUse) {
		t.Error("Has(post_tool_use) = true with no such hook")
	}
}

// --- 分发语义 ---

func TestRunner_FirstDenyShortCircuits(t *testing.T) {
	t.Parallel()

	var secondRan bool
	second := &hooks.FuncHook{
		HookName: "second",
		OnEvents: []hooks.Event{hooks.EventPreToolUse},
		Fn: func(context.Context, hooks.Payload) (hooks.Outcome, error) {
			secondRan = true
			return hooks.Outcome{}, nil
		},
	}

	r := mustRunner(t,
		fn("first", hooks.EventPreToolUse, hooks.Outcome{Deny: true, Message: "nope"}, nil),
		second,
	)

	res := r.Run(context.Background(), hooks.Payload{Event: hooks.EventPreToolUse, ToolName: "bash"})

	if !res.Deny {
		t.Fatal("Deny = false, want true")
	}
	if res.DeniedBy != "first" {
		t.Errorf("DeniedBy = %q, want first", res.DeniedBy)
	}
	if res.Message != "nope" {
		t.Errorf("Message = %q", res.Message)
	}
	if secondRan {
		t.Error("a later hook ran after a denial")
	}
}

// 一个写坏的钩子脚本不该瘫掉整个 agent：hook 是治理增强而非授权源。
func TestRunner_HookFailureDoesNotBlockAndIsReported(t *testing.T) {
	t.Parallel()

	var reported []string
	r, err := hooks.NewRunner([]hooks.Hook{
		fn("broken", hooks.EventPreToolUse, hooks.Outcome{}, errors.New("script crashed")),
		fn("healthy", hooks.EventPreToolUse, hooks.Outcome{Message: "ok"}, nil),
	}, func(name string, err error) {
		reported = append(reported, name+": "+err.Error())
	})
	if err != nil {
		t.Fatal(err)
	}

	res := r.Run(context.Background(), hooks.Payload{Event: hooks.EventPreToolUse, ToolName: "bash"})

	if res.Deny {
		t.Fatal("a hook failure must not deny the call")
	}
	if res.Message != "ok" {
		t.Errorf("Message = %q; the healthy hook must still run", res.Message)
	}
	if len(reported) != 1 || !strings.Contains(reported[0], "script crashed") {
		t.Errorf("onError reported %v, want the failure", reported)
	}
}

// 入参改写必须累积传递，否则第二个 hook 审的是旧值。
func TestRunner_ArgRewritesAccumulate(t *testing.T) {
	t.Parallel()

	var seenBySecond string
	second := &hooks.FuncHook{
		HookName: "second",
		OnEvents: []hooks.Event{hooks.EventPreToolUse},
		Fn: func(_ context.Context, p hooks.Payload) (hooks.Outcome, error) {
			seenBySecond = string(p.ToolArgs)
			return hooks.Outcome{UpdatedArgs: json.RawMessage(`{"n":2}`)}, nil
		},
	}

	r := mustRunner(t,
		fn("first", hooks.EventPreToolUse, hooks.Outcome{UpdatedArgs: json.RawMessage(`{"n":1}`)}, nil),
		second,
	)

	res := r.Run(context.Background(), hooks.Payload{
		Event: hooks.EventPreToolUse, ToolName: "bash", ToolArgs: json.RawMessage(`{"n":0}`),
	})

	if seenBySecond != `{"n":1}` {
		t.Errorf("second hook saw args %q, want the first hook's rewrite", seenBySecond)
	}
	if string(res.UpdatedArgs) != `{"n":2}` {
		t.Errorf("UpdatedArgs = %q, want the last rewrite", res.UpdatedArgs)
	}
}

func TestRunner_MessagesAccumulate(t *testing.T) {
	t.Parallel()

	r := mustRunner(t,
		fn("a", hooks.EventPostToolUse, hooks.Outcome{Message: "first note"}, nil),
		fn("b", hooks.EventPostToolUse, hooks.Outcome{Message: "second note"}, nil),
	)

	res := r.Run(context.Background(), hooks.Payload{Event: hooks.EventPostToolUse, ToolName: "ls"})

	for _, want := range []string{"first note", "second note"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("Message = %q, want it to contain %q", res.Message, want)
		}
	}
}

func TestRunner_MatcherFiltersByToolName(t *testing.T) {
	t.Parallel()

	h := &hooks.FuncHook{
		HookName: "bash-only",
		OnEvents: []hooks.Event{hooks.EventPreToolUse},
		Matcher:  regexp.MustCompile(`^bash$`),
		Fn: func(context.Context, hooks.Payload) (hooks.Outcome, error) {
			return hooks.Outcome{Deny: true, Message: "no bash"}, nil
		},
	}
	r := mustRunner(t, h)

	if res := r.Run(context.Background(), hooks.Payload{Event: hooks.EventPreToolUse, ToolName: "bash"}); !res.Deny {
		t.Error("the matcher should have matched bash")
	}
	if res := r.Run(context.Background(), hooks.Payload{Event: hooks.EventPreToolUse, ToolName: "ls"}); res.Deny {
		t.Error("the matcher should not have matched ls")
	}
}

func TestRunner_OnlyRunsMatchingEvent(t *testing.T) {
	t.Parallel()

	r := mustRunner(t, fn("pre", hooks.EventPreToolUse, hooks.Outcome{Deny: true}, nil))

	if res := r.Run(context.Background(), hooks.Payload{Event: hooks.EventPostToolUse, ToolName: "ls"}); res.Deny {
		t.Fatal("a pre_tool_use hook ran for post_tool_use")
	}
}

func TestRunner_RespectsContextCancellation(t *testing.T) {
	t.Parallel()

	r := mustRunner(t, fn("h", hooks.EventPreToolUse, hooks.Outcome{Deny: true}, nil))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if res := r.Run(ctx, hooks.Payload{Event: hooks.EventPreToolUse, ToolName: "ls"}); res.Deny {
		t.Fatal("hooks ran despite a cancelled context")
	}
}

func TestRunner_OneHookCanServeMultipleEvents(t *testing.T) {
	t.Parallel()

	h := &hooks.FuncHook{
		HookName: "both",
		OnEvents: []hooks.Event{hooks.EventPreToolUse, hooks.EventPostToolUse},
		Fn: func(context.Context, hooks.Payload) (hooks.Outcome, error) {
			return hooks.Outcome{Message: "seen"}, nil
		},
	}
	r := mustRunner(t, h)

	for _, e := range []hooks.Event{hooks.EventPreToolUse, hooks.EventPostToolUse} {
		if res := r.Run(context.Background(), hooks.Payload{Event: e, ToolName: "ls"}); res.Message != "seen" {
			t.Errorf("event %s: Message = %q", e, res.Message)
		}
	}
}

// --- 外部子进程 hook ---

func TestCommandHook_ExitZeroAllowsAndStdoutIsFeedback(t *testing.T) {
	t.Parallel()
	requireShell(t)

	h := shellHook(t, "allow", `echo "looks fine"`)
	out, err := h.Run(context.Background(), hooks.Payload{Event: hooks.EventPreToolUse, ToolName: "ls"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.Deny {
		t.Fatal("exit 0 must allow")
	}
	if out.Message != "looks fine" {
		t.Fatalf("Message = %q", out.Message)
	}
}

// 专用退出码而不是"任何非零"：脚本崩溃与深思熟虑的拒绝是两回事。
func TestCommandHook_ExitTwoDeniesWithStderrReason(t *testing.T) {
	t.Parallel()
	requireShell(t)

	h := shellHook(t, "deny", `echo "dangerous command" >&2; exit 2`)
	out, err := h.Run(context.Background(), hooks.Payload{Event: hooks.EventPreToolUse, ToolName: "bash"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !out.Deny {
		t.Fatal("exit 2 must deny")
	}
	if out.Message != "dangerous command" {
		t.Fatalf("Message = %q, want the stderr reason", out.Message)
	}
}

func TestCommandHook_OtherExitCodesAreFailuresNotDenials(t *testing.T) {
	t.Parallel()
	requireShell(t)

	h := shellHook(t, "crash", `echo "boom" >&2; exit 127`)
	out, err := h.Run(context.Background(), hooks.Payload{Event: hooks.EventPreToolUse, ToolName: "bash"})

	if err == nil {
		t.Fatal("a non-deny non-zero exit must be reported as a failure, not a denial")
	}
	if out.Deny {
		t.Fatal("a crashed hook must not deny the call")
	}
	if !strings.Contains(err.Error(), "127") {
		t.Errorf("error = %q, want it to carry the exit code", err)
	}
}

func TestCommandHook_ReceivesPayloadOnStdin(t *testing.T) {
	t.Parallel()
	requireShell(t)

	// 把 stdin 原样回显，便于断言 payload 内容
	h := shellHook(t, "echo-stdin", `cat`)
	out, err := h.Run(context.Background(), hooks.Payload{
		Event:    hooks.EventPreToolUse,
		ToolName: "bash",
		ToolArgs: json.RawMessage(`{"command":"rm -rf /"}`),
		ThreadID: "t-42",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	for _, want := range []string{"pre_tool_use", "bash", "rm -rf /", "t-42"} {
		if !strings.Contains(out.Message, want) {
			t.Errorf("stdin payload missing %q; got %q", want, out.Message)
		}
	}
}

func TestCommandHook_StructuredStdoutRewritesArgs(t *testing.T) {
	t.Parallel()
	requireShell(t)

	h := shellHook(t, "rewrite", `echo '{"updated_args":{"command":"ls -la"},"message":"sanitised"}'`)
	out, err := h.Run(context.Background(), hooks.Payload{Event: hooks.EventPreToolUse, ToolName: "bash"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if string(out.UpdatedArgs) != `{"command":"ls -la"}` {
		t.Fatalf("UpdatedArgs = %q", out.UpdatedArgs)
	}
	if out.Message != "sanitised" {
		t.Fatalf("Message = %q", out.Message)
	}
}

// 绝大多数 hook 就是 echo 一句话，不能强制它们输出 JSON。
func TestCommandHook_NonJSONStdoutIsPlainFeedback(t *testing.T) {
	t.Parallel()
	requireShell(t)

	h := shellHook(t, "plain", `echo "just a note"`)
	out, err := h.Run(context.Background(), hooks.Payload{Event: hooks.EventPreToolUse, ToolName: "ls"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Message != "just a note" {
		t.Fatalf("Message = %q", out.Message)
	}
}

func TestCommandHook_TimesOut(t *testing.T) {
	t.Parallel()
	requireShell(t)

	h := shellHook(t, "slow", `sleep 5`)
	h.Timeout = 50 * time.Millisecond

	start := time.Now()
	_, err := h.Run(context.Background(), hooks.Payload{Event: hooks.EventPreToolUse, ToolName: "ls"})

	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Run() error = %v, want a timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Run() took %v; the timeout did not fire", elapsed)
	}
}

func TestCommandHook_MissingCommandIsAnError(t *testing.T) {
	t.Parallel()

	h := &hooks.CommandHook{HookName: "empty", OnEvents: []hooks.Event{hooks.EventPreToolUse}}
	if _, err := h.Run(context.Background(), hooks.Payload{}); err == nil {
		t.Fatal("Run() with no command succeeded")
	}
}

func TestFuncHook_MissingFunctionIsAnError(t *testing.T) {
	t.Parallel()

	h := &hooks.FuncHook{HookName: "empty", OnEvents: []hooks.Event{hooks.EventPreToolUse}}
	if _, err := h.Run(context.Background(), hooks.Payload{}); err == nil {
		t.Fatal("Run() with no function succeeded")
	}
}

// --- helpers ---

func requireShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-based hook tests require a POSIX shell")
	}
}

// shellHook 把脚本写到临时文件并返回执行它的 CommandHook。
func shellHook(t *testing.T, name, script string) *hooks.CommandHook {
	t.Helper()

	path := filepath.Join(t.TempDir(), name+".sh")
	body := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("writing hook script: %v", err)
	}

	return &hooks.CommandHook{
		HookName: name,
		OnEvents: []hooks.Event{hooks.EventPreToolUse, hooks.EventPostToolUse},
		Command:  "/bin/sh",
		Args:     []string{path},
	}
}
