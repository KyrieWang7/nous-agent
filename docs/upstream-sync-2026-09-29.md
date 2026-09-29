# DeepSeek Harness → Nous Go 同步记录（2026-09-29）

## 对比基线

- 上游：`deepseek-ai/deepseek-harness`，`4878cdabd87d4041bdaff61d04c966883b9fd07a`，2026-09-28，`release(dsh): 0.2.0-rc.1 (#5387)`。本次执行 `git fetch origin` 后，参考目录的 HEAD 与 `origin/master` 相同。
- Nous：修改前为 `27ff1fbeb4e744ee367f93c4b6a991fed9404efd`，2026-08-19，`fix subagent harness lifecycle ownership`。
- Nous 没有记录一个可以直接合并的 DeepSeek fork 基点。8 月 19 日是本地最近提交日期，只作为筛选上游变化的时间窗口，不代表精确的上次同步点。
- 按 [Runtime v2 架构](architecture-v2.md) 继续维护独立 Go 实现，移植行为和回归约束，不复制 Cordis/TypeScript 架构，也不导入上游 Session 文件格式。
- 本次完成所列 Go 运行时适配项，包含独立 DeepSeek Messages/Files provider；桌面、账号体系和存储格式维持 Nous 架构，不表示整套上游产品逐项等价。Go 工具链声明仍为 1.25.0，本机验证使用 Go 1.25.6。

## 本次已落地

### 1. 主动压缩为模型输出预留上下文

