// Package permission 实现 5 级权限模型。
//
// 判定是 fail-closed 的：任何内部错误都返回拒绝。一个"判不出来所以放行"的
// 权限系统比没有权限系统更危险，因为它看起来像在限制（设计文档 §8.1）。
package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// Mode 是权限级别。
type Mode string

const (
	// ModeReadOnly 只允许只读工具。
	ModeReadOnly Mode = "read_only"

	// ModeWorkspaceWrite 允许只读 + 工作区内写。
	ModeWorkspaceWrite Mode = "workspace_write"

	// ModePrompt 高风险工具需要用户确认。
	ModePrompt Mode = "prompt"

	// ModeAllow 全部放行。默认级别。
	ModeAllow Mode = "allow"

	// ModeDangerFullAccess 完全不受限，连沙箱要求也不检查。
	ModeDangerFullAccess Mode = "danger_full_access"
)

// Valid 报告 m 是否是已知级别。
func (m Mode) Valid() bool {
	switch m {
	case ModeReadOnly, ModeWorkspaceWrite, ModePrompt, ModeAllow, ModeDangerFullAccess:
		return true
	default:
		return false
	}
}

// Modes 返回全部合法级别，供错误信息与配置校验使用。
func Modes() []Mode {
	return []Mode{ModeReadOnly, ModeWorkspaceWrite, ModePrompt, ModeAllow, ModeDangerFullAccess}
}

// Decision 是一次授权判定的结果。
type Decision struct {
	Allowed bool
	Reason  string
}

// Prompter 向用户征求确认。ModePrompt 下使用。
//
// 返回 error 一律视为拒绝：征求确认失败时不能替用户点同意。
type Prompter interface {
	Confirm(ctx context.Context, req ConfirmRequest) (bool, error)
}

// ConfirmRequest 是一次确认请求。
type ConfirmRequest struct {
	ToolName string
	Args     json.RawMessage
	Reason   string
}

// Config 配置权限策略。
type Config struct {
	// Mode 是默认级别。空时用 ModeAllow。
	Mode Mode

	// ToolOverrides 按工具名覆盖级别，例如 {"bash": "danger_full_access"}。
	ToolOverrides map[string]Mode

	// Prompter 在 ModePrompt 下征求确认。为 nil 时 ModePrompt 等同于拒绝 ——
	// 没有人能回答的确认请求只能是拒绝。
	Prompter Prompter

	// PromptTimeout 是等待确认的上限。<= 0 时用 2 分钟。超时视为拒绝。
	PromptTimeout time.Duration
}

// Policy 是权限判定引擎。
type Policy struct {
	mode      Mode
	overrides map[string]Mode
	prompter  Prompter
	timeout   time.Duration
}

// NewPolicy 校验配置并返回判定引擎。
//
// 未知级别在这里就失败，不留到运行时：一个拼错的 mode 若被当成默认值处理，
// 就是静默降级为放行。
func NewPolicy(cfg Config) (*Policy, error) {
	mode := cfg.Mode
	if mode == "" {
		mode = ModeAllow
	}
	if !mode.Valid() {
		return nil, fmt.Errorf("permission: unknown mode %q; valid modes: %v", cfg.Mode, Modes())
	}

	overrides := make(map[string]Mode, len(cfg.ToolOverrides))
	for name, m := range cfg.ToolOverrides {
		if !m.Valid() {
			return nil, fmt.Errorf("permission: unknown mode %q for tool %q; valid modes: %v", m, name, Modes())
		}
		overrides[name] = m
	}

	timeout := cfg.PromptTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	return &Policy{mode: mode, overrides: overrides, prompter: cfg.Prompter, timeout: timeout}, nil
}

// Mode 返回某个工具生效的级别。
func (p *Policy) Mode(toolName string) Mode {
	if m, ok := p.overrides[toolName]; ok {
		return m
	}
	return p.mode
}

// Authorize 判定一次工具调用是否放行。
//
// 判定只看工具元数据与级别，不看参数内容 —— 参数级的审计（例如 bash 命令
// 是否危险）归 SandboxAudit 中间件，两者职责不同不该混在一处。
func (p *Policy) Authorize(ctx context.Context, d tool.Definition, args json.RawMessage) Decision {
	mode := p.Mode(d.Name)

	switch mode {
	case ModeDangerFullAccess, ModeAllow:
		return Decision{Allowed: true}

	case ModeReadOnly:
		if d.Metadata.IsReadOnly {
			return Decision{Allowed: true}
		}
		return Decision{Reason: fmt.Sprintf(
			"%s modifies state and the current permission mode is %s", d.Name, mode)}

	case ModeWorkspaceWrite:
		if d.Metadata.IsReadOnly || d.Metadata.RequiresSandbox {
			return Decision{Allowed: true}
		}
		return Decision{Reason: fmt.Sprintf(
			"%s writes outside the workspace and the current permission mode is %s", d.Name, mode)}

	case ModePrompt:
		return p.confirm(ctx, d, args)

	default:
		// 不可达：NewPolicy 已校验。真到了这里说明有人绕过构造器，
		// fail-closed 拒绝。
		return Decision{Reason: fmt.Sprintf("permission: unhandled mode %q", mode)}
	}
}

// confirm 在 ModePrompt 下征求用户确认。
func (p *Policy) confirm(ctx context.Context, d tool.Definition, args json.RawMessage) Decision {
	if p.prompter == nil {
		return Decision{Reason: fmt.Sprintf(
			"%s requires confirmation but no prompter is configured", d.Name)}
	}
	// 只读工具不打扰用户：prompt 模式的意图是拦住有副作用的操作。
	if d.Metadata.IsReadOnly {
		return Decision{Allowed: true}
	}

	askCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	ok, err := p.prompter.Confirm(askCtx, ConfirmRequest{
		ToolName: d.Name,
		Args:     args,
		Reason:   fmt.Sprintf("%s has side effects", d.Name),
	})
	switch {
	case err != nil:
		// 征求确认失败不能替用户点同意。
		return Decision{Reason: fmt.Sprintf("%s was not confirmed: %v", d.Name, err)}
	case !ok:
		return Decision{Reason: fmt.Sprintf("%s was declined by the user", d.Name)}
	default:
		return Decision{Allowed: true}
	}
}

// AllowedTools 从候选集里筛出该级别允许调用的工具名。
//
// 内核每轮用它算工具集：不允许调用的工具连 schema 都不投递，
// 省得模型反复尝试再反复被拒（设计文档 §7.3）。
func (p *Policy) AllowedTools(r *tool.Registry, candidates []string) []string {
	out := make([]string, 0, len(candidates))
	for _, name := range candidates {
		d, err := r.Get(name)
		if err != nil {
			continue // 未注册的名字不放进工具集
		}
		// 这里不做 ModePrompt 的确认：披露与调用是两件事，
		// prompt 模式下工具应当可见，只是调用时需要确认。
		if p.Mode(name) == ModePrompt || p.Authorize(context.Background(), d, nil).Allowed {
			out = append(out, name)
		}
	}
	return out
}

// ParseMode 把配置里的字符串解析为 Mode，未知值返回 error。
func ParseMode(s string) (Mode, error) {
	m := Mode(strings.TrimSpace(strings.ToLower(s)))
	if !m.Valid() {
		return "", fmt.Errorf("permission: unknown mode %q; valid modes: %v", s, Modes())
	}
	return m, nil
}
