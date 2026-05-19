# Swarm Mode 前端可视化设计

## 概述

为 nous-agent 的 Swarm/Team 多代理协作系统设计前端可视化体验。用户开启 Swarm Mode 后，team-lead 可以创建 team 并 spawn teammates 协作完成复杂任务。前端通过分栏式布局实时展示 swarm 内部的消息流动和 teammate 状态，类似 Claude Code 的 tmux 分屏体验。

### 设计决策

| 维度 | 选择 | 备选 |
|------|------|------|
| 布局风格 | 分栏式（左主对话 + 右 Swarm Panel） | 嵌入式、拓扑图式 |
| 启动方式 | 手动开关（input-box 旁） | 模型自主决定、两者结合 |
| 消息推送 | 独立 SSE 端点 `/api/swarm/{team_id}/stream` | 复用主 SSE 流、WebSocket |
| 用户交互 | 纯观察（只读面板） | 可干预、可追加 |
| 实现方案 | Gateway 轮询 + SSE 桥接 | PG LISTEN/NOTIFY、内存事件总线 |

---

## 1. 后端：Swarm 模式启用与 Tool 注入

### 1.1 配置传递

仿照现有 `subagent_enabled`，新增 `swarm_enabled` 作为 `config.configurable` 参数：

- 前端在请求 `/runs/stream` 时，body 中传递 `config.configurable.swarm_enabled = true`
- `make_lead_agent` 中读取此参数，决定是否注入 swarm tools

### 1.2 Tool 注入逻辑

当 `swarm_enabled = true` 时，在 `get_available_tools()` 中额外注入 4 个 swarm tools：

| Tool | 作用 |
|------|------|
| `team_create` | 创建 team，team-lead 自动注册为首个成员 |
| `team_delete` | 删除 team，级联清理成员和消息 |
| `send_message` | 发消息给指定 teammate 或广播 `to="*"` |
| `list_teammates` | 查看当前 team 成员列表及状态 |

`subagent_enabled` 的 `task` tool 保留——swarm 模式下 team-lead 通过 `team_create` 建组后，用 `TeammateSpawner`（内部基于 `SubagentExecutor`）spawn teammates。

注意：`swarm_enabled = true` 时会隐式启用 `subagent_enabled`，因为 swarm 依赖 `task` tool 来 spawn teammates。前端开启 Swarm 开关时自动同时传递两个 flag。

### 1.3 TeammateSpawner 状态事件增强

在现有 `TeammateSpawner.spawn()` 的关键节点，往 `swarm_messages` 写入系统消息（`from_agent = "system"`，`to_agent = "*"`），供前端 SSE 展示：

| 事件 | 时机 | content 格式 |
|------|------|-------------|
| `teammate_joined` | teammate 注册并开始执行时 | `[Joined] {name} started working on: {prompt_summary}` |
| `teammate_status` | teammate 产生中间输出时（可选） | `[Status] {name}: {progress_text}` |
| `teammate_completed` | teammate 正常完成 | `[Completed] {name}: {result_summary}` |
| `teammate_failed` | teammate 执行失败 | `[Failed] {name}: {error_message}` |
| `teammate_timeout` | teammate 超时 | `[Timeout] {name}: exceeded {timeout}s` |

### 1.4 InboxPollerMiddleware

当 `swarm_enabled = true` 且存在活跃 team 时，中间件在每次 agent loop 迭代时通过 `PostgresMailbox.poll_inbox()` 拉取 team-lead 的未读消息，注入为 ToolMessage，让 team-lead "听到" teammates 的汇报。

---

## 2. 后端：Gateway Swarm SSE 端点

### 2.1 新增 `routers/swarm.py`

挂载在 `/api/swarm`，3 个端点：

**REST：**

```
GET /api/swarm/teams?thread_id={id}
→ 查询当前 thread 关联的 team（通过 lead_thread_id 匹配）
→ Response: { teams: [{ id, name, description, created_at }] }

GET /api/swarm/teams/{team_id}/members
→ 查询 team 成员列表
→ Response: { members: [{ name, status, model, joined_at }] }
```

