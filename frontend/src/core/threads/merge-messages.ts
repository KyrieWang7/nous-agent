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
  const nextMessageIds = new Set(nextMessages.map((m) => m.id).filter(Boolean));

  const preserved: Message[] = [];
  for (const previousMessage of previousMessages) {
    if (!previousMessage.id || nextMessageIds.has(previousMessage.id)) {
      continue;
    }
    if (
      previousMessage.type === "human" &&
      nextMessages.some((nextMessage) =>
        isSameHumanMessage(previousMessage, nextMessage),
      )
    ) {
      continue;
    }
    preserved.push(previousMessage);
  }

  return preserved.length > 0
    ? [...preserved, ...nextMessages]
    : nextMessages;
}
