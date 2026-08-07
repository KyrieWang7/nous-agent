import assert from "node:assert/strict";
import test from "node:test";

import {
  findEquivalentMessageIndex,
  mergeSSEValuesMessages,
} from "./merge-messages.ts";

test("drops optimistic human duplicate when values include the same content", () => {
  const optimistic = {
    id: "optimistic-id",
    type: "human",
    content: [{ type: "text", text: "你可以做什么" }],
  };
  const canonical = {
    id: "backend-id",
    type: "human",
    content: [{ type: "text", text: "你可以做什么" }],
  };

  const merged = mergeSSEValuesMessages([optimistic], [canonical]);

  assert.deepEqual(merged, [canonical]);
});

test("preserves genuinely local messages when values do not contain them", () => {
  const local = {
    id: "local-id",
    type: "human",
    content: [{ type: "text", text: "正在发送的下一条" }],
  };
  const canonical = {
    id: "backend-id",
    type: "ai",
    content: "上一条回复",
  };

  const merged = mergeSSEValuesMessages([local], [canonical]);

  assert.deepEqual(merged, [local, canonical]);
});

test("replaces streamed assistant chunks with the authoritative values snapshot", () => {
  const streamed = {
    id: "run-1:0",
    type: "ai",
    content: "partial",
  };
  const canonical = {
    id: "backend-id",
    type: "ai",
    content: "complete",
  };

  const merged = mergeSSEValuesMessages([streamed], [canonical]);

  assert.deepEqual(merged, [canonical]);
});

test("matches final tool messages to their authoritative snapshot entries", () => {
  const messages = [
    {
      id: "assistant-canonical",
      type: "ai",
      content: "",
      tool_calls: [{ id: "call-1", name: "ask_clarification", args: {} }],
    },
    {
      id: "tool-canonical",
      type: "tool",
      content: "Which environment?",
      tool_call_id: "call-1",
    },
  ];

  assert.equal(
    findEquivalentMessageIndex(messages, {
      id: "assistant-final-event",
      type: "ai",
      content: "",
      tool_calls: [{ id: "call-1", name: "ask_clarification", args: {} }],
    }),
    0,
  );
  assert.equal(
    findEquivalentMessageIndex(messages, {
      id: "tool-final-event",
      type: "tool",
      content: "Which environment?",
      tool_call_id: "call-1",
    }),
    1,
  );
});
