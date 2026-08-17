/**
 * SSE transport for the versioned Agent API using native
 * `fetch` + `ReadableStream`.  No SDK dependency.
 *
 * Supports:
 * - streamSSE: Create a new run and stream events (POST)
 * - reconnectSSE: Reconnect to an existing run's stream (GET)
 * - fetchActiveRun: Check if a thread has a running task
 */

import { getAgentAPIBaseURL } from "../config";

import type { PendingUserQuestion } from "./types";

export interface SSEEvent {
  event: string;
  data: unknown;
  /** Event ID from the server (used for Last-Event-ID reconnection) */
  id?: string;
}

export interface StreamPayload {
  input: Record<string, unknown> | null | undefined;
  config?: Record<string, unknown>;
  context?: Record<string, unknown>;
  signal?: AbortSignal;
}

/**
 * Parse SSE lines from a ReadableStream, yielding structured events.
 */
async function* parseSSEStream(
  reader: ReadableStreamDefaultReader<Uint8Array>,
  signal?: AbortSignal,
): AsyncGenerator<SSEEvent> {
  const decoder = new TextDecoder();
  let buffer = "";
  let currentEvent: string | null = null;
  let currentId: string | null = null;

  try {
    while (true) {
      if (signal?.aborted) break;
      const { done, value } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split("\n");
      buffer = lines.pop() ?? "";

      let streamEnded = false;

      for (const line of lines) {
        if (line.startsWith("event:")) {
          currentEvent = line.slice(6).trim();
        } else if (line.startsWith("id:")) {
          currentId = line.slice(3).trim();
        } else if (line.startsWith("data:") && currentEvent) {
          const raw = line.slice(5).trim();
          if (!raw) continue;
          try {
            const data = JSON.parse(raw);
            yield { event: currentEvent, data, id: currentId ?? undefined };
            if (currentEvent === "end") {
              streamEnded = true;
            }
          } catch {
            console.warn("Failed to parse SSE data:", raw);
          }
          currentEvent = null;
          currentId = null;
        } else if (line.trim() === "") {
          currentEvent = null;
          currentId = null;
        }
      }

      if (streamEnded) break;
    }
  } finally {
    reader.releaseLock();
  }
}

/**
 * Create a new run and stream SSE events (POST /threads/{id}/runs).
 */
export async function* streamSSE(
  threadId: string,
  assistantId: string,
  payload: StreamPayload,
): AsyncGenerator<SSEEvent> {
  const baseUrl = getAgentAPIBaseURL();
  const url = `${baseUrl}/threads/${threadId}/runs`;

  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    Accept: "text/event-stream",
  };

  const response = await fetch(url, {
    method: "POST",
    headers,
    credentials: "include",
    body: JSON.stringify({
      assistant_id: assistantId,
      input: payload.input,
      config: payload.config,
      context: payload.context,
      on_disconnect: "continue",
    }),
    signal: payload.signal,
  });

  if (!response.ok) {
    const text = await response.text().catch(() => response.statusText);
    throw new Error(`Stream request failed (${response.status}): ${text}`);
  }

  const reader = response.body!.getReader();
  yield* parseSSEStream(reader, payload.signal);
}

/**
 * Reconnect to an existing run's SSE stream (GET /threads/{id}/runs/{runId}/events).
 *
 * Used when:
 * - User switches away from a thread and comes back while the run is still active
 * - Network interruption causes SSE disconnect
 * - Page refresh while a run is in progress
 */
export async function* reconnectSSE(
  threadId: string,
  runId: string,
  signal?: AbortSignal,
  lastEventId?: string,
): AsyncGenerator<SSEEvent> {
  const baseUrl = getAgentAPIBaseURL();
  const url = `${baseUrl}/threads/${threadId}/runs/${runId}/events`;

  const headers: Record<string, string> = {
    Accept: "text/event-stream",
  };
  if (lastEventId) {
    headers["Last-Event-ID"] = lastEventId;
  }

  const response = await fetch(url, {
    method: "GET",
    headers,
    credentials: "include",
    signal,
  });

  if (!response.ok) {
    const text = await response.text().catch(() => response.statusText);
    throw new Error(`Reconnect request failed (${response.status}): ${text}`);
  }

  const reader = response.body!.getReader();
  yield* parseSSEStream(reader, signal);
}

/**
 * Fetch the list of runs for a thread and return the active one (if any).
 *
 * Returns the run_id of the currently running task, or null if none.
 */
export async function fetchActiveRunId(
  threadId: string,
): Promise<string | null> {
  const baseUrl = getAgentAPIBaseURL();
  const url = `${baseUrl}/threads/${threadId}/runs`;

  try {
    const response = await fetch(url, { credentials: "include" });
    if (!response.ok) return null;

    const runs = (await response.json()) as Array<{
      run_id: string;
      status: string;
    }>;
    const active = runs.find(
      (r) => r.status === "running" || r.status === "pending",
    );
    return active?.run_id ?? null;
  } catch {
    return null;
  }
}

export async function answerUserQuestion(
  question: PendingUserQuestion,
  answer: { selected?: string[]; custom?: string; dismiss?: boolean },
): Promise<void> {
  const baseUrl = getAgentAPIBaseURL();
  const response = await fetch(
    `${baseUrl}/threads/${question.thread_id}/runs/${question.run_id}/questions/${question.id}/answer`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "include",
      body: JSON.stringify(answer),
    },
  );
  if (!response.ok) {
    const text = await response.text().catch(() => response.statusText);
    throw new Error(`Question response failed (${response.status}): ${text}`);
  }
}
