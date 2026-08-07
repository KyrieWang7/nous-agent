# Nous Agent 架构演进文档

> Nous Agent 基于 DeerFlow 深度改造，融合 Claude Code 核心架构理念，并新增 PostgreSQL 持久化和 Swarm/Team 协作能力。

---

## 1. 项目定位

| 维度 | Claude Code | DeerFlow | **Nous Agent** |
|------|-------------|----------|----------------|
| **开发方** | Anthropic | ByteDance | 社区 (开源) |
| **开源协议** | 闭源 (CLI) | MIT | MIT |
| **定位** | 官方 Agent 产品 | 企业超级平台 | **融合 Claude Code 架构的开源 Agent 平台** |
| **技术栈** | TypeScript/Node.js | Python + LangGraph | Python + LangGraph |
| **前端** | CLI | Web UI + IM 多渠道 | **Web UI (Next.js, 去除认证)** |
| **持久化** | 自研 session 存储 | JSON 文件 | **PostgreSQL** |
| **代码规模** | ~2000+ 源文件 | ~20+ 核心模块 | **25+ 核心模块** |

---

## 2. 相比 DeerFlow 的改造清单

### 2.1 新增模块 (来自 Claude Code 架构)

以下 **8 个模块/子系统** 是 Nous Agent 在 DeerFlow 基础上全新引入的，灵感直接来自 Claude Code 源码分析：

| # | 模块 | 目录 | 来自 Claude Code 的设计 | DeerFlow 状态 |
|---|------|------|------------------------|--------------|
| 1 | **权限系统** | `src/permissions/` | 7级 PermissionMode → 简化为 5级 | ❌ 不存在 |
| 2 | **Hook 治理层** | `src/hooks/` | 7种 HookType → 4种 + 外部/Python Hook | ❌ 不存在 |
| 3 | **~~零成本上下文压缩~~** | ~~`src/context/`~~ | 已移除 — 双层压缩在实践中互相干扰，回退为 DeerFlow 同款单一路径 LLM 摘要 | 已对齐 DeerFlow |
| 4 | **声明式插件系统** | `src/plugins/` | 无直接对应，借鉴其模块化思想 | ❌ 不存在 |
| 5 | **Swarm/Team 协作** | `src/swarm/` | Swarm 文件 Mailbox → PostgreSQL Mailbox | ❌ 不存在 |
| 6 | **自定义 API 服务器** | `src/api/` + `src/server.py` | 替代 `langgraph dev` 的自管理 FastAPI | ❌ 依赖 langgraph CLI |
| 7 | **PostgreSQL 持久化** | `src/storage/` | 自研 session 存储 → AsyncPostgresSaver | ❌ JSON 文件 |
| 8 | **嵌入式客户端** | `src/client.py` | in-process 调用思路 | ❌ 不存在 |

### 2.2 新增中间件 (10 个)

DeerFlow 原有 12 个中间件，Nous Agent 扩展至 **22 个**。新增的 10 个中间件：

| # | 中间件 | 文件 | 功能 | 来源 |
|---|--------|------|------|------|
| 1 | **PermissionMiddleware** | `permissions/middleware.py` | 5级权限检查，工具级覆盖 | Claude Code PermissionMode |
| 2 | **HookMiddleware** | `hooks/middleware.py` | Pre/Post 工具钩子，阻断能力 | Claude Code Hooks |
| 3 | **~~CompactionMiddleware~~** | ~~`context/middleware.py`~~ | 已移除（双压缩路径互相干扰），统一为 SummarizationMiddleware 单一路径 | — |
| 4 | **SandboxAuditMiddleware** | `middlewares/sandbox_audit_middleware.py` | Bash 命令安全审计 | Claude Code 安全层 |
| 5 | **ToolErrorHandlingMiddleware** | `middlewares/tool_error_handling_middleware.py` | 工具异常转换为错误消息 | Claude Code 容错 |
| 6 | **TokenUsageMiddleware** | `middlewares/token_usage_middleware.py` | LLM Token 消耗日志追踪 | 独立增强 |
| 7 | **DeferredToolFilterMiddleware** | `middlewares/deferred_tool_filter_middleware.py` | 过滤延迟工具的 schema | 独立增强 |
| 8 | **LoopDetectionMiddleware** | `middlewares/loop_detection_middleware.py` | 检测并打断重复工具调用循环 | 独立增强 |
| 9 | **InboxPollerMiddleware** | `middlewares/inbox_poller_middleware.py` | Swarm 收件箱轮询注入消息 | Claude Code Swarm |
| 10 | **GuardrailMiddleware** | `guardrails/` | 内容安全过滤与授权 | Claude Code 安全层 |

