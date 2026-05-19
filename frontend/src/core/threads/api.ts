import { getBackendBaseURL } from "../config";
import { authFetch } from "../api/auth-fetch";

export interface GatewayThread {
  id: string;
  title: string | null;
  model_name: string | null;
  created_at: string;
  updated_at: string;
  is_archived: boolean;
  message_count: number;
}

interface GatewayThreadListResponse {
  threads: GatewayThread[];
  total: number;
}

export async function fetchThreads(params?: {
  limit?: number;
  offset?: number;
  includeArchived?: boolean;
}): Promise<GatewayThread[]> {
  const query = new URLSearchParams();
  if (params?.limit) query.set("limit", String(params.limit));
  if (params?.offset) query.set("offset", String(params.offset));
  if (params?.includeArchived)
    query.set("include_archived", String(params.includeArchived));

  const url = `${getBackendBaseURL()}/api/threads?${query.toString()}`;
  const response = await authFetch(url);
  if (!response.ok) {
    throw new Error(`Failed to fetch threads: ${response.status}`);
  }
  const data: GatewayThreadListResponse = await response.json();
  return data.threads;
}

export async function createThread(params: {
  id: string;
  title?: string;
  model_name?: string;
}): Promise<GatewayThread> {
  const response = await authFetch(`${getBackendBaseURL()}/api/threads`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      id: params.id,
      title: params.title,
      model_name: params.model_name,
    }),
  });
  if (!response.ok) {
    throw new Error(`Failed to create thread: ${response.status}`);
  }
  return response.json();
}

export async function updateThread(
  threadId: string,
  params: { title?: string; is_archived?: boolean },
): Promise<GatewayThread> {
  const response = await authFetch(
    `${getBackendBaseURL()}/api/threads/${threadId}`,
    {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(params),
    },
  );
  if (!response.ok) {
    throw new Error(`Failed to update thread: ${response.status}`);
  }
  return response.json();
}

export async function deleteThread(threadId: string): Promise<void> {
  const response = await authFetch(
    `${getBackendBaseURL()}/api/threads/${threadId}`,
    {
      method: "DELETE",
    },
  );
  if (!response.ok) {
    throw new Error(`Failed to delete thread: ${response.status}`);
  }
}
