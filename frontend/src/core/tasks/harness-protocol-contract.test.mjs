import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const contract = JSON.parse(
  await readFile(
    new URL("../../../../contracts/harness_protocol_contract.json", import.meta.url),
    "utf8",
  ),
);
const pageSource = await readFile(
  new URL("../../app/workspace/chats/[thread_id]/page.tsx", import.meta.url),
  "utf8",
);
const messageListSource = await readFile(
  new URL("../../components/workspace/messages/message-list.tsx", import.meta.url),
  "utf8",
);

test("frontend sends every shared runtime context field", () => {
  for (const field of contract.runtime_context_fields) {
    assert.match(pageSource, new RegExp(`\\b${field}\\s*:`));
  }
});

test("frontend task cards consume every canonical task argument", () => {
  for (const field of contract.task_required_fields) {
    assert.match(messageListSource, new RegExp(`args(?:\\?\\.|\\.)${field}\\b`));
  }
});