### 2.3 新增 Subagent 类型 (3 个)

DeerFlow 仅有 `general-purpose` 和 `bash` 两种 Subagent。Nous Agent 新增：

| Subagent | 文件 | 用途 |
|----------|------|------|
| **ExploreAgent** | `subagents/builtins/explore_agent.py` | 快速代码库探索 |
| **PlanAgent** | `subagents/builtins/plan_agent.py` | 实现计划的结构化分解 |
| **VerificationAgent** | `subagents/builtins/verification_agent.py` | 代码验证和测试 |

### 2.4 Swarm/Team 工具 (4 个)

| 工具 | 文件 | 功能 |
|------|------|------|
| `team_create` | `tools/builtins/team_create_tool.py` | 创建协作团队 |
| `team_delete` | `tools/builtins/team_delete_tool.py` | 删除团队 |
| `send_message` | `tools/builtins/send_message_tool.py` | 跨 Agent 消息通信 |
| `list_teammates` | `tools/builtins/list_teammates_tool.py` | 查看团队成员 |

---

## 3. 核心架构改进详解

### 3.1 中间件链：12 → 22

完整的 22 层中间件执行顺序：

```
 1. ThreadDataMiddleware        — 创建线程目录
 2. UploadsMiddleware           — 注入上传文件
 3. SandboxMiddleware           — 获取沙箱实例
 4. DanglingToolCallMiddleware  — 补全悬挂工具调用
 ── 以下为 Claude Code 风格新增 ──
 5. PermissionMiddleware    ⭐  — 5级权限检查
 6. GuardrailMiddleware     ⭐  — 内容安全过滤
 7. HookMiddleware          ⭐  — Pre/Post 工具钩子
 8. SandboxAuditMiddleware  ⭐  — Bash 命令审计
 9. ToolErrorHandlingMiddleware ⭐ — 工具异常处理
── 原有中间件（保留或增强）──
10. SummarizationMiddleware     — LLM 驱动的上下文摘要
11. ~~CompactionMiddleware~~     — 已移除，统一走 SummarizationMiddleware 单一路径
12. TodoMiddleware              — 任务管理（Plan 模式）
13. TokenUsageMiddleware    ⭐  — Token 用量追踪
14. TitleMiddleware             — 自动生成标题
15. MemoryMiddleware            — 异步记忆更新
16. ViewImageMiddleware         — 图片 base64 注入
17. DeferredToolFilterMiddleware ⭐ — 延迟工具过滤
18. SubagentLimitMiddleware     — 并发 Subagent 限制
19. LoopDetectionMiddleware ⭐  — 循环检测与打断
20. InboxPollerMiddleware   ⭐  — Swarm 收件箱轮询
21. [Custom Middlewares]        — 插件自定义中间件
22. ClarificationMiddleware     — 用户澄清拦截（始终最后）
```

### 3.2 权限系统：从无到有

```
DeerFlow:   SandboxMiddleware 做基本的沙箱隔离，无工具级权限

Nous Agent: 5级权限模型
            ┌─────────────────────────────────────────┐
            │  READ_ONLY          仅读操作              │
            │  WORKSPACE_WRITE    读 + 工作区写          │
            │  PROMPT             高风险工具需用户审批    │
            │  ALLOW              所有工具放行（默认）    │
            │  DANGER_FULL_ACCESS 完全不受限              │
            └─────────────────────────────────────────┘
            + 工具级覆盖: tool_overrides: { bash: "danger_full_access" }
            + PolicyEngine: authorize(tool_name, tool_input, prompter)
```

