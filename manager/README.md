# Manager

Manager 的独立模块位于 `manager/manager_service`。目录职责、依赖方向和生命周期参考 `fitness/AGENTS.md` 中的 Nunu Advanced 约定。单个 Manager 服务运行 Go HTTP 入口、Easel Web 前端和仅监听回环地址的 Easel 兼容进程。Gateway 提供外部同源路径 `/creative/`，Agent Harness 独立运行。

| 路径 | 入口和业务归属 |
| --- | --- |
| `/creative/`、`/creative/assets/*` | Manager Router → Easel 兼容 Handler → React Web 及静态资源 |
| `/creative/api/ideas`、`/creative/api/ideas/{iid}` | Manager Router → Ideas Handler → Service → GORM Repository |
| 其他 Easel Web API | Manager Router → Easel 兼容 Handler → 服务内回环 HTTP |
| `/workspace/*`、`/api/agent/*` | 现有 Nous 前端和 Agent API |

Go API 契约在 `api/v1`，入口在 `cmd`，Fx 组装及启停在 `internal/bootstrap`，Gin 路由及可信 Gateway 校验分别在 `internal/router` 和 `internal/middleware`。Viper 加载强类型配置，Zap 记录运行状态。Database 模块维护一个 PostgreSQL 池，启动时执行带校验和的版本 SQL 迁移。HTTP 接收连接、兼容进程和数据库按依赖顺序启动和关闭。

选题迁移保留原 Easel 的路径、方法、返回字段、状态规则和创建时间。首次迁移在同一事务内导入既有 `outputs/_ideas.json`，保留原文件。此后的选题读写均使用 Manager PostgreSQL。

兼容 Handler 暂时承接原 Easel 的对话、流式事件、媒体与素材、画像、日历、发布、账号及复盘。每个模块在 Go 用例和持久化边界迁移完成并通过现有 HTTP 契约、浏览器行为和状态兼容验收后，删除对应的兼容转发。整体移除兼容进程还需要迁移依赖 Python 的媒体和平台工具调用，并替换前端静态资源的现有服务入口。

源码来自固定的 [Easel 版本](../third_party/easel/SOURCE.md)。Manager 的账号授权、发布及模型运行使用自己的项目配置和数据卷。运行模型 API 直连需配置模型地址；使用 Easel 的 OpenClaw、OpenCode、Codex、ACP 或 Hermes 适配器时，需在 Manager 的运行环境中提供相应命令和配置。
