# 会话压缩 (Conversation Summarization)

DeerFlow 包含了自动会话压缩功能，以处理接近模型 Token 限制的超长对话。启用后，系统会自动压缩较旧的消息，同时保留最近的上下文。

## 概览 (Overview)

会话压缩功能利用了 `deepagents` 的 `SummarizationMiddleware` 以及自定义的 `PostgresBackend`。与其破坏性地清除旧消息，该系统会在压缩当前对话之前，智能地将历史上下文卸载存入 PostgreSQL 数据库。触发压缩时，它会：

1. 实时监控消息的 Token 数量。
2. 当达到阈值时触发压缩。
3. 保持最近的消息原样不变，同时筛选出较旧的对话片段进行压缩。
4. **安全地将这些较旧的消息卸载（Offload）** 到与当前对话线程关联的专用 `conversation_history` 数据库表中。
5. 将一条包含总结内容的 HumanMessage 注入回当前对话，并附注数据库存储位置，以确保大语言模型（LLM）知道原始上下文已被安全保留。
6. 确保 AI 和 Tool 消息对的连贯性不被切断。

## 配置 (Configuration)

压缩功能在 `config.yaml` 中的 `summarization` 键下进行配置：

```yaml
summarization:
  enabled: true
  model_name: null  # 使用默认模型或指定一个轻量级模型

  # 触发条件 (OR 逻辑 - 满足任意条件即触发压缩)
  trigger:
    - type: tokens
      value: 4000
    # 其他可选触发器
    # - type: messages
    #   value: 50
    # - type: fraction
    #   value: 0.8  # 模型最大输入 Token 的 80%

  # 上下文保留策略
  keep:
    type: messages
    value: 20

  # 用于压缩请求本身的 Token 截断限制
  trim_tokens_to_summarize: 4000

  # 自定义压缩提示词 (可选)
  summary_prompt: null
```

### 配置选项详解 (Configuration Options)

#### `enabled`
- **类型**: Boolean
- **默认值**: `false`
- **说明**: 启用或禁用自动压缩功能

#### `model_name`
- **类型**: String 或 null
- **默认值**: `null` (使用默认模型)
- **说明**: 用于生成总结的模型。建议使用轻量且高性价比的模型，如 `gpt-4o-mini` 或同等级别模型。

#### `trigger`
- **类型**: 单个 `ContextSize` 或 `ContextSize` 对象列表
- **必填**: 启用时至少需要指定一个触发器
- **说明**: 触发压缩的阈值。使用 OR（或）逻辑 - 满足**任何一个**阈值就会执行压缩。

**ContextSize 类型:**

1. **Token 触发器**: 当 Token 数量达到指定值时激活
   ```yaml
   trigger:
     type: tokens
     value: 4000
   ```

2. **消息数量触发器**: 当消息条数达到指定值时激活
   ```yaml
   trigger:
     type: messages
     value: 50
   ```

3. **比例触发器**: 当 Token 使用量达到模型最大输入限制的百分比时激活
   ```yaml
   trigger:
     type: fraction
     value: 0.8  # 最大输入 Token 的 80%
   ```

**多个触发器组合:**
```yaml
trigger:
  - type: tokens
    value: 4000
  - type: messages
    value: 50
```

#### `keep`
- **类型**: `ContextSize` 对象
- **默认值**: `{type: messages, value: 20}`
- **说明**: 指定在压缩后要保留多少最近的对话历史。

**示例:**
```yaml
# 保留最近的 20 条消息
keep:
  type: messages
  value: 20

# 保留最近的 3000 Tokens
keep:
  type: tokens
  value: 3000

# 保留模型最大输入限制 30%的近期内容
keep:
  type: fraction
  value: 0.3
```

#### `trim_tokens_to_summarize`
- **类型**: Integer 或 null
- **默认值**: `4000`
- **说明**: 准备传递给底层总结大模型时包含的旧消息的最大 Token 数。设置为 `null` 可跳过截断（对于极长对话不建议使用）。

#### `summary_prompt`
- **类型**: String 或 null
- **默认值**: `null` (使用 LangChain 默认的提示词)
- **说明**: 用于生成总结的自定义提示词模板。提示词应当引导模型提取最重要的上下文。

**默认提示词行为:**
默认的 LangChain 提示词会指示模型去：
- 提取最高质量/最相关的上下文
- 聚焦于对整体目标至关重要的信息
- 避免重复已完成的操作
- 仅返回提取出的上下文内容

## 运行机制 (How It Works)

### 压缩流 (Summarization Flow)

1. **监控 (Monitoring)**: 在每次调用大模型前，中间件会计算当前活跃消息历史的 Token 数量。
2. **触发检查 (Trigger Check)**: 如果满足任何配置的阈值，将触发压缩逻辑。
3. **消息分区 (Message Partitioning)**: 消息会被分为两部分：
   - 待压缩消息 (超出 `keep` 阈值的较旧消息)。
   - 待保留消息 (在 `keep` 阈值内的较新消息)。
