/**
 * MessageManager accumulates incremental Agent API message chunks.
 *
 * Accumulates SSE `messages` (messages-tuple) chunks by id, correctly
 * concatenating content, deep-merging additional_kwargs (with string
 * append for reasoning_content), and tracking unfinished calls by index.
 *
 * Zero SDK / @langchain/core dependency.
 */

import type { Message } from "@/core/types";

import { ToolPreparation } from "./tool-preparation.ts";

interface ChunkEntry {
  message: Message;
  metadata?: Record<string, unknown>;
  index?: number;
  preparations?: Map<number, ToolPreparation>;
  replaced?: boolean;
}

function normalizeType(raw: string): Message["type"] {
  const t = raw.replace(/Message(?:Chunk)?$/, "").toLowerCase();
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

function mergeResponseMetadata(
  prev: Record<string, unknown> | undefined,
  chunk: Record<string, unknown> | undefined,
): Record<string, unknown> | undefined {
  if (!chunk || Object.keys(chunk).length === 0) return prev;
  if (!prev) return { ...chunk };
  return { ...prev, ...chunk };
}

export class MessageManager {
  private chunks = new Map<string, ChunkEntry>();

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

    let existing = this.chunks.get(id);
    if (!(serialized.type as string)?.endsWith("Chunk")) {
      this.chunks.set(id, {
        message: incoming,
        metadata,
        index: existing?.index,
      });
      return id;
    }
    if (!existing) {
      existing = { message: { id, type, content: "" } as Message, metadata };
      this.chunks.set(id, existing);
    }

    const prev = existing.message;

    const merged: Record<string, unknown> = {
      ...prev,
      id,
      type: prev.type,
      content: concatContent(prev.content, incoming.content),
      additional_kwargs: mergeAdditionalKwargs(
        (prev as unknown as Record<string, unknown>).additional_kwargs as
          | Record<string, unknown>
          | undefined,
        (incoming as unknown as Record<string, unknown>).additional_kwargs as
          | Record<string, unknown>
          | undefined,
      ),
      response_metadata: mergeResponseMetadata(
        prev.response_metadata,
        incoming.response_metadata,
      ),
    };

    const incomingAny = incoming as unknown as Record<string, unknown>;
    const calls = serialized.tool_call_chunks;
    if (Array.isArray(calls) && !existing.replaced) {
      existing.preparations ??= new Map();
      for (const call of calls) {
        if (!call || typeof call !== "object") continue;
        const tc = call as Record<string, unknown>;
        if (!Number.isInteger(tc.index) || (tc.index as number) < 0) continue;
        const index = tc.index as number;
        let preparation = existing.preparations.get(index);
        if (!preparation) {
          preparation = new ToolPreparation(index);
          existing.preparations.set(index, preparation);
        }
        preparation.append({
          id: typeof tc.id === "string" ? tc.id : undefined,
          name: typeof tc.name === "string" ? tc.name : undefined,
          args: typeof tc.args === "string" ? tc.args : undefined,
        });
      }
      merged.tool_call_preparations = [...existing.preparations.values()]
        .map((call) => call.snapshot())
        .sort((a, b) => a.index - b.index);
    }

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
  ): {
    message: Message;
    index: number;
    metadata?: Record<string, unknown>;
  } | null {
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

  /**
   * Replace the accumulated content for an already-streamed message.
   * Guardrail compensation events arrive after deltas, so simply changing the
   * React snapshot would let a later chunk resurrect the rejected text.
   */
  replaceContent(id: string, content: Message["content"]): boolean {
    const entry = this.chunks.get(id);
    if (!entry) return false;

    const next: Record<string, unknown> = {
      ...(entry.message as unknown as Record<string, unknown>),
      content,
    };
    const additionalKwargs = next.additional_kwargs;
    if (additionalKwargs && typeof additionalKwargs === "object") {
      const cleanedKwargs = {
        ...(additionalKwargs as Record<string, unknown>),
      };
      delete cleanedKwargs.reasoning_content;
      next.additional_kwargs = cleanedKwargs;
    }
    delete next.tool_calls;
    delete next.tool_call_chunks;
    delete next.tool_call_preparations;
    delete next.invalid_tool_calls;
    entry.message = next as unknown as Message;
    entry.preparations = undefined;
    entry.replaced = true;
    return true;
  }

  clear(): void {
    this.chunks.clear();
  }
}
