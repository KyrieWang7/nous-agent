# Nous Agent Runtime v2 目标架构

> 状态：Phase 0-5 主路径已落地。RunManager、生产 capability generation、
> canonical event replay、PostgreSQL projection snapshot、durable control 与
> deterministic interruption recovery 已接入真实执行路径。重启不做同一 run
> 的盲目 continuation；旧 run 确认为 `interrupted`，调用方以新 run 重试。
>
> 目标：以 DeepSeek Agent Harness 的 Runtime 思想为架构参考，以 Go 版本的显式 Agent Loop 为执行内核，构建独立、可恢复、可扩展的 Nous Agent Harness。

## 1. 决策摘要

Nous Agent 不直接 fork 或依赖 DeepSeek Harness，也不再把 middleware chain 作为顶层架构。目标是吸收其四个核心思想：

1. Runtime 由可装配的插件和服务构成。
2. Agent、模型、工具、沙箱、记忆和技能都是可发现、可替换的 capability。
3. Session/run 的事实来源是可追加、可重放的事件；History 是模型上下文投影。
4. Agent Loop 保持显式、稳定、可测试，不把核心语义隐藏在扩展机制中。

实现语言继续使用 Go。Cordis 的动态插件模型不做一比一移植，改成 Go 的显式接口、依赖声明和生命周期管理。

## 2. 目标架构

```text
Product / Transport
  HTTP, SSE, CLI, External Scheduler Adapter
          |
          v
Runtime
  RunManager, StateMachine, Generation, Replay, EventBus
          |
          v
Agent Kernel
  AgentLoop, History Projection, ToolTransaction, Budget, Compaction
          |
          v
Capability Runtime
  Agent, Model, Tool, Sandbox, Memory, Skill, MCP, ACP, Policy
          |
          v
Persistence
  EventStore, SnapshotStore, MetadataStore, TaskStore
```

依赖方向只能向下：

```text
Product -> Runtime -> Kernel -> Capability -> Persistence interfaces
```

Kernel 不依赖 HTTP、PostgreSQL、Docker、YAML 或具体模型供应商。具体实现通过 Runtime 装配并通过接口注入。

### 2.1 术语约束

顶层架构只使用 Runtime、Kernel、Capability、Plugin、Event、Projection、Policy、
Hooks 和 Transport。`middleware` 已从目录和公共 API 中删除，也不提供旧包 alias。
Kernel 只暴露固定的 lifecycle stage；`pkg/runtime/lifecycle` 负责执行顺序和 run state，
`pkg/runtime/lifecycle/handlers` 保存进程装配使用的内置处理器。它不是业务扩展入口。
新业务能力进入 capability，进程级装配进入 plugin，授权进入 policy，外部治理脚本进入
hooks，传输协议进入 transport。

职责归属固定如下：

| 职责 | 所有者 |
| --- | --- |
| 循环、预算、工具事务、停止判定 | Kernel (`pkg/loop`) |
| 模型、工具、沙箱、记忆、技能、Agent | Capability Runtime |
| 授权与审批 | Policy / Permission capability |
| 用户定义的前后置治理脚本 | Hooks |
| 运行观测与用量 | Telemetry / Event projection |
| HTTP、SSE、前端字段 | Transport projection |

ACP 是外部 Agent 调用能力，注册为 capability/tool，不伪装成接收请求的 transport。
Scheduler 是产品触发器：通过版本化 `/api/v1` 或 `agentctl run` 创建普通 run，不在
Kernel 中维护第二条调度执行路径。

### 2.2 目录与协议边界

Go Harness 不出现 LangGraph package、route、checkpoint 或 graph schema 概念。
HTTP/SSE 是产品传输适配器，不是 Runtime：

```text
agent-go/
  cmd/agentd/                    process assembly
  cmd/agentctl/                  CLI run stream / admin commands
  internal/transport/httpapi/    HTTP / SSE / wire projection
  internal/tools/layercheck/     dependency policy
  pkg/runtime/                   run / event / capability contracts
  pkg/runtime/metadata/          Runtime metadata persistence port
  pkg/runtime/lifecycle/         fixed Kernel lifecycle dispatcher
  pkg/runtime/lifecycle/handlers/ assembled built-in lifecycle handlers
  pkg/runtime/postgres/          PostgreSQL runtime adapters
  pkg/runtime/redis/             Redis runtime adapters
  pkg/loop/                      explicit Agent Kernel
```

