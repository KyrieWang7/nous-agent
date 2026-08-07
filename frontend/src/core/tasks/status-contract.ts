/**
 * Backend↔frontend contract for the structured subagent status.
 *
 * Mirror of the Python module `src/subagents/status_contract.py`. The backend
 * stamps `ToolMessage.additional_kwargs.subagent_status` (and optional
 * `subagent_error`) using the same value set and prefix table; this module reads
 * that structured field and falls back to prefix-matching the task tool result
 * text for older messages that predate the structured stamp.
 *
 * The shared fixture at `contracts/subagent_status_contract.json` is the single
 * source of truth — both sides' tests load it and must agree.
 */

export const SUBAGENT_STATUS_KEY = "subagent_status";
export const SUBAGENT_ERROR_KEY = "subagent_error";

export type SubagentStatusValue =
  | "completed"
  | "failed"
  | "cancelled"
  | "timed_out"
  | "polling_timed_out";

export const SUBAGENT_STATUS_VALUES: readonly SubagentStatusValue[] = [
  "completed",
  "failed",
  "cancelled",
  "timed_out",
  "polling_timed_out",
];

// Prefix table — ordered most-specific-first because some prefixes are
// substrings of others ("Task timed out" vs "Task polling timed out", "Task
// failed" vs "Task failed. Error: ..."). Mirrors `_PREFIX_TO_STATUS` in the
// Python module.
const PREFIX_TO_STATUS: readonly [string, SubagentStatusValue][] = [
  ["Task Succeeded. Result:", "completed"],
  ["Task polling timed out", "polling_timed_out"],
  ["Task timed out", "timed_out"],
  ["Task cancelled by user", "cancelled"],
  ["Task failed.", "failed"],
  ["Error", "failed"],
];

/** Infer the structured status for a `task` tool result string. */
export function extractSubagentStatus(
  content: string,
): SubagentStatusValue | null {
  const trimmed = content.trim();
  for (const [prefix, status] of PREFIX_TO_STATUS) {
    if (trimmed.startsWith(prefix)) {
      return status;
    }
  }
  return null;
}

/**
 * Resolve a subagent terminal status from a tool message, preferring the
 * structured field stamped by the backend and falling back to the prefix table.
 * Returns `null` for non-terminal / streaming chunks so the caller keeps the
 * card on its in-progress placeholder.
 */
export function resolveSubagentStatus(
  additionalKwargs: Record<string, unknown> | undefined,
  resultText: string,
): { status: SubagentStatusValue | null; error?: string } {
  const structured = additionalKwargs?.[SUBAGENT_STATUS_KEY];
  if (
    typeof structured === "string" &&
    (SUBAGENT_STATUS_VALUES as readonly string[]).includes(structured)
  ) {
    const error = additionalKwargs?.[SUBAGENT_ERROR_KEY];
    return {
      status: structured as SubagentStatusValue,
      error: typeof error === "string" ? error : undefined,
    };
  }
  // Legacy fallback: parse the leading text of the result.
  return { status: extractSubagentStatus(resultText) };
}
