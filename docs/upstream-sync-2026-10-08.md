# DeepSeek Harness → Nous 同步记录（2026-10-08）

本次参考 `deepseek-ai/deepseek-harness` 的 `dsh-v0.2.1-alpha.1`，提交 `5badb15009`，在 Nous 的 `develop` 分支更新现有 Go API 和 Next.js 客户端。运行时继续遵循 [Runtime v2](architecture-v2.md)，上次 Go 运行时同步见 [2026-09-29 记录](upstream-sync-2026-09-29.md)。

## 工具调用准备过程

参考上游的 [工具参数准备阶段](https://github.com/deepseek-ai/deepseek-harness/blob/5badb15009/.agents/notes/implemented/architecture/2026-09-24-preparing-tool-arguments.md)，将 provider 的索引、工具标识和参数片段通过 `tool_call_delta` trace event 投影为 SSE `messages` 中的 `tool_call_chunks`。子 Agent 的片段不进入主 Agent 的消息流。

客户端按消息和工具 index 保存准备过程，展示工具名、参数预览和收到的参数字符数。准备过程使用独立的 `tool_call_preparations` 字段，不能创建子任务、打开文件产物或作为已执行调用。最终完整模型消息替换准备数据及 streamed content，避免重复工具和重复文本。流中止后仍未完成的调用显示为未执行。

参数预览使用现有 `best-effort-json-parser`，只保留并解析最前面的 4096 个 UTF-16 code units，后续片段只累计字符数。因此预览不等于完整、有效的工具参数，位于较后面的字段可能到最终消息时才出现；执行仍使用内核验证后的完整参数。Bash 新增可选的 `description`，文件写入和替换工具提示模型先生成路径。Nous 没有移植上游的全量懒扫描器或持久化格式。

## 运行事件检查器

参考上游 [Session Inspector](https://github.com/deepseek-ai/deepseek-harness/tree/5badb15009/packages/experimental/session-inspector)，在已有会话的标题栏加入运行事件入口。检查器可选择该会话的 run、分页浏览原始事件、筛选已加载的事件类型，并查看事件 JSON。刷新由用户触发。

`GET /api/v1/threads/{tid}/runs/{rid}/events/raw?after=0&limit=200` 查询 canonical event store，包括普通 SSE 隐藏的 transcript 和 model commit 事件。响应为 `events`、`next_after`、`has_more`；事件按 seq 升序，游标是最后一个返回的 seq，空页保持原游标并返回空数组。默认 limit 为 200，范围为 1–1000；服务端验证 run 属于 tid，并禁止缓存。Trace event 可能被丢弃或尚未刷入存储，页面不是完整的模型上下文证明。原始事件可能包含输入、工具参数和内部审计内容，访问沿用 Agent API 的部署访问策略。

## 会话输入草稿

参考上游 [结构化草稿初始化](https://github.com/deepseek-ai/deepseek-harness/blob/5badb15009/.agents/notes/implemented/architecture/2026-09-30-structured-draft-initialization.md)，将文本草稿按会话保存到浏览器 localStorage，键为 `nous.draft.<thread_id>`。首次输入对象读取得到已保存文本，会话切换读取对应草稿，已有草稿优先于技能创建的预填文本。

提交回调被完整等待；成功后只清理本次提交的附件和仍等于提交内容的文本。切换会话或输入新文本后，旧提交的完成回调不会清除新草稿。损坏或无法访问的浏览器存储不阻止输入。保存范围为文本，Blob 附件和选中的 Skill 仍属于内存状态；不声称提供上游的文件、文件夹和 Session 引用组件恢复。

## 验证

新增回归覆盖首个工具片段、并行 index、嵌套 JSON 和转义、已发布对象不被修改、512 KiB 参数的有限预览、最终消息替换、治理补偿、草稿隔离及存储异常。Go 回归覆盖原始事件分页和 run 归属、工具准备片段的 SSE 投影及子 Agent 流隔离。

前端检查使用现代 Node 运行 `node --test src/core/**/*.test.mjs`（44 项通过）和 `tsc --noEmit`。Go 全量检查 `go test ./...` 通过，`go run ./internal/tools/layercheck` 检查 55 个包，无分层违规。

浏览器在 1440×900 和 390×844 视口验证了事件分页、类型筛选、JSON 详情、草稿重载和会话隔离；桌面端还验证了侧边栏会话切换。提交失败保留草稿，等待提交时新输入的文本不会被旧提交清除，新会话草稿转移到正式会话 ID 后可在失败和重载时恢复。浏览器检查采用本地脚本化 API 响应，不调用付费模型服务。