共享 Skill capability 资源位于仓库顶层 `skills/`，不归属于某个 Harness
实现。Go Harness 与 Gateway 只通过配置路径读取该目录，不能从历史 Python
源码目录加载运行时资源。

对外只提供版本化的 `/api/v1` Agent API。旧 `/threads`、`/runs`、
`/assistants`、graph schema 和 checkpoint-shaped state 不注册兼容路由，也不提供
package alias 或 proxy rewrite。前端和调用方随版本同步升级。

Run 请求只接受 `assistant_id`、`input`、平铺的 `config`、`context`、`metadata` 和
`on_disconnect`。`command`、`stream_mode`、`stream_subgraphs` 以及
`config.configurable` 均返回 400；未知 JSON 字段不被静默忽略。Task capability 只接受
`subagent_type`，终态只能来自 `subagent_status`/`subagent_error` 结构化元数据，不从输出
文本推断。

历史数据迁移是离线边界：Go 原生 `agent_*` 表继续原地使用；Python/LangGraph
checkpoint 数据若需要保留，先由独立 migration/import command 转换为 canonical
thread/message/run/event，再启动新版本。Runtime 不读取旧表、不做 dual write，也不在
请求路径中探测旧格式。

Go 原生 schema 的字段重命名由普通数据库 migration 完成。例如 009 将
`agent_run_completion.middleware_tokens` 原地重命名为 `auxiliary_tokens`，不保留双列、
视图或读写回退。全新数据库从 001 起直接创建 `auxiliary_tokens`。

## 3. 核心边界

### 2.3 自建 Sandbox Control Plane

Nous Agent 是云端服务，生产执行边界不是 agentd 进程，也不是部署 agentd 的节点。
本地和云端使用同一套 Remote Sandbox API v2，只替换 sandbox-controller 的 backend：

```text
local/cloud: agentd -> independent sandbox service -> Docker or open-source runtime
```

`agentd` 不持有 Docker socket、sandbox runtime 管理凭据或云沙箱凭据。模型 API key、
数据库凭据和 Gateway 凭据也不得传入 sandbox workload。FS、Bash、后续 PTY 与 LSP 必须
解析到同一个 lease 和同一个执行世界，禁止各自创建独立容器。

Remote Sandbox API v2 的最小契约为：

```text
POST   /v2/sandboxes/acquire
POST   /v2/sandboxes/{sandbox_id}/heartbeat
GET    /v2/sandboxes/{sandbox_id}
DELETE /v2/sandboxes/{sandbox_id}?lease_id=...
POST   /v2/sandboxes/{sandbox_id}/exec
POST   /v2/sandboxes/{sandbox_id}/fs/read
POST   /v2/sandboxes/{sandbox_id}/fs/write
POST   /v2/sandboxes/{sandbox_id}/fs/list
POST   /v2/sandboxes/{sandbox_id}/fs/stat
GET    /v2/capabilities
```

Acquire 使用部署侧可信的 `tenant_id` 与 canonical `thread_id`，返回不可猜测的
`sandbox_id`、`lease_id`、TTL、根路径和实际 enforcement facts。同一
tenant/thread 的 acquire 幂等，跨 tenant 永不共享。后续请求必须同时携带 bearer token
和 lease ID。租约在活动请求时续期；controller 重启后 reconciler 清理无主 workload。

每次 Exec/FS 请求都携带 Harness permission capability 判定后的 `sandbox_mode`。审批只会
把本次调用提升到工具要求的 mode，不修改 thread 默认策略。`danger-full-access` 仅表示
sandbox workload 内的完整权限，绝不表示 host/node 权限。

安全不变量：

- controller 不可用、认证失败、lease 过期或 backend enforcement 不足时 fail-closed；
- agentd 不得回退到宿主 shell 或进程内 Docker；
- 生产环境仅允许 `sandbox.provider: remote`；
- Docker workload 默认无网络、非 root、只读根文件系统、drop ALL capabilities、
  `no-new-privileges`，并设置 CPU、内存、PID、输出和执行时限；
