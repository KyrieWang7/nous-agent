# agent-go Harness 设计文档

> 状态：已实施并完成闭环复核；Go runtime 独立演进，LangGraph 仅作为可选 wire 兼容层
> 日期：2026-08-06
> 目标目录：`agent-go/`
> 模块路径：`github.com/KyrieWang7/nous-agent/agent-go`
> Go 版本：1.25

## 1. 目标与非目标

### 实施边界更新

Go 与 Python 服务可并存，Go 版不要求复刻 LangGraph 的内部状态机、图或
checkpoint 语义。当前稳定边界是前端 SSE 投影：原生 `/api/v1` 与兼容路由
统一输出 `metadata / values / messages / custom / error / end`。Go runtime
内部继续使用 `content_delta / reasoning_delta / tool_start / usage / run_end`
等细粒度事件，并在 HTTP adapter 处完成投影。

### 目标

在 `agent-go/` 从零构建一套 **生产级 Go Agent Harness 库**。内核是自持的 `for` 循环（形态取自 `agentsdk-go` / Claude Code），编排语义归内核；横切关注点归四阶段中间件链（代码风格与链装配方式取自 `nous-agent`）。最外层提供 LangGraph 兼容的 HTTP/SSE 适配器，使现有 Next.js 前端可通过同一组事件语义连接 Go 实现。

### 非目标

- **不引用 `agentsdk-go`**。它是架构参照，不是依赖。全部代码从零实现。
- **不做多租户、不做认证**。身份维度对齐 LangGraph：只有 `thread_id`。表结构不预留 `tenant_id`。
- **不引入图/DAG 编排框架**。内核是 `for` 循环，不是状态机、不是 StateGraph。
- **不做可挂起/可恢复的持久化状态机**（理由见 §3.4）。
- **不做运行时动态加载**（`plugin.Open` / .so）。第三方扩展一律进程外。
- **不复用 LangGraph 的 `checkpoints` 表**。

### 设计来源

| 维度 | 参考实现 | 采纳内容 |
|---|---|---|
| Agent Loop 内核 | `agentsdk-go` `pkg/api/runtime_internal.go::runLoop` | `for` 循环、编排归内核、Stop 续跑与 `StopReinjectionLimit`、`MaxIterations`、token 预算 |
| 中间件阶段模型 | `agentsdk-go` `pkg/middleware` | 四阶段扁平链 + 共享 `State` |
| 工具并发 | `agentsdk-go` `pkg/api/tool_concurrency.go` | 只读且并发安全才分到并发段、信号量限流、级联取消、按序回写 |
| 压缩算法 | `agentsdk-go` `pkg/api/compact.go` | 阈值触发、tool 事务安全切点、`stripToolIO`、摘要替换前半段 |
| Skills 惰性加载 | `agentsdk-go` `pkg/runtime/skills` | L1 元数据 / L2 正文惰性加载 / mtime 热重载 |
| 中间件代码风格 | `nous-agent` `src/agents/middlewares/` | 一文件一中间件、单一职责、可选阶段方法 |
| 链装配与排序 | `nous-agent` `src/agents/middleware_ordering.py` | 声明式 `AnchorAfter` / `AnchorBefore`、装配期冲突与循环检测、终端中间件 |
| 权限模型 | `nous-agent` `src/permissions/` | 5 级模式、工具级覆盖、fail-closed、Prompter |
| Hook 治理 | `nous-agent` `src/hooks/` | 5 事件、外部子进程 + 进程内、deny/rewrite |
| Subagent 形态 | `nous-agent` `src/subagents/executor.py` | **循环内 `task` 工具派发**（而非 `agentsdk-go` 的前置流水线） |
| Swarm | `nous-agent` `src/swarm/` | PG mailbox、Team/Member/Message、Inbox 轮询注入 |
| Run 生命周期与流式 | `nous-agent` `src/core/` + `src/runtime/` | 三通路 EventBus、Run 注册表、断连策略、可插拔事件存储、三桶用量归因 |
| 已验证的 Go 实现与坑 | pace-grid `pacegrid-ai` `docs/AGENT_HARNESS_DESIGN.md` | 压缩持久化水位、`superseded` 转录重写、`message_replace` 补偿、SSE 重连三约束 |

`agentsdk-go` 与 `nous-agent` 的编排哲学同源（主循环 + 切面 + subagent + SKILL.md），差异在于前者内核持有编排、后者把编排交给了 vendored 的 `langchain.agents.create_agent`。本设计取前者的内核，取后者的中间件组织方式。

## 2. 分层架构与包结构

```
agent-go/
├── go.mod                      github.com/KyrieWang7/nous-agent/agent-go
├── Makefile                    check / test / race / lint / fmt / coverage / migrate
├── config.yaml                 段名与 agent/config.yaml 同构
├── cmd/
│   ├── agentd/                 LangGraph 兼容 HTTP/SSE 服务
│   └── agentctl/               单次 run · skill 校验 · 迁移执行
├── pkg/                        ★ 库本体，可被外部 import
│   ├── message/                Message · History(Watermark/Since/Replace) · Trimmer
│   ├── model/                  单一 Model 接口 · Request/Response · 流式事件
│   │   └── provider/           oai-compatible · anthropic
│   ├── modelrouter/            分层 · 降级链 · 熔断 · 错误分类恢复
│   ├── tool/                   Definition · Metadata · Registry · Executor
│   │   └── builtin/            内置工具
│   ├── middleware/             契约 · Chain · 锚点排序 · State
│   │   └── builtin/            内置中间件，一文件一个
│   ├── loop/                   ★ for 循环内核，唯一编排者
│   ├── prompt/                 系统提示装配（缓存边界）
│   ├── compaction/             单路径 LLM 摘要
│   ├── permission/             5 级模式 · Policy · Prompter
│   ├── hooks/                  5 事件 · 外部子进程 · 进程内 · Runner
│   ├── guardrail/              Provider 接口 · fail-closed
│   ├── skill/                  三级渐进披露 · Registry · Matcher · mtime 热重载
│   ├── subagent/               Definition · Manager · 嵌套 Runner · 异步任务
│   ├── swarm/                  Team · Mailbox · Spawner
│   ├── sandbox/                Provider 接口 + local / docker / remote
│   ├── mcp/                    客户端 · 工具注册
│   ├── memory/                 事实抽取 · 注入
│   ├── runtime/                Run 生命周期：EventBus · Registry · EventStore · Journal
│   ├── storage/                Postgres 实现 · Store 接口
│   ├── telemetry/              OTel span · 三桶归因
│   ├── config/                 YAML 加载与校验（应用层，内核不依赖它）
│   └── harness/                门面：Options → 装配好的 Runner
├── internal/
│   └── langgraphapi/           LangGraph wire 格式唯一所在地
├── migrations/                 golang-migrate NNN_description.up.sql
└── test/                       集成 · 契约 · 基准
```

