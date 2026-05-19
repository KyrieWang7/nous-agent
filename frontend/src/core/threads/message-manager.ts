/**
 * MessageManager — pure-TypeScript replica of the LangGraph SDK's
 * `MessageTupleManager` (libs/sdk/src/ui/messages.ts).
 *
 * Accumulates SSE `messages` (messages-tuple) chunks by id, correctly
 * concatenating content, deep-merging additional_kwargs (with string
 * append for reasoning_content), and merging tool_calls by index.
 *
 * Zero SDK / @langchain/core dependency.
 */

import type { Message } from "@/core/types";

interface ChunkEntry {
  message: Message;
  metadata?: Record<string, unknown>;
  index?: number;
}

function normalizeType(raw: string): Message["type"] {
  const t = raw.replace("MessageChunk", "").toLowerCase();
  if (t === "human" || t === "user") return "human";
  if (t === "ai" || t === "assistant") return "ai";
  if (t === "tool") return "tool";
  if (t === "system") return "system";
  if (t === "function") return "function";
  if (t === "remove") return "remove";
  return t as Message["type"];
}

function concatContent(
  prev: Message["content"],
  chunk: Message["content"],
): Message["content"] {
  if (typeof prev === "string" && typeof chunk === "string") {
    return prev + chunk;
  }
  if (typeof prev === "string" && typeof chunk !== "string") {
    return prev;
  }
  return prev;
}

function mergeAdditionalKwargs(
  prev: Record<string, unknown> | undefined,
  chunk: Record<string, unknown> | undefined,
): Record<string, unknown> | undefined {
  if (!chunk || Object.keys(chunk).length === 0) return prev;
  if (!prev || Object.keys(prev).length === 0) return { ...chunk };

  const merged = { ...prev };
  for (const [key, val] of Object.entries(chunk)) {
    if (val === undefined || val === null) continue;
    if (key === "reasoning_content" && typeof val === "string") {
      merged[key] = ((merged[key] as string) ?? "") + val;
    } else {
      merged[key] = val;
    }
  }
  return merged;
}

interface ToolCallLike {
  name?: string;
  args?: Record<string, unknown> | string;
  id?: string;
  index?: number;
  type?: string;
}

/**
 * Merge tool_call_chunks or tool_calls by index.
 * Same index => accumulate args (string concat for partial JSON).
 * Different index => new entry.
 */
function mergeToolCalls(
  prev: ToolCallLike[] | undefined,
  chunk: ToolCallLike[] | undefined,
): ToolCallLike[] | undefined {
  if (!chunk || chunk.length === 0) return prev;
  if (!prev || prev.length === 0) return [...chunk];

  const result = [...prev];
  for (const tc of chunk) {
    const idx = tc.index;
    if (idx !== undefined && idx !== null) {
      const existing = result.find((r) => r.index === idx);
      if (existing) {
        if (typeof tc.args === "string" && typeof existing.args === "string") {
          existing.args = existing.args + tc.args;
        } else if (tc.args && typeof tc.args === "object") {
          existing.args = { ...(existing.args as Record<string, unknown> ?? {}), ...tc.args };
        }
        if (tc.name) existing.name = tc.name;
        if (tc.id) existing.id = tc.id;
      } else {
        result.push({ ...tc });
      }
    } else {
      const existingById = tc.id ? result.find((r) => r.id === tc.id) : null;
      if (existingById) {
        if (tc.args && typeof tc.args === "object") {
          existingById.args = { ...(existingById.args as Record<string, unknown> ?? {}), ...tc.args };
        }
        if (tc.name) existingById.name = tc.name;
      } else {
        result.push({ ...tc });
      }
    }
  }
  return result;
}

/**
 * Try to parse a (possibly incomplete) JSON string into a partial object.
 * First tries full JSON.parse. On failure, attempts to extract
 * already-completed key-value pairs via regex for string values.
 */
function parsePartialJSON(s: string): Record<string, unknown> {
  try {
    return JSON.parse(s);
  } catch {
    // Fall through to partial extraction
  }
  const result: Record<string, unknown> = {};
  const re = /"(\w+)"\s*:\s*"((?:[^"\\]|\\.)*)"/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(s)) !== null) {
    result[m[1]!] = m[2]!.replace(/\\"/g, '"').replace(/\\\\/g, "\\");
  }
  return result;
}

/**
 * Derive tool_calls from tool_call_chunks by attempting to parse
 * accumulated string args into objects. Uses partial JSON parsing
 * so that fields like `path` are available before the full JSON
 * (which may include large `content`) is complete.
 */
