# 主会话消息编排与多层子 Agent

此记录接续当天的后端会话闭环和前端工作区验收。工作分支为 `develop`，当前为本地修改，未提交、推送或远端部署。早前文档中“主会话不支持排队”和“禁止嵌套委派”的边界由本记录更新。

## 主会话输入

参考本地 DSH `39163dd30f703a79079e2d2446545cff3c453738` 的 `packages/core/agent-loop/src/inbox.ts`。持久输入分为 `next_turn` 与 `next_step`：开始一轮时接纳全部下一步补充，再接纳至多一条下一轮任务；轮内只拉取下一步补充。界面使用“下一轮任务 / 补充当前任务”标明意图。

- PostgreSQL migration **016_session_inbox** 保存消息 ID、顺序、输入快照、目标运行、接纳和撤回状态。相同 ID、相同输入幂等；相同 ID、不同输入返回 409。
- API 为 `/api/v1/threads/{tid}/messages` 的 GET / POST，以及 `/{mid}` GET、`/{mid}/cancel` POST。有效入队返回 202，表示持久保存，不表示已经发送给模型。
- Service 调度器持有线程执行租约，调用原有 RunManager，没有新增 Agent Loop。不同实例竞争同一租约；健康运行不会反复抢占连接。每个服务最多处理 8 个队列线程，线程内顺序执行，其他线程的消息留在数据库等待。
- 模型适配器冻结请求、提交真实输入账本之后，才在同一数据库事务中写入 canonical transcript 并 ACK 本批消息。写入通过运行 ID fencing，撤回和请求准备共享行锁。
- 消息进入 preparing 后不能撤回，返回 409，需使用当前运行的停止入口；待接纳消息可单独撤回。停止当前运行会取消其尚未接纳的定向补充，保留独立排队的下一轮任务。
- 进程中断后，未 ACK 的 claim 可安全恢复；已经 ACK 的输入保留为中断记录，不自动重放可能发生过的模型或工具副作用。未回答的工具调用继续通过既有 TOOL_OUTCOME_UNKNOWN 机制恢复。
- 原直接 `/runs` 接口保留，线程忙或队列已有待办时返回 409；前端聊天入口使用持久消息 API。子会话显式续接仍使用已有直接运行路径。

前端显示 pending / preparing / admitted / withdrawn / rejected，以及对应运行的真实状态。接受响应丢失时，完整原请求（包括附件和图像）保存在 IndexedDB，sessionStorage 只保存恢复标记和草稿。恢复先 GET 原消息 ID，只有确认 404 才用原 ID 和原请求重发。没有自动 POST 重试。当前运行和排队任务使用独立的停止、撤回操作；刷新后可恢复队列并接入后续实际运行的 SSE。

## 多层委派

生产工具集不再无条件剥离 `task` / `subagent_control`。委派仍受父级工具白名单、注册 profile、可信深度、共享预算和权限策略约束。只读与命令 profile 未获得额外委派权限。到达深度上限后隐藏 task，dispatcher 也会拒绝越界调用。

默认 `loop.max_recursion_depth` 改为 3，可通过服务配置设置；默认全树 `subagent_budget` 仍为 20。`subagents.max_concurrent` 为每个父 Agent 的并行孩子上限。全局一个信号量会导致父任务占满配额并等待无法启动的后代，现在改为按父运行分配、引用计数回收的并发池。

子会话继续持久保存 ParentSessionID，背景任务属于父作用域。取消传播到后代，并在父运行结算前等待资源释放。一次性授权不向下传递，子级仍使用 ForDelegation 权限策略。嵌套进度带有父运行标识，不会因为模型工具 ID 重复而覆盖主对话的直接孩子任务卡；完整层级通过已有会话树展示。

## 验证记录

后端的 race 集成测试覆盖：双实例竞争、入队幂等、定向补充、撤回、请求准备边界、原子 ACK 失败回滚、旧 writer 拒绝写入、取消保留独立排队任务、ACK 前恢复、ACK 后禁止自动重放，以及 DSH 的补充先于下一轮输入的批次顺序。

多层测试覆盖：单个同级并发配额下两棵三层子树均完成、并发池资源回收、深度上限的工具隐藏与强制拒绝、三层 PostgreSQL 会话树的重建，以及取消根级后全部后代结算为 cancelled。测试入口：

```sh
go test -race ./pkg/subagent ./cmd/agentd ./internal/handler
go test -race -tags docker ./cmd/agentd -run 'Test(RootInboxDurableAdmissionQueueSteeringAndRecovery|NestedSessionsPersistLineageAndCancelTheWholeTree)$' -count=1
```

真实服务的 Playwright 记录位于 `/tmp/nous-root-inbox-20261117/`。主会话 `inbox-ui-83cf164c2ffc4721b4ce443a9b5a1275` 验证了运行中排队、撤回、刷新恢复、停止后继续独立任务、接入下一次运行、真实模型输出、接受响应丢失后 GET 恢复（恢复阶段 0 次 POST），以及 390×844 移动端无横向溢出、无页面 JS 异常。

503 场景的消息 `971d60a0-51b9-4b4b-b398-d902e1730009` 验证了不自动重试、刷新保留草稿、GET 404 后从 IndexedDB 恢复同一请求，并仅重发一次，实际模型返回 RECOVERED_503_OK。

真实模型三层委派另已通过：thread `nested-live-442eb4e848684529ae014fdaec882025`，run `6d50ac5a-fbf7-495b-ba35-1becb84ebacf`。仅在该测试会话中通过 API 审核一次计划，随后模型实际调用三层 task；两个 general-purpose 和一个 explore 子会话均为 completed，分别传回同一随机标记。会话 ID、父子关系和结果保存在上述目录的 `nested-live-result.json`。

## 当前边界

已安装原生插件的管理与运行时切换见 `2026-11-17-plugin-control.md`。本地功能与真实模型验证不等于线上部署、长期压测或用户最终验收；未将所有界面声明为与 DSH 桌面完全相同。

文件、Swarm、设置移动端入口以及计划取消的补充验收见 `2026-11-17-workbench-surfaces.md`。