### 依赖纪律

CI 用 `go list -deps` 断言，违反即失败：

- 编排只在 `pkg/loop`。`pkg/loop` 之下的包**不得** import `pkg/loop`。
- `pkg/runtime` 只被 `Publish`。**内核只发布事件，不感知订阅者**，单向依赖。
- `pkg/sandbox` 只做隔离，不得生长出审批或权限判定职责。
- `pkg/model` 收敛为单一 `Model` 接口，供应商适配到它，不分叉运行时契约。
- `pkg/middleware` 不得 import `pkg/middleware/builtin`（契约不依赖实现）。
- `pkg/config` 只被 `cmd/` 与 `pkg/harness` 的装配函数 import。`pkg/loop` 及其余内核包**不得** import `pkg/config`（内核只吃 typed Options，不认识 YAML）。
- `internal/langgraphapi` 单向依赖 `pkg`，反向禁止。`pkg` 不得 import `internal`。
- `cmd/` 之外不得出现 `os.Exit`。库代码不得 `panic`（已文档化的不可能不变量除外）。

## 3. Agent Loop 内核

### 3.1 循环规范

内核是一个 `for` 循环。控制流由模型输出决定：请求工具则继续，不请求工具则进入停止判定。

```go
// pkg/loop/loop.go
func (r *Runner) Run(ctx context.Context, prep PreparedRun) (*Result, error) {
    st := middleware.NewState(prep)

    if err := r.chain.Execute(ctx, middleware.StageBeforeAgent, st); err != nil {
        return st.Result(), err
    }
    defer r.chain.ExecuteQuiet(ctx, middleware.StageAfterAgent, st)   // 监听类，错误只记日志

    var last *model.Response
    stopReinjections := 0

    for iteration := 0; ; iteration++ {
        st.Iteration = iteration

        if err := ctx.Err(); err != nil {                       // 1 取消
            return st.Result(), err
        }
        if err := r.limits.Check(iteration, st); err != nil {   // 2 不可删的安全上限
            return st.Result(), err
        }

        compacted, err := r.compactor.MaybeCompact(ctx, st.History, r.summaryModel) // 3 采样前压缩
        if err != nil {
            return st.Result(), err
        }
        st.Compacted = st.Compacted || compacted

        snapshot := r.trimmer.Trim(st.History.All())            // 4 裁剪
        st.ToolSet = r.toolsForTurn(st)                         // 5 本轮工具集

        st.ModelInput = &model.Request{
            Messages: convert(snapshot),
            Tools:    st.ToolSet,
            System:   st.SystemPrompt,
        }

        if err := r.chain.Execute(ctx, middleware.StageBeforeAgent2, st); err != nil { // 6 采样前切面
            return st.Result(), err
        }
        if st.Directive == middleware.DirectiveStop {
            return st.Result(), nil
        }

        resp, streamed, err := r.router.Sample(ctx, st)         // 7 采样（分层/降级/错误分类恢复）
        if err != nil {
            return st.Result(), err
        }
        last = resp
        st.ModelOutput = resp
        st.History.Append(assistantFrom(resp))                  // 8 落存回复

        if err := r.chain.Execute(ctx, middleware.StageAfterAgent2, st); err != nil { // 9 采样后切面
            return st.Result(), err
        }

        r.publishReply(ctx, st, streamed)                       // 9b 切面通过后才对外发布

        if st.Directive == middleware.DirectiveStop {            // Clarification 等提前结束
            return st.Result(), nil
        }
        if st.Directive == middleware.DirectiveContinue {        // Schema 校验失败重试等
            continue
        }

        if len(resp.Message.ToolCalls) > 0 {                     // 10 执行工具后继续
            if err := r.executor.Run(ctx, st, r.chain); err != nil {
                return st.Result(), err
            }
            if st.Directive == middleware.DirectiveStop {
                return st.Result(), nil
            }
            continue
        }

        blocking, err := r.stopGate.Evaluate(ctx, resp.StopReason, st) // 11 停止判定
        if err != nil {
            return st.Result(), err
        }
        if blocking != "" {
            stopReinjections++
            if stopReinjections > r.stopReinjectionLimit {
                return st.Result(), fmt.Errorf("loop: stop blocked: %s", blocking)
            }
            st.History.Append(message.Message{
                Role:    "user",
                Content: fmt.Sprintf("[System] 停止被拦截：%s。请先处理该问题。", blocking),
            })
            continue
        }
        return st.Result(), nil                                  // 12 回合结束
    }
}
```

> 说明：上文 `StageBeforeAgent2` / `StageAfterAgent2` 是占位写法，实际阶段命名见 §4.1（`StageBeforeModel` / `StageAfterModel`）。`StageBeforeAgent` / `StageAfterAgent` 每次 run 各执行一次，`StageBeforeModel` / `StageAfterModel` 每轮迭代执行一次。

### 3.2 阶段职责与失败语义

| 步 | 职责 | 失败语义 |
|---|---|---|
| 1 | 取消检查 | 返回 `ctx.Err()`，run 置 `cancelled` |
| 2 | 安全上限（轮次 / 墙钟 / 成本） | 返回 `ErrMaxIterations` / `ErrDeadline` / `ErrBudgetExhausted`，对外为"任务过于复杂"或"额度不足" |
| 3 | 采样前压缩 | 摘要模型不可用时回退结构化占位摘要，不终止回合；摘要器返回 error 才终止。置位 `st.Compacted` |
| 4 | 历史裁剪 | 纯函数，不失败 |
| 5 | 本轮工具集 | 按权限 ∩ 组 ∩ 已激活 skill 的 `allowed-tools` − 未披露延迟工具（见 §7.3） |
| 6 / 9 | 中间件切面 | 按类别分级（见 §17），治理类短路，监听类只记日志 |
| 7 | 采样 | 分类恢复：上下文超限→强制压缩重发（上限 2 次，**已流出内容的请求不重试**）；429→退避；供应商故障→降级链；均失败→"暂时不可用" |
| 9b | 对外发布回复 | 非流式时在切面之后才发 `content_delta`；流式时若切面改写了内容，补发 `message_replace` |
| 10 | 工具执行 | 权限拒绝 / 澄清拦截→结束回合；工具自身错误→转 error 工具结果回灌，不杀 run |
| 11 | 停止判定 | 续跑受 `StopReinjectionLimit` 约束，防死循环 |

### 3.3 不变量

