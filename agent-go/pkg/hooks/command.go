package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// exitCodeDeny 是外部 hook 表示"拒绝"的退出码。
//
// 用一个专门的码而不是"任何非零"：脚本崩溃（127 command not found、
// 137 OOM kill）与深思熟虑的拒绝是两回事，混为一谈会让一次拼写错误
// 变成一次安全拦截。
const exitCodeDeny = 2

// waitDelay 是取消后等待子进程收口的上限。
//
// 取太小会截断慢收尾但正常的 hook 输出，取太大等于超时不生效。
const waitDelay = time.Second

// CommandHook 是外部子进程 hook。
//
// 协议：Payload 的 JSON 从 stdin 传入；退出码 0 放行、2 拒绝、其余视为执行失败；
// stdout 作为反馈消息，拒绝时 stderr 作为理由。
// stdout 若是形如 {"updated_args":{...}} 的 JSON，则用于改写工具入参。
type CommandHook struct {
	HookName string
	OnEvents []Event

	// Command 与 Args 是要执行的程序。
	Command string
	Args    []string

	// Matcher 是工具名的正则。为 nil 时匹配全部工具。
	Matcher *regexp.Regexp

	// Timeout 是执行上限。<= 0 时用 30 秒。
	Timeout time.Duration
}

// Name 实现 Hook。
func (c *CommandHook) Name() string { return c.HookName }

// Events 实现 Hook。
func (c *CommandHook) Events() []Event { return c.OnEvents }

// Matches 实现 Hook。
func (c *CommandHook) Matches(toolName string) bool {
	if c.Matcher == nil {
		return true
	}
	return c.Matcher.MatchString(toolName)
}

// commandOutput 是 stdout 上可选的结构化输出。
type commandOutput struct {
	UpdatedArgs json.RawMessage `json:"updated_args,omitempty"`
	Message     string          `json:"message,omitempty"`
}

// Run 实现 Hook。
func (c *CommandHook) Run(ctx context.Context, p Payload) (Outcome, error) {
	if c.Command == "" {
		return Outcome{}, fmt.Errorf("hooks: %q has no command", c.HookName)
	}

	payload, err := json.Marshal(p)
	if err != nil {
		return Outcome{}, fmt.Errorf("hooks: marshalling payload: %w", err)
	}

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, c.Command, c.Args...)
	cmd.Stdin = bytes.NewReader(payload)

	// 超时杀掉的是 hook 进程本身，但它 fork 出的子进程会继承 stdout 管道的写端，
	// 而 cmd.Run 要等管道关闭才返回 —— 于是超时形同不存在。
	// WaitDelay 让 Go 在取消后至多再等这么久，然后强制关闭管道收口。
	cmd.WaitDelay = waitDelay

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	if runCtx.Err() != nil {
		return Outcome{}, fmt.Errorf("hooks: %q timed out after %s", c.HookName, timeout)
	}

	switch runErr {
	case nil:
		return parseAllow(stdout.String()), nil

	default:
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return Outcome{}, fmt.Errorf("hooks: running %q: %w", c.HookName, runErr)
		}
		if exitErr.ExitCode() == exitCodeDeny {
			reason := strings.TrimSpace(stderr.String())
			if reason == "" {
				reason = strings.TrimSpace(stdout.String())
			}
			if reason == "" {
				reason = fmt.Sprintf("denied by hook %q", c.HookName)
			}
			return Outcome{Deny: true, Message: reason}, nil
		}
		// 其余退出码是脚本自身出错，不是拒绝。
		return Outcome{}, fmt.Errorf("hooks: %q exited with %d: %s",
			c.HookName, exitErr.ExitCode(), strings.TrimSpace(stderr.String()))
	}
}

// parseAllow 解析放行路径上的 stdout。
//
// 不是我们的结构化输出时，整段作为反馈消息 —— 绝大多数 hook 就是 echo 一句话，
// 强制它们输出 JSON 只会提高门槛。
//
// 判定"是结构化输出"必须要求至少命中一个已知键：json.Unmarshal 到结构体会
// 忽略未知字段，所以任何合法 JSON 都能"解析成功"却得到全空结果 ——
// 那样一个输出 JSON 报告的 linter hook，它的整段输出会被静默吞掉。
func parseAllow(stdout string) Outcome {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return Outcome{}
	}

	if strings.HasPrefix(trimmed, "{") {
		var out commandOutput
		if err := json.Unmarshal([]byte(trimmed), &out); err == nil {
			if out.UpdatedArgs != nil || out.Message != "" {
				return Outcome{Message: out.Message, UpdatedArgs: out.UpdatedArgs}
			}
		}
	}
	return Outcome{Message: trimmed}
}

// FuncHook 是进程内 hook，由编译期注册表按名字提供。
//
// Go 没有运行时类加载，Python 的 dotted-path 在此无法平移：
// 进程内 hook 必须在编译期注册（设计文档 §16）。
type FuncHook struct {
	HookName string
	OnEvents []Event
	Matcher  *regexp.Regexp
	Fn       func(ctx context.Context, p Payload) (Outcome, error)
}

// Name 实现 Hook。
func (f *FuncHook) Name() string { return f.HookName }

// Events 实现 Hook。
func (f *FuncHook) Events() []Event { return f.OnEvents }

// Matches 实现 Hook。
func (f *FuncHook) Matches(toolName string) bool {
	if f.Matcher == nil {
		return true
	}
	return f.Matcher.MatchString(toolName)
}

// Run 实现 Hook。
func (f *FuncHook) Run(ctx context.Context, p Payload) (Outcome, error) {
	if f.Fn == nil {
		return Outcome{}, fmt.Errorf("hooks: %q has no function", f.HookName)
	}
	return f.Fn(ctx, p)
}
