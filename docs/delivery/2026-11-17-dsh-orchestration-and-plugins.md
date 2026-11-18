# Demo 的 DSH 编排与 Go 插件契约

日期：2026-11-17；分支：`develop`。

## 实现边界

Agent 已有执行路径按 DSH 的编排契约实现；外层维持 Go 服务的七层结构。
基础设施只统一 Gin、GORM/PostgreSQL、Viper、Zap、go-redis；Redis 限流默认关闭。
Demo 不增加 WeKnora 的 RAG 流程、任务平台、Fx、Nacos 或其他基础设施。

本轮对照本地 `deepseek-harness` 的源码，参照提交
`39163dd30f703a79079e2d2446545cff3c453738`：

- `packages/core/agent-loop/src/agent.ts`：turn、preStep、step、prepareRequest。
- `packages/core/scope/src/index.ts`：资源作用域、重复 dispose 等待同一排空结果。
- `packages/core/agent/src/index.ts`：服务与具体 Agent 实现的职责分离。

这里是 Go 的独立实现，不加载 DSH 的 TypeScript/Cordis 插件。已覆盖的编排路径有行为测试；
没有实现的 DSH 产品能力不计入完成范围。

## 实际执行顺序

```text
RunManager 固定 generation / run identity
  -> Runner 创建独立 driver 与私有输入提案
  -> turn/start
  -> BeforeAgent
  -> preStep：组装 History 投影、工具、固定治理阶段
  -> step/start
  -> 适配器 PrepareCall：校验、冻结请求；不发送模型生成请求
  -> 每次实际 attempt 的 model_input_committed
  -> 首次提交成功：将提案输入接纳进正式 History（仅一次）
  -> provider / model_output_committed
  -> assistant 接纳 / 用量 / 治理 / 发布
  -> 工具批次：有序结果、失败和取消恢复、排空已启动任务
  -> step/end
  -> 下一步或 AfterAgent
  -> turn/end
```

`preStep` 拒绝不会产生 step/start。请求准备或输入提交失败不会修改调用者的正式 History。
模型已经开始后失败，已接纳输入仍保留。重试与降级重新准备请求、分别写入 attempt 记录，
初始用户输入不重复追加。三个生产适配器（OpenAI-compatible、Anthropic、DeepSeek Messages）
均支持冻结后的 PreparedCall；自定义 Model 使用 JSON 快照作为默认准备路径。
DeepSeek Files 的输入准备可涉及文件上传，但不会提前发送模型生成请求。

`step/end` 在工具恢复之后写入，`turn/end` 在 AfterAgent 之后写入；取消时仍执行收尾。
AfterAgent 的错误会返回调用者。输出触及 token 上限时，本轮以 `max-tokens` 结束；
即使输出中包含工具调用，也只补齐未启动结果，不执行可能截断的工具请求。

新增四个 audit 事件：`agent_turn_start`、`agent_step_start`、`agent_step_end`、
`agent_turn_end`。包含 execution run、generation、turn/step、终止原因和错误；
子 Agent 与根执行共用事件分区时仍可区分身份。既有模型、工具与 SSE 投影契约沿用。
一个现有 HTTP run 对应一个 turn，不把新的内部边界当成新的外部 run。

## 插件加载和资源所有权

```go
type Plugin interface {
    Name() string
    Inject() []string
    Apply(*plugin.Context) error
}
```

- `Inject` 引用服务名，服务提供者的插件名可以不同；消费者在服务可用后激活。
- `Provide` 在 Apply 成功后一次性发布服务，避免依赖方看到半初始化实例。
- `RegisterTool` / `RegisterCapability` 绑定可撤销注册，插件不能拿到原始 Registry 绕过所有权。
- `OnDispose` 绑定连接、订阅等资源的清理；`Go` 绑定可取消、需要排空的后台工作。
- 所有插件激活后汇总工具 capability，再冻结 generation；Apply 完成后拒绝新增注册。
- 缺失必需服务或依赖环导致启动失败并回滚；显式 Optional 插件可保持 pending。
- Apply 错误或 panic 会回滚失败插件自身及此前已激活插件；清理错误不会覆盖初始化错误。
- 关闭取消后台工作、逆序撤销效果并等待排空。并发调用等待同一次关闭，返回相同结果。
- disposer 按注册身份撤销；反复调用旧 disposer 不会删除同名的新注册。

现有 `plugin.json` 的命令工具通过适配器进入上述生命周期，没有另设一套工具注册路径。
配置、扩展、Skill、manifest 仍参与 generation 指纹。变更后先装配完整新 generation，
成功才让新 run 使用；旧 generation 等已有 run 引用释放后统一关闭。

## Demo 尚未提供的能力

当前保留 RunManager 的 HTTP run API 和 PostgreSQL 事件/投影存储；没有新增 DSH 持续 Agent
的 Inbox、followup/steer/inject、maintenance API、同一 session 的断点续跑或 Cordis
单插件原地服务热替换。重启仍将旧 run 标为 interrupted，以新 run 重试。
因此，本轮是已有 Demo 编排路径的契约对齐，不宣称完整移植 DSH 的全部运行时 API。

## 验证

行为回归覆盖：依赖服务激活、可选插件 pending、缺失依赖回滚、失败 Apply 的部分注册撤销、
清理异常聚合、并发关闭与后台排空、旧 disposer 身份隔离、发布后拒绝注册、请求准备快照、
输入拒绝/提交失败不污染正式转录、输出上限不执行工具、工具恢复先于 step/end。

本轮通过：

- Agent：`go test ./...`、`go vet ./...`、`go build ./...`。
- Agent 相关包竞态回归：`go test -race ./pkg/model/... ./pkg/modelrouter ./pkg/loop ./pkg/runtime/plugin ./pkg/runtime/scope ./pkg/harness ./internal/...`。
- Gateway 和 platform-go：分别运行 `go test -race ./...`。
- `make layer-check`：Agent 62 个、Gateway 12 个生产包无分层违规。
- 临时 PostgreSQL + HTTP：`go test -tags=docker ./cmd/agentd -run '^TestSwarmTaskHTTPFlowPersistsLifecycleAndUsage$' -count=1 -v`。
- 修改代码的 gofmt、`git diff --check`。

库和服务测试使用 faux 或本地 HTTP 测试适配器，没有调用付费模型。
Docker PostgreSQL 回归使用临时数据库，不连接应用数据库或执行其迁移。
改动保留在本地工作区，未提交、推送或部署。