- **模型不请求工具即回合结束**，除非停止门显式拦截。这是唯一的正常终止判据。
- **对话历史是唯一状态**。不引入并行隐藏状态机。只有两种操作可以改写既有历史：压缩（`History.Replace`）与护栏对最后一条回复的撤回（`History.ReplaceLastAssistant`）。后者是必需的——护栏判定发生在回复入库之后，若只改 `ModelOutput`，被拦截的文本会留在持久化转录和下一轮请求里。
- **"本轮新增了什么"用水位而不是下标**。`History.Watermark()` / `Since()` 基于累计 append 次数从尾部回溯。压缩会在一轮之内缩短转录，保存的下标会指向错误消息甚至越界。
- **每轮重新计算工具集**。权限、plan 模式、skill 收窄、subagent 白名单都在此生效。
- **循环内不做业务写入**。落库通过工具或回合结束后的持久化层完成。
- **安全上限不可删**。`MaxIterations`、`ctx` 取消、墙钟 deadline、成本天花板在内核，不做成中间件。`LoopDetection` 作为可删的启发式检测叠在上面。

### 3.4 为什么不做可挂起状态机

`nous-agent` 的 `ClarificationMiddleware` 并未使用 LangGraph 的 `interrupt()`。它返回 `Command(update={"messages": [tool_message]}, goto=END)`，即**提前结束本次 run**，把问题作为 ToolMessage 留在历史里由前端渲染，用户下一条消息开一个新 run 接着跑。没有挂起态、没有 resume、没有 pending interrupt。

同时 `GET /threads/{id}/state` 的 `next` 与 `tasks` 是硬编码空值，`POST /threads/{id}/state` 实际只用于改标题。**前端不依赖真正的 checkpoint 语义。**

结论：纯 `for` 循环内核足够。澄清 = 终端中间件提前结束回合（`DirectiveStop`）。这是本次移植最容易误判的一处。

## 4. 中间件

### 4.1 契约

采用**可选接口**而非胖接口——中间件只实现自己需要的阶段，`Chain` 靠类型断言收集。25 个内置实现里没有一个需要写空方法。

```go
// pkg/middleware/middleware.go
type Middleware interface{ Name() string }

// 每次 run 各一次
type BeforeAgent interface{ BeforeAgent(context.Context, *State) error }
type AfterAgent  interface{ AfterAgent(context.Context, *State) error }

// 每轮迭代各一次
type BeforeModel interface{ BeforeModel(context.Context, *State) error }
type AfterModel  interface{ AfterModel(context.Context, *State) error }

// 工具调用两侧
type BeforeTool interface{ BeforeTool(context.Context, *State) (Decision, error) }
type AfterTool  interface{ AfterTool(context.Context, *State) error }

type Decision struct {
    Deny    bool
    EndTurn bool             // 澄清拦截：结束回合
    Reason  string
    Args    json.RawMessage  // 非 nil 则改写工具入参
}
```

`BeforeTool` 返回决策而不是纯 `error`，是为了覆盖 `nous-agent` 六个工具类中间件的真实语义（权限拒绝、Hook 改写入参、澄清拦截），而不必引入环绕链。`AfterTool` 可改写 `State.ToolResult`（输出预算外部化、Hook feedback 追加、执行期错误转 error ToolMessage）。

### 4.2 State

内置链需要的一切都是**具体字段**，不是 map 键。`Values` 保留但树内代码一次都不用。

```go
type State struct {
    ThreadID, RunID, AssistantID string
    Iteration    int
    SystemPrompt string
    History      *message.History

    ModelInput  *model.Request
    ModelOutput *model.Response
    ToolCall    *model.ToolCall
    ToolResult  *tool.Result

    Directive Directive        // Proceed / Stop / Continue

    Compacted       bool
    ToolSet         []string   // 工具名而非 Definition：Registry.Schemas 接受名字，
                               // 复制定义只会造出两份真相
    DisclosedTools  []string
    ActivatedSkills []string
    Journal         *runtime.Journal
    Sandbox         sandbox.Handle
    Bus             runtime.Publisher

    Values map[string]any      // 仅供树外第三方中间件
}
```

并发工具调用时 `State` 按调用克隆（`CloneForTool`）；`History` 与 `Journal` 共享且带锁。

### 4.3 排序机制

基础链在 `middleware.Build(cfg) ([]Middleware, error)` 中显式装配。扩展中间件声明锚点：

- `AnchorAfter(X)` → X 之后；`AnchorBefore(X)` → X 之前；二者互斥。
- 未声明锚点 → 插到终端中间件之前。
- 终端固定为 `Clarification`（澄清拦截必须看到最终输出）。
- **冲突、成环、锚点不可达在装配期返回 error**，进程起不来。

两条硬约束：

- `StageAfterModel` / `StageAfterAgent` **逆注册顺序执行**（像 `defer` 展开）。`nous-agent` 依赖 LangChain 的这个反序行为让 `SafetyFinishReason` 先于 `LoopDetection` 观察原始输出，代码里靠注释维持。Go 侧写进契约文档 + 顺序快照测试，不留隐式知识。
- `Build()` 必须有一个测试断言"配置里声明启用的每个中间件都出现在最终链里"。这条是为了避免 `nous-agent` 那两处死缝：`agents/features.py::RuntimeFeatures` 定义了从未被读，`make_lead_agent` 的 `extra_middleware` 参数从未被传。**未知名字或未生效声明一律启动失败，不静默跳过。**

### 4.4 中间件矩阵

`来源` 列：A = `agentsdk-go` 已有能力，N = `nous-agent` 移植。

