package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// Bash 返回命令执行工具。
//
// 它不做命令内容的安全判定 —— 那归 SandboxAudit 中间件与 pkg/permission。
// 本工具只负责在沙箱里执行并如实回报结果（设计文档 §2 依赖纪律）。
func Bash() tool.Definition {
	return tool.Definition{
		Name:        "bash",
		Group:       GroupBash,
		Description: "Run a shell command inside the workspace sandbox.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "command":     {"type": "string", "description": "Shell command line to run."},
    "workdir":     {"type": "string", "description": "Working directory relative to the workspace root."},
    "timeout_sec": {"type": "integer", "description": "Timeout in seconds. Defaults to the sandbox default."}
  },
  "required": ["command"]
}`),
		Metadata: tool.Metadata{RequiresSandbox: true},
		Handler:  handleBash,
	}
}

type bashArgs struct {
	Command    string `json:"command"`
	WorkDir    string `json:"workdir"`
	TimeoutSec int    `json:"timeout_sec"`
}

func handleBash(ctx context.Context, call tool.Call) (*tool.Result, error) {
	var args bashArgs
	if err := decode(call.Args, &args); err != nil {
		return errResult(err), nil
	}
	if strings.TrimSpace(args.Command) == "" {
		return errResult(fmt.Errorf("bash: command is required")), nil
	}

	h, err := sandbox.HandleFromContext(ctx)
	if err != nil {
		return nil, err
	}

	cmd := sandbox.Command{Line: args.Command, WorkDir: args.WorkDir}
	if args.TimeoutSec > 0 {
		cmd.Timeout = time.Duration(args.TimeoutSec) * time.Second
	}

	res, err := h.Exec(ctx, cmd)
	if err != nil {
		// 路径越界这类是模型能纠正的，回灌说明。
		return errResult(err), nil
	}

	return &tool.Result{Content: formatExec(res), IsError: res.TimedOut || res.ExitCode != 0}, nil
}

// formatExec 把执行结果渲染成模型友好的文本。
//
// 退出码与 stderr 必须始终可见：只回 stdout 会让模型把失败当成功，
// 然后基于空输出继续往下做。
func formatExec(res *sandbox.ExecResult) string {
	var sb strings.Builder

	if res.TimedOut {
		sb.WriteString("Command timed out and was killed.\n")
	} else {
		sb.WriteString(fmt.Sprintf("Exit code: %d\n", res.ExitCode))
	}

	if out := strings.TrimRight(res.Stdout, "\n"); out != "" {
		sb.WriteString("\nstdout:\n")
		sb.WriteString(out)
		sb.WriteString("\n")
	}
	if errOut := strings.TrimRight(res.Stderr, "\n"); errOut != "" {
		sb.WriteString("\nstderr:\n")
		sb.WriteString(errOut)
		sb.WriteString("\n")
	}
	if res.Stdout == "" && res.Stderr == "" && !res.TimedOut {
		sb.WriteString("\n(no output)\n")
	}
	if res.Truncated {
		sb.WriteString("\n(output was truncated)\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}

// All 返回全部内置工具。
//
// 装配方按 config.yaml 的 tool_groups 决定注册哪些组：
// sandbox.enabled 为 false 时 file/bash 组整个不绑定，
// 而不是注册了再靠权限去拦 —— 少一层可以出错的地方。
func All() []tool.Definition {
	return []tool.Definition{
		LS(), Glob(), Grep(), ReadFile(), WriteFile(), StrReplace(), Bash(), ViewImage(), PresentFiles(),
	}
}
