# 本地 DSH：Subagent 与工具调度融合

日期：2026-11-17；Nous 工作分支：`develop`。
参考本地仓库：`/Users/wangbw/work/creative-project/deepseek-harness`，提交
`39163dd30f703a79079e2d2446545cff3c453738`。没有拉取远端，也没有修改参考仓库；
其中已有的 Office 测试 fixture 修改未参与本轮工作。

## 筛选结果

本轮优先融合会影响现有 demo 正确性的设计，沿用 Go 服务七层、五项基础设施封装和
PostgreSQL。DSH 的 TypeScript/Cordis 包不直接复制进 Go，也没有新建另一套 Agent Loop。

| 优先级 | DSH 设计 | 现有差距 | 本轮结果 |
| --- | --- | --- | --- |
| P0 | 后台子任务由所属 Agent/运行时持有，取消并排空后才释放资源 | DispatchAsync 使用 WithoutCancel 后脱离取消；代际关闭不管理它 | 接入父执行 scope、Manager.Close、排空和终态持久化 |
| P0 | 初始化失败回滚已分配资源 | prepared factory 同时返回资源和 error 时，Release 没有执行 | 无论初始化成功与否，已返回的 Release 恰好执行一次；拒绝 nil runner |
| P0 | 子执行结果与资源关闭结果分开 | 无 error 即 completed，截断和策略拒绝可被当成成功 | 增加 StopReason，截断/拒绝是非成功终态；保留部分输出和协调错误 |
| P0 | 工具使用滚动池，策略与结果按模型顺序，独占调用构成屏障 | 静态 Partition + 等待 goroutine；前后置治理和事务结果可并发乱序 | 动态读取执行模式，顺序 prepare/complete、并行 handler、失败后排空 |
| P0 | 每个批次持有自己的停止结论 | 共享 Executor 的 EndTurnRequested 可跨并发 run 干扰 | Kernel 使用 RunBatch 的批次结果；旧 Run 接口保留兼容 |
| P1 | spawn 与 fork 的上下文差异由后端决定 | 所有子任务只有 fresh history | task 增加 provider=spawn/fork；fork 仅继承当前 run 开始前的历史快照 |
| P1 | 委派时捕获策略，避免后续父级修改污染子级 | Values 和 profile 工具列表存在浅拷贝 | 在排队前复制 JSON 形状的嵌套值、工具列表和 fork 前缀 |
| P1 | 空的 usage-only 消息不能抹掉已有答案 | 只返回最后一个 loop.Result.Output | 选择子级自己产生的最后一条非空 assistant 文本；不返回继承的父级答案 |

## 对照的上游源码

- `packages/subagent/subagent/src/types.ts`：执行结果、stop reason、独立 dispose 与资源所有权。
- `packages/subagent/subagent/src/manager.ts`：启动、终态捕获、取消、排空和关闭。
- `packages/subagent/subagent/src/lifecycle.ts`：执行终态不能从资源释放成功推断。
- `packages/subagent/subagent/src/assistant-output.ts`：最后一条非空 assistant 输出。
- `packages/subagent/subagent/src/child-agent.ts`：委派前捕获策略和上下文。
- `packages/subagent/subagent-fork-in-process/src/index.ts`：仅复制已完成轮次的前缀。
- `packages/core/tools/src/batch.ts`：滚动并发池、模型顺序结算、动态独占屏障及取消排空。

## Go 中的实际行为

`pkg/subagent/execution.go` 管理已接纳的同步和异步子任务，`outcome.go` 解释终态，
`subagent.go` 仍是现有 dispatch 入口。代际装配把 Manager.Close 加入资源关闭列表。
每个 Loop.Run 创建自己的 execution scope，子任务排空完成后，RunManager 才能释放该 run
持有的 generation。后台任务不再因短暂工具上下文结束而立即退出，也不能越过所属 run
和 generation 的生命周期继续使用已关闭的模型、MCP 或插件资源。