| 顺序 | 中间件 | 阶段 | 来源 | 职责 |
|---|---|---|---|---|
| 1 | `ThreadData` | BeforeAgent | N | 装载 thread 元数据与工作目录 |
| 2 | `Uploads` | BeforeAgent | N | 注入上传文件引用 |
| 3 | `Sandbox` | BeforeAgent | N | 获取沙箱句柄，`lazy_init` 延后到首次工具调用 |
| 4 | `GuardrailInput` | BeforeAgent | N | 输入侧内容安全判定 |
| 5 | `DynamicContext` | BeforeModel | N | 注入动态上下文提醒（跨压缩救援） |
| 6 | `DanglingToolCall` | BeforeModel | N | 修复历史中悬空 tool_call，防请求非法 |
| 7 | `SkillActivation` | BeforeModel | N | 召回并注入 SKILL.md，收窄工具集 |
| 8 | `DeferredToolFilter` | BeforeModel | A | 过滤延迟工具 schema，减小请求体 |
| 9 | `Todo` | BeforeModel | N | plan 模式任务清单 |
| 10 | `InboxPoller` | BeforeModel | N | Swarm 收件箱轮询注入 |
| 11 | `ViewImage` | BeforeModel | N | 视觉模型下注入图片内容 |
| 12 | `Memory` | BeforeModel / AfterAgent | N | 读取注入 + 回合结束排队更新 |
| 13 | `Permission` | BeforeTool | N | 5 级权限判定，`Deny` 返回错误 ToolMessage |
| 14 | `Hook` | BeforeTool / AfterTool | N | 5 事件治理，pre 可 deny/改写，post 可追加 feedback |
| 15 | `SandboxAudit` | BeforeTool | N | bash 命令安全审计 |
| 16 | `ToolErrorHandling` | AfterTool | N | 工具异常转 error ToolMessage，不中断回合 |
| 17 | `ToolOutputBudget` | AfterTool | N | 超大输出外部化/截断，防炸上下文 |
| 18 | `Clarification` | BeforeTool（终端） | N | 拦截 `ask_clarification`，`EndTurn` 结束回合 |
| 19 | `SafetyFinishReason` | AfterModel | N | 供应商安全终止时清空截断的 tool_calls |
| 20 | `LoopDetection` | AfterModel | N | 检测重复工具调用循环并打断 |
| 21 | `SubagentLimit` | AfterModel | A | 截断超额并行 `task` 调用 |
| 22 | `GuardrailOutput` | AfterModel | N | 输出侧判定，命中则 `ReplaceLastAssistant` |
| 23 | `TokenUsage` | AfterModel | N | 记录 Token/延迟/模型到 Journal |
| 24 | `Title` | AfterAgent | N | 首轮生成会话标题 |

排序要点：`ToolSet` 计算在内核（步 5），所以 `SkillActivation` 与 `DeferredToolFilter` 都在 `BeforeModel` 且必须晚于内核赋值——`Chain` 在步 6 执行，天然满足。`SafetyFinishReason` 必须在 `LoopDetection` 之后注册（逆序执行使其先观察原始输出）。

## 5. 注入接口

这些是**依赖**不是切面：有多种实现但运行时只需要一份，换实现而不是加切面。

| 接口 | 包 | 默认实现 | 备选 |
|---|---|---|---|
| `model.Model` | `pkg/model` | oai-compatible | anthropic · faux（测试） |
| `modelrouter.Router` | `pkg/modelrouter` | 分层 + 降级 + 熔断 | 单模型直通 |
| `compaction.Compactor` | `pkg/compaction` | 单路径 LLM 摘要 | noop |
| `message.Trimmer` | `pkg/message` | token 上限裁剪 | noop |
| `tool.Executor` | `pkg/tool` | 并发分段执行器 | 全串行 |
| `sandbox.Provider` | `pkg/sandbox` | local | docker · remote(AIO) |
| `skill.Registry` | `pkg/skill` | 文件系统 + mtime 热重载 | embed.FS |
| `subagent.Manager` | `pkg/subagent` | 嵌套 Runner | — |
| `runtime.EventBus` | `pkg/runtime` | 三通路（内存 ×2 + Redis Stream） | 仅内存 |
| `runtime.RunRegistry` | `pkg/runtime` | Redis 权威 | 进程内（仅单实例） |
| `runtime.RunEventStore` | `pkg/runtime` | Postgres | 内存（测试） |
| `storage.Store` | `pkg/storage` | Postgres | 内存（测试） |
| `permission.Policy` | `pkg/permission` | 5 级模式引擎 | allow-all |
| `guardrail.Provider` | `pkg/guardrail` | 关键词 + 可选 LLM | noop |

内存实现必须与 SQL 实现遵守**同一份游标语义**。忽略 `afterID` 的测试替身会让分页与重连测试全绿，而真实路径永远返回同一页。

## 6. 消息与上下文

### 6.1 History

```go
type History struct {
    mu       sync.RWMutex
    msgs     []Message
    appends  uint64          // 累计 append 次数，水位基准
}

func (h *History) All() []Message
func (h *History) Append(Message)
func (h *History) Replace([]Message)             // 压缩专用
func (h *History) ReplaceLastAssistant(Message)   // 护栏专用
func (h *History) Watermark() uint64
func (h *History) Since(uint64) []Message         // 从尾部回溯，压缩安全
func (h *History) TokenCount() int
```

### 6.2 压缩

**单一路径 LLM 摘要**。`nous-agent` 曾并行跑 `SummarizationMiddleware` + `CompactionMiddleware` 双层压缩，实践中互相干扰已移除；本设计不重犯。

触发：`tokenCount / limit >= threshold`（默认 0.8），且 `msgCount > preserveCount`（默认 10）。

切点安全：`cut = len - preserveCount`，然后扫描 tool 事务跨度（assistant 带 tool_calls 起、连续 tool 消息止），若 `cut` 落在事务中间则前移到事务起点。摘要输入先 `stripToolIO`（剔除 tool 消息、清空 `ToolCalls` 与 reasoning），避免把工具 JSON 喂给摘要模型。摘要走 fast 层模型，`MaxTokens` 封顶，结果作为一条 `system` 消息 + 保留窗口 `Replace` 回 History。

摘要输入按条数与总长封顶——**压缩比不压缩更贵是真实失效模式**。摘要器故障回退结构化占位摘要，不让长会话彻底不可用。

## 7. 工具体系

### 7.1 定义与元数据

```go
type Definition struct {
    Name        string
    Group       string          // web · file:read · file:write · bash · ...
    Description string
    Schema      json.RawMessage
    Deferred    bool            // 默认不披露，由 skill 声明后才进请求
    Metadata    Metadata
}

type Metadata struct {
    IsReadOnly        bool
    IsConcurrencySafe bool
    RequiresSandbox   bool
    Destructive       bool      // 永不自动执行，需审批
}
```

### 7.2 并发分段

`partitionSegments` 把一批 tool_calls 切成"连续只读且并发安全"段（并发执行）与"单个写操作"段（独占执行），段之间严格顺序。并发段用信号量限流（`ToolConcurrency`），`groupCtx` 级联取消，执行期不写 History，跑完按原始 index 顺序回写。

判据是**工具元数据**而不是模型意图：只有 `IsReadOnly && IsConcurrencySafe` 才并发。这比让工具作者自行声明 `sequential` 更安全。

### 7.3 每轮工具集

```
基础集 = 启用组的默认工具
      ∩ 权限模式允许集
      ∩ 已激活 skill 的 allowed-tools（若有 skill 激活）
      − 未被已激活 skill 声明的 Deferred 工具
      ∩ subagent 白名单（若在 subagent 上下文内）
```

