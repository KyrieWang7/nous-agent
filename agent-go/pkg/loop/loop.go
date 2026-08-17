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
	"slices"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
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

// ToolExecutor 执行一批工具调用。
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
func (r *Runner) Run(ctx context.Context, req Request) (*Result, error) {
	if req.History == nil {
		return nil, errors.New("loop: request requires a history")
	}
	var err error
	ctx, err = prepareRunContext(ctx, req)
	if err != nil {
		return nil, err
	}

	// Every runner gets a run-scoped child ledger. When a parent context already
	// carries a root ledger, this child keeps the configured per-run limits while
	// still charging the shared ancestor used by subagents.
	ctx, err = r.installBudget(ctx)
	if err != nil {
		return nil, err
	}

	st := lifecycle.NewState(lifecycle.StateInit{
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
		_ = r.cfg.Lifecycle.Execute(context.WithoutCancel(ctx), lifecycle.StageAfterAgent, st)
		r.syncSpend(ctx, tracker)
		res.Compacted = st.Compacted
		res.CostMicros = tracker.CostMicros()
		res.Values = cloneValues(st.Values)
		res.RiskLevel = st.RiskLevel
	}()

	if err := r.cfg.Lifecycle.Execute(ctx, lifecycle.StageBeforeAgent, st); err != nil {
		return res, err
	}
	if st.Take() == lifecycle.DirectiveStop {
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
		st.ModelInput, err = r.buildRequest(ctx, st)
		if err != nil {
			return res, err
		}

		// 6 采样前切面
		if err := r.cfg.Lifecycle.Execute(ctx, lifecycle.StageBeforeModel, st); err != nil {
			return res, err
		}
		switch st.Take() {
		case lifecycle.DirectiveStop:
			if st.ModelOutput != nil {
				res.Response = st.ModelOutput
				res.StopReason = st.ModelOutput.StopReason
				res.Output = st.ModelOutput.Message.Content
				if r.cfg.Publisher != nil {
					r.cfg.Publisher.PublishReply(ctx, st, false)
				}
			}
			return res, nil
		case lifecycle.DirectiveContinue:
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
		if err := normalizeToolCallIDs(resp, st.RunID, iteration); err != nil {
			return res, err
		}
		st.ModelOutput = resp
		st.Streamed = streamed
		st.OriginalOutput = resp.Message.Content
		res.Streamed = res.Streamed || streamed
		res.Response = resp
		res.StopReason = resp.StopReason
		res.Usage = res.Usage.Add(resp.Usage)
		r.observeLeadUsage(ctx, resp)
		if err := r.chargeModelBudget(ctx, resp); err != nil {
			return res, err
		}

		// 8 落存回复
		st.History.Append(resp.Message)

		// 9 采样后切面（逆序：Safety → LoopDetection → Guardrail → Schema）
		if err := r.cfg.Lifecycle.Execute(ctx, lifecycle.StageAfterModel, st); err != nil {
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
		case lifecycle.DirectiveStop:
			return res, nil
		case lifecycle.DirectiveContinue:
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

func prepareRunContext(ctx context.Context, req Request) (context.Context, error) {
	run, _ := runtime.RunContextFrom(ctx)
	if run.RunID == "" {
		run.RunID = req.RunID
	}
	if run.ThreadID == "" {
		run.ThreadID = req.ThreadID
	}
	if run.RunID == "" || run.ThreadID == "" {
		return ctx, errors.New("loop: request requires run_id and thread_id")
	}
	if run.RunID != req.RunID || run.ThreadID != req.ThreadID {
		return ctx, fmt.Errorf("loop: run context identity %q/%q does not match request %q/%q", run.RunID, run.ThreadID, req.RunID, req.ThreadID)
	}
	if run.StateMachine == nil {
		state, err := runtime.NewRunStateMachine(run.RunID, run.ThreadID)
		if err != nil {
			return ctx, err
		}
		if err := state.Start(); err != nil {
			return ctx, err
		}
		run.StateMachine = state
	}
	snapshot := run.StateMachine.Snapshot()
	if snapshot.RunID != req.RunID || snapshot.ThreadID != req.ThreadID {
		return ctx, fmt.Errorf("loop: run state identity %q/%q does not match request %q/%q", snapshot.RunID, snapshot.ThreadID, req.RunID, req.ThreadID)
	}
	if snapshot.Phase != runtime.RunRunning {
		return ctx, fmt.Errorf("loop: run state must be %s, got %s", runtime.RunRunning, snapshot.Phase)
	}
	return runtime.WithRunContext(ctx, run), nil
}

func (r *Runner) syncSpend(ctx context.Context, tracker *Tracker) {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Journal == nil {
		return
	}
	tokens, cost := run.Journal.Spend()
	tracker.ObserveSpend(tokens, cost)
}

func (r *Runner) installBudget(ctx context.Context) (context.Context, error) {
	run, _ := runtime.RunContextFrom(ctx)
	limits := runtime.BudgetAmount{
		Tokens:     int64(r.cfg.Limits.MaxTokens),
		CostMicros: r.cfg.Limits.MaxCostMicros,
		ToolCalls:  int64(r.cfg.Limits.MaxToolCalls),
		Subagents:  int64(r.cfg.Limits.MaxSubagents),
	}
	var budget *runtime.BudgetLedger
	var err error
	if run.Budget != nil {
		budget, err = run.Budget.Child(limits)
	} else {
		budget = runtime.NewBudgetLedger(limits)
	}
	if err != nil {
		return ctx, err
	}
	run.Budget = budget
	return runtime.WithRunContext(ctx, run), nil
}

func (r *Runner) chargeModelBudget(ctx context.Context, response *model.Response) error {
	if response == nil {
		return nil
	}
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Budget == nil {
		return nil
	}
	amount := runtime.BudgetAmount{Tokens: int64(response.Usage.TotalTokens())}
	if run.Journal != nil {
		amount.CostMicros = run.Journal.EstimateCost(response.ModelName, response.Usage)
	}
	if err := run.Budget.Charge(amount); err != nil {
		if errors.Is(err, runtime.ErrBudgetExceeded) {
			return ErrBudgetExhausted
		}
		return err
	}
	return nil
}

func (r *Runner) observeLeadUsage(ctx context.Context, response *model.Response) {
	if response == nil {
		return
	}
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || run.Journal == nil {
		return
	}
	run.Journal.Observe(runtime.Entry{
		Bucket: runtime.BucketLead, Source: "lead", CallID: response.CallID,
		ModelName: response.ModelName, Usage: response.Usage,
	})
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
func (r *Runner) seed(st *lifecycle.State, req Request) error {
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

func (r *Runner) compact(ctx context.Context, st *lifecycle.State) error {
	if r.cfg.Compactor == nil {
		return nil
	}
	run, hasRun := runtime.RunContextFrom(ctx)
	trackState := false
	if decider, ok := r.cfg.Compactor.(CompactionDecider); ok && decider.ShouldCompact(st.History) && hasRun && run.StateMachine != nil && run.StateMachine.Snapshot().Phase == runtime.RunRunning {
		if err := run.StateMachine.BeginCompaction(); err != nil {
			return fmt.Errorf("loop: beginning compaction: %w", err)
		}
		trackState = true
		if err := publishCompactionEvent(ctx, run, runtime.EventCompactionStart, st.Iteration, map[string]any{"iteration": st.Iteration}); err != nil {
			return err
		}
	}
	compacted, err := r.cfg.Compactor.MaybeCompact(ctx, st.History)
	if trackState {
		payload := map[string]any{"iteration": st.Iteration, "compacted": compacted}
		if err != nil {
			payload["error"] = err.Error()
		}
		if publishErr := publishCompactionEvent(context.WithoutCancel(ctx), run, runtime.EventCompactionComplete, st.Iteration, payload); publishErr != nil && err == nil {
			err = publishErr
		}
		if !run.StateMachine.Snapshot().Phase.Terminal() {
			if resumeErr := run.StateMachine.Resume(); resumeErr != nil && err == nil {
				err = resumeErr
			}
		}
	}
	if err != nil {
		return fmt.Errorf("loop: compaction: %w", err)
	}
	// 标志一旦置位就保持：持久化层要知道"本回合内发生过压缩"，
	// 而不是"最后一轮是否压缩"。
	st.Compacted = st.Compacted || compacted
	return nil
}

func publishCompactionEvent(ctx context.Context, run runtime.RunContext, typ runtime.EventType, iteration int, payload any) error {
	event := runtime.MustEvent(run.EventStreamRunID(), run.ThreadID, typ, payload)
	event.IdempotencyKey = fmt.Sprintf("run:%s:compaction:%d:%s", run.RunID, iteration, typ)
	if run.Publish != nil {
		_, err := run.Publish(ctx, event)
		return err
	}
	if run.Bus != nil {
		run.Bus.Publish(ctx, event)
	}
	return nil
}

// buildRequest 组装本轮的模型请求。每轮都重算工具集。
func (r *Runner) buildRequest(ctx context.Context, st *lifecycle.State) (*model.Request, error) {
	msgs := st.History.All()
	if r.cfg.Trimmer != nil {
		msgs = r.cfg.Trimmer.Trim(msgs)
	}

	allow, disclosed, err := r.resolveToolSet(ctx, st)
	if err != nil {
		return nil, err
	}
	st.ToolSet = allow
	st.DisclosedTools = disclosed

	return &model.Request{
		System:   st.SystemPrompt,
		Messages: msgs,
		Tools:    r.cfg.Registry.Schemas(allow, disclosed...),
	}, nil
}

func (r *Runner) resolveToolSet(ctx context.Context, st *lifecycle.State) (allow, disclosed []string, err error) {
	if r.cfg.ToolSet != nil {
		allow, disclosed, err = r.cfg.ToolSet.Resolve(ctx, st)
		if err != nil {
			return nil, nil, fmt.Errorf("loop: resolving tool capabilities: %w", err)
		}
	} else {
		// 没有解析器时披露全部已注册工具。这是库的默认行为；
		// 生产装配必须提供解析器以让权限与 skill 收窄生效。
		allow = r.cfg.Registry.Names()
	}
	run, ok := runtime.RunContextFrom(ctx)
	if !ok || !run.Capabilities.Initialized() {
		return allow, disclosed, nil
	}
	capabilities := make(map[string]struct{}, len(run.Capabilities.Names()))
	for _, name := range run.Capabilities.Names() {
		capabilities[name] = struct{}{}
	}
	filtered := allow[:0]
	for _, name := range allow {
		if _, permitted := capabilities["tool."+name]; permitted {
			filtered = append(filtered, name)
		}
	}
	return filtered, disclosed, nil
}

// runTools 执行本轮的工具调用，返回是否应结束回合。
func (r *Runner) runTools(ctx context.Context, st *lifecycle.State, calls []tool.Call) (bool, error) {
	for _, call := range calls {
		if !r.toolAvailable(st, call.Name) {
			return false, fmt.Errorf("loop: tool %q is not available in the current capability view", call.Name)
		}
	}
	run, hasRun := runtime.RunContextFrom(ctx)
	if hasRun && run.Budget != nil {
		if err := run.Budget.Charge(runtime.BudgetAmount{ToolCalls: int64(len(calls))}); err != nil {
			if errors.Is(err, runtime.ErrBudgetExceeded) {
				return false, ErrBudgetExhausted
			}
			return false, err
		}
	}
	waitingTool := false
	if hasRun && run.StateMachine != nil && run.StateMachine.Snapshot().Phase == runtime.RunRunning {
		if err := run.StateMachine.WaitTool(); err != nil {
			return false, err
		}
		waitingTool = true
	}
	if waitingTool {
		defer func() {
			if !run.StateMachine.Snapshot().Phase.Terminal() && run.StateMachine.Snapshot().Phase != runtime.RunRunning {
				_ = run.StateMachine.Resume()
			}
		}()
	}
	execCtx := tool.WithTransactionObserver(ctx, newRuntimeToolTransactionObserver(run))
	outcomes, err := r.cfg.Executor.Run(execCtx, calls, r.cfg.Lifecycle.ToolInterceptor(st))

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
		// Framework execution failures have an authoritative structured status.
		// Tool results themselves must provide their own task status metadata.
		if toolMessage.Name == "task" {
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
	if r.cfg.Executor.EndTurnRequested() || st.Take() == lifecycle.DirectiveStop {
		return true, nil
	}
	return false, nil
}

func (r *Runner) toolAvailable(st *lifecycle.State, name string) bool {
	if !slices.Contains(st.ToolSet, name) {
		return false
	}
	definition, err := r.cfg.Registry.Get(name)
	if err != nil || !definition.Deferred {
		return err == nil
	}
	return slices.Contains(st.DisclosedTools, name)
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

func normalizeToolCallIDs(resp *model.Response, runID string, iteration int) error {
	if resp == nil {
		return errors.New("loop: sampler returned no response")
	}
	seen := make(map[string]struct{}, len(resp.Message.ToolCalls))
	for i := range resp.Message.ToolCalls {
		call := &resp.Message.ToolCalls[i]
		if call.ID == "" {
			call.ID = fmt.Sprintf("%s:tool:%d:%d", runID, iteration, i)
		}
		if _, duplicate := seen[call.ID]; duplicate {
			return fmt.Errorf("loop: model returned duplicate tool call id %q", call.ID)
		}
		seen[call.ID] = struct{}{}
	}
	return nil
}
