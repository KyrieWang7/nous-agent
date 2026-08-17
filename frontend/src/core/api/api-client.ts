"use client";

import type { ThreadState } from "@/core/types";

import { getAgentAPIBaseURL } from "../config";

import { authFetch } from "./auth-fetch";

class AgentAPI {
  private baseUrl: string;

  constructor(baseUrl: string) {
    this.baseUrl = baseUrl;
  }

  readonly threads = {
    create: async (threadId: string): Promise<void> => {
      const res = await authFetch(`${this.baseUrl}/threads`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          thread_id: threadId,
          if_exists: "do_nothing",
        }),
      });
      if (!res.ok) throw new Error(`create thread failed: ${res.status}`);
    },

    getState: async <V = Record<string, unknown>>(
      threadId: string,
    ): Promise<ThreadState<V>> => {
      const res = await authFetch(`${this.baseUrl}/threads/${threadId}/state`);
      if (!res.ok) throw new Error(`getState failed: ${res.status}`);
      return res.json();
    },

    delete: async (threadId: string): Promise<void> => {
      const res = await authFetch(`${this.baseUrl}/threads/${threadId}`, {
        method: "DELETE",
      });
      if (!res.ok) throw new Error(`delete thread failed: ${res.status}`);
    },

    updateState: async (
      threadId: string,
      body: { values: Record<string, unknown> },
    ): Promise<void> => {
      const res = await authFetch(`${this.baseUrl}/threads/${threadId}/state`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) throw new Error(`updateState failed: ${res.status}`);
    },
  };
}

let _singleton: AgentAPI | null = null;

export function getAPIClient(): AgentAPI {
  _singleton ??= new AgentAPI(getAgentAPIBaseURL());
  return _singleton;
}
