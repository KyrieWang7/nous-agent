"""Prompt templates for memory update and injection."""

from typing import Any

try:
    import tiktoken

    TIKTOKEN_AVAILABLE = True
except ImportError:
    TIKTOKEN_AVAILABLE = False

# Prompt template for updating memory based on conversation
MEMORY_UPDATE_PROMPT = """你是一个记忆管理系统。你的任务是分析对话内容，并更新用户的记忆档案。

**重要：所有 summary 和 fact 的 content 必须使用中文输出。** 技术术语、专有名词、公司名和项目名保留原始形式（如 DeepSeek、LangGraph、PostgreSQL 等）。

当前记忆状态：
<current_memory>
{current_memory}
</current_memory>

需要处理的新对话：
<conversation>
{conversation}
</conversation>

分析指引：
1. 分析对话中关于用户的重要信息
2. 提取相关的事实、偏好和上下文，包含具体细节（数字、名称、技术）
3. 按照以下长度指引更新记忆各分区

{correction_hint}

记忆分区指引：

**用户上下文**（当前状态 - 简明摘要）：
- workContext（工作）：职业角色、公司、关键项目、主要技术栈（2-3句话）
  示例：核心贡献者，项目名及数据（16k+ stars），技术栈
- personalContext（个人）：语言能力、沟通偏好、关键兴趣（1-2句话）
  示例：双语能力、特定兴趣领域、专长领域
- topOfMind（近期关注）：多个进行中的关注点和优先事项（3-5句话，详细段落）
  示例：主要项目工作、并行技术调研、持续学习/追踪
  包含：正在实施的工作、排查的问题、市场/研究兴趣
  注意：应捕捉多个并行的关注领域，而非仅一个任务

**历史背景**（时间维度 - 丰富段落）：
- recentMonths（近几个月）：近期活动的详细总结（4-6句话或1-2段）
  时间线：最近1-3个月的交互
  包含：探索的技术、参与的项目、解决的问题、展现的兴趣
- earlierContext（更早上下文）：重要的历史模式（3-5句话或1段）
  时间线：3-12个月前
  包含：过去的项目、学习历程、已建立的模式
- longTermBackground（长期背景）：持续存在的背景和基础信息（2-4句话）
  时间线：整体/基础性信息
  包含：核心专长、长期兴趣、基本工作风格

**事实提取**：
- 提取具体的、可量化的细节（如「16k+ GitHub stars」「200+ 数据集」）
- 包含专有名词（公司名、项目名、技术名称）
- 保留技术术语和版本号
- 类别：
  * preference：用户偏好/不喜欢的工具、风格、方法
  * knowledge：特定专长、掌握的技术、领域知识
  * context：背景事实（职位、项目、地点、语言）
  * behavior：工作模式、沟通习惯、解决问题的方法
  * goal：明确的目标、学习目标、项目目标
- 置信度：
  * 0.9-1.0：明确陈述的事实（「我在做 X」「我的角色是 Y」）
  * 0.7-0.8：从行为/讨论中强烈暗示的
  * 0.5-0.6：推断的模式（谨慎使用，仅用于明确的模式）

**分区归属**：
- workContext：当前工作、活跃项目、主要技术栈
- personalContext：语言、性格、工作以外的兴趣
- topOfMind：用户近期关注的多个优先事项和关注领域（更新最频繁）
  应捕捉 3-5 个并行主题：主要工作、附带探索、学习/追踪兴趣
- recentMonths：近期技术探索和工作的详细记录
- earlierContext：略早期但仍相关的交互模式
- longTermBackground：关于用户的不变的基础事实

输出格式（JSON）：
{{
  "user": {{
    "workContext": {{ "summary": "...", "shouldUpdate": true/false }},
    "personalContext": {{ "summary": "...", "shouldUpdate": true/false }},
    "topOfMind": {{ "summary": "...", "shouldUpdate": true/false }}
  }},
  "history": {{
    "recentMonths": {{ "summary": "...", "shouldUpdate": true/false }},
    "earlierContext": {{ "summary": "...", "shouldUpdate": true/false }},
    "longTermBackground": {{ "summary": "...", "shouldUpdate": true/false }}
  }},
  "newFacts": [
    {{ "content": "...", "category": "preference|knowledge|context|behavior|goal", "confidence": 0.0-1.0 }}
  ],
  "factsToRemove": ["fact_id_1", "fact_id_2"]
}}

重要规则：
- 仅在有有意义的新信息时设置 shouldUpdate=true
- 遵循长度指引：workContext/personalContext 简洁（1-3句），topOfMind 和 history 各分区详细（段落）
- 在事实中包含具体指标、版本号和专有名词
- 仅添加明确陈述（0.9+）或强烈暗示（0.7+）的事实
- 移除被新信息否定的事实
- 更新 topOfMind 时，整合新的关注领域，同时移除已完成/放弃的
  保持 3-5 个仍活跃且相关的并行主题
- 对于 history 各分区，按时间顺序整合新信息到适当的时间段
- 保持技术准确性 - 保留技术、公司、项目的精确名称
- 聚焦于对未来交互和个性化有用的信息
- **所有 summary 和 fact content 使用中文书写，技术术语保留原文**

仅返回有效的 JSON，不要解释或 markdown。"""


