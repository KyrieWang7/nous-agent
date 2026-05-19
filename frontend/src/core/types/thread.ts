/**
 * Thread and stream-related types.
 *
 * Replaces SDK types (`Thread`, `UseStream`, `UseStreamCustom`) with plain
 * TypeScript interfaces so the frontend has zero LangChain SDK dependency.
 */

import type { Message, ToolCall } from "./message";

export type ThreadStatus = "idle" | "busy" | "interrupted" | "error";

export interface TokenUsage {
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  totalTokens: number;
}

export interface ThreadState<V = Record<string, unknown>> {
  values: V;
  next?: string[];
  checkpoint?: Record<string, unknown>;
  metadata?: Record<string, unknown>;
  created_at?: string;
  parent_config?: Record<string, unknown>;
  tasks?: unknown[];
  token_usage?: {
    type: string;
    input_tokens: number;
    output_tokens: number;
    cache_read_tokens: number;
    total_tokens: number;
  } | null;
}

export interface Thread<V = Record<string, unknown>> {
  thread_id: string;
  created_at: string;
  updated_at: string;
  metadata: Record<string, unknown>;
  status: ThreadStatus;
  values: V;
  interrupts: Record<string, unknown[]>;
  config?: Record<string, unknown>;
  error?: string | Record<string, unknown> | null;
}

export interface ToolCallWithResult {
  id: string;
  call: ToolCall;
  result: Message | undefined;
  aiMessage: Message;
  index: number;
  state: "pending" | "completed" | "error";
}

/**
 * Public surface of the thread stream hook, consumed by UI components.
 *
 * Mirrors the subset of `UseStream` / `UseStreamCustom` that the app
 * actually uses — nothing more — so we're free from SDK version churn.
 */
export interface ThreadStream<V extends Record<string, unknown> = Record<string, unknown>> {
  values: V;
  messages: Message[];
  isLoading: boolean;
  isThreadLoading: boolean;
  error: unknown;
  tokenUsage: TokenUsage | null;

  submit: (
    values: Record<string, unknown> | null | undefined,
    options?: {
      optimisticValues?:
        | Partial<V>
        | ((prev: V) => Partial<V>);
      config?: Record<string, unknown>;
      context?: Record<string, unknown>;
      command?: Record<string, unknown>;
    },
  ) => Promise<void>;

  stop: () => void;

  interrupt?: unknown;
  toolCalls: ToolCallWithResult[];
  getToolCalls: (message: Message) => ToolCallWithResult[];

  /** Stubs kept for backward compat with components that reference them. */
  branch: string;
  setBranch: (branch: string) => void;
  history: ThreadState<V>[];
  experimental_branchTree: { type: "sequence"; items: unknown[] };
  getMessagesMetadata: (message: Message, index?: number) => undefined;
  assistantId: string;
  joinStream: (runId: string) => Promise<void>;
}
