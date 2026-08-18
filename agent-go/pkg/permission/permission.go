// Package permission owns the independent sandbox and approval policies used
// by the tool admission gate. Presets are product-facing bundles; execution
// always evaluates the two mechanism policies separately.
package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

type SandboxMode string

const (
	SandboxReadOnly         SandboxMode = "read-only"
	SandboxWorkspaceWrite   SandboxMode = "workspace-write"
	SandboxDangerFullAccess SandboxMode = "danger-full-access"
)

func (m SandboxMode) Valid() bool { return slices.Contains(SandboxModes(), m) }

func SandboxModes() []SandboxMode {
	return []SandboxMode{SandboxReadOnly, SandboxWorkspaceWrite, SandboxDangerFullAccess}
}

func ParseSandboxMode(value string) (SandboxMode, error) {
	mode := SandboxMode(strings.TrimSpace(strings.ToLower(value)))
	if !mode.Valid() {
		return "", fmt.Errorf("permission: unknown sandbox mode %q; valid modes: %v", value, SandboxModes())
	}
	return mode, nil
}

type ApprovalPolicy string

const (
	ApprovalAsk   ApprovalPolicy = "ask"
	ApprovalNever ApprovalPolicy = "never"
)

func (p ApprovalPolicy) Valid() bool { return p == ApprovalAsk || p == ApprovalNever }

type Preset string

const (
	PresetReadOnly         Preset = "read-only"
	PresetWorkspaceWrite   Preset = "workspace-write"
	PresetDangerFullAccess Preset = "danger-full-access"
	PresetCustom           Preset = "custom"
)

type PresetSpec struct {
	Sandbox  SandboxMode    `yaml:"sandbox" json:"sandbox"`
	Approval ApprovalPolicy `yaml:"approval" json:"approval"`
}

func DefaultPresets() map[Preset]PresetSpec {
	return map[Preset]PresetSpec{
		PresetReadOnly:         {Sandbox: SandboxReadOnly, Approval: ApprovalNever},
		PresetWorkspaceWrite:   {Sandbox: SandboxWorkspaceWrite, Approval: ApprovalAsk},
		PresetDangerFullAccess: {Sandbox: SandboxDangerFullAccess, Approval: ApprovalNever},
	}
}

type Decision struct {
	Allowed      bool
	Reason       string
	Sandbox      SandboxMode
	Required     SandboxMode
	ApprovalUsed bool
}

type Prompter interface {
	Confirm(ctx context.Context, req ConfirmRequest) (bool, error)
}

type ConfirmRequest struct {
	ToolCallID string
	ToolName   string
	Args       json.RawMessage
	Reason     string
}

type Config struct {
	Preset        Preset
	Presets       map[Preset]PresetSpec
	Prompter      Prompter
	PromptTimeout time.Duration
}

type Policy struct {
	preset   Preset
	sandbox  SandboxMode
	approval ApprovalPolicy
	prompter Prompter
	timeout  time.Duration
}

func NewPolicy(cfg Config) (*Policy, error) {
	presets := cfg.Presets
	if presets == nil {
		presets = DefaultPresets()
	}
	if _, reserved := presets[PresetCustom]; reserved {
		return nil, fmt.Errorf("permission: preset %q is reserved", PresetCustom)
	}
	for name, spec := range presets {
		if strings.TrimSpace(string(name)) == "" {
			return nil, fmt.Errorf("permission: preset name is required")
		}
		if !spec.Sandbox.Valid() {
			return nil, fmt.Errorf("permission: preset %q has invalid sandbox mode %q", name, spec.Sandbox)
		}
		if !spec.Approval.Valid() {
			return nil, fmt.Errorf("permission: preset %q has invalid approval policy %q", name, spec.Approval)
		}
	}
	preset := cfg.Preset
	if preset == "" {
		preset = PresetWorkspaceWrite
	}
	spec, ok := presets[preset]
	if !ok {
		return nil, fmt.Errorf("permission: unknown preset %q", preset)
	}
	timeout := cfg.PromptTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return &Policy{
		preset: preset, sandbox: spec.Sandbox, approval: spec.Approval,
		prompter: cfg.Prompter, timeout: timeout,
	}, nil
}

