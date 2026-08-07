package middleware

import (
	"context"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// TerminalName 是终端中间件的名字。
//
// 澄清拦截必须看到最终输出，所以它固定排在链尾。未声明锚点的扩展中间件
// 插到它之前（设计文档 §4.3）。
const TerminalName = "clarification"

// Stage 是中间件的挂载阶段。
type Stage int

const (
	// StageBeforeAgent 每次 run 执行一次，在循环之前。
	StageBeforeAgent Stage = iota

	// StageBeforeModel 每轮迭代执行一次，在采样之前。
	StageBeforeModel

	// StageAfterModel 每轮迭代执行一次，在采样之后、发布回复之前。
	// 逆注册顺序执行。
	StageAfterModel

	// StageAfterAgent 每次 run 执行一次，在循环之后。逆注册顺序执行。
	StageAfterAgent
)

// String 实现 fmt.Stringer。
func (s Stage) String() string {
	switch s {
	case StageBeforeAgent:
		return "BeforeAgent"
	case StageBeforeModel:
		return "BeforeModel"
	case StageAfterModel:
		return "AfterModel"
	case StageAfterAgent:
		return "AfterAgent"
	default:
		return "unknown"
	}
}

// Middleware 是所有中间件的基础契约。
//
// Name 是中间件的身份：锚点排序、错误信息与"声明的中间件是否在链里"的校验
// 都靠它，所以链内不允许重名。
type Middleware interface {
	Name() string
}

// 以下均为可选接口。中间件只实现自己需要的阶段，Chain 靠类型断言收集。
// 这样 24 个内置实现里没有一个需要写空方法。

// BeforeAgent 在每次 run 开始时执行一次。
type BeforeAgent interface {
	BeforeAgent(ctx context.Context, st *State) error
}

// BeforeModel 在每轮采样之前执行。
type BeforeModel interface {
	BeforeModel(ctx context.Context, st *State) error
}

// AfterModel 在每轮采样之后、发布回复之前执行。逆注册顺序。
type AfterModel interface {
	AfterModel(ctx context.Context, st *State) error
}

// AfterAgent 在每次 run 结束时执行一次。逆注册顺序。
type AfterAgent interface {
	AfterAgent(ctx context.Context, st *State) error
}

// BeforeTool 在每次工具调用之前执行。
//
// 返回决策而不是纯 error，覆盖三种真实语义：权限拒绝、Hook 改写入参、澄清拦截
// （设计文档 §4.1）。State.ToolCall 已置为当次调用。
type BeforeTool interface {
	BeforeTool(ctx context.Context, st *State) (tool.Decision, error)
}

// AfterTool 在每次工具调用之后执行，可改写 State.ToolResult。
//
// State.ToolExecErr 承载 Handler 的框架层错误，ToolErrorHandling 中间件
// 靠它把执行期失败转成 error ToolMessage。
type AfterTool interface {
	AfterTool(ctx context.Context, st *State) error
}

// Grade 是中间件失败时的处理级别。
type Grade int

const (
	// GradeAbort 表示该中间件失败必须中断回合。默认级别。
	//
	// 权限、护栏、Schema 校验、历史修复类中间件都属于此类：
	// 它们失败意味着"无法确认安全"或"请求会非法"，继续跑下去更糟。
	GradeAbort Grade = iota

	// GradeListener 表示该中间件只是旁路监听，失败只记日志。
	//
	// Title、Memory、TokenUsage 属于此类。标题生成失败把一个已经成功的回合
	// 判死是纯负收益（设计文档 §17.1）。
	GradeListener
)

// String 实现 fmt.Stringer。
func (g Grade) String() string {
	switch g {
	case GradeAbort:
		return "abort"
	case GradeListener:
		return "listener"
	default:
		return "unknown"
	}
}

// Graded 让中间件声明自己的失败级别。未实现该接口的按 GradeAbort 处理 —— 默认保守。
type Graded interface {
	Grade() Grade
}

// Anchor 声明扩展中间件在链中的相对位置。After 与 Before 互斥。
type Anchor struct {
	After  string
	Before string
}

// Anchored 让扩展中间件声明锚点。未实现该接口的插到终端中间件之前。
type Anchored interface {
	Anchor() Anchor
}

func gradeOf(m Middleware) Grade {
	if g, ok := m.(Graded); ok {
		return g.Grade()
	}
	return GradeAbort
}
