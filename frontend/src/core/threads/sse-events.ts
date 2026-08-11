import type { Message, MessageContent, RunRiskVerdict } from "@/core/types";

export type { RunRiskVerdict } from "@/core/types";

/**
 * The wire protocol intentionally carries the provider's raw risk level. The
 * UI also needs a small, stable vocabulary for deciding how to handle it.
 */
export interface MessageReplacement {
  content: MessageContent;
  messageId?: string;
  reason?: string;
}

/**
 * A missing safety verdict must never turn an unverified model response into
 * a normal assistant answer. This text is only used when the backend sends a
 * successful terminal event without a usable verdict.
 */
export const FAIL_CLOSED_RESPONSE =
  "The response could not be verified by the safety policy.";

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object"
    ? (value as Record<string, unknown>)
    : null;
}

function asNonEmptyString(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() !== "" ? value : undefined;
}

/**
 * Decode both the Go runtime payload and the older Python custom payload.
 * Python integrations sometimes call the replacement field `replacement`,
 * while Go uses the canonical `content` field.
 */
export function parseMessageReplacement(
  value: unknown,
): MessageReplacement | null {
  const record = asRecord(value);
  if (record?.type !== "message_replace") return null;

  const content = record.content ?? record.replacement;
  if (typeof content !== "string" && !Array.isArray(content)) return null;

  const replacement: MessageReplacement = {
    content: content as MessageContent,
  };
  const messageId =
    asNonEmptyString(record.message_id) ??
    asNonEmptyString(record.messageId) ??
    asNonEmptyString(record.id);
  const reason = asNonEmptyString(record.reason);
  if (messageId) replacement.messageId = messageId;
  if (reason) replacement.reason = reason;
  return replacement;
}

/**
 * Replace one assistant message without depending on a backend-specific ID.
 * Explicit IDs win; otherwise the current streamed assistant (or the last AI
 * message) is the only safe fallback. The returned array is always immutable.
 */
export function replaceAssistantMessage(
  messages: Message[],
  replacement: MessageReplacement,
  activeMessageId?: string | null,
): { messages: Message[]; messageId: string | null } {
  const assistantIndexByID = (id: string | undefined): number => {
    if (!id) return -1;
    return messages.findIndex(
      (message) => message.id === id && message.type === "ai",
    );
  };

  const requestedId = replacement.messageId ?? activeMessageId ?? undefined;
  let index = assistantIndexByID(requestedId);
  if (index < 0 && activeMessageId && activeMessageId !== requestedId) {
    index = assistantIndexByID(activeMessageId);
  }
  if (index < 0) {
    const lastIndex = messages.length - 1;
    if (messages[lastIndex]?.type === "ai") {
      index = lastIndex;
    }
  }

  if (index >= 0) {
    const current = messages[index]!;
    const next = messages.slice();
    const nextMessage = { ...current, content: replacement.content };
    if (nextMessage.additional_kwargs) {
      const additionalKwargs = { ...nextMessage.additional_kwargs };
      delete additionalKwargs.reasoning_content;
      nextMessage.additional_kwargs = additionalKwargs;
    }
    delete (nextMessage as Partial<Record<string, unknown>>).tool_calls;
    delete (nextMessage as Partial<Record<string, unknown>>).tool_call_chunks;
    delete (nextMessage as Partial<Record<string, unknown>>).invalid_tool_calls;
    next[index] = nextMessage;
    return { messages: next, messageId: current.id ?? null };
  }

  // A replacement can race the initial messages event after reconnect. Keep
  // the safety result visible instead of silently dropping the audit event.
  const generatedID = replacement.messageId ?? `replacement-${messages.length}`;
  return {
    messages: [
      ...messages,
      { id: generatedID, type: "ai", content: replacement.content },
    ],
    messageId: generatedID,
  };
}

/** Normalize the raw provider level into the client decision vocabulary. */
export function classifyRiskLevel(value: unknown): RunRiskVerdict {
  const record = asRecord(value);
  const raw =
    asNonEmptyString(record?.risk_level) ?? asNonEmptyString(record?.riskLevel);
  if (!raw) return "unknown";

  switch (raw.trim().toLowerCase()) {
    case "pass":
    case "passed":
    case "allow":
    case "allowed":
    case "safe":
    case "none":
    case "low":
      return "pass";
    case "review":
    case "review_required":
    case "medium":
    case "moderate":
      return "review";
    case "block":
    case "blocked":
    case "deny":
    case "denied":
    case "high":
    case "critical":
    case "unknown":
      return "block";
    default:
      // Unknown provider values are intentionally not treated as safe.
      return "unknown";
  }
}

export function rawRiskLevel(value: unknown): string | null {
  const record = asRecord(value);
  return (
    asNonEmptyString(record?.risk_level) ??
    asNonEmptyString(record?.riskLevel) ??
    null
  );
}

export function isSuccessfulRunEnd(value: unknown): boolean {
  const status = asNonEmptyString(asRecord(value)?.status)?.toLowerCase();
  if (!status) return true;
  if (
    status === "error" ||
    status === "failed" ||
    status === "cancelled" ||
    status === "canceled"
  ) {
    return false;
  }
  return (
    status === "completed" ||
    status === "complete" ||
    status === "success" ||
    status === "succeeded" ||
    status === "ok" ||
    status === "done" ||
    status === "finished"
  );
}

/**
 * Decide whether the client must suppress the terminal response. A blocked
 * run that already emitted its audit/replacement events is safe to render;
 * otherwise an absent/unknown verdict is treated as unsafe.
 */
export function shouldFailClosedRunEnd(
  value: unknown,
  guardrailBlocked: boolean,
  replacementReceived: boolean,
): boolean {
  const verdict = classifyRiskLevel(value);
  return (
    isSuccessfulRunEnd(value) &&
    (verdict === "unknown" ||
      (verdict === "block" && !guardrailBlocked && !replacementReceived))
  );
}
