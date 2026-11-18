import { getAgentAPIBaseURL } from "@/core/config";

import type { ChildInput, ChildSession } from "./types";

export class ChildAPIError extends Error {
  constructor(
    public readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "ChildAPIError";
  }
}

async function request<T>(
  threadId: string,
  path: string,
  options?: RequestInit,
): Promise<T> {
  const response = await fetch(
    `${getAgentAPIBaseURL()}/threads/${encodeURIComponent(threadId)}/subagents${path}`,
    {
      ...options,
      credentials: "include",
      cache: "no-store",
      headers: { "Content-Type": "application/json" },
    },
  );
  if (!response.ok) {
    const text = await response.text();
    let detail = text;
    try {
      detail = (JSON.parse(text) as { detail?: string }).detail ?? text;
    } catch {
      /* plain error response */
    }
    throw new ChildAPIError(response.status, detail || response.statusText);
  }
  return response.json() as Promise<T>;
}

export const childSessionKey = (threadId: string) =>
  ["child-sessions", threadId] as const;
const sessionPath = (id: string) => `/${encodeURIComponent(id)}`;

export const listChildren = (threadId: string, signal?: AbortSignal) =>
  request<ChildSession[]>(threadId, "", { signal });
export const getChild = (threadId: string, id: string, signal?: AbortSignal) =>
  request<ChildSession>(threadId, sessionPath(id), { signal });
export const childInputs = (
  threadId: string,
  id: string,
  signal?: AbortSignal,
) => request<ChildInput[]>(threadId, `${sessionPath(id)}/messages`, { signal });
export const cancelChild = (
  threadId: string,
  id: string,
  signal?: AbortSignal,
) =>
  request<ChildSession>(threadId, `${sessionPath(id)}/cancel`, {
    method: "POST",
    signal,
  });
export const waitForChild = (
  threadId: string,
  id: string,
  signal?: AbortSignal,
) =>
  request<ChildSession>(
    threadId,
    `${sessionPath(id)}/wait?timeout_seconds=20`,
    { method: "POST", signal },
  );
export const sendChildInput = (
  threadId: string,
  id: string,
  input: { id: string; content: string },
  signal?: AbortSignal,
) =>
  request<ChildInput>(threadId, `${sessionPath(id)}/messages`, {
    method: "POST",
    body: JSON.stringify(input),
    signal,
  });
export const continueChild = (
  threadId: string,
  id: string,
  prompt: string,
  signal?: AbortSignal,
) =>
  request<{ run_id: string; status: string }>(
    threadId,
    `${sessionPath(id)}/continue`,
    { method: "POST", body: JSON.stringify({ prompt }), signal },
  );