上游 [0fadb08fbd](https://github.com/deepseek-ai/deepseek-harness/commit/0fadb08fbdd3316c4605fc4e6be96cb9990b56c6) 修复了压缩阈值按整个 context window 计算，导致请求在触发压缩前就因输入加输出超过窗口而失败的问题；后续 [555b664b08](https://github.com/deepseek-ai/deepseek-harness/commit/555b664b08) 又加入独立 headroom。

Nous 原来在进程装配时使用原始配置的 `context_length × 75%`。现在每次实际请求按路由模型计算：

```text
message_budget = routed_context - effective_output_tokens - headroom_tokens
trigger_tokens = max(1, floor(message_budget × 75%))
```

例如窗口 10000、输出预留 6000 时，原阈值为 7500，现在为 3000。没有剩余消息预算会在装配时报告配置错误。使用 provider 已解析的默认值，并计入 request MaxTokens、OpenAI extra_body 的输出覆盖优先级；named model、vision tier 和 fallback 均按实际选中的模型计算。system、tool schemas 与临时 messages 一并计入输入压力。阈值不写回共享 compactor，避免根/子 run 相互污染。

保留 Nous 原来的 75% 比例和摘要输出上限；新增可配置 `headroom_tokens`（默认 0），不把上游适配大窗口模型的固定 64K 作为所有模型的默认值。示例配置改为 `trigger_tokens: 0` 使用自动计算；已有的正数手动阈值保持其覆盖语义。

### 2. 超限恢复不再被普通压缩阈值阻挡

参考上游 [compaction-basic 的 overflow 分支](https://github.com/deepseek-ai/deepseek-harness/blob/4878cdabd87d4041bdaff61d04c966883b9fd07a/packages/compaction/compaction-basic/src/index.ts)。上游明确区分主动压力压缩和 provider 已确认的 context overflow。

Nous 的 router 原来声称“强制压缩”，实际上仍调用 `MaybeCompact`，因此 token 估算低于阈值、或历史条数低于保留窗口时不会恢复。现在原生 compactor 提供 `ForceCompact`：

- 收到 context overflow 时跳过主动阈值。
- 必要时将保留条数缩至不超过历史的一半，至少保留最后一条；仍使用原有工具事务切点规则。
- 摘要必须降低历史 token 估算值，才替换 History、标记 `Compacted` 并重试；更长或等长的摘要不提交。
- 保持现有重试上限；无法安全缩短时返回原错误，不盲目重发。
- 自定义仅实现 `MaybeCompact` 的 compactor 仍兼容；关闭 summarization 时不装配 compactor。

### 3. 不把流意外中断当作成功响应

参考上游 [llm-pi-ai 的 terminal/transport 语义](https://github.com/deepseek-ai/deepseek-harness/blob/4878cdabd87d4041bdaff61d04c966883b9fd07a/packages/llm/llm-pi-ai/src/stream.ts)：缺失协议终态属于传输失败，失败的部分输出不构成完整模型响应。

- OpenAI-compatible：`[DONE]`，或 `finish_reason` 后的干净 EOF，才构成正常结束。没有这些终态的 EOF 返回 `io.ErrUnexpectedEOF`，归类为 provider unavailable，不返回半截 assistant/tool-call 响应。
- 保留兼容端点仅发送 `[DONE]` 的既有行为；这不是所有 OpenAI 帧的完整协议校验。
- Anthropic：必须收到 `message_stop`；提前 EOF 报错，读错误只报告一次，正常结束后不再消费后续事件。修复了 scanner 失败后反复返回错误、调用方无法退出的问题。
- 保留 caller cancellation/deadline 的错误身份。router 原来的“文字或 reasoning 已输出就不重试、不降级”约束保持生效。

### 4. 失败 step 在关闭前补齐工具结果（第二批）

移植上游 [6a6f350b94](https://github.com/deepseek-ai/deepseek-harness/commit/6a6f350b9437cf24e34a34f39ee4dfd107897d0c) 的 live step recovery 语义，保留 Nous 的 canonical transcript 和 PostgreSQL 架构。

原来的 Loop 只把 executor 返回的 outcomes 加入 History，因此存在两类缺口：模型回复进入历史后、执行器启动前的权限/预算/治理失败；以及结果已经持久化、执行器却因后续治理失败没有返回 outcome。两者都会留下缺少 tool result 的 assistant 请求。

现在每个进入 History 的模型回复拥有独立的 step recovery：

- 在进入下一轮或返回 run 之前结算，覆盖失败、取消、治理 `Stop` / `Continue`、澄清拦截和部分执行。
- 原生 executor 的 `tool_start` 成功记录后才视为已启动。没有启动的缺失调用生成 `TOOL_NOT_STARTED`；已启动却没有成功记录结果的调用生成 `TOOL_OUTCOME_UNKNOWN`，提示先核实可能的副作用，不能盲目重试。
- 已成功记录的结果保持内容、多模态块和元数据，不覆盖为占位结果；所有补齐结果按 assistant 的调用顺序进入 History。
- executor 返回前等待已启动的并发调用退出，恢复过程不会与仍活跃的调用竞争。自定义 executor 也必须遵守排空契约；缺少原生启动记录的自定义 dispatch 保守标为结果未知。
- 补齐仍使用已有 `tool_result` 事件和幂等 key，先记录成功才进入 History。事件新增可选 `recovery_code` 字段，History 使用 `additional_kwargs.tool_recovery_code`；不新增数据库 migration。
- 取消后使用独立的 30 秒 cleanup context 完成记录。若恢复记录也失败，同时返回原始失败与恢复失败，不把写入失败的占位结果当作已提交。executor 也不再吞掉 BeforeTool/AfterTool 出错分支中的结果写入错误。
- 在 HTTP/RunManager 路径，失败 run 的 canonical transcript 在 `run_end` 之前保存；已验证事件重放结果与 History 相同，下一轮请求能够读取配对完整的工具事务。

边界：本批次处理当前进程内的 live step 收尾；不自动重跑工具，不把失败回合转为成功，不回写已关闭的历史回合，也不改变进程崩溃后将 run 标记为 interrupted 的策略。只有 raw `model_output_committed` 审计事实、尚未进入 History 的回复，不会因恢复逻辑被强行变成 model-visible transcript。治理已删除的工具调用不会复活。存储持续不可写时不能保证补齐成功，错误会明确返回。

### 5. 每次实际尝试持久化输入，并保留临时上下文

对应上游 [425a0a55e3](https://github.com/deepseek-ai/deepseek-harness/commit/425a0a55e31e5684bbeb64f336594e2e76316261)。router 在应用实际模型选项、必要压缩之后，通过内核回调先写 `model_input_committed`，写成功才调用 provider。事件包含 `attempt`、`model_name`、`effective_output_tokens`，重试和 fallback 使用独立幂等 key，首次尝试保留旧 key 形状。

压缩后重新执行纯 History trimming，并按消息 occurrence 协调临时消息的位置。不会再次运行收件箱读取、skill activation 等有副作用的 BeforeModel handlers。回归覆盖临时消息、重复历史、按路由模型预算及实际调用前持久化失败。compaction 事件及 run state 的 compacting/running 转换也迁入 router 路径，强制压缩沿用同一事件边界。

### 6. 动态工具集使用完整快照回放

对应上游 [bc8c0dbf40](https://github.com/deepseek-ai/deepseek-harness/commit/bc8c0dbf40)。Nous 保留 capability view、disclosure 和 immutable generation lease：每轮重新解析当前可用工具，每次实际模型尝试持久化完整 `Tools` 与 `GenerationID`。消费者按 `(execution_run_id, iteration, attempt)` 读取快照即可确定工具集；不用从工具执行结果反推权限。新增集成回归验证同一轮限流重试、下一轮撤去工具时，provider 实际请求与持久化快照一致，且 lifecycle side effect 不重复。

本适配提供完整工具快照语义，不发送上游专有的 `tool_addition` / `tool_removal` 增量历史块，因此不声称获得该协议的前缀缓存优化。

### 7. 独立 DeepSeek Messages 与 Files provider

对应上游 [99e22ebbeb](https://github.com/deepseek-ai/deepseek-harness/commit/99e22ebbeb) 及该基线下的 [llm-deepseek](https://github.com/deepseek-ai/deepseek-harness/tree/4878cdabd87d4041bdaff61d04c966883b9fd07a/packages/llm/llm-deepseek)。新增 `provider: deepseek`，默认根地址 `https://api.deepseek.com/anthropic`，正确拼接 `/v1/messages`，使用 `x-api-key` 和 Messages version header。原有 OpenAI-compatible 配置保持不变，示例配置提供显式启用方式。

- Complete 和 Stream 共用严格 Messages SSE 处理：完整 frame、多行 data、终态与 block 关闭检查、in-band error 分类、稀疏 wire index、缓存 token 用量及取消。输出达到 token 上限时丢弃不完整的 tool JSON，避免执行半截调用。
- 序列化 text、thinking、tools、用户/工具结果图片；检查工具配对，拒绝不能表示的内容。summary 和临时 system text 合并进本次 system 快照。
- thinking 签名和原生块顺序保存在 AdditionalKwargs，绑定模型及 canonical 内容摘要；治理改写、工具 ID 变化或换模型后不重用旧签名。
- 可选 `use_files: true` 将图片上传至 Files，使用相应 beta header；提供 upload/list/retrieve/delete 方法。上传限制 128 MiB，expiry 范围 1 小时至 30 天，自动图片上传使用 7 天期限。
- 上传缓存限制 256 个条目，隔离在拥有独立凭证的模型 generation 内，源图字节仍保留在 canonical transcript。缓存 ID 失效时清除并最多重传一次；不重跑工具。
- 禁止 Messages/Files 请求跟随重定向转发凭证；Files 与非流请求使用独立于 stream idle 的普通 HTTP 超时。未启用 Files 时，inline 图片请求上限 20 MiB。

适配边界：不引入上游账号登录、持久化 upload-index、全账号 quota 清理和产品附件 offload 服务。缓存可由 canonical 字节在重启后重建；不自动删除账号中的其他远端文件。Files API 不可用时明确返回错误，不声称支持该端点，也不悄悄丢图。新 provider 已通过本地 HTTP 协议测试，未使用真实账号做线上 API 验证。

### 8. Stream idle 与普通请求超时分离

对应上游 [d618bfebb4](https://github.com/deepseek-ai/deepseek-harness/commit/d618bfebb4)。新增 `stream_idle_timeout`（秒）：OpenAI-compatible/Anthropic 默认 120，DeepSeek Messages 默认 300。body 活动（包括 SSE heartbeat）刷新 idle 计时；持续活动的长响应不受普通 `timeout` 总时限中断。调用方的 cancellation/deadline 仍优先生效，reader Close 停止 watchdog。空闲中止归类 provider unavailable，保持已流出文字/reasoning 后不自动重试的约束。

## 与 Nous 架构不直接对应的上游项

| 上游变化 | 适配结果 |
| --- | --- |
| [Session V4 integration format](https://github.com/deepseek-ai/deepseek-harness/commit/669b724a78) | 保留 PostgreSQL canonical events/snapshots；工具结算、输入写前日志和回放不变量已覆盖，不迁移上游 JSONL schema |
| [子 Agent 默认委派深度 1](https://github.com/deepseek-ai/deepseek-harness/commit/04a3c30a04) | `config.Defaults()` 已为 1，dispatch 已检查并向子任务传递限制，无需重复修改 |
| 桌面/Web preview、账号登录、Session Log upload、跨平台窗口修复 | Nous 是独立 Next.js + Go 产品，这些不属于本次 Go harness 同步范围 |

## 验证

回归测试先在修改前观察到以下失败：提前 EOF 被返回为成功；Anthropic scanner 错误重复、终态后仍继续读；超限恢复未触发；3500-token 输入未在 4000-token 消息预算下提前压缩；没有剩余消息预算的配置被接受。

以下验证均已通过，分层检查扫描当前全部 package，无违规依赖：

```sh
cd agent-go
go test ./...
go test -race ./pkg/compaction ./pkg/modelrouter ./pkg/model/provider/openai ./pkg/model/provider/anthropic ./cmd/agentd
go vet ./...
go run ./internal/tools/layercheck
```

Provider 回归和生产装配测试使用本地 HTTP server/脚本化模型，不调用付费模型 API。本批次没有变更数据库 schema、Gateway 或前端，也没有将上游工作区切换到其他提交。

第二批新增的回归测试先复现了 capability/预算/AfterModel 失败留下悬空调用、结果写入失败后丢失工具状态、已持久化结果未进入 History，以及 BeforeTool/AfterTool 吞掉写入错误。验证还覆盖取消、并发排空、Stop/Continue、治理删去工具调用、自定义 executor 的保守分类，以及真实 Harness 经 HTTP 失败后持久化、事件重放和继续对话。

第二批以下检查均已通过：

```sh
cd agent-go
go test ./...
go test -race ./pkg/loop ./pkg/tool ./pkg/harness ./internal/transport/httpapi ./pkg/runtime/...
go vet ./...
go run ./internal/tools/layercheck
```

最终批次增加按路由请求预算、实际尝试 ledger、动态工具快照、重复历史、system/tool 压力、配置校验、stream idle/cancellation、Messages framing/replay 和 Files 失效恢复等回归。统一验收命令：

```sh
cd agent-go
go test ./...
go test -race ./pkg/compaction ./pkg/model/... ./pkg/modelrouter ./pkg/loop ./pkg/tool ./pkg/harness ./internal/transport/httpapi ./pkg/runtime/... ./cmd/agentd
go vet ./...
go run ./internal/tools/layercheck
```
