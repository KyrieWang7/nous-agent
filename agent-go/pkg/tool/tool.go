// Package tool 定义工具的声明、注册与执行。
//
// 本包不认识中间件：BeforeTool/AfterTool 的介入通过 Interceptor 这个窄接口进入，
// 由 pkg/middleware 提供适配器。依赖方向是 middleware → tool，不可反向
// （设计文档 §2 依赖纪律）。
package tool

import (
	"context"
	"encoding/json"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

// Metadata 描述工具的执行特征。并发分段与权限判定都读它（设计文档 §7.1、§7.2）。
type Metadata struct {
	// RequiredPermission is an optional minimum permission mode. It is kept as
	// a string so tool remains independent of pkg/permission.
	RequiredPermission string

	// IsReadOnly 表示该工具不修改任何状态。
	IsReadOnly bool

	// IsAgentState marks orchestration state owned by the Harness itself (for
	// example task and Swarm lifecycle records). These writes do not escape to
	// the user's filesystem, so workspace_write may allow them without treating
	// arbitrary external side effects as workspace changes.
	IsAgentState bool

	// IsConcurrencySafe 表示多个实例可同时执行而互不干扰。
	//
	// 与 IsReadOnly 同时为真才会被分到并发段。判据取自工具元数据而非模型意图 ——
	// 模型说"这几个可以并行"是不可信的。
	IsConcurrencySafe bool

	// RequiresSandbox 表示该工具必须在沙箱句柄内执行。
	RequiresSandbox bool

	// Destructive 表示该操作不可逆且影响面大（删除、退款确认、停用账号）。
	//
	// 这类工具永不注册为可自动调用：Registry.Register 直接拒绝。它们只能走
	// 人工审批路径（设计文档 §7.3）。
	Destructive bool
}

// Call 是一次工具调用的输入。
type Call struct {
	ID   string
	Name string
	Args json.RawMessage
}

// Artifact 是工具产出的外部化内容（超大输出、生成的文件）。
type Artifact struct {
	Kind string // file | url | blob
	Ref  string
	Size int64
}

// Result 是一次工具调用的输出。
//
// Content 是回灌给模型的文本。工具自身的错误不返回 error，而是置 IsError 并把
// 说明写进 Content —— 让模型看到失败原因并改换方案，而不是杀掉整个回合
// （设计文档 §3.2 步 10、§17.2）。
type Result struct {
	Content       string
	ContentBlocks []message.ContentBlock
	IsError       bool
	Artifacts     []Artifact
	// AdditionalKwargs is persisted alongside the tool message and projected
	// unchanged to the LangGraph-compatible wire format. It is intentionally
	// generic because provider/UI contracts (for example the subagent status
	// contract) should not make the tool package depend on a specific consumer.
	AdditionalKwargs map[string]any
}

// Handler 执行一次工具调用。
//
// 返回的 error 表示"执行框架层面出错"（沙箱不可用、参数无法解析），
// 由 ToolErrorHandling 中间件转成 error Result。工具的业务失败应走 Result.IsError。
type Handler func(ctx context.Context, call Call) (*Result, error)

// Definition 是一个工具的完整声明。
type Definition struct {
	Name        string
	Group       string
	Description string

	// Parameters 是 JSON Schema，直接投递给模型。
	Parameters json.RawMessage

	// Deferred 表示默认不向模型披露该工具的 schema。
	//
	// 只有被已激活 skill 声明后才进请求体，用于压小请求体并收窄可调用面。
	// 披露不等于授权：仍须通过权限白名单（设计文档 §7.3）。
	Deferred bool

	Metadata Metadata
	Handler  Handler
}

// ModelSchema 返回投递给模型的 schema 视图。
func (d Definition) ModelSchema() model.ToolSchema {
	return model.ToolSchema{
		Name:        d.Name,
		Description: d.Description,
		Parameters:  d.Parameters,
	}
}

// Concurrent 报告该工具是否可与同类并发执行。
func (d Definition) Concurrent() bool {
	return d.Metadata.IsConcurrencySafe && (d.Metadata.IsReadOnly || d.Metadata.IsAgentState)
}

// Decision 是 BeforeTool 拦截的结果。
//
// 返回决策而不是纯 error，是为了覆盖三种真实语义而不引入环绕链
// （设计文档 §4.1）：权限拒绝、Hook 改写入参、澄清拦截。
type Decision struct {
	// Deny 表示不执行该工具，改为回灌一条错误结果。
	Deny bool

	// EndTurn 表示结束整个回合（澄清拦截用）。
	EndTurn bool

	// Reason 是给模型看的说明，Deny 时必填。
	Reason string

	// Args 非 nil 时替换工具入参（Hook 改写用）。
	Args json.RawMessage
}

// Interceptor 是执行器与中间件链之间的窄接口。
//
// pkg/tool 不 import pkg/middleware：后者的 State 持有 tool.Result，
// 反向依赖会成环。由 pkg/middleware 提供实现此接口的适配器。
type Interceptor interface {
	// BeforeTool 在工具执行前介入，可拒绝、改写入参或结束回合。
	BeforeTool(ctx context.Context, call Call) (Decision, error)

	// AfterTool 在工具执行后介入，可改写结果。
	// execErr 是 Handler 返回的框架层错误，可能为 nil。
	AfterTool(ctx context.Context, call Call, res *Result, execErr error) (*Result, error)
}
