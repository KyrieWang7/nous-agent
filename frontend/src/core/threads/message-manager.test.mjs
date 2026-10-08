import assert from "node:assert/strict";
import test from "node:test";

import { MessageManager } from "./message-manager.ts";
import { ARGUMENT_PREVIEW_LIMIT } from "./tool-preparation.ts";

function delta(manager, call) {
  manager.add({
    id: "ai-1",
    type: "AIMessageChunk",
    content: "",
    tool_call_chunks: [call],
  });
  return manager.get("ai-1").message;
}

test("first tool fragment creates only a preparing presentation", () => {
  const manager = new MessageManager();
  const message = delta(manager, {
    index: 0,
    id: "call-1",
    name: "write_file",
    args: '{"path":"a.txt","content":"',
  });
  assert.equal(message.tool_calls, undefined);
  assert.equal(message.tool_call_preparations[0].name, "write_file");
  assert.equal(message.tool_call_preparations[0].args.path, "a.txt");
});

test("interleaved indices retain identities without mutating published snapshots", () => {
  const manager = new MessageManager();
  const first = delta(manager, {
    index: 1,
    id: "c2",
    name: "bash",
    args: '{"description":"sec',
  });
  const snapshot = structuredClone(first);
  delta(manager, {
    index: 0,
    id: "c1",
    name: "read_file",
    args: '{"path":"a.txt"}',
  });
  const final = delta(manager, { index: 1, args: 'ond","command":"pwd"}' });
  assert.deepEqual(first, snapshot);
  assert.deepEqual(
    final.tool_call_preparations.map((call) => call.id),
    ["c1", "c2"],
  );
  assert.equal(final.tool_call_preparations[1].args.description, "second");
});

test("nested keys and escaped text are decoded by the JSON parser", () => {
  const manager = new MessageManager();
  delta(manager, {
    index: 0,
    name: "read_file",
    args: '{"nested":{"path":"wrong"},"path":"a\\',
  });
  const final = delta(manager, { index: 0, args: 'n\\u4e2d.txt"}' });
  assert.equal(final.tool_call_preparations[0].args.path, "a\n中.txt");
  assert.equal(final.tool_call_preparations[0].args.nested.path, "wrong");
});

test("large streamed arguments retain a bounded preview and an exact character count", () => {
  const manager = new MessageManager();
  const prefix = '{"path":"big.txt","content":"';
  delta(manager, { index: 0, id: "c1", name: "write_file", args: prefix });
  for (let i = 0; i < 512; i++)
    delta(manager, { index: 0, args: "x".repeat(1024) });
  const prepared = manager.get("ai-1").message.tool_call_preparations[0];
  assert.equal(prepared.args.path, "big.txt");
  assert.ok(prepared.args.content.length < ARGUMENT_PREVIEW_LIMIT);
  assert.equal(prepared.argumentLength, prefix.length + 512 * 1024);
});

test("authoritative message replaces streamed text and all preparations", () => {
  const manager = new MessageManager();
  manager.add({ id: "ai-1", type: "AIMessageChunk", content: "hello" });
  delta(manager, {
    index: 0,
    id: "c1",
    name: "write_file",
    args: '{"path":"partial"',
  });
  const complete = {
    id: "ai-1",
    type: "ai",
    content: "hello",
    tool_calls: [
      {
        id: "c1",
        name: "write_file",
        args: { path: "final.txt", content: "full" },
      },
    ],
  };
  manager.add(complete);
  assert.deepEqual(manager.get("ai-1").message, complete);
});

test("compensation removes preparing calls and ignores their late fragments", () => {
  const manager = new MessageManager();
  delta(manager, { index: 0, name: "bash", args: '{"command":"' });
  manager.replaceContent("ai-1", "Blocked");
  const message = delta(manager, { index: 0, args: 'pwd"}' });
  assert.equal(message.content, "Blocked");
  assert.equal(message.tool_call_preparations, undefined);
});
