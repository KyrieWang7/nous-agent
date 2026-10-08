import { getAgentAPIBaseURL } from "../config";

export interface InspectableRun {
  run_id: string;
  status: string;
  created_at: string;
}

export interface RuntimeEvent {
  seq: number;
  run_id: string;
  thread_id?: string;
  type: string;
  category: string;
  data?: unknown;
  created_at: string;
}

export interface EventPage {
  events: RuntimeEvent[];
  next_after: number;
  has_more: boolean;
}

async function readJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const response = await fetch(`${getAgentAPIBaseURL()}${path}`, {
    credentials: "include",
    signal,
    cache: "no-store",
  });
  if (!response.ok)
    throw new Error(`Run events request failed (${response.status})`);
  return response.json() as Promise<T>;
}

export function fetchInspectableRuns(threadId: string, signal?: AbortSignal) {
  return readJSON<InspectableRun[]>(
    `/threads/${encodeURIComponent(threadId)}/runs`,
    signal,
  );
}

export function fetchRunEvents(
  threadId: string,
  runId: string,
  after: number,
  signal?: AbortSignal,
) {
  return readJSON<EventPage>(
    `/threads/${encodeURIComponent(threadId)}/runs/${encodeURIComponent(runId)}/events/raw?after=${after}&limit=100`,
    signal,
  );
}
