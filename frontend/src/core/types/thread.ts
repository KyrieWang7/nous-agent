/**
 * Thread and stream-related types.
 *
 * Native Agent API interfaces shared by transport and UI code.
 */

import type { Message } from "./message";

export type RunRiskVerdict = "pass" | "review" | "block" | "unknown";

export type ThreadStatus = "idle" | "busy" | "interrupted" | "error";

export interface TokenUsage {
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  totalTokens: number;
}

export interface ThreadState<V = Record<string, unknown>> {
  values: V;
  metadata?: Record<string, unknown>;
  created_at?: string;
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

/**
 * Public surface of the thread stream hook, consumed by UI components.
 */
export interface ThreadStream<
  V extends Record<string, unknown> = Record<string, unknown>,
> {
  values: V;
  messages: Message[];
  isLoading: boolean;
  isThreadLoading: boolean;
  threadNotFound?: boolean;
  error: unknown;
  tokenUsage: TokenUsage | null;
  /** Raw structured safety level from the terminal run event, when present. */
  riskLevel?: string | null;
  /** Normalized safety decision used by the stream consumer. */
  riskVerdict?: RunRiskVerdict | null;

  submit: (
    values: Record<string, unknown> | null | undefined,
    options?: {
      optimisticValues?: Partial<V> | ((prev: V) => Partial<V>);
      config?: Record<string, unknown>;
      context?: Record<string, unknown>;
    },
  ) => Promise<void>;

  stop: () => void;
}
