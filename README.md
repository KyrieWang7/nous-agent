# Nous Agent

Nous Agent 是一套有状态、可持久化、支持 Subagent 与 Swarm 协作的 Agent Harness 产品。当前主线以 DeepSeek Agent Harness 的 Runtime 思想为架构参考，以 Go 显式 Agent Loop 为内核：

- `agent-go/`：独立的 Agent Harness Runtime，负责 Agent Loop、capability、工具、状态、事件与持久化。
- `gateway-go/`：控制面与文件服务，负责模型目录、配置、Skills、上传、文档转换和 Swarm 查询。
- `frontend/`：Next.js 客户端，通过稳定的 SSE 事件契约连接 Go 服务。

`agent/` 与 `gateway/` 仅作为历史实现和离线迁移输入保留。Go Harness 不导入、不查询、不回退到这些实现，对外只提供版本化 Agent API。

## 默认架构

```text
Browser
  |
  | http://localhost:7775
  v
Next.js frontend
  |-- /api/agent/* -----> Go Agent Harness `/api/v1/*` :7776
  `-- /api/* -----------> Go Gateway       :7777
                              |
                   PostgreSQL / Redis (optional)
```

默认端口：

| 服务 | 实现 | 端口 | 启动方式 |
| --- | --- | ---: | --- |
| Frontend | Next.js | 7775 | 本地 `npm run dev` |
| Agent Harness | Go | 7776 | Docker + Air 热重载 |
| Gateway | Go | 7777 | Docker |

## 架构基线

目标架构与迁移路线见 [`docs/architecture-v2.md`](docs/architecture-v2.md)。旧的 [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) 保留为历史迁移记录，不再作为新模块的设计依据。

```text
Product / Transport
        ↓
Runtime: generation / plugin / capability / run / replay
        ↓
Kernel: explicit AgentLoop / tool transaction / budget / compaction
        ↓
Capabilities: agent / model / tool / sandbox / memory / skill / MCP
        ↓
EventStore / Snapshot / Metadata persistence
```

Agent Loop 的核心语义不通过 middleware 扩展机制隐式改变；业务能力通过 capability 和 plugin 装配，运行时状态通过事件和投影恢复。

## 核心能力

- 自有模型循环与 capability runtime，不依赖历史 Python 运行时。
- 模型、工具、沙箱、记忆、技能、MCP 和子 Agent 统一作为可注册、可替换的 capability。
- OpenAI-compatible 与 Anthropic 模型路由，支持按 run 选择模型与 thinking 配置。
- SSE 流式响应、事件回放、取消、断线重连和 PostgreSQL/Redis 持久化。
- 每次模型调用前持久化实际 model-visible input，模型输出在工具执行前持久化；提交失败
  fail-closed，崩溃后可定位最后一个确定的模型/工具事务边界。
- 沙箱文件系统、权限控制、Hooks、MCP、Skills、Todo、摘要压缩和 Guardrails。
- 配置化 Web 工具、命令插件、ACP v1 Agent，以及统一 Remote Sandbox API v2：本地连接自建 Docker 沙箱，云端连接部署在独立机器上的自建或开源沙箱服务，不提供 Kubernetes backend。
- Subagent 生命周期、并发限制、状态事件和父子 Token 归因。
- Swarm 团队、可信身份、批量并发 worker、可选 reviewer、定向消息、逐成员广播回执和收件箱轮询。
- Go 原生 PDF、DOCX、PPTX、XLSX 转 Markdown；旧 Office 格式由 Gateway 镜像内的 LibreOffice 归一化。

前端事件投影使用 `metadata`、`values`、`messages`、`custom`、`error`、`end`。运行时内部保留更细的 `content_delta`、`reasoning_delta`、工具、任务、用量和审计事件。

Harness 会在每个新 run 前检测模型、MCP、Skill、插件与 YAML 配置变化，并以完整 generation 热切换；进行中的 run 持有旧 generation lease，结束后才回收对应模型、MCP、Plugin 与外部客户端资源。监听地址、PostgreSQL URL 和 Redis URL 属于进程级配置，修改后需要重启。

## 快速开始

前置条件：Docker、Node.js 20+、pnpm。只有直接在宿主机运行或测试 Go 服务时才需要 Go 1.25。

```bash
cp .env.example .env
# 在 .env 中配置所选模型对应的 API Key。
# Swarm 或持久化运行还需配置 DATABASE_URL；Redis 可选。

make install
make dev
```

打开 [http://localhost:7775](http://localhost:7775)。`make dev` 会：

1. 构建并启动 `agent-go:7776` 与 `gateway-go:7777`。
2. 在本地以前台方式启动 Frontend `:7775`。
3. 通过 Air 监听 `agent-go/` 下的 Go、YAML 与 JSON 变更并自动重编译 Agent。

常用命令：

```bash
make backend         # 只启动 Go 后端
make frontend        # 只启动本地前端
make logs            # 跟随两个 Go 服务日志
make stop            # 停止 Go 后端与本地前端
make compose-config  # 仅验证 Compose，不启动服务
```

也可以直接使用 Compose：

```bash
docker compose up -d --build
cd frontend
GATEWAY_BASE_URL=http://127.0.0.1:7777 \
HARNESS_BASE_URL=http://127.0.0.1:7776 \
npm run dev
```

## 配置与持久化

| 文件或变量 | 用途 |
| --- | --- |
| `.env` | API Key、`DATABASE_URL`、`REDIS_URL` 与端口覆盖 |
| `agent-go/config.example.yaml` | 模型、沙箱、权限、Subagent、Swarm、摘要与运行时配置 |
| `gateway_config` volume | Gateway 管理的扩展配置 |
| `gateway_custom_skills` volume | Gateway 管理的自定义 Skills |
| `agent_go_data` volume | Harness 工作区与 Gateway 上传文件共享存储 |

无 `DATABASE_URL` 和 `REDIS_URL` 时，Agent 可使用进程内存完成基础本地对话。生产、多实例、持久化历史与 Swarm 必须配置 PostgreSQL；多实例取消和事件恢复建议同时配置 Redis。

应用 Go 数据库迁移：

```bash
make migrate
make migrate-current
```

## Subagent 与 Swarm

Subagent 与 Swarm 以 Go 实现为准。Subagent 使用受限并发的子 Harness，子任务事件通过现有 `custom` 事件投影为 `task_started`、`task_running`、`task_completed`、`task_failed` 或 `task_timed_out`。

Swarm 身份来自可信的 run context，模型不能伪造 `team_id` 或发送者。`swarm_batch` 以稳定任务契约并发分发 2-N 个 worker，可顺序追加 reviewer；worker 仍使用普通 Subagent Session 的预算、权限、持久化和事件链，最终结果由 lead 综合。广播消息使用逐成员回执，不会被第一个轮询者独占。Swarm 依赖 PostgreSQL，并需在 Harness 配置或 run 能力中启用。

## 历史数据迁移

Go 原生 `agent_*` 表直接沿用。其他历史状态必须通过独立离线导入流程转换为 canonical thread、message、run 和 event；线上 Runtime 不读取旧表、不探测旧格式，也不做 dual write。仓库中的历史 Python 源码仅用于确定离线转换规则，不是备用运行时。

## 验证

```bash
cd agent-go
go test ./...
go test -race ./...
go vet ./...
go run ./internal/tools/layercheck

cd ../gateway-go
go test ./...
go test -race ./...
go vet ./...

cd ../frontend
pnpm typecheck
```

## 目录

```text
nous-agent/
├── agent-go/       # 主 Agent Harness
├── gateway-go/     # 主 Gateway
├── frontend/       # Next.js UI
├── skills/         # Harness 与 Gateway 共享的 capability 资源
├── agent/          # 历史实现，仅作离线迁移参考
├── gateway/        # 历史实现，仅作离线迁移参考
├── docker-compose.yml
└── Makefile
```

## 协议

MIT
