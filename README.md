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
- LLM 服务限流 → 级联雪崩拖垮网关
- 多租户并发 → 状态竞争、内存泄漏

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
│  中间件链 + 遥测审计 + 熔断保护：                              │
│  线程数据 → 上传 → 沙盒 → 权限 → 护栏 → 动态上下文            │
│  → LLM 熔断 → 审计 → 工具错误 → 摘要压缩 → 上下文紧缩         │
│  → Todo 管理 → Token 分账 → 标题生成 → 记忆提取               │
│  → 视觉注入 → 延迟工具 → 子任务限流 → 循环检测                 │
│  → Swarm 收件箱 → 澄清拦截                                   │
└──────────────┬──────────────────────────────────────────────┘
               │
               ▼
┌──────────────────────┐  ┌──────────────┐  ┌────────────────┐
│   PostgreSQL          │  │    Redis     │  │   沙盒          │
│ 检查点 + 记忆 + 遥测  │  │  事件总线    │  │  本地/Docker    │
└──────────────────────┘  └──────────────┘  └────────────────┘
```

## 核心设计

### EventBus — SSE 断连不丢数据

将 Agent 执行与 SSE 推送完全解耦：

- Agent 在后台 `asyncio.Task` 中独立运行（生产者）
- SSE 端点从 EventBus 订阅事件（消费者）
- **浏览器刷新 / 切换会话 → 后台任务继续运行至完成**
- 重连时回放缓冲区中的历史事件 + 从断点续传
- Redis Streams 双写 → 服务重启后仍可恢复

### 沙箱隔离 — 多租户安全

生产级沙箱，借鉴 DeerFlow 的隔离设计：

| 能力 | 实现 |
|------|------|
| 路径穿越防御 | `resolved_path.relative_to(local_root)` 阻断 `../` 和符号链接逃逸 |
| 写保护机制 | `read_only=True` 挂载属性，越权写入报 EROFS |
| 线程安全 | `threading.Lock` 保护所有缓存操作 |
| LRU 驱逐 | `OrderedDict` 缓存上限 256，自动淘汰非活跃实例 |
| 反向解析隔离 | `_agent_written_paths` 仅对 agent 写入的文件做路径还原 |
| Per-thread 隔离 | 每个 thread_id 独立沙箱实例，防止跨会话数据泄漏 |

### 遥测与 Token 分账审计 (RunJournal)

精细化的多层成本归因系统：

```
┌─────────────────────────────────────────────┐
│           RunJournal (Callback Handler)      │
│                                             │
│  on_chat_model_start → 采集首个 HumanMessage │
│  on_llm_end → Token 分桶路由：               │
│                                             │
│    tag: lead_agent    → lead_agent_tokens    │
│    tag: subagent:*    → subagent_tokens      │
│    tag: middleware:*  → middleware_tokens     │
│                                             │
│  去重: _counted_llm_run_ids                  │
│  延迟: latency_ms per LLM call              │
│  缓冲: async batch flush → PostgreSQL        │
└─────────────────────────────────────────────┘
```

每次 run 完成后自动写入 `run_completions` 表，支持按 thread/user 查询历史消耗。前端通过 SSE `run_completion` 事件实时获取分账数据。

### LLM 容错与熔断器 (Circuit Breaker)

抵御级联雪崩的三层防护：

```
请求 → [Circuit Breaker 检查] → [LLM 调用] → [成功 → reset]
              │                       │
              │ OPEN: fast-fail       │ 失败 → classify
              ▼                       ▼
         返回友好提示         ┌─ quota/auth → 不重试，返回提示
                             ├─ busy/transient → 重试 (指数退避)
                             └─ 连续失败 ≥ 阈值 → 熔断 (OPEN)
```

| 状态 | 行为 |
|------|------|
| **Closed** | 正常放行所有请求 |
| **Open** | 快速失败，不调用 LLM，保护网关 |
| **Half-Open** | 放行单次探测请求，成功 → Closed，失败 → Open |

支持解析 `Retry-After-Ms` / `Retry-After` 响应头，自适应退避。

### Prefix-Cache 优化 (DynamicContextMiddleware)

通过冰冻快照模式使 System Prompt 保持 100% 静态：

```
传统方式 (每轮变化，cache miss):
  SystemMessage: "...当前日期: 2026-05-22..."  ← 每天变化

Nous Agent (冰冻快照，cache hit 95%+):
  SystemMessage: "..."                        ← 完全静态
  HumanMessage[hidden]: "<system-reminder>    ← 首轮注入，此后冻结
    <memory>用户偏好 Python</memory>
    <current_date>2026-05-22, Friday</current_date>
  </system-reminder>"
  HumanMessage: "用户的第一条消息"
