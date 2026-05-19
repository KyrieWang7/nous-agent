import type { BaseMessage } from "@/core/types";

import type { AgentThread } from "./types";

export function pathOfThread(threadId: string) {
  return `/workspace/chats/${threadId}`;
}

export function textOfMessage(message: BaseMessage) {
  if (typeof message.content === "string") {
    return message.content;
  } else if (Array.isArray(message.content)) {
    const textPart = message.content.find(
      (part): part is { type: "text"; text: string } =>
        part.type === "text" && "text" in part,
    );
    return textPart?.text ?? null;
  }
  return null;
}

export function cleanThreadTitle(title: string | null | undefined) {
  if (!title || title === "Untitled") {
    return "Untitled";
  }

  const contentBlockMatch = title.match(
    /^\[\{\s*['"]type['"]:\s*['"]text['"]\s*,\s*['"]text['"]:\s*['"]([^'"]+)/,
  );
  if (contentBlockMatch?.[1]) {
    return contentBlockMatch[1];
  }

  return title;
}

export function titleOfThread(thread: AgentThread) {
  if (thread.values && "title" in thread.values) {
    return cleanThreadTitle(thread.values.title);
  }
  return "Untitled";
}