### 3.3 上下文压缩：双层实验 → 对齐 DeerFlow 单一路径

```
历史方案:   2层压缩（已废弃）
            Layer 1: SummarizationMiddleware (LLM 摘要)
            Layer 2: CompactionMiddleware (零成本确定性压缩)
            → 双层并行触发，行为互相干扰，已移除 Layer 2

当前方案:   NousSummarizationMiddleware（对齐 DeerFlow，单一 LLM 摘要路径）
            ┌──────────────────────────────────────────────────────┐
            │ - 继承 LangChain 官方 SummarizationMiddleware         │
            │ - Token/消息数/比例阈值触发（OR 逻辑）                │
            │ - 摘要写入 state 的 summary_text 通道并计入触发判断   │
            │ - 动态上下文提醒（DynamicContextReminder）跨摘要救援  │
            │ - 摘要模型带 TAG_NOSTREAM，不产生前端幻影流式消息     │
            │ - before_summarization 钩子（压缩前外送的扩展点）     │
            └──────────────────────────────────────────────────────┘
```

### 3.4 Hook 系统：从 Callback 到治理层

```
DeerFlow:   LangGraph callbacks（无阻断能力）

Nous Agent: 完整 Hook 治理层
            ┌──────────────────────────────────────┐
            │ HookEvent:                            │
            │   PRE_TOOL_USE      工具执行前         │
            │   POST_TOOL_USE     工具执行后         │
            │   POST_TOOL_USE_FAILURE 工具失败后     │
            │   SUBAGENT_START    子代理启动时       │
            │   SUBAGENT_END      子代理结束时       │
            ├──────────────────────────────────────┤
            │ 执行方式:                              │
            │   外部进程 Hook (stdin JSON, exit code)│
            │   Python Hook (module:attr 路径解析)   │
            ├──────────────────────────────────────┤
            │ HookRunner:                            │
            │   顺序执行 → allow / deny / warn       │
            │   deny 时短路后续 hooks                │
            └──────────────────────────────────────┘
```

### 3.5 Swarm/Team 模式：全新能力

```
DeerFlow:   无多 Agent 协作能力

Nous Agent: PostgreSQL 驱动的 Team 模式
            ┌──────────────────────────────────────────────────┐
            │ 数据层 (PostgreSQL):                               │
            │   swarm_teams        — 团队表                     │
            │   swarm_team_members — 成员表                     │
            │   swarm_messages     — 消息表（含广播 to='*'）     │
            ├──────────────────────────────────────────────────┤
            │ 核心模块:                                          │
            │   TeamManager        — CRUD teams + members       │
            │   PostgresMailbox    — send + poll + broadcast     │
            │   TeammateSpawner    — 复用 SubagentExecutor       │
            ├──────────────────────────────────────────────────┤
            │ 工具: team_create / team_delete /                  │
            │       send_message / list_teammates                │
            │ task(name="x", team_name="y") → spawn teammate     │
            ├──────────────────────────────────────────────────┤
            │ InboxPollerMiddleware:                              │
            │   before_model → 轮询收件箱 → HumanMessage 注入    │
            ├──────────────────────────────────────────────────┤
            │ ThreadState.swarm_context:                          │
            │   { team_id, team_name, agent_name, is_leader }    │
            └──────────────────────────────────────────────────┘
```

### 3.6 持久化：JSON → PostgreSQL

```
DeerFlow:   JSON 文件存储 checkpoints
            .deer-flow/threads/{id}/ 目录结构

Nous Agent: PostgreSQL (AsyncPostgresSaver)
            ┌──────────────────────────────────────┐
            │ checkpoints         线程状态持久化      │
            │ threads             线程元数据          │
            │ swarm_teams         团队表              │
            │ swarm_team_members  成员表              │
            │ swarm_messages      消息表              │
            └──────────────────────────────────────┘
            + 支持多实例部署（共享数据库）
            + 事务安全（ACID）
```