- 外部开源 runtime 必须通过同一 backend enforcement contract 报告隔离、无网络、
  non-root、只读根和资源限制事实；controller 在启动和每次 lease 使用时复核，
  不满足即 fail-closed；
- sandbox-controller 是独立部署单元，只有它持有 Docker 或外部开源 runtime 的
  control-plane 权限；本地与云端均不引入 Kubernetes backend。

### 3.1 Agent Kernel

保留 `agent-go/pkg/loop.Runner` 作为唯一的 Agent 语义编排者。每轮固定执行：

```text
cancel/budget check
  -> compact
  -> trim
  -> resolve capabilities
  -> build model request
  -> sample
  -> append model result
  -> publish governed result
  -> execute tool transactions
  -> stop gate
  -> next iteration
```

以下逻辑属于 Kernel，不改造成插件：

- 模型循环和迭代上限；
- 工具调用事务完整性；
- 上下文压缩的触发和切点；
- 预算、取消和超时的最终判定；
- stop gate 和 run terminal state；
- parent/child run 的预算继承。

### 3.2 Capability Runtime

工具、模型、沙箱、记忆、技能和 Agent 统一视为 capability。它们都有定义、提供者和运行时实例三个层次：

```text
Definition  -> 名称、schema、描述、策略元数据
Provider    -> 根据 RuntimeContext 创建或解析实现
Instance    -> 当前 generation/run 使用的具体能力
```

建议的最小接口形状：

```go
type Capability interface {
    Name() string
    Kind() Kind
}

type Provider interface {
    Resolve(context.Context, ResolveRequest) (Capability, error)
}
```

实际实现按领域保持强类型接口，不使用一个包含大量可选方法的万能接口：

```text
AgentProvider
ModelProvider
ToolProvider
SandboxProvider
MemoryProvider
SkillProvider
MCPProvider
PolicyProvider
```

能力注册表必须：

- 启动时 fail fast 检查未知名称和重名；
- 按 generation 固定快照；
- 支持 capability 白名单和 scope；
- 保证投递给模型的 schema 顺序稳定；
- 禁止子 Agent 扩大父 Agent 的权限和预算。

Swarm 不进入 Kernel，也不建立第二套 Agent Loop。它作为 Agent capability
之上的协作策略：`swarm_batch` 将稳定的 `objective + items` 契约映射为多个普通
Subagent Session，复用父 run 的预算、权限、事件和持久化边界；可选 reviewer 在 worker
终态后顺序执行，lead 再完成最终综合。Team、Member、Inbox 与广播回执保持 durable，
批量调度只负责 fan-out/fan-in，不拥有模型循环。

### 3.3 Mode Policy

产品可以提供 Flash、Thinking、Pro、Ultra 等交互模式，但模式语义必须在 Harness
admission 边界归一化，不能由前端直接决定 Kernel 行为。调用方只提交 `mode` 与独立的
产品意图；`pkg/runtime.ModePolicy` 派生 `thinking_enabled`、`is_plan_mode`、
`subagent_enabled`、`swarm_enabled` 和 `reasoning_effort`。缺少 `mode` 时使用规范默认值
`pro`；旧派生字段不作为策略输入。未知模式或非 Ultra 模式下的 Swarm 请求在 admission
阶段失败，不静默降级。

模式只定义能力与策略边界，不改变 Agent Loop 的固定语义：

```text
flash    -> no thinking, no plan, no delegation
thinking -> thinking + low reasoning effort
pro      -> thinking + plan policy + medium reasoning effort
ultra    -> thinking + plan policy + delegation + high reasoning effort
```