库层新增 `Interrupt(ctx, taskID)` 与 `Wait(ctx, taskID)`，按 thread + 直接父 execution
校验归属。等待者取消只终止等待；真正取消由 Interrupt、父执行取消或 Manager.Close
发起。后台执行自身不将父状态机置为 waiting_subagent；同步委派和显式 Wait 才占用等待状态。
Close 拒绝新任务、取消执行中和排队的子任务，并等待 prepared runner 释放和终态写入。
资源协调错误保留在 CoordinationError，不把已完成的模型执行改写成另一种执行结果。

对模型暴露的 `task` 仍是等待终态的现有契约，本轮没有新增后台派发或控制工具、HTTP 路由。
新增参数只选择上下文来源：

```json
{
  "description": "Review the earlier decision",
  "prompt": "Review the completed discussion and identify missing tests.",
  "subagent_type": "verification",
  "provider": "fork"
}
```

省略 provider 等同 spawn。fork 的快照由 Loop 在接纳当前输入前建立；当前 turn 中的用户
输入、assistant 工具调用、未配对结果和后续追加均不会进入子级种子。任务 prompt 始终单独追加。
fork 只传递历史，权限、预算、递归深度与批准策略继续走已有的父子收窄边界。

子任务 Result 追加 parent/thread/run、provider 和 stop_reason。正常完成为 completed；
max-tokens、refusal 不再作为 completed task；取消/超时保持原有前端状态。
部分答案保留在 Output，错误与 CoordinationError 单独表达，TaskTool 错误结果也会标注部分输出。

工具 Executor 不再依赖预先计算的 Partition 执行整批。Partition 仅保留为独立分类辅助 API。
前置策略和事务开始按声明顺序，handler 在受限池中并行，后置治理和结果提交按原顺序。
未启动的工具每次重新查表；变为独占的调用等待此前任务及结果结算完成，并阻挡后续调用。
取消或失败后不补新任务，排空全部已启动 handler 后由 step 恢复未结算调用。
被取消的后置治理没有接纳的原始结果不会直接发布，继续使用保守的 outcome-unknown 恢复。

## 建议后续融合，尚未实施

1. **持续 Agent Inbox 和可继续的子 Agent**：这是下一项主要编排能力，包括 followup、steer、
   inject、send_message、wait/list children、维护期与冷恢复。需要先建立持久 Agent/session
   所有权，不能在现有 HTTP run 上简单启动脱离管理的 goroutine。本轮 Wait/Interrupt 是
   库层基础，不等于已经提供完整的 DSH 子 Agent 控制面。
2. **结构化子任务输出**：适合后续复杂审查和多步骤聚合；需要输出 schema 校验和独立 capture
   机制，不能只在 prompt 中要求 JSON。当前 demo 的文本综合流程尚不依赖它。
3. **以最近实际请求路由继承子 Agent 的模型选择**：已有 trusted run options 继承；完整照齐
   DSH request header，还需处理降级后的实际模型及其 reasoning effort，而不只读取创建配置。
4. **独立后端提供方注册表**：当确实需要 Codex/Claude/DSH SDK 进程外子执行时再统一适配。
   现有 ACP 包保持原状；本轮未复制外部 SDK、子进程协议和产品 UI。

## 验证

- Agent 模块 `go test -race ./...`，覆盖全量现有测试。
- 新增 DSH 合同行为测试：spawn/fork、排除当前输入、截断和拒绝、空终态输出、失败初始化
  清理、父 scope 排空、并发关闭、控制归属和不可变策略快照。
- 新增调度测试：滚动补位、前后置治理顺序、动态独占屏障、并发批次停止隔离。
- `go vet ./...`、`go build ./...`、`make layer-check`。
- 临时 PostgreSQL 的 `TestSwarmTaskHTTPFlowPersistsLifecycleAndUsage`，验证 task、协作状态、
  HTTP/SSE、持久化和用量归因，未连接应用数据库或调用付费模型。
- gofmt 与 `git diff --check`。

改动保留在本地工作区，未提交、推送或部署。
