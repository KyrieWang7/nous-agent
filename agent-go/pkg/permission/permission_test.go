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

func definition(name string, readOnly, sandboxed bool) tool.Definition {
	return tool.Definition{
		Name: name, Group: "test", Parameters: json.RawMessage(`{"type":"object"}`),
		Metadata: tool.Metadata{IsReadOnly: readOnly, RequiresSandbox: sandboxed},
		Handler:  func(context.Context, tool.Call) (*tool.Result, error) { return &tool.Result{}, nil },
	}
}

var (
	readTool     = definition("read_file", true, true)
	writeTool    = definition("write_file", false, true)
	externalTool = definition("http_post", false, false)
)

func TestPresetBundlesIndependentPolicies(t *testing.T) {
	tests := []struct {
		preset   permission.Preset
		sandbox  permission.SandboxMode
		approval permission.ApprovalPolicy
	}{
		{permission.PresetReadOnly, permission.SandboxReadOnly, permission.ApprovalNever},
		{permission.PresetWorkspaceWrite, permission.SandboxWorkspaceWrite, permission.ApprovalAsk},
		{permission.PresetDangerFullAccess, permission.SandboxDangerFullAccess, permission.ApprovalNever},
	}
	for _, tt := range tests {
		policy := mustPolicy(t, permission.Config{Preset: tt.preset})
		if policy.Preset() != tt.preset || policy.SandboxMode() != tt.sandbox || policy.ApprovalPolicy() != tt.approval {
			t.Fatalf("preset %s resolved to %s/%s", tt.preset, policy.SandboxMode(), policy.ApprovalPolicy())
		}
	}
}

func TestWorkspaceWriteOnlyAsksForEscalation(t *testing.T) {
	prompter := &fakePrompter{approve: true}
	policy := mustPolicy(t, permission.Config{Preset: permission.PresetWorkspaceWrite, Prompter: prompter})
	for _, direct := range []tool.Definition{readTool, writeTool} {
		decision := authorize(policy, direct, nil)
		if !decision.Allowed || decision.ApprovalUsed {
			t.Fatalf("%s decision = %#v", direct.Name, decision)
		}
	}
	decision := authorize(policy, externalTool, json.RawMessage(`{"target":"remote"}`))
	if !decision.Allowed || !decision.ApprovalUsed || prompter.calls != 1 {
		t.Fatalf("external decision = %#v, calls=%d", decision, prompter.calls)
	}
	if prompter.last.ToolCallID != "call-1" || !strings.Contains(prompter.last.Reason, "workspace-write to danger-full-access") {
		t.Fatalf("approval request = %#v", prompter.last)
	}
}

