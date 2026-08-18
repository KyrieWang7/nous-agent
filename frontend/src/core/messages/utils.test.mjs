import assert from "node:assert/strict";
import test from "node:test";

import { groupMessages } from "./utils.ts";

test("groups all reasoning and tool activity in one user turn", () => {
  const messages = [
    { id: "user-1", type: "human", content: "research this" },
    {
      id: "ai-1",
      type: "ai",
      content: "",
      additional_kwargs: { reasoning_content: "search first" },
      tool_calls: [{ id: "call-1", name: "web_search", args: { query: "first" } }],
    },
    { id: "tool-1", type: "tool", content: "first result", tool_call_id: "call-1" },
    { id: "ai-note", type: "ai", content: "I will verify one more source." },
    {
      id: "ai-2",
      type: "ai",
      content: "",
      additional_kwargs: { reasoning_content: "verify result" },
      tool_calls: [{ id: "call-2", name: "web_search", args: { query: "second" } }],
    },
    { id: "tool-2", type: "tool", content: "second result", tool_call_id: "call-2" },
    { id: "ai-final", type: "ai", content: "Final report" },
  ];

  const groups = groupMessages(messages, (group) => ({
    type: group.type,
    ids: group.messages.map((message) => message.id),
  }));

  assert.deepEqual(groups, [
    { type: "human", ids: ["user-1"] },
    { type: "assistant:processing", ids: ["ai-1", "tool-1", "ai-2", "tool-2"] },
    { type: "assistant", ids: ["ai-note"] },
    { type: "assistant", ids: ["ai-final"] },
  ]);
});

test("starts a new processing group after the next human turn", () => {
  const messages = [
    { id: "user-1", type: "human", content: "first" },
    { id: "ai-1", type: "ai", content: "", additional_kwargs: { reasoning_content: "one" } },
    { id: "user-2", type: "human", content: "second" },
    { id: "ai-2", type: "ai", content: "", additional_kwargs: { reasoning_content: "two" } },
  ];

  const processing = groupMessages(messages, (group) => group.type).filter(
    (type) => type === "assistant:processing",
  );
  assert.equal(processing.length, 2);
});