`SkillActivation` **只能披露已注册且已通过权限校验的延迟工具，不能借 skill 获得新权限**。`Destructive` 工具永不注册为可自动调用。

### 7.4 输出预算

超过 `externalize_min_chars`（默认 12000）的工具输出持久化到对象存储/磁盘，模型只看到 head + tail 预览 + 引用句柄。磁盘不可用时退化为 head+tail 截断（`fallback_max_chars`）。`read_file` 类工具在豁免名单。

## 8. 权限、Hook、护栏

### 8.1 权限（5 级）

```
read_only            仅只读工具
workspace_write      只读 + 工作区内写
prompt               高风险工具需用户确认（经 Prompter）
allow                全部放行（默认）
danger_full_access   完全不受限
```

工具级覆盖：`tool_overrides: { bash: danger_full_access }`。判定 `authorize(toolName, toolInput, prompter)`，**内部错误一律 deny（fail-closed）**。拒绝返回错误 ToolMessage 让模型改换方案，不抛异常杀 run。

### 8.2 Hook（5 事件）

`pre_tool_use` · `post_tool_use` · `post_tool_use_failure` · `subagent_start` · `subagent_end`。

两种执行方式：外部子进程（stdin 收 JSON payload，exit code 表决，非零 stderr 转 feedback）与进程内注册函数（编译期注册表，按名字引用）。`HookRunner` 顺序执行，`deny` 短路后续 hook。pre hook 可通过 `Decision.Args` 改写工具入参。

### 8.3 护栏

`GuardrailProvider` 接口，`fail_closed: true` 默认。输入侧命中则不进模型，安全话术作为 assistant 轮写入转录并以 `content_delta` 下发（不是 `error` 事件），run 记为 `completed`。输出侧命中则同时改写 `ModelOutput` 与转录中最后一条 assistant，内核随后发 `content_delta`（未流式）或 `message_replace`（已流式）。

判定结果是**结构化字段**（`pass` / `review` / `block`），随 run 状态与 `run_end` 事件下发，客户端据此分支。字段缺失必须 fail-closed。**不允许靠匹配回答文案推断**——拒答话术属于护栏、会独立变更，客户端一旦匹配失败就会把拒答当正常回答展示。

## 9. Skills

### 9.1 三级渐进披露

| 级别 | 内容 | 时机 | 成本 |
|---|---|---|---|
| L1 元数据 | `name` + `description`（YAML frontmatter） | 注册表装载时全量读取 | 每 skill 数十 token |
| L2 正文 | SKILL.md body | 命中召回、首次激活时惰性加载 | 数百至数千 token |
| L3 附属资源 | `references/` `scripts/` `templates/` | 模型显式读取时 | 按需 |

L2 惰性句柄按文件 mtime 热重载，上传新版 skill 无需重启。`Registry.Invalidate` 后重新读文件。

### 9.2 召回与注入

1. 构造 `ActivationContext`（用户输入 + 会话摘要）。
2. `Registry.Match` 基于 L1 匹配，按 `Priority` 排序，`MutexKey` 互斥去重。
3. 命中项加载 L2 注入为隐藏消息，位置在压缩类处理之后，避免当轮被摘要掉。
4. 按 `allowed-tools` 收窄工具集，允许披露该 skill 声明的 `Deferred` 工具但不扩大权限。
5. **同一 thread 内只注入一次**：转录里已有 `[技能已激活：<name>]` 标记则跳过。会话跨多个 run，同一 skill 通常每轮都命中，重复注入等于每轮白烧几百 token。**跳过注入时仍须应用工具收窄与延迟披露**，否则工具限制会在下一轮悄悄失效。
6. 记录 `st.ActivatedSkills` 供 trace 与用量归因。

请求可通过 `ForceSkills` 绕过匹配强制激活，只在首轮注入。斜杠激活（`/skill-name <task>`）复用同一路径。

Skill 安全扫描：禁止引用宿主路径、禁止声明本地 FS/shell 工具、附属脚本默认不可执行。`fail_open` 可配，默认 false。

## 10. Subagent 与 Swarm

### 10.1 Subagent

**采用 `nous-agent` 的循环内 `task` 工具形态**，不用 `agentsdk-go` 的 `prepare()` 前置流水线。理由：由模型自己决定何时分派，比在请求前改写 prompt 灵活得多，也是 Claude Code 的形态。

每个 subagent 嵌套一个 `loop.Runner`：独立 History 与上下文窗口、最小中间件链（`ThreadData` + `Sandbox` lazy，继承父沙箱句柄）、`recursion_limit = config.MaxTurns`、流式回收中间结果。

- `Context` 携带派生的权限副本，`RestrictTools` **只能收窄不能放宽**。
- 同步 `Dispatch` + 异步 `DispatchAsync` + `TaskStatus`（进 Run 注册表，跨进程可查）。
- 并发上限默认 3，`SubagentLimit` 中间件截断超额调用。
- 用量回灌父 Journal 的 `subagent` 桶，按 `subagent:<name>:<toolCallID>` 去重。**不回灌则 `subagent_tokens` 永远是 0，而且 run 可以靠派发绕过预算。**
- 事件 `subagent_start` / `subagent_result` 发布到父 run 事件流。
- 内置 5 种：`general-purpose` · `explore` · `plan` · `bash` · `verification`。

### 10.2 Swarm

复用现有 PG mailbox 的产品语义，但 Go 与 Python 是相对独立的运行时和
持久化边界。Go 使用 `agent_swarm_teams` · `agent_swarm_team_members` ·
`agent_swarm_messages` · `agent_swarm_message_receipts`；迁移期 Python legacy
继续使用 `swarm_*`。两边保持工具与事件契约一致，不支持在同一个 team 中混跑；
需要迁移存量 team 时走显式迁移程序，不能让两个运行时同时写一组表。

广播仍保存为单条 `to_agent='*'` 消息，通过逐成员 receipt 独立消费；发送者
不会收到自己的广播，发送后加入的成员不会收到历史广播。`TeamManager` +
`Mailbox` + `Spawner`（复用 `subagent.Manager`）。`InboxPoller` 在
`BeforeModel` 轮询注入。工具：`team_create` · `team_delete` · `send_message` ·
`list_teammates`。

层级固定为一层（主 agent + worker），不做自由协商多 agent。

## 11. 模型层

### 11.1 单一接口

```go
type Model interface {
    Complete(ctx context.Context, req Request) (*Response, error)
    Stream(ctx context.Context, req Request) (StreamReader, error)
    Info() Info    // 名称 · 上下文长度 · 是否支持 thinking/vision/结构化输出
}
```

