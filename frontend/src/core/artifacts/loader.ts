import type { ThreadStream } from "@/core/types/thread";

import type { AgentThreadState } from "../threads";

import { urlOfArtifact } from "./utils";

export async function loadArtifactContent({
  filepath,
  threadId,
}: {
  filepath: string;
  threadId: string;
}) {
  let enhancedFilepath = filepath;
  if (filepath.endsWith(".skill")) {
    enhancedFilepath = filepath + "/SKILL.md";
  }
  const url = urlOfArtifact({ filepath: enhancedFilepath, threadId });
  const response = await fetch(url);
  const text = await response.text();
  return text;
}

export function loadArtifactContentFromToolCall({
  url: urlString,
  thread,
}: {
  url: string;
  thread: ThreadStream<AgentThreadState>;
}) {
  const url = new URL(urlString);
  const toolCallId = url.searchParams.get("tool_call_id");
  const messageId = url.searchParams.get("message_id");
  if (messageId && toolCallId) {
    const message = thread.messages.find((message) => message.id === messageId);
    if (message?.type === "ai") {
      // First try parsed tool_calls
      const toolCall = message.tool_calls?.find(
        (tc) => tc.id === toolCallId,
      );
      if (toolCall?.args?.content) {
        return toolCall.args.content;
      }

      // During streaming, args may still be a partial JSON string in
      // tool_call_chunks. Try to extract the "content" value from it.
      const msgAny = message as unknown as {
        tool_call_chunks?: { id?: string; args?: string | Record<string, unknown> }[];
      };
      const chunks = msgAny.tool_call_chunks as
        | { id?: string; args?: string | Record<string, unknown> }[]
        | undefined;
      if (chunks) {
        const chunk = chunks.find((c) => c.id === toolCallId);
        if (chunk && typeof chunk.args === "string") {
          return extractContentFromPartialJSON(chunk.args);
        }
      }
    }
  }
}

function extractContentFromPartialJSON(json: string): string | undefined {
  // Find the start of the "content" value.
  // The JSON looks like: {"path":"...","description":"...","content":"actual content here...
  const marker = '"content"';
  const idx = json.indexOf(marker);
  if (idx === -1) return undefined;

  // Skip past `"content"` and optional whitespace / colon / whitespace / opening quote
  let pos = idx + marker.length;
  while (pos < json.length) {
    const current = json[pos];
    if (current === undefined || (current !== " " && current !== ":")) {
      break;
    }
    pos++;
  }
  const openingQuote = json[pos];
  if (openingQuote !== '"') return undefined;
  pos++; // skip opening quote

  // Now read the string value, handling escape sequences
  const chars: string[] = [];
  while (pos < json.length) {
    const ch = json[pos];
    if (ch === undefined) {
      break;
    }
    if (ch === "\\") {
      pos++;
      if (pos >= json.length) break;
      const esc = json[pos];
      if (esc === undefined) {
        break;
      }
      if (esc === "n") chars.push("\n");
      else if (esc === "t") chars.push("\t");
      else if (esc === "r") chars.push("\r");
      else if (esc === '"') chars.push('"');
      else if (esc === "\\") chars.push("\\");
      else if (esc === "/") chars.push("/");
      else chars.push(esc);
    } else if (ch === '"') {
      break; // end of string value
    } else {
      chars.push(ch);
    }
    pos++;
  }

  return chars.length > 0 ? chars.join("") : undefined;
}