4. **数据保存与卸载 (Data Preservation)**: 需要被压缩的旧消息会被整体打包成 Markdown 格式，并由 `PostgresBackend` 安全地保存到 PostgreSQL 中的 `conversation_history` 表里。
5. **生成总结 (Summary Generation)**: 系统会发起一次轻量级的 LLM 调用，将这些较旧的消息浓缩成一份简明的总结。
6. **上下文替换 (Context Replacement)**: 发送给 LLM 的活跃消息历史将被更新（该更新仅在单次请求层面生效，不会破坏底层的 LangGraph 状态机）：
   - 生成一条单独的总结消息，包含总结文本，并声明被卸载的历史文件的位置。
   - 最近的消息将被原样保留。
7. **AI与工具配对保护 (AI/Tool Pair Protection)**: 系统在计算截断边界时，确保 AI 消息及其对应的工具调用消息绑定在一起，防止被强行分开。

### Token 计算 (Token Counting)

- 基于字符数量使用近似 Token 计算机制。
- 对于 Anthropic 模型：每个 Token 约 3.3 个字符。
- 对于其他模型：使用 LangChain 的默认估算方法。
- 可以通过自定义的 `token_counter` 函数进行替换和定制。

### 消息保留机制 (Message Preservation)

即使发生压缩，中间件也会智能地保护关键上下文：

- **近期消息 (Recent Messages)**: 始终基于 `keep` 的配置完好保留。
- **AI/Tool 配对 (AI/Tool Pairs)**: 永不拆开 - 如果计算出的截断点正好落在工具消息序列中间，系统会自动向回调整边界以确保完整块不被切断。
- **总结格式 (Summary Format)**: 总结会以 HumanMessage 的形式注入，并声明：
  ```
  You are in the middle of a conversation that has been summarized.
  The full conversation history has been saved to [path] should you need to refer back to it for details.
  A condensed summary follows...
  ```
- **存储层 (Storage Layer)**: 数据库底层采用了 `ON DELETE CASCADE` 约束与 `threads(id)` 进行关联。一旦任何一个对话线程被删除，其关联的“卸载历史总结”也会自动被彻底擦除，以保障数据隐私与多租户隔离。

## 最佳实践 (Best Practices)

### 选择触发阈值 (Choosing Trigger Thresholds)

1. **Token 触发器**: 推荐用于大多数情况
   - 设置为该模型上下文窗口限制的 60-80%
   - 示例：如果上下文窗口是 8K，阈值可设为 4000-6000 Tokens。

2. **消息数触发器**: 适用于控制长轴对话
   - 非常适合那种用户发非常多句短消息的应用场景。
   - 示例：设为 50-100 条消息（取决于平均每条消息的长度）。

3. **比例触发器**: 多模型混切环境下的首选
   - 自动适应当前使用模型的最大容量。
   - 示例：0.8（该模型最大输入限制的 80%）。

### 选择保留策略 (Choosing Retention Policy `keep`)

1. **基于消息数保留**: 适用于绝大多数场景
   - 能保持对话中最自然的情境连贯性。
   - 推荐值：15-25 条消息。

2. **基于 Token 数保留**: 当你需要精确算力预算时的选择
   - 有利于严苛地卡死 Token 预算。
   - 推荐值：2000-4000 Tokens。

3. **基于比例保留**: 与比例触发器搭配使用
   - 会随着模型处理能力的提高而自动放大边界。
   - 推荐值：0.2-0.4 (最大输入的 20-40%)。

### 模型选择 (Model Selection)

- **推荐**: 对于生成压缩摘要，建议使用廉价的轻量级模型。
  - 示例: `gpt-4o-mini`, `claude-haiku`, 或者是其他国产低价大杯模型。
  - 总结回顾并不需要模型具有最顶尖的推理力。
  - 对于大并发应用，这将能显著节省成本开销。

- **默认设置**: 如果 `model_name` 设为 `null`，系统会复用当前对话正在使用的主模型。
  - 虽然会增加些许成本，但确保了多端逻辑的一致性，且非常适合简化部署配置。

### 优化提示 (Optimization Tips)

1. **混合使用触发器**: 结合 token 和消息数触发以应对各种极端长篇场景。
   ```yaml
   trigger:
     - type: tokens
       value: 4000
     - type: messages
       value: 50
   ```

2. **相对宽松的保留**: 初始先保留较多内容，随后根据表现微调。
   ```yaml
   keep:
     type: messages
     value: 25  # 从高额度起步，视情况再往下缩减
   ```

