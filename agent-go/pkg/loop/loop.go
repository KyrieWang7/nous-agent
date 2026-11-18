// Package loop 是 agent 内核：一个自持的 for 循环，唯一的编排者。
//
// 回合的步骤顺序（压缩 → 裁剪 → 工具集 → 采样 → 落存 → 切面 → 发布 → 工具 → 停止判定）
// 归内核。可装配的横切关注点由固定阶段的 lifecycle handler 承载（设计文档 §3）。
//
// 内核依赖的一切都是注入接口。它不认识具体的供应商、沙箱、存储或事件总线。
package loop

import (
	"context"
	"errors"
	"fmt"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/scope"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// Sampler 调用模型。
//
// 分层、降级链、熔断与错误分类恢复都在实现里（pkg/modelrouter），内核不管。
// 第二个返回值表示本次响应是否已有内容流给了客户端 —— 已流出的内容无法收回，
// 护栏改写后需要补 message_replace 而不是普通的 content_delta。
type Sampler interface {
	Sample(ctx context.Context, st *lifecycle.State) (*model.Response, bool, error)
}

// Compactor 在采样前按阈值压缩转录，返回是否发生了压缩。
type Compactor interface {
	MaybeCompact(ctx context.Context, h *message.History) (bool, error)
}

// CompactionDecider is the optional preflight contract used by production
// compactors. It lets the kernel publish a real compacting phase without
// reporting every no-op threshold check as compaction work.
type CompactionDecider interface {
	ShouldCompact(h *message.History) bool
}

// Trimmer 裁剪本次投递给模型的消息。它不改写转录。
type Trimmer interface {
	Trim(msgs []message.Message) []message.Message
}

// ToolSetResolver 计算本轮的工具白名单与被披露的延迟工具。
//
// 每轮重算：权限、plan 模式、skill 收窄、subagent 白名单都在此生效。
type ToolSetResolver interface {
	Resolve(context.Context, *lifecycle.State) (allow []string, disclosed []string, err error)
}

// Publisher 把回复发布到事件流。
//
// 内核只 Publish，不感知订阅者。实现必须非阻塞：事件通路故障不得卡住回合。
type Publisher interface {
	PublishReply(ctx context.Context, st *lifecycle.State, streamed bool)
}

// StopGate 在模型不请求工具时判定是否放行结束。
//
// 返回非空字符串表示拦截，内核会把理由作为 user 消息回灌并续跑，
// 次数受 StopReinjectionLimit 约束。
type StopGate interface {
	Evaluate(ctx context.Context, stopReason string, st *lifecycle.State) (string, error)
}

// ToolExecutor executes a batch and must drain all started calls before Run
// returns. The loop settles missing results only after that quiescent boundary.
type ToolExecutor interface {
	Run(ctx context.Context, calls []tool.Call, ic tool.Interceptor) ([]tool.Outcome, error)
	EndTurnRequested() bool
}

// Config 是内核的依赖与上限。
type Config struct {
	Sampler   Sampler
	Registry  *tool.Registry
	Executor  ToolExecutor
	Lifecycle *lifecycle.Dispatcher

	Limits Limits

	// 以下均可为 nil，内核退化为不做该步骤。
	Compactor Compactor
	Trimmer   Trimmer
	ToolSet   ToolSetResolver
	Publisher Publisher
	StopGate  StopGate
}

// Request 是一次 run 的输入。
type Request struct {
	PullInputs func(context.Context) ([]message.Message, func(context.Context, []message.Message) error, error)
	// Checkpoint persists admitted input, assistant tool intent and settled step
	// history. A failed write prevents subsequent model/tool work.
	Checkpoint   func(context.Context, []message.Message) error
	ThreadID     string
	RunID        string
	AssistantID  string
	SystemPrompt string

	// History 是转录，必填。续跑时传入已恢复的历史。
	History *message.History

	// Prompt 是本轮的用户输入。转录已非空时可为空（续跑）。
	Prompt string

	// ContentBlocks 承载多模态输入，与 Prompt 并存。
	ContentBlocks []message.ContentBlock

	// Values seeds typed run state from the transport/application layer.
	Values map[string]any
}

// Result 是一次 run 的产出。
type Result struct {
	// Output 是最终回复文本。
	Output string

	Response   *model.Response
	StopReason string
	// EndReason is the turn outcome, independent of the provider finish reason.
	EndReason  string
	Iterations int
	Usage      model.Usage

	// Compacted 表示本回合发生过压缩。持久化层据此走整体重写路径
	// 而不是追加路径（设计文档 §13.2）。
	Compacted bool
	// Streamed reports whether at least one model text delta was emitted.
	Streamed bool

	CostMicros int64
	Values     map[string]any
	RiskLevel  string
}

// Runner 是内核。
type Runner struct {
	cfg Config
}

// NewRunner 校验配置并返回内核。
func NewRunner(cfg Config) (*Runner, error) {
	switch {
	case cfg.Sampler == nil:
		return nil, errors.New("loop: config requires a sampler")
	case cfg.Registry == nil:
		return nil, errors.New("loop: config requires a tool registry")
	case cfg.Executor == nil:
		return nil, errors.New("loop: config requires a tool executor")
	}

	if cfg.Lifecycle == nil {
		empty, err := lifecycle.NewDispatcher(nil, lifecycle.DispatcherOptions{})
		if err != nil {
			return nil, fmt.Errorf("loop: building empty lifecycle dispatcher: %w", err)
		}
		cfg.Lifecycle = empty
	}

	return &Runner{cfg: cfg}, nil
}

// Run 执行一次完整回合。
//
// 失败语义见设计文档 §3.2。要点：模型与治理 handler 的错误终止回合；
// 工具自身的错误转成 error 结果回灌，不终止回合。
func (r *Runner) Run(ctx context.Context, req Request) (_ *Result, runErr error) {
	if req.History == nil {
		return nil, errors.New("loop: request requires a history")
	}
	owner := scope.New(ctx)
	defer func() { runErr = errors.Join(runErr, owner.Dispose()) }() //nolint:contextcheck // scope disposal uses its captured cancellation and cleanup context
	ctx = owner.Context()
	var err error
	ctx, err = prepareRunContext(ctx, req)
	if err != nil {
		return nil, err
	}

	run, _ := runtime.RunContextFrom(ctx) //nolint:contextcheck // ctx is inherited through the execution owner scope
	run.ExecutionOwner = owner
	ctx = runtime.WithRunContext(ctx, run)

	// Every runner gets a run-scoped child ledger. When a parent context already
	// carries a root ledger, this child keeps the configured per-run limits while
	// still charging the shared ancestor used by subagents.
	ctx, err = r.installBudget(ctx)
	if err != nil {
		return nil, err
	}

	// Assemble proposed input privately. Policy and request preparation may reject
	// it; the caller-owned transcript changes only at the first model barrier.
	proposed := message.NewHistory()
	proposed.Load(req.History.All())
	st := lifecycle.NewState(lifecycle.StateInit{
		ThreadID:     req.ThreadID,
		RunID:        req.RunID,
		AssistantID:  req.AssistantID,
		SystemPrompt: req.SystemPrompt,
		History:      proposed,
	})
	// Values belongs to this run and is intentionally shared with RunContext.
	// Trusted tool lifecycle code can therefore update state that later model
	// iterations and child dispatches must observe. Child runs clone the map in
	// subagent.cloneRunContext before they mutate it.
	st.Values = req.Values
	if st.Values == nil {
		st.Values = make(map[string]any)
	}

	if err := r.seed(st, req); err != nil {
		return nil, err
	}

	driver := &agentDriver{runner: r, ctx: ctx, state: st, canonical: req.History, checkpoint: req.Checkpoint, pullInputs: req.PullInputs, tracker: r.cfg.Limits.NewTracker(), result: &Result{}}
	return driver.run() //nolint:contextcheck // the driver retains the scoped context for every step
}

func cloneValues(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneAdditionalKwargs(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (r *Runner) evaluateStop(ctx context.Context, st *lifecycle.State) (string, error) {
	if r.cfg.StopGate == nil {
		return "", nil
	}
	stopReason := ""
	if st.ModelOutput != nil {
		stopReason = st.ModelOutput.StopReason
	}
	return r.cfg.StopGate.Evaluate(ctx, stopReason, st)
}

func (r *Runner) stopReinjectionLimit() int {
	if r.cfg.Limits.StopReinjectionLimit > 0 {
		return r.cfg.Limits.StopReinjectionLimit
	}
	return defaultStopReinjectionLimit
}
