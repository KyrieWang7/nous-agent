import assert from "node:assert/strict";
import test from "node:test";

import { ServerEventCursor, ServerEventDecoder } from "./sse.ts";

test("decodes fragmented CRLF and multiline SSE frames", () => {
  const decoder = new ServerEventDecoder();
  assert.deepEqual(decoder.push("id: 41\r\nevent: mess"), []);
  assert.deepEqual(
    decoder.push('age\r\ndata: {"part":\r\ndata: "one"}\r\n\r\n'),
    [
      {
        id: "41",
        event: "message",
        data: '{"part":\n"one"}',
      },
    ],
  );
});

test("flushes a final frame and does not leak an explicit id to the next event", () => {
  const decoder = new ServerEventDecoder();
  assert.deepEqual(
    decoder.push(
      'id: 7\nevent: message\ndata: {"id":7}\n\nevent: status\ndata: {}\n\n',
    ),
    [
      { id: "7", event: "message", data: '{"id":7}' },
      { event: "status", data: "{}" },
    ],
  );
  assert.deepEqual(decoder.push("event: team_deleted\ndata: {}"), []);
  assert.deepEqual(decoder.finish(), [{ event: "team_deleted", data: "{}" }]);
});

test("cursor rejects replayed and out-of-order numeric message ids", () => {
  const cursor = new ServerEventCursor();
  assert.equal(cursor.value, undefined);
  assert.equal(cursor.accept(), true);
  assert.equal(cursor.accept("10"), true);
  assert.equal(cursor.value, "10");
  assert.equal(cursor.accept("10"), false);
  assert.equal(cursor.accept("9"), false);
  assert.equal(cursor.accept("11"), true);
  assert.equal(cursor.value, "11");
});
