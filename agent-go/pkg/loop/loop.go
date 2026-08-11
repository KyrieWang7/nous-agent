// Package loop 是 agent 内核：一个自持的 for 循环，唯一的编排者。
//
// 回合的步骤顺序（压缩 → 裁剪 → 工具集 → 采样 → 落存 → 切面 → 发布 → 工具 → 停止判定）
// 归内核，不做成中间件。中间件只承载横切关注点（设计文档 §3）。
//
// 内核依赖的一切都是注入接口。它不认识具体的供应商、沙箱、存储或事件总线。
package loop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

// Sampler 调用模型。
//
// 分层、降级链、熔断与错误分类恢复都在实现里（pkg/modelrouter），内核不管。
// 第二个返回值表示本次响应是否已有内容流给了客户端 —— 已流出的内容无法收回，
// 护栏改写后需要补 message_replace 而不是普通的 content_delta。
type Sampler interface {
	Sample(ctx context.Context, st *middleware.State) (*model.Response, bool, error)
}

// Compactor 在采样前按阈值压缩转录，返回是否发生了压缩。
type Compactor interface {
	MaybeCompact(ctx context.Context, h *message.History) (bool, error)
}

// Trimmer 裁剪本次投递给模型的消息。它不改写转录。
type Trimmer interface {
	Trim(msgs []message.Message) []message.Message
}

// ToolSetResolver 计算本轮的工具白名单与被披露的延迟工具。
//
// 每轮重算：权限、plan 模式、skill 收窄、subagent 白名单都在此生效。
type ToolSetResolver interface {
	Resolve(st *middleware.State) (allow []string, disclosed []string)
}

// Publisher 把回复发布到事件流。
//
// 内核只 Publish，不感知订阅者。实现必须非阻塞：事件通路故障不得卡住回合。
type Publisher interface {
	PublishReply(ctx context.Context, st *middleware.State, streamed bool)
}

// StopGate 在模型不请求工具时判定是否放行结束。
//
// 返回非空字符串表示拦截，内核会把理由作为 user 消息回灌并续跑，
// 次数受 StopReinjectionLimit 约束。
type StopGate interface {
	Evaluate(ctx context.Context, stopReason string, st *middleware.State) (string, error)
}

// ToolExecutor 执行一批工具调用。
type ToolExecutor interface {
	Run(ctx context.Context, calls []tool.Call, ic tool.Interceptor) ([]tool.Outcome, error)
	EndTurnRequested() bool
}

// Config 是内核的依赖与上限。
type Config struct {
	Sampler  Sampler
	Registry *tool.Registry
	Executor ToolExecutor
	Chain    *middleware.Chain

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

	// Values seeds typed middleware state from the transport/application layer.
	Values map[string]any
}

// Result 是一次 run 的产出。
type Result struct {
	// Output 是最终回复文本。
	Output string

	Response   *model.Response
	StopReason string
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

	if cfg.Chain == nil {
		empty, err := middleware.NewChain(nil, middleware.ChainOptions{})
		if err != nil {
			return nil, fmt.Errorf("loop: building empty middleware chain: %w", err)
		}
		cfg.Chain = empty
	}

	return &Runner{cfg: cfg}, nil
}