Plan Mode 是显式的 collaboration state。Runtime 在 run admission 后写入 canonical
`plan_mode_changed` audit event；PlanPolicy 在每次模型请求组装时根据 canonical
`is_plan_mode` 注入 deployment-owned guidance。Planning phase 的工具视图只包含只读探索、
澄清、`write_todos` 和 `exit_plan_mode`，写操作、成品提交和 delegation 在审批前不可见。
`write_todos` 把完整列表写入 durable thread values 并实时投影到 UI；Approve 后仍可调用它
推进 pending / in_progress / completed 状态。`exit_plan_mode` 要求先存在至少一个未完成任务，
通过独立的 `interaction.questions` Capability 提交完整 Markdown plan，并等待用户选择 Approve
或 Keep planning。只有 Approve 且 `plan_mode_changed(active=false)` durable 写成功后才恢复
execution phase 的业务工具；反馈、dismiss、取消、服务异常和事件存储失败都保持 Plan active。
PlanPolicy 同时是 Kernel StopGate：未提交审批或仍有未完成 Todo 时，普通 final response 不能
结束 run。新建子 Agent 默认 inactive，不继承父 Agent 的 Plan collaboration state；Ultra 的
delegation 只在 execution phase 开放。

User Question 是协作 Capability，不是安全 Approval。两者使用不同的类型、持久化表、事件和
Run phase：问题回答只能表达用户对内容/计划的选择，永远不能授予 sandbox elevation 或工具
权限。`question_requested/question_resolved` 可投影到 UI；Plan mode event 仍是 log-only 事实。

审批、沙箱、预算和权限仍由各自 Runtime capability/policy 负责；模式可以收窄或开启
这些能力，但不能绕过 fail-closed 审批、父子 capability 交集或预算账本。

工具安全策略对齐 DeepSeek Harness，拆成两个相互独立的 mechanism policy：

```text
SandboxPolicy  -> read-only | workspace-write | danger-full-access
ApprovalPolicy -> ask | never
PermissionPreset -> { sandbox, approval }
```

默认 `workspace-write` preset 等于 `workspace-write + ask`：工作区内写入直接执行，只有
工具声明的最小沙箱模式高于当前模式时才请求一次审批。`danger-full-access` preset 等于
`danger-full-access + never`。审批服务缺失、超时、异常、非法策略值均 fail-closed。
Preset 只是产品层组合，工具 admission 必须分别比较 sandbox mode 并执行 approval policy；
不得再引入同时表达隔离范围和审批行为的五级权限枚举。

父子安全继承遵循单向收窄：子 Agent 继承父级 sandbox ceiling，但 Harness 根据可信的
`AgentDepth` 将其 ApprovalPolicy 固定为 `never`。普通 run context 不接受
`sandbox_mode`/`approval_policy` 覆盖，防止调用方自行扩大权限。

### 3.4 Runtime Plugin

插件是 Runtime 装配单元，不是 Agent Loop 的每一步。插件可以注册 capability、事件消费者、持久化实现或 API 扩展。

```go
type Plugin interface {
    Name() string
    Dependencies() []string
    Start(context.Context, Host) error
    Stop(context.Context) error
}

type Host struct {
    Capabilities *capability.Registry
    Tools        *tool.Registry
    GenerationID string
}
```

插件生命周期：

```text
discover -> validate -> resolve dependencies -> start -> ready
                                             |
                                    stop/reload generation
```

插件必须是可卸载的；不得在 `Start` 中创建无法被 Runtime 收集和关闭的 goroutine、连接池或订阅。

## 4. Session、Run 和事件

### 4.1 事件是事实来源

目标状态模型：

```text
RunEvent (append-only)
        |
        +--> History Projection (model input)
        +--> UI/SSE Projection
        +--> Audit Projection
        +--> Usage Projection
        +--> Projection Snapshot
```

`History` 不再承担所有持久化语义，而是可重建的投影。快照仅用于性能优化，不能成为不可解释的第二事实来源。

事件至少覆盖：

```text
run_started / run_completed / run_failed / run_cancelled
user_message / model_input_committed / model_delta / model_output_committed
tool_started / tool_completed / tool_failed
approval_requested / approval_resolved
question_requested / question_resolved
compaction_started / compaction_completed
subagent_started / subagent_completed
```

每个事件需要 `seq`、`run_id`、`thread_id`、`type`、`payload`、`created_at` 和幂等键。对外 SSE 可以是事件投影，但不能绕过事件存储单独制造事实。

### 4.2 Run 状态机

```text
created -> running -> waiting_tool -> running
                  -> waiting_approval -> running
                  -> waiting_user -> waiting_tool -> running
                  -> waiting_subagent -> running
                  -> compacting -> running
                  -> completed | failed | cancelled | interrupted
```