func (p *Policy) Preset() Preset                 { return p.preset }
func (p *Policy) SandboxMode() SandboxMode       { return p.sandbox }
func (p *Policy) ApprovalPolicy() ApprovalPolicy { return p.approval }

// ForDelegation pins approval to never without changing the inherited sandbox
// ceiling. Only trusted child-run assembly may select this derived policy.
func (p *Policy) ForDelegation() *Policy {
	if p == nil {
		return nil
	}
	derived := *p
	derived.approval = ApprovalNever
	return &derived
}

func (p *Policy) AuthorizeCall(ctx context.Context, definition tool.Definition, call tool.Call) Decision {
	required, err := requiredSandboxMode(definition)
	if err != nil {
		return Decision{Reason: err.Error(), Sandbox: p.sandbox}
	}
	decision := Decision{Sandbox: p.sandbox, Required: required}
	if sandboxRank(p.sandbox) >= sandboxRank(required) {
		decision.Allowed = true
		return decision
	}
	if p.approval == ApprovalNever {
		decision.Reason = fmt.Sprintf("%s requires sandbox mode %s but preset %s provides %s and approval policy is never", definition.Name, required, p.preset, p.sandbox)
		return decision
	}
	if p.approval != ApprovalAsk {
		decision.Reason = fmt.Sprintf("permission: unhandled approval policy %q", p.approval)
		return decision
	}
	decision.ApprovalUsed = true
	if p.prompter == nil {
		decision.Reason = fmt.Sprintf("%s requires sandbox escalation from %s to %s but no approval service is configured", definition.Name, p.sandbox, required)
		return decision
	}

	askCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	approved, err := p.prompter.Confirm(askCtx, ConfirmRequest{
		ToolCallID: call.ID,
		ToolName:   definition.Name,
		Args:       call.Args,
		Reason:     fmt.Sprintf("%s requires sandbox escalation from %s to %s", definition.Name, p.sandbox, required),
	})
	if err != nil {
		decision.Reason = fmt.Sprintf("%s escalation was not approved: %v", definition.Name, err)
		return decision
	}
	if !approved {
		decision.Reason = fmt.Sprintf("%s sandbox escalation was declined", definition.Name)
		return decision
	}
	decision.Allowed = true
	decision.Sandbox = required
	return decision
}

func (p *Policy) AllowedTools(registry *tool.Registry, candidates []string) []string {
	out := make([]string, 0, len(candidates))
	for _, name := range candidates {
		definition, err := registry.Get(name)
		if err != nil {
			continue
		}
		required, err := requiredSandboxMode(definition)
		if err != nil {
			continue
		}
		if sandboxRank(p.sandbox) >= sandboxRank(required) || p.approval == ApprovalAsk {
			out = append(out, name)
		}
	}
	return out
}

func requiredSandboxMode(definition tool.Definition) (SandboxMode, error) {
	if raw := strings.TrimSpace(definition.Metadata.RequiredSandboxMode); raw != "" {
		mode, err := ParseSandboxMode(raw)
		if err != nil {
			return "", fmt.Errorf("%s declares invalid required sandbox mode: %w", definition.Name, err)
		}
		return mode, nil
	}
	if definition.Metadata.IsReadOnly {
		return SandboxReadOnly, nil
	}
	if definition.Metadata.RequiresSandbox || definition.Metadata.IsAgentState {
		return SandboxWorkspaceWrite, nil
	}
	return SandboxDangerFullAccess, nil
}

func sandboxRank(mode SandboxMode) int {
	switch mode {
	case SandboxReadOnly:
		return 0
	case SandboxWorkspaceWrite:
		return 1
	case SandboxDangerFullAccess:
		return 2
	default:
		return -1
	}
}