**SSE：**

```
GET /api/swarm/teams/{team_id}/stream
→ 长连接，持续推送 swarm 事件
→ 支持 Last-Event-ID 断点续传
```

### 2.2 SSE 事件协议

```
event: team_update
data: { "members": [{ "name": "researcher", "status": "active", "model": "gpt-4o" }, ...] }

event: message
data: { "id": 42, "from": "researcher", "to": "team-lead", "content": "Found 3 papers...", "created_at": "..." }

event: status
data: { "agent_name": "researcher", "status": "completed", "detail": "Task finished successfully" }

event: heartbeat
data: { "timestamp": "2025-04-10T12:00:00Z" }
```

### 2.3 SSE 内部实现

SSE 端点是一个 async generator：

1. 首次连接：发送 `team_update`（成员快照）+ 回补最近 50 条历史消息
2. 进入轮询循环：每 2 秒查询 `swarm_messages` 表（`created_at > last_poll_time`）
3. 同时查询 `swarm_team_members` 的变更，有变更则发送 `team_update`
4. 每 30 秒发送 `heartbeat`
5. 如果 team 被删除（查询返回 None），发送 `team_deleted` 事件后退出
6. 客户端断开时 generator 退出

轮询间隔 2 秒来自 `SwarmConfig.message_poll_interval_seconds`，可通过 `config.yaml` 调整。

### 2.4 全局观察查询

新增 `PostgresMailbox.get_recent_messages()`，与现有 `poll_inbox()` 的区别：

| 方法 | 视角 | 标记已读 | 用途 |
|------|------|---------|------|
| `poll_inbox(agent_name)` | 单 agent | 是 | agent 内部通信 |
| `get_recent_messages(since)` | 全局 | 否 | 前端观察面板 |

```python
async def get_recent_messages(self, team_id: str, since: datetime) -> list[SwarmMessage]:
    """获取指定时间之后的所有消息（全局视角，不标记已读）"""
    async with get_db_connection() as conn:
        rows = await conn.fetch(
            """SELECT id, team_id, from_agent, to_agent, content, read, created_at
               FROM swarm_messages
               WHERE team_id = $1 AND created_at > $2
               ORDER BY created_at ASC""",
            team_id, since,
        )
        return [SwarmMessage(...) for r in rows]
```

### 2.5 代理配置

前端 `next.config.js` 已有 rewrite 规则把 `/api/*` 代理到 Gateway（8001），`/api/swarm/*` 自动走相同规则，无需额外配置。

---

## 3. 前端：Swarm Mode 开关与面板布局

### 3.1 Swarm Mode 开关

在 `input-box.tsx` 中，现有 subagent 开关旁增加 "Swarm" 开关：

- 存储在 thread 级别的 local settings（同 `subagent_enabled`）
- 开启后 `config.configurable` 中包含 `swarm_enabled: true`
- 开关状态同步控制右侧 Swarm Panel 显隐

### 3.2 页面布局

```
┌──────────┬────────────────────────┬──────────────────┐
│          │                        │                  │
│ Sidebar  │    Main Chat           │  Swarm Panel     │
│ (不变)    │   (team-lead 对话)      │  (右侧面板)       │
│          │    宽度自适应缩小        │  固定宽 360px     │
│          │                        │                  │
└──────────┴────────────────────────┴──────────────────┘
```

- 未开启 Swarm 或没有活跃 team 时不渲染
- team-lead 执行 `team_create` 后（通过主 SSE 的 tool_call 检测），面板自动弹出
- 右上角 X 可收起为小图标，不影响后端

### 3.3 SwarmPanel 内部结构