function deriveToolCalls(
  baseTCs: ToolCallLike[] | undefined,
  chunks: ToolCallLike[] | undefined,
): ToolCallLike[] | undefined {
  if (!chunks || chunks.length === 0) return baseTCs;

  const derived: ToolCallLike[] = baseTCs ? [...baseTCs] : [];

  for (const chunk of chunks) {
    const existingByIndex =
      chunk.index !== undefined && chunk.index !== null
        ? derived.find((tc) => tc.index === chunk.index)
        : null;
    const existingById =
      !existingByIndex && chunk.id
        ? derived.find((tc) => tc.id === chunk.id)
        : null;
    const existing = existingByIndex ?? existingById;

    let parsedArgs: Record<string, unknown> | undefined;
    if (typeof chunk.args === "string" && chunk.args.length > 0) {
      parsedArgs = parsePartialJSON(chunk.args);
    } else if (chunk.args && typeof chunk.args === "object") {
      parsedArgs = chunk.args as Record<string, unknown>;
    }

    if (existing) {
      if (parsedArgs && Object.keys(parsedArgs).length > 0) {
        existing.args = { ...(typeof existing.args === "object" ? existing.args : {}), ...parsedArgs };
      }
      if (chunk.name) existing.name = chunk.name;
      if (chunk.id) existing.id = chunk.id;
    } else {
      derived.push({
        ...chunk,
        args: parsedArgs && Object.keys(parsedArgs).length > 0
          ? parsedArgs
          : (typeof chunk.args === "string" ? {} : (chunk.args ?? {})),
      });
    }
  }

  return derived.length > 0 ? derived : baseTCs;
}

function mergeResponseMetadata(
  prev: Record<string, unknown> | undefined,
  chunk: Record<string, unknown> | undefined,
): Record<string, unknown> | undefined {
  if (!chunk || Object.keys(chunk).length === 0) return prev;
  if (!prev) return { ...chunk };
  return { ...prev, ...chunk };
}

export class MessageManager {
  private chunks: Map<string, ChunkEntry> = new Map();

  /**
   * Add a serialized message chunk. Returns the message id, or null if
   * the chunk has no id and cannot be tracked.
   */
  add(
    serialized: Record<string, unknown>,
    metadata?: Record<string, unknown>,
  ): string | null {
    const id = serialized.id as string | undefined;
    if (!id) return null;

    const type = normalizeType((serialized.type as string) ?? "ai");

    const incoming = {
      ...serialized,
      type,
    } as unknown as Message;

    const existing = this.chunks.get(id);
    if (!existing) {
      this.chunks.set(id, { message: incoming, metadata });
      return id;
    }

    const prev = existing.message;

    const merged: Record<string, unknown> = {
      ...prev,
      id,
      type: prev.type,
      content: concatContent(prev.content, incoming.content),
      additional_kwargs: mergeAdditionalKwargs(
        (prev as unknown as Record<string, unknown>).additional_kwargs as Record<string, unknown> | undefined,
        (incoming as unknown as Record<string, unknown>).additional_kwargs as Record<string, unknown> | undefined,
      ),
      response_metadata: mergeResponseMetadata(
        prev.response_metadata,
        incoming.response_metadata,
      ),
    };

    const prevAny = prev as unknown as Record<string, unknown>;
    const incomingAny = incoming as unknown as Record<string, unknown>;

    merged.tool_call_chunks = mergeToolCalls(
      prevAny.tool_call_chunks as ToolCallLike[] | undefined,
      incomingAny.tool_call_chunks as ToolCallLike[] | undefined,
    );

    const baseTCs = mergeToolCalls(
      prevAny.tool_calls as ToolCallLike[] | undefined,
      incomingAny.tool_calls as ToolCallLike[] | undefined,
    );
    merged.tool_calls = deriveToolCalls(
      baseTCs,
      merged.tool_call_chunks as ToolCallLike[] | undefined,
    );

    if (incomingAny.invalid_tool_calls) {
      merged.invalid_tool_calls = incomingAny.invalid_tool_calls;
    }
    if (incomingAny.usage_metadata) {
      merged.usage_metadata = incomingAny.usage_metadata;
    }

    existing.message = merged as unknown as Message;
    if (metadata) existing.metadata = metadata;

    return id;
  }

  /**
   * Get the accumulated message for the given id.
   * If `defaultIndex` is provided and no index was assigned yet, assigns it.
   */
  get(
    id: string,
    defaultIndex?: number,
  ): { message: Message; index: number; metadata?: Record<string, unknown> } | null {
    const entry = this.chunks.get(id);
    if (!entry) return null;
    if (defaultIndex !== undefined && entry.index === undefined) {
      entry.index = defaultIndex;
    }
    return {
      message: entry.message,
      index: entry.index ?? 0,
      metadata: entry.metadata,
    };
  }

  clear(): void {
    this.chunks.clear();
  }
}