### 3.7 自定义 API 服务器：摆脱 langgraph CLI

```
DeerFlow:   依赖 langgraph dev / langgraph_runtime_inmem
            通过 langgraph.json 配置启动

Nous Agent: 自管理 FastAPI 应用 (src/server.py)
            ┌──────────────────────────────────────┐
            │ server.py (Agent API, port 2024)       │
            │   实现 LangGraph Platform API 子集      │
            │   /threads /runs/stream /assistants    │
            │   SSE 流式输出                          │
            │   直接使用 uvicorn 启动                  │
            ├──────────────────────────────────────┤
            │ gateway/app.py (Gateway API, port 8001)│
            │   /api/models /api/mcp /api/skills     │
            │   /api/memory /api/uploads /api/artifacts│
            └──────────────────────────────────────┘
            + 无需 langgraph CLI
            + 可直接部署到任何 ASGI 容器
```

---

## 4. 完整架构图

```
┌──────────────────────────────────────────────────────────────────────┐
│                        Nous Agent System                              │
│                                                                       │
│  ┌──────────────────────────┐                                        │
│  │     Frontend (:3000)     │                                        │
│  │   Next.js + React        │                                        │
│  │   (去除认证, 纯 Chat UI) │                                        │
│  └──────────┬───────────────┘                                        │
│             │ /api/langgraph/*        /api/*                          │
│             ▼                          ▼                              │
│  ┌────────────────────┐    ┌──────────────────────────────────┐      │
│  │ Agent API (:2024)  │    │    Gateway API (:8001)           │      │
│  │ FastAPI (自管理)    │    │  Models/MCP/Skills/Memory/Uploads│      │
│  └────────┬───────────┘    └──────────────────────────────────┘      │
│           │                                                           │
│           ▼                                                           │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │              LangGraph Runtime (Lead Agent)                    │    │
│  │                                                                │    │
│  │  ┌────────────────────────────────────────────────────────┐  │    │
│  │  │              22层 Middleware Chain                       │  │    │
│  │  │                                                         │  │    │
│  │  │ [基础] ThreadData → Uploads → Sandbox → DanglingToolCall│  │    │
│  │  │ [安全] Permission → Guardrail → Hook → SandboxAudit     │  │    │
│  │  │ [容错] ToolErrorHandling                                 │  │    │
│  │  │ [压缩] Summarization → Compaction                        │  │    │
│  │  │ [功能] Todo → TokenUsage → Title → Memory → ViewImage   │  │    │
│  │  │ [控制] DeferredToolFilter → SubagentLimit → LoopDetect  │  │    │
│  │  │ [协作] InboxPoller                                       │  │    │
│  │  │ [终止] Clarification                                     │  │    │
│  │  └────────────────────────────────────────────────────────┘  │    │
│  │                              │                                  │    │
│  │                              ▼                                  │    │
│  │  ┌────────────────────────────────────────────────────────┐  │    │
│  │  │                   Tool Runtime                          │  │    │
│  │  │  Config │ MCP │ Builtin │ Community │ Plugin │ Swarm    │  │    │
│  │  │                                                         │  │    │
│  │  │  权限检查 → PreHook → 执行 → PostHook → 结果合并        │  │    │
│  │  └────────────────────────────────────────────────────────┘  │    │
│  │                              │                                  │    │
│  │  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐    │    │
│  │  │  Subagents   │    │  Swarm/Team  │    │   Sandbox    │    │    │
│  │  │  5种内置类型  │    │  PostgreSQL  │    │  Local/Docker│    │    │
│  │  │  + Teammate  │    │   Mailbox    │    │  虚拟路径映射 │    │    │
│  │  └──────────────┘    └──────────────┘    └──────────────┘    │    │
│  └──────────────────────────────────────────────────────────────┘    │
│                                 │                                     │
│                                 ▼                                     │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │                    PostgreSQL (:5432)                          │    │
│  │  Checkpoints │ Threads │ Swarm Teams/Members/Messages          │    │
│  └──────────────────────────────────────────────────────────────┘    │
│                                                                       │
└──────────────────────────────────────────────────────────────────────┘
```