供应商适配到这个接口，不分叉运行时契约。day 1 实现：OpenAI 兼容（覆盖 deepseek / minimax / qwen / gpt-4o，thinking 走 `extra_body` 透传）+ Anthropic 原生 + `faux`（测试用脚本化确定性响应）。

### 11.2 Router

分层 `fast` / `standard` / `vision`；降级链 `主供应商 → 同层备用 → 降级到 fast → "暂时不可用"`；熔断按 `供应商 × 模型` 维度，连续失败达阈值打开、半开探测恢复；429 走指数退避不立即降级；上下文超限触发压缩后重发不计入失败。

**已流出内容的请求不重试**——重试会导致客户端看到两段回答。

## 12. Run 生命周期与流式

内核只 `Publish`，不感知订阅者。这一层解决"一次 run 如何被观察、恢复、取消与计费"。

### 12.1 Run 实体

```
Thread（会话，长期）
   └── Run（单次执行，短期）── 事件流 / 遥测 / 用量归因的单位
```

### 12.2 三通路 EventBus

| 通路 | 载体 | 语义 | 用途 |
|---|---|---|---|
| 实时 | 每订阅者有界 channel | **允许丢失**，满则丢最旧 | SSE 实时推送 |
| 缓冲 | 每 run 环形缓冲（500 条） | 尽力保留 | 秒级断线重连回放 |
| 持久 | Redis Stream + TTL | 精确 | `Last-Event-ID` 恢复、跨实例订阅 |

`Publish` **非阻塞**，Redis 写失败降级为仅内存，**不阻塞主循环**。事件 ID 单调递增。**Redis Stream 的 entry ID 用 harness 序号（`<seq>-1`）**，使 `Last-Event-ID` 成为真正的 seek——从流头 `XRANGE` 再按游标过滤的实现，游标一旦越过第一页就永远返回空。

事件分类 `trace` / `usage` / `audit`。异步批量 flush，队列满时丢 `trace`、保留 `audit`（非可丢弃事件最多阻塞 2 秒，超时记 error 而不是静默丢弃）。

### 12.3 Run 注册表

以 Redis 为权威（Hash 存句柄元数据 + Pub/Sub 广播取消信号），进程内仅缓存本实例持有的 `context.CancelFunc`。`nous-agent` 的 `src/core/task_registry.py` 与 `src/core/state.py` 是进程内 dict，多实例下取消失效、状态不一致，**本设计不采纳**。

`OnDisconnect` 策略：

| 策略 | 适用 | 行为 |
|---|---|---|
| `cancel` | 即时问答 | SSE 断开且无其他订阅者时取消 run，省成本 |
| `continue` | 长任务 | 断开后继续跑，结果落库，客户端重连或轮询获取 |

长跑 run 定期刷新注册表 TTL，避免超时后取消命令找不到目标。

### 12.4 事件类型

```
run_start · message_start · content_delta · message_replace · reasoning_delta
tool_start · tool_result · skill_activated · subagent_start · subagent_result
guardrail_blocked · usage · message_stop · run_end · error
```

`message_replace` 是流式的必要补丁：已流出的 token 无法收回，护栏改写回复后必须让客户端整段替换。它归 `audit` 类，**不可在缓冲压力下丢弃**——丢掉它等于把被拦截的文本留在用户屏幕上。

工具调用轮与最终回答保持不同可见性：assistant 工具调用轮完整保留在内部转录供下一轮推理，但不作为用户历史消息返回。

### 12.5 SSE 重连三约束

- **先订阅再回放**。订阅在读取回放之前完成，否则两步之间发布的事件整条丢失。
- **回放分页到取空**。token 级流式下一轮问答就能产生上千条 `content_delta`，单次取 200 条或设总量上限会静默截断，客户端既拿不到回答尾部也拿不到 `run_end`。
- **重叠按 ID 去重**。先订阅的代价是回放与实时通路重叠，不去重客户端会把同一段 delta 追加两次。游标只前进，实时事件 ID 不大于游标即丢弃。

重连前先查 `GET /threads/{tid}/runs/{rid}` 探活，run 已结束则直接读历史，不建立 SSE。定期发送注释帧保活。

## 13. 持久化

### 13.1 表

自有 Go schema，`golang-migrate` 管理，`NNN_description.up.sql` 命名，不复用 LangGraph 的 `checkpoints`。

```sql
thread            (id, title, assistant_id, model_name, state, metadata, created_at, updated_at)
message           (id, thread_id, seq, role, content, tool_calls, tool_result,
                   superseded BOOLEAN NOT NULL DEFAULT FALSE, created_at,
                   UNIQUE (thread_id, seq))
run               (id, thread_id, status, on_disconnect, model_name, risk_level,
                   started_at, completed_at, duration_ms)
run_event         (id BIGSERIAL, run_id, event_type, category, content, metadata, created_at)
run_completion    (run_id, thread_id, status, iterations, llm_call_count,
                   input_tokens, output_tokens, lead_tokens, subagent_tokens,
                   middleware_tokens, cost_micros, duration_ms, completed_at)
agent_swarm_teams / agent_swarm_team_members / agent_swarm_messages /
agent_swarm_message_receipts
memory_fact       (id, thread_id, fact, confidence, created_at)
skill_usage       (skill_name, use_count, view_count, last_activity_at)
```

三表分工：`run` 是生命周期，`run_event` 是明细（`trace` 类按 TTL 归档），`run_completion` 是汇总（长期保留，报表读它，避免扫明细表）。

迁移纪律遵循 `golang-migrate`：已合入的迁移不得修改/重命名/重编号；应用启动**不得**自动执行 DDL，迁移是独立步骤（`cmd/agentctl migrate` 或 `make migrate`）；字段变更走 expand–migrate–contract；不提供破坏性自动 down migration。

### 13.2 压缩的持久化

必须分两条路：

| 情况 | 写法 |
|---|---|
| 本轮未压缩 | `AppendMessages(history.Since(watermark))`，只追加新增 |
| 本轮发生压缩 | `ReplaceTranscript(history.All())`：原活跃行标 `superseded = TRUE`，当前活跃转录整体写入 |

三条不变量：

- `LoadHistory` 与 `ListVisibleMessages` **只读 `superseded = FALSE`**。否则压缩会在下一轮被撤销，摘要的钱每轮重付。
- 被替换的行**只标记不删除**：它们是"模型当时看到了什么"的审计记录。`seq` 跨压缩持续递增。
- 判据是 `Result.Compacted`，**不是"消息数变少了"这种推断**。

这条路在 pace-grid 上坏过一次：`baseline` 存的是切片下标，压缩后 `len(history) < baseline`，于是整轮问答都不入库，DB 与内存转录从此分叉。

