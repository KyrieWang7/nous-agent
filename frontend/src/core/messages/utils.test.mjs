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
      tool_calls: [
        { id: "call-1", name: "web_search", args: { query: "first" } },
      ],
    },
    {
      id: "tool-1",
      type: "tool",
      content: "first result",
      tool_call_id: "call-1",
    },
    { id: "ai-note", type: "ai", content: "I will verify one more source." },
    {
      id: "ai-2",
      type: "ai",
      content: "",
      additional_kwargs: { reasoning_content: "verify result" },
      tool_calls: [
        { id: "call-2", name: "web_search", args: { query: "second" } },
      ],
    },
    {
      id: "tool-2",
      type: "tool",
      content: "second result",
      tool_call_id: "call-2",
    },
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
    {
      id: "ai-1",
      type: "ai",
      content: "",
      additional_kwargs: { reasoning_content: "one" },
    },
    { id: "user-2", type: "human", content: "second" },
    {
      id: "ai-2",
      type: "ai",
      content: "",
      additional_kwargs: { reasoning_content: "two" },
    },
  ];

  const processing = groupMessages(messages, (group) => group.type).filter(
    (type) => type === "assistant:processing",
  );
  assert.equal(processing.length, 2);
});

test("unfinished task and artifact calls remain ordinary preparation activity", () => {
  for (const name of ["task", "present_files"]) {
    const message = {
      id: `preparing-${name}`,
      type: "ai",
      content: "",
      tool_call_preparations: [{ index: 0, name, args: {}, argumentLength: 1 }],
    };

    assert.deepEqual(
      groupMessages([message], (group) => group.type),
      ["assistant:processing"],
    );
    assert.equal(message.tool_calls, undefined);
  }
});

test("keeps a delegated task result with its task after earlier tool activity", () => {
  const messages = [
    { id: "u", type: "human", content: "delegate" },
    {
      id: "plan",
      type: "ai",
      content: "",
      tool_calls: [{ id: "todo", name: "write_todos", args: {} }],
    },
    { id: "planned", type: "tool", tool_call_id: "todo", content: "planned" },
    {
      id: "delegate",
      type: "ai",
      content: "",
      tool_calls: [
        { id: "child", name: "task", args: { subagent_type: "explore" } },
      ],
    },
    {
      id: "result",
      type: "tool",
      tool_call_id: "child",
      content: "Task Succeeded. Result: done",
      additional_kwargs: {
        subagent_status: "completed",
        subagent_session_id: "persisted-child",
      },
    },
    {
      id: "finish",
      type: "ai",
      content: "",
      tool_calls: [{ id: "finish-todo", name: "write_todos", args: {} }],
    },
    {
      id: "finished",
      type: "tool",
      tool_call_id: "finish-todo",
      content: "done",
    },
  ];
  const groups = groupMessages(messages, (g) => ({
    type: g.type,
    ids: g.messages.map((m) => m.id),
  }));
  assert.deepEqual(
    groups.find((g) => g.type === "assistant:subagent"),
    { type: "assistant:subagent", ids: ["delegate", "result"] },
  );
  assert.deepEqual(
    groups.filter((g) => g.type === "assistant:processing").map((g) => g.ids),
    [
      ["plan", "planned"],
      ["finish", "finished"],
    ],
  );
});