---

## 5. 功能矩阵对比

| 功能 | Claude Code | DeerFlow | **Nous Agent** | 变化 |
|------|:-----------:|:--------:|:--------------:|:----:|
| Agent Loop | ✅ 自研状态机 | ✅ LangGraph | ✅ LangGraph | = |
| 中间件链 | — | 12个 | **22个** | **+10** |
| 权限系统 | ✅ 7级 | ❌ | ✅ **5级** | **新增** |
| Hook 治理 | ✅ 7种 | ❌ | ✅ **4种** | **新增** |
| 零成本压缩 | ✅ | ❌ | ❌（已移除，对齐 DeerFlow 单一 LLM 摘要） | **回退** |
| 插件系统 | ❌ | ❌ | ✅ | **新增** |
| Swarm/Team | ✅ 文件 Mailbox | ❌ | ✅ **PG Mailbox** | **新增** |
| 持久化 | 自研 | JSON 文件 | **PostgreSQL** | **升级** |
| API 服务器 | — | langgraph CLI | **自管理 FastAPI** | **升级** |
| 嵌入式客户端 | — | ❌ | ✅ | **新增** |
| Subagent 类型 | 2种 | 2种 | **5种** | **+3** |
| 循环检测 | ❌ | ❌ | ✅ | **新增** |
| Token 追踪 | ❌ | ❌ | ✅ | **新增** |
| 工具异常处理 | ❌ | ❌ | ✅ | **新增** |
| Bash 安全审计 | ❌ | ❌ | ✅ | **新增** |
| Memory | ✅ 7层 | 2层 | 2层 | = |
| Skills | ✅ | ✅ | ✅ | = |
| MCP | ✅ | ✅ | ✅ | = |
| Sandbox | ✅ | ✅ | ✅ | = |
| Web UI | ❌ | ✅ | ✅ | = |
| 多渠道 IM | ❌ | ✅ | ❌ (已移除) | 简化 |

---

## 6. 技术继承关系

```
Claude Code (Anthropic, 闭源)
    │
    │  架构理念
    │  ┌─ 权限系统 (PermissionMode)
    │  ├─ Hook 系统 (7 HookTypes)
    │  ├─ 上下文压缩 (8层)
    │  ├─ Swarm/Team (Mailbox)
    │  └─ 模块化 Prompt
    │
    ▼
DeerFlow (ByteDance, 开源)
    │  保留: LangGraph + 12 Middlewares + Web UI + Tools + MCP + Skills + Memory
    │  持久化: JSON 文件
    │  API: langgraph CLI
    │
    ▼  深度改造 + Claude Code 融合
Nous Agent (社区, 开源)
    ├─ 保留 DeerFlow 全部核心 (LangGraph, Middleware, Tools, MCP, Skills, Memory)
    ├─ 新增 (来自 Claude Code):
    │   ├── PermissionMiddleware (5级权限)
    │   ├── HookMiddleware + 外部/Python Hook
    │   ├── PluginManifest (声明式插件)
    │   ├── SandboxAuditMiddleware
    │   ├── ToolErrorHandlingMiddleware
    │   ├── LoopDetectionMiddleware
    │   ├── TokenUsageMiddleware
    │   ├── DeferredToolFilterMiddleware
    │   └── Swarm/Team 模式 (PostgreSQL Mailbox)
    ├─ 架构升级:
    │   ├── JSON 文件 → PostgreSQL (AsyncPostgresSaver)
    │   ├── langgraph CLI → 自管理 FastAPI (server.py)
    │   ├── 嵌入式客户端 (DeerFlowClient)
    │   └── 新增 3种 Subagent (Explore, Plan, Verification)
    └─ 简化:
        ├── 去除多渠道 IM (Feishu/Slack/Telegram)
        ├── 去除前端认证系统
        └── 去除 Nginx 反向代理 (Next.js rewrite 直连)
```

---

## 7. 项目结构

