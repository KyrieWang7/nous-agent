import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import {
  SUBAGENT_STATUS_VALUES,
  extractSubagentStatus,
  resolveSubagentStatus,
} from "./status-contract.ts";

const contractPath = new URL(
  "../../../../contracts/subagent_status_contract.json",
  import.meta.url,
);
const contract = JSON.parse(await readFile(contractPath, "utf8"));

test("status vocabulary matches the shared backend contract", () => {
  assert.deepEqual(SUBAGENT_STATUS_VALUES, contract.valid_status_values);
});

for (const contractCase of contract.cases) {
  test(`shared subagent status case: ${contractCase.name}`, () => {
    assert.equal(
      extractSubagentStatus(contractCase.content),
      contractCase.expected_status,
    );
  });
}

test("structured status remains authoritative over legacy result text", () => {
  assert.deepEqual(
    resolveSubagentStatus(
      { subagent_status: "failed", subagent_error: "structured failure" },
      "Task Succeeded. Result: legacy text",
    ),
    { status: "failed", error: "structured failure" },
  );
});
