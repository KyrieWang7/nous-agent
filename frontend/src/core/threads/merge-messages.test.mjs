import assert from "node:assert/strict";
import test from "node:test";

import { mergeSSEValuesMessages } from "./merge-messages.ts";

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
