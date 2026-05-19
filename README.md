# Nous Agent

生产级 AI Agent 框架。基于 LangGraph 构建，融合 DeerFlow 的中间件架构和 Claude Code 的多层记忆思想。

不是演示项目，不是 SDK 封装，而是一套完整的有状态、可持久化、自我学习的 Agent 运行时。

## 为什么需要它

大多数 Agent 框架只解决了"能跑起来"的问题。它们在以下场景崩溃：

- SSE 断连 → 正在生成的内容丢失
- 页面刷新 → 对话状态消失
- 长对话 → 上下文溢出，无法恢复
- 跨会话 → Agent 完全忘记之前的交互
- 沙盒故障 → 整个系统挂起

Nous Agent 解决了以上所有问题。

## 架构

```
┌─────────────────────────────────────────────────────────────┐
│                     前端 (Next.js)                           │
│         SSE 自动重连 + Last-Event-ID 断点续传                │
└──────────────┬──────────────────────────────┬───────────────┘
               │ POST /runs/stream            │ GET /runs/{id}/stream
               │ (创建 + 流式)                │ (断线重连)
               ▼                              ▼
┌─────────────────────────────────────────────────────────────┐
│                    EventBus (核心总线)                        │
│                                                             │
│  ┌─────────────┐    ┌──────────────┐    ┌───────────────┐  │
│  │  发布者      │───▶│  内存缓冲区   │───▶│   订阅者       │  │
│  │ (Agent 任务) │    │   (deque)    │    │  (SSE Queue)  │  │
│  └─────────────┘    └──────┬───────┘    └───────────────┘  │
│                             │                               │
│                    ┌────────▼────────┐                      │
│                    │  Redis Streams   │                      │
│                    │  (持久化双写)     │                      │
│                    └─────────────────┘                      │
└─────────────────────────────────────────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────────────────────────┐
│              Agent 运行时 (LangGraph)                        │
│                                                             │
│  22 层中间件链：                                              │
│  线程数据 → 上传 → 沙盒 → 权限 → 护栏 → 钩子 → 审计          │
│  → 工具错误 → 摘要压缩 → 上下文紧缩 → 计划模式 → Token 统计   │
│  → 标题生成 → 记忆提取 → 视觉注入 → 延迟工具 → 子任务限流     │
│  → 循环检测 → Swarm 收件箱 → 澄清拦截                        │
└──────────────┬──────────────────────────────────────────────┘
               │
               ▼
┌──────────────────────┐  ┌──────────────┐  ┌────────────────┐
│   PostgreSQL          │  │    Redis     │  │   沙盒          │
│ 检查点 + 记忆持久化    │  │  事件总线    │  │  本地/Docker    │
└──────────────────────┘  └──────────────┘  └────────────────┘
```

## 核心设计

### EventBus — SSE 断连不丢数据

参考 Koda 的 `AgentEventBus` 设计，将 Agent 执行与 SSE 推送完全解耦：

- Agent 在后台 `asyncio.Task` 中独立运行（生产者）
- SSE 端点从 EventBus 订阅事件（消费者）
- **浏览器刷新 / 切换会话 → 后台任务继续运行至完成**
- 重连时回放缓冲区中的历史事件 + 从断点续传
- Redis Streams 双写 → 服务重启后仍可恢复

```python
# 断连前：任务继续执行
bus.publish(run_id, "values", {...})

# 重连后：回放 + 实时流
for event in bus.get_buffered_events(run_id, after_id=last_event_id):
    yield event
queue = bus.subscribe(run_id)  # 加入实时事件流
```

### 记忆系统 — 多层记忆提取

融合 DeerFlow 的 LLM 驱动记忆和 Claude Code 的分层思想：

| 层级 | 实现 | 生命周期 |
|------|------|---------|
| 工作记忆 | LangGraph State（prompt buffer） | 当前请求 |
| 情景记忆 | PostgreSQL `user_memory_sections` + `user_memory_facts` | 永久 / 按用户隔离 |
| 程序记忆 | config.yaml + skills 定义 | 项目级 |

记忆增强特性（来自 DeerFlow）：
- **纠错检测** — 用户说"不对/你理解错了/重新来" → 提取 correction fact（置信度 ≥ 0.95）
- **正面确认检测** — 用户说"完全正确/就是这样" → 提取 preference fact
- **上传过滤** — `<uploaded_files>` 标签不会污染长期记忆
- **事实去重** — 大小写无关内容匹配，避免重复写入
- **Token 预算注入** — 按置信度排序，渐进式填充至 max_tokens 上限

### 沙盒 — 可选隔离执行

```yaml
sandbox:
  enabled: true   # 设为 false = 纯对话模式，移除 file/bash 工具
  use: src.sandbox.local:LocalSandboxProvider
```

支持本地直接执行和 Docker 隔离容器两种模式。通过 `enabled: false` 一键关闭沙盒，Agent 仍可正常对话、调用 web_search 等不依赖沙盒的工具。