FACT_EXTRACTION_PROMPT = """从以下消息中提取关于用户的事实信息。**所有 content 使用中文输出，技术术语保留原文。**

消息：
{message}

按以下 JSON 格式提取事实：
{{
  "facts": [
    {{ "content": "...", "category": "preference|knowledge|context|behavior|goal", "confidence": 0.0-1.0 }}
  ]
}}

类别：
- preference：用户偏好（喜好/厌恶、风格、工具）
- knowledge：用户的专长或知识领域
- context：背景信息（地点、职位、项目）
- behavior：行为模式
- goal：用户的目标或目的

规则：
- 仅提取清晰、具体的事实
- 置信度应反映确定性（明确陈述 = 0.9+，暗示 = 0.6-0.8）
- 跳过模糊或临时性的信息

仅返回有效的 JSON。"""


def _count_tokens(text: str, encoding_name: str = "cl100k_base") -> int:
    """Count tokens in text using tiktoken.

    Args:
        text: The text to count tokens for.
        encoding_name: The encoding to use (default: cl100k_base for GPT-4/3.5).

    Returns:
        The number of tokens in the text.
    """
    if not TIKTOKEN_AVAILABLE:
        # Fallback to character-based estimation if tiktoken is not available
        return len(text) // 4

    try:
        encoding = tiktoken.get_encoding(encoding_name)
        return len(encoding.encode(text))
    except Exception:
        # Fallback to character-based estimation on error
        return len(text) // 4


def format_memory_for_injection(memory_data: dict[str, Any], max_tokens: int = 2000) -> str:
    """Format memory data for injection into system prompt.

    Args:
        memory_data: The memory data dictionary.
        max_tokens: Maximum tokens to use (counted via tiktoken for accuracy).

    Returns:
        Formatted memory string for system prompt injection.
    """
    if not memory_data:
        return ""

    sections = []

    # Format user context
    user_data = memory_data.get("user", {})
    if user_data:
        user_sections = []

        work_ctx = user_data.get("workContext", {})
        if work_ctx.get("summary"):
            user_sections.append(f"Work: {work_ctx['summary']}")

        personal_ctx = user_data.get("personalContext", {})
        if personal_ctx.get("summary"):
            user_sections.append(f"Personal: {personal_ctx['summary']}")

        top_of_mind = user_data.get("topOfMind", {})
        if top_of_mind.get("summary"):
            user_sections.append(f"Current Focus: {top_of_mind['summary']}")

        if user_sections:
            sections.append("User Context:\n" + "\n".join(f"- {s}" for s in user_sections))

    # Format history
    history_data = memory_data.get("history", {})
    if history_data:
        history_sections = []

        recent = history_data.get("recentMonths", {})
        if recent.get("summary"):
            history_sections.append(f"Recent: {recent['summary']}")

        earlier = history_data.get("earlierContext", {})
        if earlier.get("summary"):
            history_sections.append(f"Earlier: {earlier['summary']}")

        if history_sections:
            sections.append("History:\n" + "\n".join(f"- {s}" for s in history_sections))

    if not sections:
        return ""

    result = "\n\n".join(sections)

    # Use accurate token counting with tiktoken
    token_count = _count_tokens(result)
    if token_count > max_tokens:
        # Truncate to fit within token limit
        # Estimate characters to remove based on token ratio
        char_per_token = len(result) / token_count
        target_chars = int(max_tokens * char_per_token * 0.95)  # 95% to leave margin
        result = result[:target_chars] + "\n..."

    return result


def format_conversation_for_update(messages: list[Any]) -> str:
    """Format conversation messages for memory update prompt.

    Args:
        messages: List of conversation messages.

    Returns:
        Formatted conversation string.
    """
    lines = []
    for msg in messages:
        role = getattr(msg, "type", "unknown")
        content = getattr(msg, "content", str(msg))

        # Handle content that might be a list (multimodal)
        if isinstance(content, list):
            text_parts = [p.get("text", "") for p in content if isinstance(p, dict) and "text" in p]
            content = " ".join(text_parts) if text_parts else str(content)

        # Truncate very long messages
        if len(str(content)) > 1000:
            content = str(content)[:1000] + "..."

        if role == "human":
            lines.append(f"User: {content}")
        elif role == "ai":
            lines.append(f"Assistant: {content}")

    return "\n\n".join(lines)