## 14. LangGraph 兼容适配器

`internal/langgraphapi` 是**唯一**知道 wire 格式的地方。它从 `thread` + `message` 合成 LangGraph 响应，`next` / `tasks` 沿用空值（前端不依赖）。

需复刻的端点（对齐 `agent/src/api/`）：

```
GET    /info                                     GET  /ok
POST   /assistants/search                        GET  /assistants/{id}/graph
GET    /assistants/{id}/schemas
POST   /threads                                  GET  /threads/{tid}
PATCH  /threads/{tid}                            DELETE /threads/{tid}
POST   /threads/search
GET    /threads/{tid}/state                      POST /threads/{tid}/state
GET    /threads/{tid}/history                    POST /threads/{tid}/history
POST   /threads/{tid}/runs/stream                POST /runs/stream
GET    /threads/{tid}/runs                       GET  /threads/{tid}/runs/{rid}
GET    /threads/{tid}/runs/{rid}/stream          POST /threads/{tid}/runs/{rid}/cancel
GET    /threads/{tid}/stream
```

兼容路由接受 Python 客户端使用的 `stream_mode` 字段；稳定 wire 边界是
`metadata / values / messages / custom / error / end` 六类事件及其前端消费所需
字段。Go 内部事件和非契约 payload 可以独立演进，不要求与 Python 逐字节一致
（§18）。

## 15. 用量与遥测

三桶归因（源自 `nous-agent` `src/runtime/journal.py`）：

| 桶 | 归属 | 含义 |
|---|---|---|
| `lead_tokens` | 主 agent | 主循环模型消耗 |
| `subagent_tokens` | `subagent:{name}` | 派发 worker 消耗 |
| `middleware_tokens` | `middleware:{name}` | 系统开销（压缩摘要、标题生成） |

**按模型调用 ID 去重**，防重复回调重复计数。累积异步批量 flush，不阻塞循环。成本折算为 `cost_micros`（整数微单位，避免浮点累积误差）。

per-run 预算：`TokenBudget` + 成本天花板在内核安全上限里（不可删）。无多租户，故不做租户额度。

OTel span 覆盖 run → 每轮采样 → 每次工具调用 → 每次 subagent 派发。审计事件（护栏拦截、权限拒绝、降级）不可被缓冲丢弃。

## 16. 配置

`agent-go/config.yaml`，段名与 `agent/config.yaml` 同构，`use:`（Python dotted path）换成 `provider:`（编译期注册名）：

```
models[]            name · provider · model · base_url · api_key · max_tokens ·
                    supports_thinking · supports_vision · when_thinking_enabled
tool_groups[]       name
tools[]             name · group · provider · 参数
sandbox             enabled · provider(local|docker|remote) · lazy_init
skills              path · container_path · slash_activation_enabled · tool_policy_enabled
skill_security      enabled · fail_open · moderation_model_name
summarization       enabled · trigger{type,value} · keep{type,value} · model_name
tool_output         enabled · externalize_min_chars · preview_head_chars ·
                    preview_tail_chars · fallback_max_chars · exempt_tools
safety_finish_reason enabled
subagents           enabled · max_concurrent · max_turns
swarm               enabled · max_team_size · message_poll_interval · teammate_timeout
memory              enabled · max_facts · fact_confidence_threshold · injection_enabled
title               enabled · max_words · max_chars · model_name
permissions         mode · tool_overrides
hooks[]             event · type(command|inproc) · target · timeout
guardrail           enabled · fail_closed · provider
loop                max_iterations · stop_reinjection_limit · middleware_timeout ·
                    deadline · tool_concurrency · token_budget
runtime             event_buffer_size · redis_stream_enabled · event_ttl ·
                    default_on_disconnect · heartbeat_interval · flush_batch · flush_interval
```

解析顺序：默认值 → 配置文件 → 环境变量 → 请求级覆盖（仅允许项，如模型名、是否流式、`force_skills`）。

**`pkg/config` 是应用层**：YAML 加载与校验在这里，`pkg/harness` 及内核只接受 typed Options，不依赖 YAML。API key 只从环境变量或密钥管理注入，不入仓库。

启动时校验配置引用的每个名字（provider / tool / hook / middleware / subagent）都已注册，**未知名字直接启动失败**。

## 17. 错误处理与失败语义

### 17.1 中间件错误分级

这是对 pace-grid 那版"所有阶段首个 error 短路"的一处修正——在监听类中间件上短路是错的。

实现时收敛为**两级** `GradeAbort` / `GradeListener`（未声明者默认 `GradeAbort`）。原设计里的"治理类"与"修复类"在链的层面行为完全一致（都短路返回错误），保留两个同义级别只会腐化成用法不一致。真正的差别——治理类失败必须 fail-closed——是中间件自身的逻辑（例如 `Permission` 内部错误一律 deny），不是链的职责。

| 类别 | 中间件 | 失败语义 |
|---|---|---|
| `GradeAbort`（默认） | `Permission` · `GuardrailInput/Output` · `SandboxAudit` · `DanglingToolCall` · `SafetyFinishReason` · `DeferredToolFilter` | 短路并返回包装后的错误，终止回合 |
| `GradeListener` | `Title` · `Memory` · `TokenUsage` · `SubagentLimit` | **只记日志，不影响回合** |

`Title` 生成失败把一个已经成功的回合判死，是纯负收益。

### 17.2 其余

- 模型错误分类恢复见 §11.2。
- 工具错误恒转为 error ToolMessage 回灌，不杀 run。
- 每个工具调用与每个 run goroutine 都有 `recover`，panic 转为 error 结果并记审计事件。
- EventBus 故障不阻塞循环。
- 摘要器故障回退占位摘要。
- 中间件错误包装为 `middleware %s failed: %w`，每阶段独立超时。

## 18. 测试策略

- **`faux` model provider 是硬要求**。脚本化确定性响应，`loop` / `chain` / `subagent` 的全部测试不碰真实 API、不花钱、不需要 key。
- 表驱动单测，`t.Parallel()` 尽量开。
- **`middleware.Build()` 顺序快照测试** + "配置声明的中间件都在最终链里"断言。
- **语义契约测试**：成功、失败、原生与兼容路由覆盖
  `metadata / values / messages / custom / error / end`；对前端依赖的消息、任务、
  风险和 token 字段做结构化断言。内部事件类型保持细粒度，不锁定 Python 的
  非契约 payload 或随机 ID。
- 集成测试用 dockertest 起 Postgres + miniredis 起 Redis。
- `-race` 覆盖并发工具执行、EventBus、Journal。
- 压缩持久化专项测试：压缩轮之后 `LoadHistory` 必须能读回那一轮的问答（这是 pace-grid 坏过的路径）。
- SSE 重连专项测试：先订阅后回放、分页到取空、去重，三条各一个测试。
- 默认 `make check` **离线、无网络、无 API key**，与 CI 一致。

