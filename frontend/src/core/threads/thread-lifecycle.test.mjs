import assert from "node:assert/strict";
import test from "node:test";

import { isThreadNotFoundError } from "./thread-lifecycle.ts";

test("recognizes a missing Harness thread", () => {
  assert.equal(isThreadNotFoundError(new Error("getState failed: 404")), true);
});

test("does not turn unrelated failures into new conversations", () => {
  assert.equal(isThreadNotFoundError(new Error("getState failed: 500")), false);
  assert.equal(isThreadNotFoundError("getState failed: 404"), false);
});