```
nous-agent/
├── agent/                          # Python 后端
│   ├── src/
│   │   ├── agents/                 # Agent 系统
│   │   │   ├── lead_agent/         # 主 Agent (工厂 + 系统提示词)
│   │   │   ├── middlewares/        # 15 个中间件组件
│   │   │   └── memory/             # 记忆提取、队列、提示词
│   │   ├── api/                    # LangGraph 兼容 REST API ⭐ 新增
│   │   ├── permissions/            # 5级权限系统 ⭐ 新增
│   │   ├── hooks/                  # Hook 治理层 ⭐ 新增
│   │   ├── plugins/                # 声明式插件系统 ⭐ 新增
│   │   ├── swarm/                  # Swarm/Team 协作 ⭐ 新增
│   │   ├── guardrails/             # 内容安全过滤
│   │   ├── storage/                # PostgreSQL 持久化 ⭐ 升级
│   │   ├── gateway/                # Gateway API
│   │   ├── sandbox/                # 沙箱执行 (Local/Docker)
│   │   ├── subagents/              # Subagent 委派 (5种类型)
│   │   │   └── builtins/           # general-purpose, bash, explore, plan, verification
│   │   ├── tools/                  # 工具系统
│   │   │   └── builtins/           # 内置工具 + Swarm 工具
│   │   ├── mcp/                    # MCP 集成
│   │   ├── models/                 # 模型工厂
│   │   ├── skills/                 # Skills 系统
│   │   ├── community/              # 社区工具 (Tavily, Jina, Firecrawl)
│   │   ├── config/                 # 配置系统
│   │   ├── reflection/             # 动态模块加载
│   │   ├── core/                   # 核心抽象
│   │   ├── utils/                  # 工具函数
│   │   ├── server.py               # Agent API 入口 ⭐
│   │   └── client.py               # 嵌入式客户端 ⭐
│   ├── config.yaml                 # Agent 配置
│   ├── extensions_config.json      # MCP/Skills 配置
│   └── pyproject.toml
├── frontend/                       # Next.js 前端 (去除认证)
├── docker-compose.yml              # PostgreSQL + MinIO
├── Makefile                        # 开发命令
└── .env.example                    # 环境变量模板
```

---

## 8. 与 Claude Code 的差距

| 差距领域 | 说明 |
|----------|------|
| Agent Loop | Claude Code 自研 Generator 状态机 vs Nous Agent LangGraph |
| 上下文压缩 | Claude Code 8层 vs Nous Agent 2层 |
| Memory | Claude Code 7层 vs Nous Agent 2层 |
| Feature Flags | Claude Code 40+ vs Nous Agent 部分配置化 |
| Swarm 后端 | Claude Code 支持 tmux/iterm2/in-process 三种，Nous Agent 仅 in-process |
| 权限级别 | Claude Code 7级 vs Nous Agent 5级 |
| Hook 类型 | Claude Code 7种 vs Nous Agent 4种 |

---

## 9. 总结

Nous Agent 在 DeerFlow 基础上进行了 **13 项核心改造**：

**来自 Claude Code 的 8 项架构融合：**
1. 5级权限系统 (PermissionMiddleware + PolicyEngine)
2. Hook 治理层 (外部进程 + Python Hook, 阻断能力)
3. ~~零成本上下文压缩 (CompactionMiddleware)~~ — 已移除，统一为 DeerFlow 同款单一路径 LLM 摘要 (NousSummarizationMiddleware)
4. 声明式插件系统 (PluginManifest + PluginRegistry)
5. Swarm/Team 多 Agent 协作 (PostgreSQL Mailbox)
6. 模块化 Prompt (缓存边界 + 动态拼接)
7. Bash 安全审计 (SandboxAuditMiddleware)
8. 工具异常处理 (ToolErrorHandlingMiddleware)

**Nous Agent 独立增强的 5 项：**
1. PostgreSQL 持久化 (替代 JSON 文件)
2. 自管理 FastAPI 服务器 (替代 langgraph CLI)
3. 嵌入式客户端 (in-process 调用)
4. 循环检测 + Token 追踪 + 延迟工具过滤
5. 3种新 Subagent (Explore, Plan, Verification)
