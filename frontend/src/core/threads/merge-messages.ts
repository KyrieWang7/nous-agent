import type { Message } from "@/core/types";

function contentSignature(message: Message): string {
  if (typeof message.content === "string") {
    return message.content.trim();
  }
  if (Array.isArray(message.content)) {
    return message.content
      .map((part) => {
        if (part.type === "text") {
          return part.text;
        }
        if (part.type === "image_url") {
          return typeof part.image_url === "string"
            ? part.image_url
            : part.image_url.url;
        }
        return "";
      })
      .join("\n")
      .trim();
  }
  return "";
}

function isSameHumanMessage(a: Message, b: Message): boolean {
  return (
    a.type === "human" &&
    b.type === "human" &&
    contentSignature(a) === contentSignature(b)
  );
}

export function mergeSSEValuesMessages(
  previousMessages: Message[],
  nextMessages: Message[],
): Message[] {
  // A values event is an authoritative state snapshot. Only optimistic human
  // messages can legitimately be newer than it; streamed assistant/tool
  // messages must be replaced or the completed turn is rendered twice.
  const preserved = previousMessages.filter(
    (previousMessage) =>
      previousMessage.type === "human" &&
      !nextMessages.some((nextMessage) =>
        isSameHumanMessage(previousMessage, nextMessage),
      ),
  );

  return preserved.length > 0
    ? [...preserved, ...nextMessages]
    : nextMessages;
}

export function findEquivalentMessageIndex(
  messages: Message[],
  candidate: Message,
): number {
  if (candidate.id) {
    const byId = messages.findIndex((message) => message.id === candidate.id);
    if (byId >= 0) return byId;
  }

  if (candidate.type === "human") {
    return messages.findIndex((message) =>
      isSameHumanMessage(message, candidate),
    );
  }

  if (candidate.type === "tool") {
    return messages.findIndex(
      (message) =>
        message.type === "tool" &&
        message.tool_call_id === candidate.tool_call_id,
    );
  }

  if (candidate.type === "ai" && candidate.tool_calls?.length) {
    const callIds = new Set(
      candidate.tool_calls.map((call) => call.id).filter(Boolean),
    );
    return messages.findIndex(
      (message) =>
        message.type === "ai" &&
        message.tool_calls?.some((call) => call.id && callIds.has(call.id)),
    );
  }

  const signature = contentSignature(candidate);
  if (signature) {
    return messages.findIndex(
      (message) =>
        message.type === candidate.type &&
        contentSignature(message) === signature,
    );
  }
  return -1;
}
