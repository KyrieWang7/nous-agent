import { parse } from "best-effort-json-parser";

import type { ToolCallPreparation } from "../types/message.ts";

// Only presentation labels are parsed while arguments stream. Full arguments
// arrive in the authoritative model message, outside this bounded preview.
export const ARGUMENT_PREVIEW_LIMIT = 4096;

export class ToolPreparation {
  private readonly index: number;
  private preview = "";
  private length = 0;
  private id?: string;
  private name = "";
  private args: Record<string, unknown> = {};

  constructor(index: number) {
    this.index = index;
  }

  append(chunk: { id?: string; name?: string; args?: string }): void {
    if (chunk.id) this.id = chunk.id;
    if (chunk.name) this.name = chunk.name;
    const delta = chunk.args ?? "";
    this.length += delta.length;
    if (this.preview.length >= ARGUMENT_PREVIEW_LIMIT || !delta) return;
    this.preview += delta.slice(
      0,
      ARGUMENT_PREVIEW_LIMIT - this.preview.length,
    );
    try {
      const value: unknown = parse(this.preview);
      if (value && typeof value === "object" && !Array.isArray(value)) {
        this.args = value as Record<string, unknown>;
      }
    } catch {
      // An incomplete escape or value has no usable preview yet.
    }
  }

  snapshot(): ToolCallPreparation {
    return {
      index: this.index,
      id: this.id,
      name: this.name,
      args: this.args,
      argumentLength: this.length,
    };
  }
}