3. **策略性截断**: 限制被提交给摘要模型的冗余 Tokens 量。
   ```yaml
   trim_tokens_to_summarize: 4000  # 避免进行超级昂贵的摘要全解析调用
   ```

4. **实时追踪迭代**: 监控摘要的生成质量，适时调整提示词配置。

## 疑难解答 (Troubleshooting)

### 总结丢失关键信息 (Summary Quality Issues)

**问题现象**: 压缩后的历史丢失了重要的上下文信息。

**解决方案**:
1. 增大 `keep` 数值，保留更多未经压缩的原生消息。
2. 调低触发阈值，让系统采取“小步快跑”的多次短摘要代替一次大摘要。
3. 自定义 `summary_prompt`，命令 LLM 着重强调某些特定信息。
4. 更换一个更强大的模型处理文本摘要。

### 接口响应慢 (Performance Issues)

**问题现象**: 大并发时一触发总结，耗时剧增。

**解决方案**:
1. 强制使用极其快速的模型来制作摘要（例如 `gpt-4o-mini` 或专职的蒸馏模型）。
2. 降低 `trim_tokens_to_summarize` 的值，避免丢太多文本让其阅读。
3. 增加触发阈值，减少自动摘要执行的频率。

### Token Limit 依然爆满 (Token Limit Errors)

**问题现象**: 即便开启了摘要压缩功能，也发生了 Token Limit 超出导致挂掉的情况。

**解决方案**:
1. 调低触发阈值，让压缩干预介入得更早。
2. 降低 `keep` 数量，减少新对话所需的基石上下文。
3. 检查是否有用户单次发来的长文本或代码片段异常庞大。
4. 考虑到跨模型切换的问题，改用按比例触发的配置方案（Fraction-based）。

## 实现细节 (Implementation Details)

### 代码结构 (Code Structure)

- **主配置**: `src/config/summarization_config.py`
- **入口集成**: `src/agents/lead_agent/agent.py`
- **数据库表结构**: `src/storage/history.py` (定义了 `conversation_history` 表的创建逻辑及交互语句)
- **底层依赖桥接**: `src/agents/middlewares/postgres_backend.py` (实现了给 deepagents 用的 `BackendProtocol`)
- **核心引擎引擎**: 调用自开源库 `deepagents.middleware.summarization.SummarizationMiddleware`

### 中间件洋葱模型层级 (Middleware Order)

摘要压缩紧随 ThreadData 初始化与沙盒系统之后，但在自动标题截取及澄清反问系统之前：

1. ThreadDataMiddleware
2. SandboxMiddleware
3. **SummarizationMiddleware** ← 压缩发生在此处
4. TitleMiddleware
5. ClarificationMiddleware

### 状态管理 (State Management)

- **无状态跟踪**: 单次压缩判定仅仅与当次 LangGraph `RunnableConfig` 线程运行时数据挂钩。
- **原始状态免疫**: 中间件只在请求发往 LLM (`ModelRequest`) 的最后一米进行拦截替换，**绝不会逆向删改 LangGraph 自己名下的任何原生 Checkpoint 数据库日志**。
- **无损查阅**: 真正超长而被丢弃的对话被无缝路由进了独立的 PostgreSQL 表，前端 UI 依然能完完整整拉取所有聊天记录并进行二次操作，不丢失任何细节。

## 典型配置示例 (Example Configurations)

### 极简配置 (Minimal Configuration)
```yaml
summarization:
  enabled: true
  trigger:
    type: tokens
    value: 4000
  keep:
    type: messages
    value: 20
```

### 生产推荐配置 (Production Configuration)
```yaml
summarization:
  enabled: true
  model_name: gpt-4o-mini  # 使用高性价比模型进行摘要
  trigger:
    - type: tokens
      value: 6000
    - type: messages
      value: 75
  keep:
    type: messages
    value: 25
  trim_tokens_to_summarize: 5000
```

### 多模型自适应配置 (Multi-Model Configuration)
```yaml
summarization:
  enabled: true
  model_name: gpt-4o-mini
  trigger:
    type: fraction
    value: 0.7  # 模型最大限制的 70%
  keep:
    type: fraction
    value: 0.3  # 保留最大限制额的 30%
  trim_tokens_to_summarize: 4000
```

### 保守求稳配置 - 高质量 (Conservative Configuration (High Quality))
```yaml
summarization:
  enabled: true
  model_name: gpt-4  # 使用完整版的模型来获取最高质量的连贯性
  trigger:
    type: tokens
    value: 8000
  keep:
    type: messages
    value: 40  # 维持极其庞大的核心上下文
  trim_tokens_to_summarize: null  # 不进行任何截断
```

## 参考文档 (References)

- [DeepAgents Summarization 开源源码](https://github.com/langchain-ai/deepagents/)
- [LangChain 中间件生态系统](https://docs.langchain.com/oss/python/langchain/middleware)
