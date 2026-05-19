from langchain_core.messages import AIMessage, HumanMessage

from src.models.patched_deepseek import PatchedChatDeepSeekOpenAI


def test_openai_compatible_deepseek_payload_preserves_reasoning_content():
    model = PatchedChatDeepSeekOpenAI(
        model="deepseek-v4-flash",
        api_key="test-key",
        base_url="https://api.deepseek.com",
        extra_body={"thinking": {"type": "enabled"}},
    )

    payload = model._get_request_payload(
        [
            HumanMessage(content="调研蔚来es9的售卖情况"),
            AIMessage(
                content="我会先加载研究方法。",
                additional_kwargs={"reasoning_content": "Need to use deep research."},
            ),
        ]
    )

    assert payload["messages"][1]["reasoning_content"] == "Need to use deep research."
