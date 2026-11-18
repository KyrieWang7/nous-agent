# 后端持久会话闭环与验收

本轮遵循“先完成后端闭环，再接前端”。前端代码未修改，后端运行路径没有 mock provider、内存降级或 Python fallback。测试中的确定性 provider 仅用于故障注入；本地另行完成了真实模型和服务重启验收。

## 执行与持久化

- `agentd` 必须配置 PostgreSQL，要求干净的 015 或更新 schema；缺少数据库或迁移时启动失败。迁移仍由 `agentctl migrate up` 显式执行。
- PostgreSQL 保存事件、主运行注册、跨实例取消意图、子任务结果、子会话历史及信箱。Redis 仅在启用 HTTP 限流时连接，默认关闭。
- 主会话通过 PostgreSQL advisory lock 保证同一 thread 只有一个运行写入历史。锁使用独立数据库连接，避免占满业务查询池。锁连接失效会取消执行；历史和状态写入还需在事务内校验 `agent_thread_writer`，新运行接管后拒绝旧运行写入。并发运行、状态覆盖或删除返回 409。
- 主会话和子会话在模型调用前保存已接纳输入，在工具执行前保存 assistant 工具意图，步骤结算后保存配对转录。写入失败会阻止后续执行。
- 进程中断后，不自动重放可能产生副作用的工具。没有持久结果的工具意图恢复为 `TOOL_OUTCOME_UNKNOWN`，显式续接前先核实外部状态。

## 子 Agent

- `task` 支持 `spawn` / `fork`；fork 只继承当前父回合之前已经完成的历史。
- 子会话返回稳定 `session_id`，保存独立历史和任务状态。父级 thread/run/tool-call 联合隔离结果，相同 provider tool-call ID 不会跨父运行覆盖。
- `task.session_id` 续接既有会话；原 profile、工具上限和轮数上限只能收窄，重新检查当前父级权限和能力 generation。
- `task.background=true` 由父回合 scope 管理。父级结束会取消、排空尚未完成的孩子；不能把它当作脱离父级的后台任务。
- `subagent_control` 提供直接子级的 list / inspect / send / wait / interrupt。它不能用猜测的 ID 控制其他 thread 或兄弟的后代。
- 子会话使用 30 秒租约和随机 token；每秒续约并检查取消意图。每次检查点均验证 token 和租约。取消期间继续持有租约，直到运行器资源清理结束。
- 等待超时仅结束等待。取消接口只确认取消意图，不提前写入终态。执行失败、截断、取消、超时及资源协调失败分别保留。
- 消息 ID 在单个子会话内幂等；相同 ID、不同内容返回 409。消息只有与下一次模型输入的历史一起事务提交后才变为 consumed。没有下一步时保留 pending，由显式续接消费，不虚报已送达。
- 续接作为新的普通 RunManager run 执行，复用相同 Loop 和配置 generation 的引用计数、预算、事件与审计，不创建第二套推理循环。

## HTTP 契约

前缀为 `/api/v1/threads/{tid}/subagents`；现有 `/api/agent/...` 代理可直接访问，不需要前端改动。

| 方法与路径 | 行为 |
| --- | --- |
| `GET /` | 子会话列表及父子关系 |
| `GET /{sid}` | 结果与子会话历史；不暴露内部 system prompt 或租约 token |
| `POST /{sid}/wait?timeout_seconds=20` | 终态返回 200，等待超时返回 202 与当前状态；最大 30 秒 |
| `POST /{sid}/cancel` | 持久化取消意图，返回 202；资源释放完成前状态仍为 running |
| `GET /{sid}/messages` | 信箱记录及 pending / consumed 状态 |
| `POST /{sid}/messages` | 输入 `{"id":"client-message-id","content":"..."}`，返回 202 |
| `POST /{sid}/continue` | 输入 `{"prompt":"..."}`，返回 202 和新主 run；事件沿已有 runs/events API 读取 |

不存在或不属于当前 thread 的会话返回 404；运行中续接、主会话已有写入者或消息 ID 内容冲突返回 409。主会话目前采用明确的并发冲突语义，不提供自动排队或根会话 next-turn inbox。插件重载采用完整 generation 切换，不提供单插件原地替换 API。这些边界应由前端按真实接口呈现。

## 验证

- 全量 `go test -race ./...` 通过。
- `make check` 通过（格式、依赖一致性、golangci-lint：0 issues、分层和全量测试）。补正原配置中测试排除项的 v2 层级，并显式处理资源关闭、类型断言与作用域 context 所有权。
- `go vet ./...`、`go build ./...`、分层检查（Agent 63 包、Gateway 12 包）通过。
- Docker PostgreSQL 全迁移回归通过，包含 fresh install、幂等升级、回滚及既有持久化契约。
- `TestDurableSubagentSessionsFenceRestartAndAdmitInbox`：工具 ID 隔离、旧 owner 拒写、过期恢复、跨 thread 隔离、信箱幂等与原子 ACK、新运行时恢复历史。
- `TestPersistentChildHTTPControlAndContinuation`：HTTP 查询、等待超时、消息消费、并发冲突、续接、取消排空、跨实例取消；单连接查询池可用及锁会话失效取消。
- `TestSwarmTaskHTTPFlowPersistsLifecycleAndUsage`：既有 Plan / Swarm / 子任务 / 用量主流程回归。

真实模型验收使用现有本地模型配置，只执行文本任务，没有文件修改、外部搜索或命令工具调用：

1. Flash 主会话回复 `BACKEND_CLOSURE_OK`。
2. 审核并批准一次只读计划，fork 子会话记住随机标记，返回 `SESSION_CREATED`。
3. 通过 HTTP 续接同一子会话，成功读回随机标记。
4. 重启 `agent-go`，核对历史完整保留，再次 HTTP 续接成功读回同一标记，历史由 6 条增加到 8 条。最终 015 代码重启后再次续接，历史增加到 10 条，仍正确读回同一标记。

本地验证标识：

- thread：`closure-d6ef7e79-085a-4e47-b70b-ba22d8e199b7`
- session：`a5e9baf0-6302-43a3-94ff-22a55738ce53`
- 重启后续接 run：`aa2f3576-977e-4f8a-9b4c-d7afc066f31c`；最终 015 复验为 `491a209a-2202-4d61-9ec5-3126637baa5b`
- 原始验证记录：`/tmp/nous-backend-closure-20261117/`（本地临时材料，不入库）。

本地数据库已从 012 升级到 015（013 为工作区已有的 schedule 建表迁移，014 为持久子会话，015 为主会话写入归属），`dirty=false`。未重置既有历史数据，未提交、推送或部署到远端。
