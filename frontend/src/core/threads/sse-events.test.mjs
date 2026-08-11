import assert from "node:assert/strict";
import test from "node:test";

import { MessageManager } from "./message-manager.ts";
import {
  FAIL_CLOSED_RESPONSE,
  classifyRiskLevel,
  isSuccessfulRunEnd,
  parseMessageReplacement,
  replaceAssistantMessage,
  shouldFailClosedRunEnd,
} from "./sse-events.ts";

test("parses the Go message_replace custom payload", () => {
  assert.deepEqual(
    parseMessageReplacement({
      type: "message_replace",
      content: "safe replacement",
      message_id: "run-1:0",
      reason: "policy",
    }),
    {
      content: "safe replacement",
      messageId: "run-1:0",
      reason: "policy",
    },
  );
});

test("accepts the Python replacement alias and explicit message id", () => {
  assert.deepEqual(
    parseMessageReplacement({
      type: "message_replace",
      replacement: "safe",
      messageId: "ai-1",
    }),
    { content: "safe", messageId: "ai-1" },
  );
});

test("replaces the active assistant and never a tool message", () => {
  const messages = [
    {
      id: "ai-1",
      type: "ai",
      content: "unsafe",
      additional_kwargs: { reasoning_content: "unsafe reasoning" },
    },
    { id: "tool-1", type: "tool", content: "tool output", tool_call_id: "c1" },
  ];

  const replaced = replaceAssistantMessage(
    messages,
    { content: "safe", messageId: "ai-1" },
    "ai-1",
  );
  assert.equal(replaced.messageId, "ai-1");
  assert.equal(replaced.messages[0].content, "safe");
  assert.deepEqual(replaced.messages[0].additional_kwargs, {});
  assert.equal(replaced.messages[1].content, "tool output");

  const fallback = replaceAssistantMessage(
    messages,
    { content: "safe" },
    "missing-id",
  );
  assert.equal(fallback.messages[0].content, "unsafe");
  assert.equal(fallback.messages[1].content, "tool output");
  assert.equal(fallback.messages[2].content, "safe");
});

test("message manager does not resurrect rejected text after replacement", () => {
  const manager = new MessageManager();
  manager.add({ id: "ai-1", type: "AIMessageChunk", content: "unsafe" });
  assert.equal(manager.replaceContent("ai-1", "safe"), true);
  manager.add({ id: "ai-1", type: "AIMessageChunk", content: " tail" });
  assert.equal(manager.get("ai-1")?.message.content, "safe tail");
});

test("risk levels are normalized conservatively", () => {
  assert.equal(classifyRiskLevel({ risk_level: "pass" }), "pass");
  assert.equal(classifyRiskLevel({ risk_level: "medium" }), "review");
  assert.equal(classifyRiskLevel({ risk_level: "high" }), "block");
  assert.equal(classifyRiskLevel({ risk_level: "unknown" }), "block");
  assert.equal(classifyRiskLevel({ risk_level: "vendor-specific" }), "unknown");
  assert.equal(classifyRiskLevel({}), "unknown");
  assert.equal(classifyRiskLevel({ action: "pass" }), "unknown");
});

test("missing verdict is only fail-closed for a successful terminal run", () => {
  assert.equal(isSuccessfulRunEnd({ status: "completed" }), true);
  assert.equal(isSuccessfulRunEnd({ status: "error" }), false);
  assert.equal(
    shouldFailClosedRunEnd({ status: "completed" }, false, false),
    true,
  );
  assert.equal(
    shouldFailClosedRunEnd(
      { status: "completed", risk_level: "block" },
      true,
      false,
    ),
    false,
  );
  assert.equal(
    shouldFailClosedRunEnd({ status: "error" }, false, false),
    false,
  );
  assert.equal(FAIL_CLOSED_RESPONSE.length > 0, true);
});