## 19. 分期实施

| 里程碑 | 内容 | 验收 |
|---|---|---|
| **M0** | `message` · `model`(+faux) · `tool` · `middleware` · `loop` · `prompt` | 带工具调用的完整回合，全离线可测，`-race` 干净 |
| **M1** | `tool/builtin` · `sandbox`(local/docker) · `permission` · `hooks` · 并发分段 · `compaction` · `modelrouter` | 5 级权限与 5 事件 hook 生效，bash/file 落沙箱，压缩触发正确 |
| **M2** | `runtime`(三通路/注册表/EventStore) · `storage` · `migrations` · `internal/langgraphapi` · `cmd/agentd` | **现有 Next.js 前端指向 agent-go 正常跑**；SSE 契约测试通过；断连策略与重连回放正确 |
| **M3** | `skill` · `subagent` · `mcp` · `memory` · `Todo`/`ViewImage`/`DynamicContext` | 技能三级披露与派发闭环，异步任务跨进程可查 |
| **M4** | `swarm` · `guardrail` · `telemetry` · 进程外插件 | 平台化能力齐，审计事件完整 |

M2 是关键里程碑：它是"能不能替换 Python 版"的分水岭。

## 20. 风险与取舍

| 风险 | 影响 | 缓解 |
|---|---|---|
| 中间件链顺序成为隐式知识 | 改动引入难查 bug | 声明式锚点 + 装配期冲突检测 + 顺序快照测试 + 逆序语义写进契约文档 |
| 配置声明静默不生效 | 功能以为开了实际没开 | 未知名字启动失败 + "声明的中间件都在链里"断言（对治 `nous-agent` 的 `RuntimeFeatures` / `extra_middleware` 死缝） |
| 压缩与持久化不一致 | 回答丢失、DB 与内存分叉、摘要费用每轮重付 | 水位而非下标 + `superseded` 整体重写 + `LoadHistory` 只读活跃行 + 专项测试 |
| 护栏判定晚于回复推流 | 红线文本已展示 | 发布延后到切面之后；已流出则补 `message_replace`（归 `audit` 不可丢） |
| 重连丢事件或重复事件 | 回答尾部消失、文本重复渲染 | 先订阅后回放 + 分页到取空 + 按 ID 去重；Redis entry ID 用 harness 序号 |
| 沿用进程内注册表 | 多实例下取消失效 | Run 注册表 Redis 权威；不做进程内权威缓存 |
| 事件通路阻塞 agent 循环 | 回合卡死 | `Publish` 非阻塞 + Redis 失败降级内存 + 异步批量 flush |
| 安全上限做成可删中间件 | 无上限循环烧穿账单 | 轮次/墙钟/成本上限钉在内核；`LoopDetection` 只是叠加的启发式 |
| subagent 用量不回灌 | 归因永远为 0，且可绕过预算 | 回灌父 Journal，按 `subagent:<name>:<toolCallID>` 去重 |
| 摘要器故障阻塞会话 | 长会话彻底不可用 | 回退结构化占位摘要；摘要输入按条数与总长封顶 |
| 监听类中间件错误判死回合 | 标题生成失败导致回答丢失 | 中间件错误按治理/修复/监听三级分类 |
| SSE 契约与 Python 版有偏差 | 同一前端无法稳定切换服务端 | 六类事件的语义契约测试 + 前端消费回归测试 |
| Go 无运行时反射加载 | Python 的 dotted-path 扩展点无法平移 | 编译期注册表 + 进程外扩展（MCP / 子进程 Hook） |

### 明确不做

- 不引用 `agentsdk-go`，不做图/DAG 编排引擎。
- 不做多租户与认证。
- 不做可挂起/可恢复的持久化状态机。
- 不做 `plugin.Open` 动态加载。
- 不做 swarm 自由协商多 agent（层级固定一层）。
- 不做双压缩路径（`nous-agent` 已验证互相干扰）。
- 不为兼容旧行为保留双实现；演进走版本化契约。

## 21. 参考实现索引

| 主题 | 参考位置 |
|---|---|
| `for` 循环内核 | `agentsdk-go` `pkg/api/runtime_internal.go`（`runLoop`，144 行起） |
| 中间件契约与链 | `agentsdk-go` `pkg/middleware/{types,chain}.go` |
| 工具并发分段 | `agentsdk-go` `pkg/api/tool_concurrency.go`（`partitionToolCallSegments`） |
| 压缩与切点安全 | `agentsdk-go` `pkg/api/compact.go`（`toolTransactionSpans` · `stripToolIO`） |
| 运行时可配置项 | `agentsdk-go` `pkg/api/options.go` |
| subagent Manager | `agentsdk-go` `pkg/runtime/subagents/` |
| Skills 惰性加载 | `agentsdk-go` `pkg/runtime/skills/` |
| 中间件链装配顺序 | `nous-agent` `src/agents/lead_agent/agent.py`（`_build_middlewares`，275 行起） |
| 声明式锚点排序 | `nous-agent` `src/agents/middleware_ordering.py` |
| 工具类中间件形态 | `nous-agent` `src/hooks/middleware.py` · `src/permissions/middleware.py` |
| 澄清提前结束（非 interrupt） | `nous-agent` `src/agents/middlewares/clarification_middleware.py` |
| 压缩中间件 | `nous-agent` `src/agents/middlewares/compaction/summarization.py` |
| 三通路事件总线 | `nous-agent` `src/core/event_bus.py` · `src/core/redis_stream.py` |
| SSE 与流转换 | `nous-agent` `src/core/stream.py` · `src/core/events.py` · `src/api/runs.py` |
| 可插拔事件存储 | `nous-agent` `src/runtime/event_store.py` |
| Token 三桶归因 | `nous-agent` `src/runtime/journal.py` |
| subagent 循环内派发 | `nous-agent` `src/subagents/executor.py` |
| Swarm PG mailbox | `nous-agent` `src/swarm/` |
| LangGraph 兼容端点 | `nous-agent` `src/api/{runs,threads,thread_state,assistants,info}.py` |
| 不采纳：进程内注册表 | `nous-agent` `src/core/task_registry.py` · `src/core/state.py` |
| 已验证的 Go 实现与踩坑记录 | pace-grid `backend/services/pacegrid-ai/docs/AGENT_HARNESS_DESIGN.md` |
| 迁移执行器参考 | pace-grid `backend/services/pacegrid-ai/internal/migrations/runner.go` |
