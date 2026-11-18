import assert from "node:assert/strict";
import test from "node:test";
import { childTree, isChildActive } from "./types.ts";
const child = (id, parent) => ({
  id,
  parent_session_id: parent,
  result: { status: "completed" },
});
test("child catalog preserves descendants, orphans and cycles exactly once", () => {
  const rows = childTree([
    child("a", "b"),
    child("b", "a"),
    child("root"),
    child("leaf", "root"),
    child("orphan", "gone"),
  ]);
  assert.equal(rows.length, 5);
  assert.equal(new Set(rows.map((row) => row.session.id)).size, 5);
  assert.equal(rows.find((row) => row.session.id === "leaf").depth, 1);
  assert.equal(rows.find((row) => row.session.id === "orphan").depth, 0);
});
test("cancellation and interruption are settled statuses, wait timeouts cannot create failures", () => {
  for (const status of ["pending", "running"])
    assert.equal(isChildActive(status), true);
  for (const status of [
    "completed",
    "failed",
    "cancelled",
    "timed_out",
    "interrupted",
  ])
    assert.equal(isChildActive(status), false);
});