func TestApprovalNeverDeniesEscalation(t *testing.T) {
	policy := mustPolicy(t, permission.Config{Preset: permission.PresetReadOnly})
	decision := authorize(policy, writeTool, nil)
	if decision.Allowed || decision.ApprovalUsed || !strings.Contains(decision.Reason, "approval policy is never") {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestApprovalAskFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		prompter permission.Prompter
	}{
		{name: "missing"},
		{name: "declined", prompter: &fakePrompter{}},
		{name: "error", prompter: &fakePrompter{err: errors.New("ui disconnected")}},
		{name: "timeout", prompter: blockingPrompter{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := mustPolicy(t, permission.Config{
				Preset: permission.PresetWorkspaceWrite, Prompter: tt.prompter, PromptTimeout: 10 * time.Millisecond,
			})
			if decision := authorize(policy, externalTool, nil); decision.Allowed || decision.Reason == "" {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}
}

func TestAllowedToolsKeepsAskEscalationsVisible(t *testing.T) {
	registry := tool.NewRegistry()
	for _, item := range []tool.Definition{readTool, writeTool, externalTool} {
		if err := registry.Register(item); err != nil {
			t.Fatal(err)
		}
	}
	ask := mustPolicy(t, permission.Config{Preset: permission.PresetWorkspaceWrite})
	if got := ask.AllowedTools(registry, registry.Names()); len(got) != 3 {
		t.Fatalf("ask tools = %v", got)
	}
	never := mustPolicy(t, permission.Config{Preset: permission.PresetReadOnly})
	if got := never.AllowedTools(registry, registry.Names()); len(got) != 1 || got[0] != "read_file" {
		t.Fatalf("never tools = %v", got)
	}
}

func TestExplicitRequiredSandboxModeAndInvalidMetadata(t *testing.T) {
	item := definition("plugin", true, false)
	item.Metadata.RequiredSandboxMode = string(permission.SandboxDangerFullAccess)
	policy := mustPolicy(t, permission.Config{Preset: permission.PresetWorkspaceWrite, Prompter: &fakePrompter{approve: true}})
	if decision := authorize(policy, item, nil); !decision.Allowed || !decision.ApprovalUsed {
		t.Fatalf("decision = %#v", decision)
	}
	item.Metadata.RequiredSandboxMode = "root-everything"
	if decision := authorize(policy, item, nil); decision.Allowed || !strings.Contains(decision.Reason, "invalid required sandbox mode") {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestPolicyRejectsInvalidPresetTables(t *testing.T) {
	tests := []permission.Config{
		{Preset: "missing"},
		{Preset: permission.PresetCustom, Presets: map[permission.Preset]permission.PresetSpec{permission.PresetCustom: {Sandbox: permission.SandboxReadOnly, Approval: permission.ApprovalNever}}},
		{Preset: "bad", Presets: map[permission.Preset]permission.PresetSpec{"bad": {Sandbox: "yolo", Approval: permission.ApprovalAsk}}},
		{Preset: "bad", Presets: map[permission.Preset]permission.PresetSpec{"bad": {Sandbox: permission.SandboxReadOnly, Approval: "sometimes"}}},
	}
	for _, cfg := range tests {
		if _, err := permission.NewPolicy(cfg); err == nil {
			t.Fatalf("invalid config accepted: %#v", cfg)
		}
	}
}

func TestPolicyAcceptsConfiguredPresetBundle(t *testing.T) {
	policy := mustPolicy(t, permission.Config{
		Preset: "reviewed-read-only",
		Presets: map[permission.Preset]permission.PresetSpec{
			"reviewed-read-only": {Sandbox: permission.SandboxReadOnly, Approval: permission.ApprovalAsk},
		},
	})
	if policy.SandboxMode() != permission.SandboxReadOnly || policy.ApprovalPolicy() != permission.ApprovalAsk {
		t.Fatalf("policy = %s/%s", policy.SandboxMode(), policy.ApprovalPolicy())
	}
}

func TestDelegationPinsApprovalNeverWithoutExpandingSandbox(t *testing.T) {
	parent := mustPolicy(t, permission.Config{Preset: permission.PresetWorkspaceWrite, Prompter: &fakePrompter{approve: true}})
	child := parent.ForDelegation()
	if child.SandboxMode() != permission.SandboxWorkspaceWrite || child.ApprovalPolicy() != permission.ApprovalNever {
		t.Fatalf("child policy = %s/%s", child.SandboxMode(), child.ApprovalPolicy())
	}
	if decision := authorize(child, externalTool, nil); decision.Allowed || decision.ApprovalUsed {
		t.Fatalf("delegated escalation = %#v", decision)
	}
}

func TestParseSandboxMode(t *testing.T) {
	if got, err := permission.ParseSandboxMode(" WORKSPACE-WRITE "); err != nil || got != permission.SandboxWorkspaceWrite {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := permission.ParseSandboxMode("workspace_write"); err == nil {
		t.Fatal("legacy underscore mode was accepted")
	}
}

func mustPolicy(t *testing.T, config permission.Config) *permission.Policy {
	t.Helper()
	policy, err := permission.NewPolicy(config)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func authorize(policy *permission.Policy, definition tool.Definition, args json.RawMessage) permission.Decision {
	return policy.AuthorizeCall(context.Background(), definition, tool.Call{ID: "call-1", Name: definition.Name, Args: args})
}

type fakePrompter struct {
	approve bool
	err     error
	calls   int
	last    permission.ConfirmRequest
}

func (p *fakePrompter) Confirm(_ context.Context, request permission.ConfirmRequest) (bool, error) {
	p.calls++
	p.last = request
	return p.approve, p.err
}

type blockingPrompter struct{}

func (blockingPrompter) Confirm(ctx context.Context, _ permission.ConfirmRequest) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}
