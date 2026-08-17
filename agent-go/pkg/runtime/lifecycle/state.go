// Package lifecycle 定义四阶段生命周期处理器契约、链的执行语义与声明式锚点排序。
//
// 编排不在这里：回合的步骤顺序（压缩 → 裁剪 → 工具集 → 采样 → 落存 → 工具 → 停止判定）
// 归 pkg/loop 的 for 循环。生命周期处理器只承载横切关注点（设计文档 §3、§4）。
package lifecycle

import (
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// Directive 是生命周期处理器对内核控制流的请求。
//
// 内核读它，生命周期处理器写它。这样澄清拦截、Schema 校验重试这类需求不必让内核特判
// 具体生命周期处理器（设计文档 §4.2）。
type Directive int

const (
	// DirectiveProceed 继续本轮，默认值。
	DirectiveProceed Directive = iota

	// DirectiveStop 立刻结束回合。澄清拦截用。
	DirectiveStop

	// DirectiveContinue 跳过本轮剩余步骤，直接进入下一轮迭代。
	// Schema 校验失败后要求模型重答时用。
	DirectiveContinue
)

// String 实现 fmt.Stringer。
func (d Directive) String() string {
	switch d {
	case DirectiveProceed:
		return "proceed"
	case DirectiveStop:
		return "stop"
	case DirectiveContinue:
		return "continue"
	default:
		return "unknown"
	}
}

// StateInit 是构造 State 的初始值。
type StateInit struct {
	ThreadID     string
	RunID        string
	AssistantID  string
	SystemPrompt string
	History      *message.History
}

// State 是生命周期处理器与内核之间的共享状态。
//
// 内置链需要的一切都是具体字段，不是 map 键。Values 保留给树外第三方生命周期处理器，
// 树内代码一次都不用 —— 一旦所有步骤都通过 map[string]any 通信，
// 就会出现时序耦合（生命周期处理器 B 只在 A 先跑过时才工作），而 Go 连动态类型那点
// 便利都没有，换来的是满地 any 断言（设计文档 §4.2）。
type State struct {
	ThreadID    string
	RunID       string
	AssistantID string

	Iteration    int
	SystemPrompt string

	// History 是回合的唯一状态。CloneForTool 出来的副本共享同一个指针。
	History *message.History

	ModelInput  *model.Request
	ModelOutput *model.Response

	// ToolCall / ToolResult / ToolExecErr 只在 BeforeTool / AfterTool 阶段有值。
	ToolCall    *tool.Call
	ToolResult  *tool.Result
	ToolExecErr error

	Directive Directive

	// Compacted 表示本回合发生过压缩。持久化层据此走整体重写路径
	// 而不是追加路径（设计文档 §13.2）。判据是这个标志，不是"消息数变少了"。
	Compacted bool

	// ToolSet 是本轮的工具名白名单，由内核每轮重算。
	//
	// 存名字而非 tool.Definition：Registry.Schemas 接受的就是名字，
	// 复制一份定义只会带来两份真相。
	ToolSet []string

	// DisclosedTools 是被已激活 skill 显式披露的延迟工具名。
	// 披露不放大权限：仍须落在 ToolSet 内。
	DisclosedTools []string

	// ActivatedSkills 记录本回合已激活的 skill，供 trace 与用量归因。
	ActivatedSkills []string

	// Streamed 表示当前模型回复已有文本对外流出。OriginalOutput 保留
	// AfterModel 改写前的内容，供护栏发送 message_replace 补偿事件。
	Streamed       bool
	OriginalOutput string

	// RiskLevel 与 GuardrailAction 是内置治理结果，归属于当前 run，不能
	// 写进 thread Values，否则一次拦截会污染后续所有回合。
	RiskLevel       string
	GuardrailAction string

	// Values 仅供树外第三方生命周期处理器。
	Values map[string]any
}

// NewState 返回初始化好的 State。
func NewState(init StateInit) *State {
	return &State{
		ThreadID:     init.ThreadID,
		RunID:        init.RunID,
		AssistantID:  init.AssistantID,
		SystemPrompt: init.SystemPrompt,
		History:      init.History,
	}
}

// Take 返回当前 Directive 并复位为 Proceed。
//
// 复位是必要的：内核在每个决策点读一次，读完就该清空，
// 否则一次 Stop 会在后续迭代里反复生效。
func (s *State) Take() Directive {
	d := s.Directive
	s.Directive = DirectiveProceed
	return d
}

// CloneForTool 返回用于单次工具调用的 State 副本。
//
// 并发段内每个调用各持一份，避免互相踩 ToolCall / ToolResult。
// History 与 Values 共享指针：转录是单实例状态，不能分叉。
func (s *State) CloneForTool(call tool.Call) *State {
	dup := *s
	dup.ToolCall = &call
	dup.ToolResult = nil
	dup.ToolExecErr = nil
	dup.Directive = DirectiveProceed
	return &dup
}

// SetValue 写入第三方生命周期处理器的自定义值。
func (s *State) SetValue(key string, v any) {
	if s.Values == nil {
		s.Values = make(map[string]any)
	}
	s.Values[key] = v
}

// Value 读取第三方生命周期处理器的自定义值。
func (s *State) Value(key string) (any, bool) {
	v, ok := s.Values[key]
	return v, ok
}
