package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// NameSandboxAudit 是命令审计中间件的名字。
const NameSandboxAudit = "sandboxAudit"

// AuditRule 是一条危险命令规则。
type AuditRule struct {
	// ID 出现在拒绝理由里，便于用户定位是哪条规则拦的。
	ID string

	// Pattern 匹配命令行。
	Pattern *regexp.Regexp

	// Reason 是给模型看的说明。
	Reason string
}

// DefaultAuditRules 返回内置的危险命令规则。
//
// 每条都锚定到词边界或行尾，因为"能拦住 rm -rf /"和"不误伤 rm -rf /tmp/x"
// 一样重要 —— 误伤会让用户直接关掉审计，那就等于没有审计。
func DefaultAuditRules() []AuditRule {
	return []AuditRule{
		{
			ID:      "rm-root",
			Pattern: regexp.MustCompile(`\brm\s+(-[a-zA-Z]+\s+)*/\s*($|[;&|])`),
			Reason:  "recursive delete of the filesystem root",
		},
		{
			ID:      "fork-bomb",
			Pattern: regexp.MustCompile(`:\s*\(\s*\)\s*\{.*\|.*&.*\}\s*;?\s*:`),
			Reason:  "fork bomb",
		},
		{
			ID:      "pipe-to-shell",
			Pattern: regexp.MustCompile(`\b(curl|wget)\b[^|;&]*\|\s*(sudo\s+)?(ba)?sh\b`),
			Reason:  "downloading and executing a remote script in one step",
		},
		{
			ID:      "reverse-shell",
			Pattern: regexp.MustCompile(`/dev/(tcp|udp)/`),
			Reason:  "reverse shell",
		},
		{
			ID: "disk-write",
			// /dev/ 前不能加 \b：空格与斜杠都是非词字符，两者之间没有词边界，
			// 于是 `mkfs.ext4 /dev/nvme0n1` 会漏过，而 `dd ... of=/dev/sda`
			// 只是碰巧从 of= 那一支匹配上 —— 这种"一半生效"的规则最危险。
			Pattern: regexp.MustCompile(`\b(dd|mkfs(\.\w+)?)\b[^;&|]*/dev/(sd|nvme|disk|hd)`),
			Reason:  "writing directly to a block device",
		},
		{
			ID:      "credential-read",
			Pattern: regexp.MustCompile(`(\.ssh/id_[a-z0-9]+|\.aws/credentials|\.kube/config|\.netrc)\b`),
			Reason:  "reading private credentials",
		},
		{
			ID:      "chmod-root",
			Pattern: regexp.MustCompile(`\bchmod\s+(-[a-zA-Z]+\s+)*777\s+/\s*($|[;&|])`),
			Reason:  "making the filesystem root world-writable",
		},
		{
			ID:      "host-control",
			Pattern: regexp.MustCompile(`\b(shutdown|reboot|halt|poweroff)\b`),
			Reason:  "shutting down or rebooting the host",
		},
	}
}

// SandboxAudit 审计命令类工具的入参。
//
// 与权限的分工：权限判"这个工具能不能用"，审计判"这次调用的内容是否危险"。
// 混在一处会让权限规则不得不理解每个工具的参数结构。
type SandboxAudit struct {
	rules []AuditRule

	// tools 是要审计的工具名集合。默认只有 bash。
	tools map[string]struct{}
}

// AuditOptions 配置命令审计。
type AuditOptions struct {
	// Rules 覆盖默认规则。为 nil 时用 DefaultAuditRules。
	Rules []AuditRule

	// Tools 是要审计的工具名。为空时只审计 bash。
	Tools []string
}

// NewSandboxAudit 返回命令审计中间件。
func NewSandboxAudit(opts AuditOptions) *SandboxAudit {
	rules := opts.Rules
	if rules == nil {
		rules = DefaultAuditRules()
	}

	names := opts.Tools
	if len(names) == 0 {
		names = []string{"bash"}
	}
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}

	return &SandboxAudit{rules: rules, tools: set}
}

// Name 实现 middleware.Middleware。
func (s *SandboxAudit) Name() string { return NameSandboxAudit }

// Grade 实现 middleware.Graded：审计失败必须中断。
func (s *SandboxAudit) Grade() middleware.Grade { return middleware.GradeAbort }

// BeforeTool 实现 middleware.BeforeTool。
func (s *SandboxAudit) BeforeTool(_ context.Context, st *middleware.State) (tool.Decision, error) {
	if st.ToolCall == nil {
		return tool.Decision{}, nil
	}
	if _, audited := s.tools[st.ToolCall.Name]; !audited {
		return tool.Decision{}, nil
	}

	line := commandLineOf(st.ToolCall.Args)
	if line == "" {
		return tool.Decision{}, nil
	}

	for _, r := range s.rules {
		if r.Pattern.MatchString(line) {
			return tool.Decision{
				Deny: true,
				Reason: fmt.Sprintf(
					"blocked by command audit rule %q (%s); if this is genuinely required, ask the user to run it directly",
					r.ID, r.Reason),
			}, nil
		}
	}
	return tool.Decision{}, nil
}

// commandLineOf 从工具入参里取命令行。
//
// 只认 command 字段：猜测式地把所有字符串字段拼起来审计，会让
// 一个恰好含 "reboot" 的文件内容参数被误拦。
func commandLineOf(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ""
	}
	return parsed.Command
}