```

跨午夜时自动在当前 turn 前追加轻量 date-update，不破坏已冻结的前缀。

### 安全守卫中间件

**SandboxAuditMiddleware** — Bash 命令安全审计：

- Quote-Aware 复合命令拆分（字符级扫描，正确处理 `"safe" && rm -rf /`）
- 14 条高危模式检测（fork bomb、LD_PRELOAD、/dev/tcp、base64 pipe 等）
- 两阶段分类：整体扫描 + 逐子命令分析
- 输入卫生检查：空命令 / NULL 字节 / 超长（>10K）拦截
- 高危命令 → 返回 error ToolMessage（不执行），Agent 继续运行

**LoopDetectionMiddleware** — 循环检测与打破：

- Stable Key 桶化（read_file 行号按 200 行 bucket 归并）
- Per-tool-type 频次限制（默认 warn=30, hard=50）+ 工具豁免覆盖
- Schema-Safe 注入：修改 AIMessage.content 而非插入 HumanMessage
- Hard Stop 清理：清空 tool_calls + additional_kwargs + 改 finish_reason="stop"

**TodoMiddleware** — 待办管理与逃避防御：

- Context-Loss 防御：`write_todos` 滑出窗口时自动注入 `<system_reminder>`
- Premature-Exit 拦截：todos 未完成时阻止 Agent 退出，`jump_to: model`
- Schema-Safe：completion reminder 通过 `wrap_model_call` 注入，不污染持久化消息

### 记忆系统 — 多层记忆提取

融合 DeerFlow 的 LLM 驱动记忆和 Claude Code 的分层思想：

| 层级 | 实现 | 生命周期 |
|------|------|---------|
| 工作记忆 | LangGraph State（prompt buffer） | 当前请求 |
| 情景记忆 | PostgreSQL `user_memory_sections` + `user_memory_facts` | 永久 / 按用户隔离 |
| 程序记忆 | config.yaml + skills 定义 | 项目级 |

记忆增强特性：
- **纠错检测** — 用户说"不对/你理解错了" → 提取 correction fact（置信度 ≥ 0.95）
- **正面确认检测** — 用户说"完全正确/就是这样" → 提取 preference fact
- **上传过滤** — `<uploaded_files>` 标签不会污染长期记忆
- **事实去重** — 大小写无关匹配，避免重复写入
- **Token 预算注入** — 按置信度排序，渐进式填充至 max_tokens 上限

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
| `agent/config.yaml` | 模型、工具、沙盒、记忆、Swarm、熔断器配置 |
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
  model_name: deepseek-v4-flash

circuit_breaker:
  failure_threshold: 5       # 连续失败 N 次后熔断
  recovery_timeout_sec: 30   # 熔断后等待 N 秒再探测

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
│   │   ├── lead_agent/         # 主 Agent + 中间件链
│   │   ├── memory/             # 记忆提取（队列 + 更新器 + 提示词）
│   │   └── middlewares/        # 所有中间件实现
│   │       ├── sandbox_audit_middleware.py       # Bash 命令安全审计
│   │       ├── loop_detection_middleware.py      # 循环检测与打破
│   │       ├── todo_middleware.py                # 待办管理 + 逃避防御
│   │       ├── dynamic_context_middleware.py     # Prefix-Cache 优化
│   │       ├── llm_error_handling_middleware.py  # 熔断器 + 自适应重试
│   │       ├── memory_middleware.py              # 记忆提取 + 去抖
│   │       └── ...                              # 其他 20+ 中间件
│   ├── runtime/
│   │   ├── journal.py          # RunJournal — Token 分账审计
│   │   └── event_store.py      # 遥测事件持久化 (PostgreSQL)
│   ├── api/runs.py             # SSE 流式 + 断线重连端点
│   ├── sandbox/                # 本地 + Docker 沙盒
│   │   └── local/
│   │       ├── local_sandbox.py          # 路径穿越防御 + 写保护
│   │       └── local_sandbox_provider.py # 线程安全 LRU 缓存
│   ├── subagents/              # 子任务执行器
│   ├── swarm/                  # 多 Agent 协作
│   ├── tools/                  # 内置 + 社区 + MCP 工具
│   └── server.py               # FastAPI 入口
├── sandbox/src/                # 独立沙盒包
│   ├── local_sandbox.py        # 生产级沙盒实现
│   ├── path_mapping.py         # PathMapping 数据结构
│   ├── exceptions.py           # 结构化异常体系
│   └── providers/
│       └── local_provider.py   # Thread-safe LRU Provider
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
| **DeerFlow** | 中间件链、沙箱隔离（LRU + 路径穿越防御）、RunJournal Token 分账、Circuit Breaker 熔断器、Prefix-Cache 冰冻快照、TodoMiddleware 逃避防御、LoopDetection Schema-Safe 注入、SandboxAudit Quote-Aware 拆分、EventBus 架构 |
| **Claude Code** | 多层记忆哲学（工作/情景/语义分离）、按作用域管理记忆生命周期 |
| **LangGraph Platform** | Checkpoint 状态持久化、astream 协议、SSE 线格式 |

## 协议

MIT