```
┌─ Swarm Panel ──────────────────┐
│ Team: "research-team"    [×]   │  ← 标题栏
├────────────────────────────────┤
│ [All] ● researcher  ○ writer  │  ← Teammate Tabs
├────────────────────────────────┤
│                                │
│ [researcher → team-lead]       │  ← 消息流
│ "Found 3 relevant papers..."   │
│                                │
│ [team-lead → researcher]       │
│ "Focus on the 2024 papers"     │
│                                │
│ ── system ──                   │
│ researcher completed           │
│                                │
└────────────────────────────────┘
```

三层结构：

1. **标题栏**：team 名称、成员数、收起按钮
2. **Teammate Tabs**：每个 teammate 一个标签 + 状态指示灯。"All" tab 显示全局消息流
3. **消息流**：选中 tab 对应的消息，显示 `from → to`、内容、时间戳。系统消息灰色居中样式

### 3.4 状态指示灯

| 状态 | 颜色 | 含义 |
|------|------|------|
| `active` 刚加入 | 蓝色脉冲 | 等待执行 |
| `active` 有输出 | 绿色呼吸 | 工作中 |
| `removed` completed | 灰色 | 完成 |
| `removed` failed | 红色 | 失败 |
| timeout | 橙色 | 超时 |

### 3.5 动画与微交互

**面板出入场：**
- slide-in：`translateX(100%) → 0`，`ease-out`，300ms。主对话区同时平滑收窄
- slide-out：反向动画，主对话区还原

**Teammate 加入/离开：**
- 加入：tab 项 fade-in + slide-down，状态灯蓝色脉冲
- 完成：状态灯绿→灰渐变 + scale 弹跳（`1.0 → 1.2 → 1.0`，200ms）
- 失败：状态灯变红 + shake 抖动（左右 2px，300ms）

**消息流：**
- 新消息 slide-up + fade-in（150ms）
- 入场时背景高亮闪烁（浅蓝半透明 → 透明，500ms）
- 系统消息虚线分割 + 居中文字，fade-in

**状态灯：**
- running：绿色呼吸灯（`opacity 0.6 ↔ 1.0`，1.5s 循环）
- waiting：蓝色慢脉冲（2s 循环）
- 切换：`transition: background-color 300ms ease`

**消息连线：**
- `A → B` 消息到达时，发送者到接收者的 tab 之间扫过一道 CSS gradient 流光（200ms）

**原则：**
- 纯 CSS transition / animation 实现，不引入动画库
- 时长 150-500ms，不拖沓
- 支持 `prefers-reduced-motion` 媒体查询自动禁用

---

## 4. 端到端生命周期

```
用户操作                    后端                              前端
───────                    ────                              ────
1. 打开 Swarm 开关          configurable.swarm_enabled=true
                           agent 获得 swarm tools

2. 发送复杂任务 ──────────→  team-lead 决定需要协作
                           调用 team_create("research-team")
                           ← SSE values ─────────────────→  检测到 team_create tool_call
                                                            GET /api/swarm/teams
                                                            获得 team_id
                                                            建立 SSE: /api/swarm/{team_id}/stream
                                                            SwarmPanel slide-in

3.                         team-lead 调用 task() spawn
                           TeammateSpawner:
                           ├─ 写入 system: teammate_joined
                           ├─ 注册 member ───────────────→  SSE: team_update
                           └─ 启动 subagent 线程              teammate tab fade-in + 蓝色脉冲

4.                         teammate 执行中...
                           调用 send_message
                           → swarm_messages ──────────────→  SSE: message
                                                            消息 slide-up + 流光

5.                         team-lead InboxPoller
                           poll 到 teammate 消息
                           team-lead 回复 ────────────────→  主对话区正常渲染
                           send_message 给 teammate
                           → swarm_messages ──────────────→  SSE: message

6.                         teammate 完成
                           写入 system: teammate_completed
                           member → removed ──────────────→  SSE: status (completed)
                                                            灯 绿→灰 + 弹跳
                                                            SSE: team_update

7.                         所有 teammate 完成
                           team-lead 汇总回复 ────────────→  主对话区最终回答
                           (可选) team_delete ─────────────→  SwarmPanel slide-out
```

