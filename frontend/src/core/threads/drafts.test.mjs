import assert from "node:assert/strict";
import test from "node:test";

import { readDraft, writeDraft } from "./drafts.ts";

function storage() {
  const values = new Map();
  return {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  };
}

test("drafts survive restoration and remain isolated by thread", () => {
  const local = storage();
  writeDraft(local, "a", "First draft\n第二行");
  writeDraft(local, "b", "Second draft");
  assert.equal(readDraft(local, "a"), "First draft\n第二行");
  assert.equal(readDraft(local, "b"), "Second draft");
  writeDraft(local, "a", "");
  assert.equal(readDraft(local, "a", "Prefill"), "Prefill");
  assert.equal(readDraft(local, "b"), "Second draft");
});

test("saved drafts take precedence over prefills", () => {
  const local = storage();
  writeDraft(local, "new", "User edit");
  assert.equal(readDraft(local, "new", "Create skill"), "User edit");
});

test("damaged, unsupported, or denied storage keeps composing available", () => {
  const local = storage();
  for (const raw of [
    '{"version":2,"text":"future"}',
    "broken",
    '{"version":1,"text":42}',
  ]) {
    local.setItem("nous.draft.a", raw);
    assert.equal(readDraft(local, "a", "Default"), "Default");
  }
  const denied = {
    getItem() {
      throw new Error("denied");
    },
    setItem() {
      throw new Error("denied");
    },
    removeItem() {
      throw new Error("denied");
    },
  };
  assert.equal(readDraft(denied, "a"), "");
  assert.doesNotThrow(() => writeDraft(denied, "a", "Draft"));
});
