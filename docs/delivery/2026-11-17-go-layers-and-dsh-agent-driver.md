# Go 应用分层与 DSH Agent driver 对齐

日期：2026-11-17。工作分支：`develop`。

## 目的与范围

两个 Go 服务按 `internal/router`、`middleware`、`handler`、`service`、`repository`、
`model`、`server` 封装产品代码。Agent 自身继续作为独立 Harness 内核，并对照本地
DeepSeek Harness 的 `packages/core/agent-loop/src/{agent,tool-calls,runtime-context}.ts`
调整实现组织；首次分层参照 `5badb15009`。后续编排与插件契约及新的源码基准见
[DSH 编排与 Go 插件契约](2026-11-17-dsh-orchestration-and-plugins.md)。

产品基础设施统一封装在 `platform-go`（Gin、GORM/PostgreSQL、Viper、Zap、go-redis）。
既有 HTTP/SSE API 和 PostgreSQL schema 沿用，未执行应用数据库迁移；详情见
[Go 平台技术栈](2026-11-17-go-platform-stack.md)。
工作区原有计划模式、问题过期反馈和 schedules 相关改动随对应文件迁移保留。

## 应用职责

| 目录 | 职责 |
| --- | --- |
| router | 路由与 endpoint 契约 |
| middleware | CORS、HTTP 请求日志 |
| handler | 严格请求解码、HTTP 错误映射、SSE 与消息投影 |
| service | 会话状态、run 准入/取消/恢复、用户问题、Gateway 业务与 Swarm 轮询 |
| repository | 内存、PostgreSQL、Runtime metadata、catalog/文件/转换适配 |
| model | 跨应用层共享的数据契约 |
| server | 依赖装配、HTTP 默认配置与服务生命周期 |

`cmd` 继续拥有进程信号和模型/沙箱等能力装配。`pkg/runtime` 与 `pkg/loop` 不反向引用
任何 `internal` 包。Remote Sandbox daemon 的独立适配器归入 `internal/handler/sandboxapi`。

旧 `internal/transport`、Gateway `internal/{httpapi,store,catalog,files,convert}` 已迁走，
没有保留转发层或旧包 alias。Gateway 原来的 schedules 数据模型位于 `internal/model`。

run 的查询、重连、取消统一在 Service 校验 thread 归属，错误归属返回 404。

## Agent 实现方式的对应关系

| DSH 参考职责 | Go 实现 |
| --- | --- |
| Agent driver 拥有执行中的状态 | `pkg/loop/agent.go` 的每 run `agentDriver`；共享 Runner 仅保存配置 |
| turn/step 推进与停止边界 | `agentDriver.run → preStep → step` |
| pre-step 组装上下文、工具与策略 | `step.go` 的 `preStep` |
| 准备每次实际模型请求 | `request.go` 的 `prepareRequest` 与输入提交回调 |
| 请求、响应与会话事实的顺序 | input commit → provider → output commit → transcript admission |
| tool calls 独立调度与有序结算 | `tool_calls.go`、`tool_transactions.go`、`tool_recovery.go` |
| step 离开时补齐未结算工具调用 | `step` 的 defer 在下一步或 AfterAgent 之前结算一次 |
| Agent 级运行上下文 | `runtime_context.go` 的身份校验、预算与用量 |
| transport 与 Agent 分离 | `harness.Agent` 适配 Runtime contract，HTTP Handler 只消费 Service |

这里对齐的是 Go 内部的状态所有权和执行组织。Nous 仍保留 run API、canonical events、
PostgreSQL 存储和自己的 capability generation。尚未等价移植 DSH 的持续 Agent Inbox、
`followup/steer/inject`、maintenance、session 文件协议和全部流式恢复行为。

## 验证

已通过：

- `agent-go`: `go test ./...`。
- `agent-go`: `go test -race ./pkg/loop ./pkg/harness ./internal/... ./pkg/runtime/runmanager`。
- `gateway-go`: `go test ./...` 和 `go test -race ./...`。
- 两个模块的 `go vet ./...`、`go build ./...` 与 `go mod tidy -diff`。
- `make layer-check`，同时检查 Agent 与 Gateway；分别覆盖 61、12 个生产包。
- 本次变更 Go 文件的 gofmt 与 `git diff --check`。

回归覆盖 HTTP/SSE 协议、模型输入/输出提交、失败/取消的工具结果补齐、终态与 replay、
两个并发 turn 的 driver 状态/历史/停止次数隔离，以及 run 的 thread 归属。分层检查同时
验证库的传递依赖和应用层的直接依赖，防止 Handler 绕过 Service 或 Runtime 引用 HTTP。

以上均为本地验证；未进行真实模型、部署或真实客户端验收。当前环境未安装
`golangci-lint`，未声称执行完整 `make check`。