---

## 5. 错误处理与边界情况

### 5.1 SSE 重连

- `useSwarmStream` hook 内置指数退避重连（1s → 2s → 4s → 8s，上限 30s）
- 重连后发送 `Last-Event-ID` header，Gateway 以此时间戳做断点续传
- 每条 SSE 事件 `id` 使用 `swarm_messages.id` 或递增序号

### 5.2 Teammate 超时

- `TeammateSpawner` 已有 `timeout_seconds`（默认 900s）
- 超时写入 `teammate_timeout` 系统消息，前端状态灯变橙色
- team-lead 的 InboxPoller 收到通知后可决定是否重试

### 5.3 异常场景

| 场景 | 处理 |
|------|------|
| 用户关闭页面 | SSE 断开，后端继续执行，回来重连补发 |
| 切换到其他 thread | 关闭当前 SSE，切回时重建 |
| 用户中断 team-lead（stop） | teammate 继续运行到自然结束或超时，面板仍可观察 |
| PostgreSQL 连接异常 | SSE 发送 error 事件，前端显示"连接异常，重试中" |
| 同名 team 重复创建 | UNIQUE 约束报错，tool 返回错误提示模型换名 |

### 5.4 数据清理

- `team_delete` 级联删除成员和消息（`ON DELETE CASCADE`）
- 面板检测到 team 被删或 members 为空时 slide-out 关闭

---

## 6. 涉及的文件变更清单

### 后端 - Agent Server

| 文件 | 变更 |
|------|------|
| `src/agents/lead_agent/agent.py` | 读取 `swarm_enabled` configurable，条件注入 swarm tools |
| `src/tools/tools.py` | `get_available_tools()` 增加 `swarm_enabled` 参数和 swarm tools 注入逻辑 |
| `src/swarm/spawner.py` | `spawn()` 中增加 `teammate_joined/completed/failed/timeout` 系统消息写入 |
| `src/swarm/mailbox.py` | 新增 `get_recent_messages()` 方法 |
| `src/agents/thread_state.py` | ThreadState 增加 `swarm_team_id: str | None` 字段（可选） |
| `src/agents/lead_agent/prompt.py` | `swarm_enabled` 时在 system prompt 中追加 swarm 协作说明 |
| `src/swarm/schema.py` | 修复 `to_agent` 重复定义问题 |
| `agent/config.yaml` | 取消 swarm 段注释，`enabled: false` 作为默认值 |

### 后端 - Gateway

| 文件 | 变更 |
|------|------|
| `src/gateway/routers/swarm.py` | **新建**：REST 查询 + SSE stream 端点 |
| `src/gateway/app.py` | 注册 swarm router |

### 前端

| 文件 | 变更 |
|------|------|
| `src/components/workspace/swarm/swarm-panel.tsx` | **新建**：Swarm 面板主组件 |
| `src/components/workspace/swarm/teammate-tabs.tsx` | **新建**：Teammate 标签栏 |
| `src/components/workspace/swarm/message-stream.tsx` | **新建**：消息流组件 |
| `src/components/workspace/swarm/status-indicator.tsx` | **新建**：状态指示灯组件 |
| `src/components/workspace/swarm/swarm-animations.css` | **新建**：动画样式 |
| `src/core/swarm/hooks.ts` | **新建**：`useSwarmStream` SSE hook + `useSwarmState` 状态管理 |
| `src/core/swarm/types.ts` | **新建**：SwarmEvent、SwarmMessage、TeammateStatus 类型 |
| `src/core/swarm/api.ts` | **新建**：REST API 调用（getTeams、getMembers） |
| `src/components/workspace/input-box.tsx` | 增加 Swarm Mode 开关 |
| `src/app/workspace/chats/[thread_id]/page.tsx` | 布局调整，条件渲染 SwarmPanel |
| `src/core/threads/types.ts` | `swarm_enabled` 加入 thread settings 类型 |
