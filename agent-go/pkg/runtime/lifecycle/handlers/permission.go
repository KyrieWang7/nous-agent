// Package builtin 提供内置生命周期处理器，一文件一个。
//
// 每个生命周期处理器只实现自己需要的阶段接口，不写空方法（设计文档 §4.1）。
package handlers

import (
	"context"
	"errors"
	"fmt"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/permission"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/capability"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// NamePermission 是权限生命周期处理器的名字，供锚点引用。
const NamePermission = "permission"

// Permission 在每次工具调用前做权限判定。
type Permission struct {
	policyCapability string
	registry         *tool.Registry
}

// NewPermission 返回权限生命周期处理器。
func NewPermission(policyCapability string, registry *tool.Registry) *Permission {
	return &Permission{policyCapability: policyCapability, registry: registry}
}

// Name 实现 lifecycle.Lifecycle。
func (p *Permission) Name() string { return NamePermission }

// Grade 实现 lifecycle.Graded：权限失败必须中断，不能吞。
func (p *Permission) Grade() lifecycle.Grade { return lifecycle.GradeAbort }

// BeforeTool 实现 lifecycle.BeforeTool。
//
// 拒绝时返回 Decision{Deny} 而不是 error：模型会收到一条错误结果并有机会
// 改换方案，这比杀掉整个回合有用得多。真正的 error 只留给"判定本身出错"，
// 而那种情况下也是拒绝（fail-closed）。
func (p *Permission) BeforeTool(ctx context.Context, st *lifecycle.State) (tool.Decision, error) {
	if st.ToolCall == nil {
		return tool.Decision{}, nil
	}

	d, err := p.registry.Get(st.ToolCall.Name)
	if err != nil {
		// 未注册的工具走不到执行器，但若真走到了，拒绝而不是放行。
		return tool.Decision{Deny: true, Reason: err.Error()}, nil //nolint:nilerr // an unknown tool is a denied decision, not lifecycle failure
	}

	run, ok := runtime.RunContextFrom(ctx)
	if !ok || !run.Capabilities.Initialized() {
		return tool.Decision{}, errors.New("permission: capability view is not initialized")
	}
	policy, err := capability.ResolveViewAs[*permission.Policy](ctx, run.Capabilities, p.policyCapability, capability.KindPolicy, capability.ResolveRequest{
		GenerationID: run.GenerationID, ThreadID: run.ThreadID, RunID: run.RunID, Values: run.Values,
	})
	if err != nil {
		return tool.Decision{}, fmt.Errorf("permission: resolving policy capability: %w", err)
	}
	if run.AgentDepth > 0 {
		policy = policy.ForDelegation()
	}
	decision := policy.AuthorizeCall(ctx, d, *st.ToolCall)
	if decision.Allowed {
		return tool.Decision{}, nil
	}
	return tool.Decision{Deny: true, Reason: decision.Reason}, nil
}