### 中间件链 — 22 层可组合

每层独立、顺序明确、可按需开关：

```
[0]  ThreadData          — 线程数据加载
[1]  Uploads             — 文件上传注入
[2]  Sandbox             — 沙盒环境分配（可关闭）
[3]  DanglingToolCall    — 孤立 tool_call 修补
[4]  Permission          — 5 级权限控制
[5]  Guardrail           — 输入/输出护栏
[6]  Hook                — Pre/Post 工具钩子
[7]  SandboxAudit        — Bash 命令安全审计
[8]  ToolErrorHandling   — 工具异常转 ToolMessage
[9]  Summarization       — 上下文压缩（token 触发）
[10] Compaction          — 零成本确定性压缩
[11] TodoList            — Plan 模式任务管理
[12] TokenUsage          — Token 消耗追踪
[13] Title               — 自动标题生成
[14] Memory              — 记忆提取 + 去抖队列
[15] ViewImage           — 视觉模型图像注入
[16] DeferredToolFilter  — 延迟工具动态注册
[17] SubagentLimit       — 并发子任务限流
[18] LoopDetection       — 重复 tool call 打断
[19] InboxPoller         — Swarm 收件箱轮询
[20] [自定义]             — 用户自定义中间件
[21] Clarification       — 澄清请求拦截（始终最后）
```

## 快速开始

### 前置条件

- Python 3.12+，[uv](https://docs.astral.sh/uv/)
- Node.js 20+，pnpm
- Docker（PostgreSQL + Redis）

### 1. 安装

```bash
git clone https://github.com/aspect-build/nous-agent.git
cd nous-agent

# 后端
cd agent && uv sync && cd ..

# 前端
cd frontend && pnpm install && cd ..
```

### 2. 配置

```bash
cp .env.example .env
# 编辑 .env：填入 API Key（OPENAI_API_KEY / MINIMAX_API_KEY / DEEPSEEK_API_KEY）
# REDIS_URL 已预配置为本地 Docker Redis
```

### 3. 启动

```bash
# 基础设施（PostgreSQL + Redis 需要先运行）
docker compose up -d

# Agent API
cd agent && uv run uvicorn src.server:app --port 7776 --reload

# 前端
cd frontend && pnpm dev
```

打开 http://localhost:3000 开始对话。

## 配置说明

| 文件 | 用途 |
|------|------|
| `.env` | API Key、数据库 URL、Redis URL |
| `agent/config.yaml` | 模型、工具、沙盒、记忆、Swarm 配置 |
| `agent/extensions_config.json` | MCP 服务器、技能开关 |

### config.yaml 核心配置

```yaml
models:
  - name: deepseek-v4-flash
    supports_thinking: true

sandbox:
  enabled: true              # false = 关闭文件/bash 工具
  use: src.sandbox.local:LocalSandboxProvider

memory:
  enabled: true
  debounce_seconds: 30       # 批量记忆更新去抖
  injection_enabled: true    # 注入 system prompt
  max_injection_tokens: 2000
  model_name: deepseek-v4-flash  # 记忆提取用的 LLM

swarm:
  enabled: false             # 多 Agent 团队模式
  max_team_size: 5
```

## 项目结构

```
nous-agent/
├── agent/src/
│   ├── core/
│   │   ├── event_bus.py        # EventBus（SSE 事件总线）
│   │   ├── task_registry.py    # 后台任务生命周期管理
│   │   ├── redis_stream.py     # Redis Streams 持久化层
│   │   └── stream.py           # astream → SSE 格式转换
│   ├── agents/
│   │   ├── lead_agent/         # 主 Agent + 22 层中间件链
│   │   ├── memory/             # 记忆提取（队列 + 更新器 + 提示词）
│   │   └── middlewares/        # 所有中间件实现
│   ├── api/runs.py             # SSE 流式 + 断线重连端点
│   ├── sandbox/                # 本地 + Docker 沙盒
│   ├── subagents/              # 子任务执行器
│   ├── swarm/                  # 多 Agent 协作
│   ├── tools/                  # 内置 + 社区 + MCP 工具
│   └── server.py               # FastAPI 入口
├── frontend/src/
│   └── core/threads/
│       ├── transport.ts        # SSE + 重连传输层
│       └── hooks.ts            # useSSEStream（自动重连）
├── docker-compose.yml
└── config.yaml
```

## 设计来源

| 来源 | 借鉴内容 |
|------|---------|
| **DeerFlow** | 中间件链架构、纠错/确认检测、上传过滤、事实去重、用户记忆隔离 |
| **Claude Code** | 多层记忆哲学（工作/情景/语义分离）、按作用域管理记忆生命周期 |
| **Koda** | EventBus 模式（SSE 断连容忍）、TaskRegistry（后台任务续命）、Redis Streams 双写 |
| **LangGraph Platform** | Checkpoint 状态持久化、astream 协议、SSE 线格式 |

## 协议

MIT