状态转换必须集中校验。终态幂等：重复的完成、取消或失败事件不能改变已经确认的终态。

### 4.3 Tool Transaction

工具调用是一等事务，不只是三条消息：

```text
assistant tool_call
  -> tool_started
  -> tool_completed/tool_failed
  -> tool_message projection
```

压缩、恢复、重放和 retry 都不能切断一个工具事务。`CallID` 是跨事件、子 Agent、审计和结果关联的稳定键。

## 5. 预算、权限和审批

预算是 parent/child run 共享的树状资源：

```text
Root Budget
  |- lead model tokens/cost
  |- tool calls
  |- subagent A
  `- subagent B
```

预算至少包括：token、费用、wall-clock、工具调用数、subagent 数、递归深度和并发数。子运行只能继承或收窄预算。

权限和审批属于 Capability/Policy 层：

```text
resolve capability
  -> policy authorize
  -> approval if required
  -> execute
  -> audit event
```

用户审批是可恢复状态，不是只存在于内存中的 hook：

```text
approval_requested -> run paused -> approval_resolved -> resume
```

内容协作走独立通路：

```text
question_requested -> waiting_user -> question_resolved -> waiting_tool
```

Question response endpoint 只能回答同一 `thread_id/run_id/question_id` 下已经持久化的问题，
首个终态回答获胜；它不调用 ApprovalManager，也不改变 SandboxPolicy/ApprovalPolicy。

## 6. 上下文与长期记忆

上下文分三层：

```text
Recent Context       最近若干轮完整消息
Task Context         目标、决策、约束、文件、未决问题
Durable Memory       用户、项目和跨会话记忆
```

摘要应优先生成结构化 task context，再由 Prompt Builder 投影为模型输入。摘要失败不能伪装成成功；可以使用明确的不可恢复占位事件，并记录原因。

Memory 是 capability，不是隐式全局变量。每次读取和写入都必须带 user/project/thread scope。

## 7. 迁移路线

### Phase 0: 文档和契约

- 采用本 RFC 作为架构基线。
- 删除 middleware 目录、公共 API 和配置术语，不保留兼容 alias。
- 冻结事件、Run 状态、Capability 和 Plugin 接口草案。

### Phase 1: Runtime spine

- 引入 `runtime/generation`、`runtime/plugin`、`runtime/capability`、`runtime/agentregistry`。
- 让现有 `harness.New` 通过 Runtime 装配，不改变 `loop.Runner` 的行为。
- 将 Model、Tool、Sandbox、Memory、Agent 逐步迁移到 Provider/Registry。

### Phase 2: Event and replay

- 统一 RunEvent 写入、SSE 发布和审计投影。
- 保留现有 History API，增加从事件重建 History 的 projector。
- 引入 snapshot 作为可删除缓存，并增加 replay 一致性测试。

### Phase 3: Durable control

- 引入显式 Run 状态机、approval resume 和 parent/child budget ledger。
- 将 Subagent Manager 接入统一 RunManager 和事件命名空间。

当前 Go 实现已落地 Runtime 契约和真实执行路径：

- `pkg/runtime/runstate.go`：集中校验 `pending -> running -> waiting_* -> running -> terminal`，并支持 `compacting`、`interrupted`。
- `pkg/runtime/tooltransaction.go`：以稳定 transaction/call id 记录工具从 pending 到 terminal 的事务状态，终态写入幂等且拒绝冲突覆盖。
- `pkg/runtime/budget.go`：token、费用、工具调用和子 Agent 额度的原子 reservation；子 ledger 同时受自身和全部祖先约束。Loop deadline、工具并发和 subagent 并发分别由 Kernel tracker 和受限 dispatcher 执行。
- `pkg/runtime/approval.go`：`ApprovalStore` 持久化边界、过期处理和首个终态决策获胜；PostgreSQL 部署使用 `PostgresApprovalStore`，单机部署使用内存实现。
- `pkg/runtime/question.go`：独立的 `QuestionStore/QuestionManager`，负责协作问题、反馈、dismiss、过期和首个终态回答；PostgreSQL 使用 `agent_question`，不复用 `agent_approval`。
- `RunContext` 携带状态机、预算账本、共享 ApprovalManager 和 QuestionManager；Kernel 入口校验 run/thread 身份并保证状态机处于 `running`，不执行无状态机路径。
- RunStateMachine 与 BudgetLedger 都先持久化候选版本，再提交内存状态；EventStore 失败时 phase/usage 保持原值。
- HTTP transport 创建 root ledger；subagent dispatcher 要求父状态机，并为每个 child run 创建独立状态机和 child ledger。
- Kernel 为每批工具调用安装 transaction observer；审批、工具等待和 subagent 等待都不能在缺少状态机时静默跳过。
- Kernel 在每次 Sampler 调用前同步写入 `model_input_committed`，记录 lifecycle 处理后的
  实际 system/messages/tools/options；写入失败时不调用模型。模型返回并规范化 ToolCall ID 后，
  在预算提交、AfterModel 治理、对外发布或执行工具前同步写入 `model_output_committed`；下一次
  input commit 记录治理后真正再次投递给模型的状态。两类事件覆盖 lead 与 child
  session，使用 execution run id + iteration 幂等，不投影到 HTTP/SSE。
- Kernel 是 `tool_start` / `tool_result` 的唯一发布者；两类事件使用稳定 CallID、幂等键和 audit 分类，不再由 lifecycle 复制发布。
- audit/usage canonical event 先同步写入 EventStore，再进入 live Bus；持久化失败返回执行路径。逐 token trace 仍允许异步和丢弃。
- BudgetLedger 在提交用量前同步写入版本化 checkpoint；checkpoint 失败会拒绝本次 charge，不改变已用额度，也不泄漏 reservation。
- 模型用量只由 Agent Loop 记账，不再保留面向外部 runner 的重复 lifecycle 记账处理器。
- `plugin.json` 只接受 `inputSchema` 和 `requiredSandboxMode`，严格拒绝旧权限字段和未知字段；插件进程只接收 `NOUS_PLUGIN_*` / `NOUS_TOOL_NAME` 环境变量。
- `sandbox.FS` 强制实现 `ListLimit`，目录工具不允许退回全量读取后再截断。
- 工具事务、durable approval、user question、subagent dispatch 和 compaction 已分别驱动 `waiting_tool`、`waiting_approval`、`waiting_user`、`waiting_subagent` 和 `compacting`，终态由 Runtime RunManager 统一确认。
- 每次状态变化写入带幂等键的 canonical `run_state_changed` 事件，`runtime/replay.StateProjector` 可重建最终状态；tool、approval、subagent 和 compaction 也写入 canonical 事件。

安全边界：服务端仍不提供安全 Approval 的匿名 approve/reject HTTP 路由；Question HTTP
接口只回答协作问题，并校验 thread/run/question 归属，不能转换成权限授予。安全审批决定
必须来自既有可信控制面。BudgetLedger 的 root checkpoint 已写入 canonical
`budget_changed` 事件并可恢复 max/used/version。进程重启后不恢复旧 goroutine，而是
从事件恢复 RunState/BudgetState 并将 orphan run 幂等确认为 `interrupted`。每次模型调用
已有完整 input/output commit，可精确定位崩溃前最后一个模型边界；若存在没有 terminal
`tool_result` 的 `tool_start`，仍按未知副作用处理，不自动重放。这是一项明确的副作用安全
策略，不宣称同一 run 已具备 provider-independent exactly-once continuation。

### Phase 4: Product capabilities

- 在稳定 Runtime 上扩展 MCP、Skills、Memory、Swarm、ACP 和业务 Agent；外部 Scheduler 复用标准 run admission。
- 新业务逻辑只能通过 capability、plugin 或 transport 层接入，不进入 Kernel。

生产 `buildAgent` 现在为每个 reload generation 注册以下能力：

```text
model.<name> / model.default
tool.<name>
sandbox.default
memory.store / memory.manager
skill.registry / skill.<name>
mcp.<server>
policy.tools
agent.subagents / agent.<profile>
```

Runtime Plugin 在 generation 冻结前按依赖顺序启动。生产 command tool 只在 Plugin
`Start` 中写入 Tool Registry，随后由 `harness.tool-catalog` 将最终工具集合登记为
`tool.<name>` capability；`buildAgent` 不保留绕过 Plugin 的直接注册路径。

静态实例通过 `capability.Value`/`RegisterValue` 注册，需要按 run/thread 构造的实现使用
`capability.Entry.Resolver`。`capability.ResolveAs[T]` 在 generation snapshot 上恢复领域
强类型，`ScopeThread` 和 `ScopeRun` 会强制检查可信身份。生产 `harness.Agent` 已从
immutable snapshot 解析 sandbox，不再直接持有生产 sandbox provider；reloadable agent
按 run 固定完整 built generation，引用释放前不会关闭旧 generation 的 MCP/plugin 资源。
配置、扩展配置、Skill 与 Plugin manifest 参与 generation fingerprint；新 run 原子切换到
完整的新 assembly，旧 generation 进入 retired，最后一个 active run 释放引用后关闭模型、
MCP、Plugin 和其他注册资源。监听地址及数据库/Redis 连接属于进程资源，变更时明确要求重启。

现有 model router、tool registry、memory/skill lifecycle handler 继续作为强类型执行
适配器，来源统一登记在 capability generation。Skill registry、tool policy 与子 Agent
profile 每次都从当前 immutable View 解析；排除 capability 会直接拒绝执行，不存在具体
实例回退。后续迁移不得再增加平行的全局 service locator。

### Phase 5: Durable recovery（已实现）

进程重启采用确定性的 interruption policy：旧进程失联且租约过期后，Runtime 从
canonical event 重建最后的 RunState 与 BudgetState，并以 compare-and-set 方式把原 run
确认成 `interrupted`。同一 run 不自动重新执行模型或工具；调用方需要基于已持久化
History 显式创建新 run 重试。原因是任意工具可能已经产生外部副作用，在没有
provider-specific resume token 和幂等执行器之前，自动重放会破坏 ToolTransaction 的
exactly-once 边界。

Phase 5 必须满足：

- Event sequence 从持久化最大游标继续，进程重启后不得回到 1；
- terminal compare-and-set，completed/failed/cancelled/interrupted 首个写入者获胜；
- orphan reconciliation 写入 canonical `run_state_changed`、`run_end` 与 completion；
- BudgetLedger 每次 commit 写入带版本的 `budget_changed`，可从事件恢复 max/used；
- retry 是新 run，不复用旧 run id，不自动重放未确认的工具事务。
- 每次模型请求必须先有 durable `model_input_committed`，每次成功返回的模型响应必须在预算、
  治理和工具执行前有 durable `model_output_committed`；提交失败时执行路径 fail-closed。

当前实现还包括：

- `EventSequenceReader` 从 PostgreSQL、Redis 或内存 EventStore 的 durable high-water
  mark 继续分配 seq；同一 run 的 allocate/publish/persist 串行化。
- adapter store 和 canonical RunState 都执行 first-terminal-wins；orphan reconciliation
  持久化 `run_state_changed`、`run_end` 与 completion。
- `BudgetLedger` 每次 commit 发布带单调 version 的 root checkpoint，
  `BudgetProjector` 与 `RestoreBudgetLedger` 可恢复并继续约束消费。
- root run 使用 immutable capability View；child 只能对父 allowlist 再求交，且
  lifecycle 返回后会重新施加可信 tools、capabilities、budget 和 control references。
- model router、Kernel tool disclosure/dispatch、memory/skill lifecycle、policy handler
  与 subagent dispatch 都从当前 immutable capability View 解析或校验实现；未进入 View
  的 tool、skill、policy 或 `agent.<profile>` 即使存在于 Registry 也不可调用。
- Durable Memory 以 user/project/thread 三维 scope 读写；010 migration 为 `memory_fact`
  原地补齐 scope 字段，运行时不读取旧字段或旧 schema。
- root budget 显式包含 tool-call/subagent 配额，RunContext 携带可信 recursion depth；
  child dispatcher 只能增加深度且在到达上限时于创建 runner 前拒绝递归。
- PostgreSQL、Redis 具体实现位于 `pkg/runtime/postgres`、`pkg/runtime/redis`；
  `pkg/runtime` 根包只保留契约、状态语义和内存实现，Kernel 不再间接链接数据库驱动。
- `pkg/runtime/metadata.Store` 是 RunManager 的执行状态持久化 port；生产 PostgreSQL
  直接使用 `pkg/runtime/postgres.MetadataStore`。HTTP Store 不再代理生产 Runtime 的
  history、thread values、terminal state 或 completion 写入。
- Subagent 只定义 `TaskStore` port 和单进程内存实现；生产 Redis adapter 位于
  `pkg/runtime/redis.TaskStore`，Capability 包不直接链接 Redis 驱动。
- Memory 只定义强类型 `Store` port 和内存实现；生产 PostgreSQL adapter 位于
  `pkg/runtime/postgres.MemoryStore`，Memory capability 不直接链接 pgx。
- RunManager 在 canonical transcript event 和 History projection 成功后保存完整 transcript
  snapshot；011 migration 提供 `agent_projection_snapshot`。Replay 从 snapshot 的
  `last_seq` 继续应用 canonical event，删除 snapshot 只影响性能，不影响正确性。
- `agentctl run` 通过 `/api/v1/threads/{id}/runs` 创建 run 并原样消费 SSE，可直接作为
  交互式 CLI 或外部定时调度器的执行入口。

未来若支持同一 run 原地 continuation，必须同时具备 provider-specific resume token
和幂等 ToolTransaction executor；在这两个条件满足前不得把 pending tool 自动重放。

## 8. 非目标

- 不直接复制 Cordis 或 DeepSeek Harness 的 TypeScript 实现。
- 不提供 LangGraph wire、route、checkpoint 或 SDK 兼容层。
- 不把所有核心步骤变成 middleware。
- 不为了架构迁移立即重写现有 Go Loop。
- 不在 Kernel 中绑定 LangGraph、HTTP、PostgreSQL 或某个 LLM provider。
- 不在 Runtime 契约稳定前扩展更多 Swarm 特性。

## 9. 验收标准

每个阶段至少满足：

1. `go test ./...`、`go test -race ./...` 和 `go vet ./...` 通过。
2. 一个 run 可以从事件重建 History，并与在线执行结果一致。
3. 取消、超时、审批和进程重启不会产生重复终态。
4. 子 Agent 无法扩大父 Agent 的 capability、权限或预算。
5. Runtime generation 切换不影响进行中的 run。
6. Kernel 包不导入具体 transport、数据库或外部 provider 实现。

### 9.1 当前验收证据

截至 2026-08-19，Phase 0-5 使用以下自动化证据验收：

1. `go test ./...`、`go test -race ./...`、`go vet ./...`、
   `go test -tags=docker ./pkg/migrations`、`go test -tags=docker ./cmd/agentd`
   与 `git diff --check` 通过。
2. `TestServerOnlineHistoryEqualsCanonicalEventReplay` 对真实 Server run 的 adapter
   History 与 canonical event projector 结果做结构化相等比较；append/replace 的
   projector 另有幂等与 malformed payload 测试。
3. Memory/PostgreSQL terminal CAS、审批相反决策竞争、cancel-after-complete、orphan
   reconciliation、worker/completion persistence failure 分支均有测试；旧 run 恢复为
   `interrupted`，不重放未知副作用。
4. `TestLoopCommitsModelLedgerBeforeSamplerAndToolSideEffects` 验证 input commit 先于模型、
   output commit 先于 `tool_start` 和工具 handler；`TestLoopFailsClosedWhenModelLedgerCommitFails`
   验证任一提交失败都不会越过对应副作用边界。两类内部事件由 SSE 投影显式过滤。
5. `TestCapabilityViewCanOnlyRestrictParent` 与
   `TestDispatchCannotWidenCapabilitiesToolsOrBudget` 覆盖父子 capability、工具和预算
   不可扩大；budget checkpoint restore 后仍拒绝超额消费。
6. generation manager 与 reloadable agent 测试证明切换后 active run 继续持有旧 lease，
   直到引用释放才关闭旧 generation 资源。
7. `go run ./internal/tools/layercheck` 对模块内、标准库和第三方依赖闭包执行边界规则；
   Kernel 禁止依赖 transport、配置、PostgreSQL/Redis、model/tool/sandbox 具体实现。
   当前 `pkg/loop` 的非标准依赖闭包仅包含 message/model/tool/runtime/lifecycle 契约包。
