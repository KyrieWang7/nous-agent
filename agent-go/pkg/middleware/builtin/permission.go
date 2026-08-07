// Package builtin 提供内置中间件，一文件一个。
//
// 每个中间件只实现自己需要的阶段接口，不写空方法（设计文档 §4.1）。
package builtin

import (
	"context"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/permission"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// NamePermission 是权限中间件的名字，供锚点引用。
const NamePermission = "permission"

// Permission 在每次工具调用前做权限判定。
type Permission struct {
	policy   *permission.Policy
	registry *tool.Registry
}

// NewPermission 返回权限中间件。
func NewPermission(policy *permission.Policy, registry *tool.Registry) *Permission {
	return &Permission{policy: policy, registry: registry}
}

// Name 实现 middleware.Middleware。
func (p *Permission) Name() string { return NamePermission }

// Grade 实现 middleware.Graded：权限失败必须中断，不能吞。
func (p *Permission) Grade() middleware.Grade { return middleware.GradeAbort }

// BeforeTool 实现 middleware.BeforeTool。
//
// 拒绝时返回 Decision{Deny} 而不是 error：模型会收到一条错误结果并有机会
// 改换方案，这比杀掉整个回合有用得多。真正的 error 只留给"判定本身出错"，
// 而那种情况下也是拒绝（fail-closed）。
func (p *Permission) BeforeTool(ctx context.Context, st *middleware.State) (tool.Decision, error) {
	if st.ToolCall == nil {
		return tool.Decision{}, nil
	}

	d, err := p.registry.Get(st.ToolCall.Name)
	if err != nil {
		// 未注册的工具走不到执行器，但若真走到了，拒绝而不是放行。
		return tool.Decision{Deny: true, Reason: err.Error()}, nil //nolint:nilerr // an unknown tool is a denied decision, not middleware failure
	}

	decision := p.policy.Authorize(ctx, d, st.ToolCall.Args)
	if decision.Allowed {
		return tool.Decision{}, nil
	}
	return tool.Decision{Deny: true, Reason: decision.Reason}, nil
}