// Run 执行一次完整回合。
//
// 失败语义见设计文档 §3.2。要点：模型与治理类中间件的错误终止回合；
// 工具自身的错误转成 error 结果回灌，不终止回合。
func (r *Runner) Run(ctx context.Context, req Request) (*Result, error) {
	if req.History == nil {
		return nil, errors.New("loop: request requires a history")
	}

	st := middleware.NewState(middleware.StateInit{
		ThreadID:     req.ThreadID,
		RunID:        req.RunID,
		AssistantID:  req.AssistantID,
		SystemPrompt: req.SystemPrompt,
		History:      req.History,
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

	tracker := r.cfg.Limits.NewTracker()
	res := &Result{}

	// AfterAgent 必须跑到，即便回合失败：它承载遥测、记忆入队、标题生成这类收尾。
	defer func() {
		_ = r.cfg.Chain.Execute(context.WithoutCancel(ctx), middleware.StageAfterAgent, st)
		r.syncSpend(ctx, tracker)
		res.Compacted = st.Compacted
		res.CostMicros = tracker.CostMicros()
		res.Values = cloneValues(st.Values)
		res.RiskLevel = st.RiskLevel
	}()

	if err := r.cfg.Chain.Execute(ctx, middleware.StageBeforeAgent, st); err != nil {
		return res, err
	}
	if st.Take() == middleware.DirectiveStop {
		return res, nil
	}

	stopReinjections := 0

	for iteration := 0; ; iteration++ {
		st.Iteration = iteration
		res.Iterations = iteration + 1

		// 1 取消
		if err := ctx.Err(); err != nil {
			res.Iterations = iteration
			return res, err
		}
		// 2 不可删的安全上限
		r.syncSpend(ctx, tracker)
		if err := tracker.Check(iteration); err != nil {
			res.Iterations = iteration
			return res, err
		}
		// 3 采样前压缩
		if err := r.compact(ctx, st); err != nil {
			return res, err
		}
		// 4 裁剪 + 5 本轮工具集
		st.ModelInput = r.buildRequest(st)

		// 6 采样前切面
		if err := r.cfg.Chain.Execute(ctx, middleware.StageBeforeModel, st); err != nil {
			return res, err
		}
		switch st.Take() {
		case middleware.DirectiveStop:
			if st.ModelOutput != nil {
				res.Response = st.ModelOutput
				res.StopReason = st.ModelOutput.StopReason
				res.Output = st.ModelOutput.Message.Content
				if r.cfg.Publisher != nil {
					r.cfg.Publisher.PublishReply(ctx, st, false)
				}
			}
			return res, nil
		case middleware.DirectiveContinue:
			continue
		}
		// BeforeModel may perform metered work (for example compaction or a
		// policy lookup). Re-read the shared budget before starting the lead
		// model so that auxiliary and child usage cannot bypass the ceiling.
		r.syncSpend(ctx, tracker)
		if err := tracker.Check(iteration); err != nil {
			return res, err
		}

		// 7 采样
		resp, streamed, err := r.cfg.Sampler.Sample(ctx, st)
		if err != nil {
			return res, err
		}
		st.ModelOutput = resp
		st.Streamed = streamed
		st.OriginalOutput = resp.Message.Content
		res.Streamed = res.Streamed || streamed
		res.Response = resp
		res.StopReason = resp.StopReason
		res.Usage = res.Usage.Add(resp.Usage)
		st.UsageRecorded = r.observeLeadUsage(ctx, resp)

		// 8 落存回复
		st.History.Append(resp.Message)

		// 9 采样后切面（逆序：Safety → LoopDetection → Guardrail → Schema）
		if err := r.cfg.Chain.Execute(ctx, middleware.StageAfterModel, st); err != nil {
			return res, err
		}
		directive := st.Take()
		r.syncSpend(ctx, tracker)

		// 9b 切面通过后才对外发布。顺序反过来就等于把被护栏拦截的文本
		// 留在用户屏幕上。
		res.Output = st.ModelOutput.Message.Content
		if r.cfg.Publisher != nil {
			r.cfg.Publisher.PublishReply(ctx, st, streamed)
		}

		switch directive {
		case middleware.DirectiveStop:
			return res, nil
		case middleware.DirectiveContinue:
			continue
		}

		// 10 有工具调用则执行后继续
		if calls := toolCallsOf(st.ModelOutput); len(calls) > 0 {
			if err := tracker.Check(iteration); err != nil {
				return res, err
			}
			stop, err := r.runTools(ctx, st, calls)
			if err != nil {
				return res, err
			}
			if stop {
				return res, nil
			}
			continue
		}

		// 11 停止判定
		blocking, err := r.evaluateStop(ctx, st)
		if err != nil {
			return res, err
		}
		if blocking != "" {
			stopReinjections++
			if stopReinjections > r.stopReinjectionLimit() {
				return res, fmt.Errorf("loop: stop blocked: %s", blocking)
			}
			st.History.Append(message.Message{
				Role:    message.RoleUser,
				Content: fmt.Sprintf("[System] 停止被拦截：%s。请先处理该问题。", blocking),
			})
			continue
		}

		// 12 回合结束
		return res, nil
	}
}

func (r *Runner) syncSpend(ctx context.Context, tracker *Tracker) {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Journal == nil {
		return
	}
	tokens, cost := run.Journal.Spend()
	tracker.ObserveSpend(tokens, cost)
}

func (r *Runner) observeLeadUsage(ctx context.Context, response *model.Response) bool {
	if response == nil {
		return false
	}
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Journal == nil {
		return false
	}
	run.Journal.Observe(runtime.Entry{
		Bucket: runtime.BucketLead, Source: "lead", CallID: response.CallID,
		ModelName: response.ModelName, Usage: response.Usage,
	})
	return true
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

// seed 把本轮的用户输入写入转录。
func (r *Runner) seed(st *middleware.State, req Request) error {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" && len(req.ContentBlocks) == 0 {
		if st.History.Len() == 0 {
			return errors.New("loop: request requires a prompt or a non-empty history")
		}
		return nil
	}

	st.History.Append(message.Message{
		Role:          message.RoleUser,
		Content:       prompt,
		ContentBlocks: req.ContentBlocks,
	})
	return nil
}

func (r *Runner) compact(ctx context.Context, st *middleware.State) error {
	if r.cfg.Compactor == nil {
		return nil
	}
	compacted, err := r.cfg.Compactor.MaybeCompact(ctx, st.History)
	if err != nil {
		return fmt.Errorf("loop: compaction: %w", err)
	}
	// 标志一旦置位就保持：持久化层要知道"本回合内发生过压缩"，
	// 而不是"最后一轮是否压缩"。
	st.Compacted = st.Compacted || compacted
	return nil
}

// buildRequest 组装本轮的模型请求。每轮都重算工具集。
func (r *Runner) buildRequest(st *middleware.State) *model.Request {
	msgs := st.History.All()
	if r.cfg.Trimmer != nil {
		msgs = r.cfg.Trimmer.Trim(msgs)
	}

	allow, disclosed := r.resolveToolSet(st)
	st.ToolSet = allow
	st.DisclosedTools = disclosed

	return &model.Request{
		System:   st.SystemPrompt,
		Messages: msgs,
		Tools:    r.cfg.Registry.Schemas(allow, disclosed...),
	}
}

func (r *Runner) resolveToolSet(st *middleware.State) (allow, disclosed []string) {
	if r.cfg.ToolSet != nil {
		return r.cfg.ToolSet.Resolve(st)
	}
	// 没有解析器时披露全部已注册工具。这是库的默认行为；
	// 生产装配必须提供解析器以让权限与 skill 收窄生效。
	return r.cfg.Registry.Names(), nil
}

// runTools 执行本轮的工具调用，返回是否应结束回合。
func (r *Runner) runTools(ctx context.Context, st *middleware.State, calls []tool.Call) (bool, error) {
	outcomes, err := r.cfg.Executor.Run(ctx, calls, r.cfg.Chain.ToolInterceptor(st))

	// 即使出错也先把已执行的结果写进转录：它们对应的 tool_calls 否则会悬空，
	// 让下一次模型请求非法。
	for _, o := range outcomes {
		if o.Result == nil {
			continue
		}
		toolMessage := message.Message{
			Role:             message.RoleTool,
			ToolCallID:       o.Call.ID,
			Name:             o.Call.Name,
			Content:          o.Result.Content,
			ContentBlocks:    o.Result.ContentBlocks,
			IsError:          o.Result.IsError,
			AdditionalKwargs: cloneAdditionalKwargs(o.Result.AdditionalKwargs),
		}
		// ToolErrorHandling may produce a task error result without knowing the
		// frontend contract. Stamp the legacy-compatible terminal status at the
		// transcript boundary so every persisted task message is structured.
		if toolMessage.Name == "task" {
			toolMessage = message.StampSubagentStatus(toolMessage)
			if _, ok := toolMessage.AdditionalKwargs[message.SubagentStatusKey]; !ok && o.ExecErr != nil {
				toolMessage.Content = "Task failed. Error: " + o.ExecErr.Error()
				toolMessage.IsError = true
				toolMessage.AdditionalKwargs = message.MakeSubagentAdditionalKwargs(message.SubagentFailed, o.ExecErr.Error())
			}
		}
		st.History.Append(toolMessage)
	}

	if err != nil {
		return false, err
	}
	if r.cfg.Executor.EndTurnRequested() || st.Take() == middleware.DirectiveStop {
		return true, nil
	}
	return false, nil
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

func (r *Runner) evaluateStop(ctx context.Context, st *middleware.State) (string, error) {
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

func toolCallsOf(resp *model.Response) []tool.Call {
	if resp == nil || len(resp.Message.ToolCalls) == 0 {
		return nil
	}
	out := make([]tool.Call, len(resp.Message.ToolCalls))
	for i, tc := range resp.Message.ToolCalls {
		out[i] = tool.Call{ID: tc.ID, Name: tc.Name, Args: tc.Arguments}
	}
	return out
}
