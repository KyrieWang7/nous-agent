import { getAgentAPIBaseURL } from "@/core/config";

export type InputDelivery = "next_turn" | "next_step";
export interface SessionInput {
  id: string;
  thread_id: string;
  delivery: InputDelivery;
  input: { prompt: string; content_blocks?: unknown[] };
  status: "pending" | "claimed" | "admitted" | "cancelled" | "rejected";
  position: number;
  updated_at: string;
  target_run_id?: string;
  run_id?: string;
  run_status?: string;
  run_created_at?: string;
  error?: string;
}
export interface PendingInput {
  id: string;
  delivery: InputDelivery;
  input: Record<string, unknown>;
  config?: Record<string, unknown>;
  context?: Record<string, unknown>;
  draft: string;
  attachment_ids?: string[];
}
export class InboxError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
const path = (tid: string) =>
  `${getAgentAPIBaseURL()}/threads/${encodeURIComponent(tid)}/messages`;
export const inboxKey = (tid: string) => ["session-inbox", tid] as const;
async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const r = await fetch(url, {
    ...options,
    credentials: "include",
    cache: "no-store",
    headers: { "Content-Type": "application/json" },
  });
  if (!r.ok) {
    const raw = await r.text();
    let detail = raw;
    try {
      detail = (JSON.parse(raw) as { detail?: string }).detail ?? raw;
    } catch {
      /* plain error */
    }
    throw new InboxError(r.status, detail || r.statusText);
  }
  return r.json() as Promise<T>;
}
export async function listSessionInputs(tid: string, signal?: AbortSignal) {
  const out: SessionInput[] = [];
  let after = 0;
  for (;;) {
    const page = await request<SessionInput[]>(
      `${path(tid)}?after=${after}&limit=200`,
      { signal },
    );
    out.push(...page);
    if (page.length < 200) return out;
    const next = page.at(-1)!.position;
    if (next <= after) throw new Error("Invalid inbox pagination");
    after = next;
  }
}
export const cancelSessionInput = (tid: string, id: string) =>
  request<SessionInput>(`${path(tid)}/${encodeURIComponent(id)}/cancel`, {
    method: "POST",
  });
export const changeSessionInput = (
  tid: string,
  id: string,
  updatedAt: string,
  change: { prompt: string } | { target_run_id: string },
) =>
  request<SessionInput>(`${path(tid)}/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: JSON.stringify({ ...change, updated_at: updatedAt }),
  });
const activeSends = new Set<string>();
export const isInputSending = (tid: string) => activeSends.has(tid);
const pendingKey = (tid: string) => `nous:session-inbox:unconfirmed:${tid}`;
export function pendingInputSnapshot(tid: string | null) {
  if (!tid || typeof window === "undefined") return null;
  return sessionStorage.getItem(pendingKey(tid));
}
export function subscribePendingInput(listener: () => void) {
  window.addEventListener("session-inbox-pending", listener);
  return () => window.removeEventListener("session-inbox-pending", listener);
}
type PendingSummary = Pick<
  PendingInput,
  "id" | "delivery" | "draft" | "attachment_ids"
>;
function payloadTransaction<T>(
  tid: string,
  write: boolean,
  apply: (store: IDBObjectStore) => IDBRequest<T>,
): Promise<T> {
  return new Promise((resolve, reject) => {
    const opening = indexedDB.open("nous-session-inbox", 1);
    opening.onupgradeneeded = () => {
      opening.result.createObjectStore("pending");
    };
    opening.onerror = () =>
      reject(
        opening.error ?? new Error("Could not open pending message storage"),
      );
    opening.onsuccess = () => {
      const db = opening.result;
      const tx = db.transaction("pending", write ? "readwrite" : "readonly");
      const request = apply(tx.objectStore("pending"));
      tx.oncomplete = () => {
        db.close();
        resolve(request.result);
      };
      tx.onabort = () => {
        db.close();
        reject(
          tx.error ??
            request.error ??
            new Error(`Could not save message for ${tid}`),
        );
      };
      tx.onerror = () => {
        /* onabort reports the transaction failure. */
      };
    };
  });
}
async function storePending(tid: string, value: PendingInput) {
  // IndexedDB holds exact upload/image bodies without sessionStorage's small
  // string quota. Only the recovery marker and draft belong in tab storage.
  await payloadTransaction(tid, true, (store) =>
    store.put(value, `${tid}:${value.id}`),
  );
  const summary: PendingSummary = {
    id: value.id,
    delivery: value.delivery,
    draft: value.draft,
    attachment_ids: value.attachment_ids,
  };
  sessionStorage.setItem(pendingKey(tid), JSON.stringify(summary));
  window.dispatchEvent(new Event("session-inbox-pending"));
}
function clearPending(tid: string, id: string) {
  const raw = pendingInputSnapshot(tid);
  if (raw && (JSON.parse(raw) as PendingSummary).id === id)
    sessionStorage.removeItem(pendingKey(tid));
  window.dispatchEvent(new Event("session-inbox-pending"));
  void payloadTransaction(tid, true, (store) =>
    store.delete(`${tid}:${id}`),
  ).catch(() => {
    /* Delivered payload can be garbage-collected later. */
  });
}
async function postInput(tid: string, pending: PendingInput) {
  const { id, delivery, input, config, context } = pending;
  const body = { id, delivery, input, config, context };
  activeSends.add(tid);
  window.dispatchEvent(new Event("session-inbox-pending"));
  try {
    const e = await request<SessionInput>(path(tid), {
      method: "POST",
      body: JSON.stringify(body),
    });
    clearPending(tid, pending.id);
    return e;
  } catch (error) {
    // A validation rejection is definitive; transport and server failures keep
    // the exact request available for read-before-retry recovery.
    if (error instanceof InboxError && error.status === 400)
      clearPending(tid, pending.id);
    throw error;
  } finally {
    activeSends.delete(tid);
    window.dispatchEvent(new Event("session-inbox-pending"));
  }
}
export async function enqueueSessionInput(tid: string, pending: PendingInput) {
  // Save the exact wire body before POST. A lost acceptance response can be
  // recovered after refresh, including uploads, without inventing another ID.
  if (activeSends.has(tid) || pendingInputSnapshot(tid))
    throw new Error(
      "Resolve the unconfirmed message before sending another one.",
    );
  activeSends.add(tid);
  window.dispatchEvent(new Event("session-inbox-pending"));
  try {
    await storePending(tid, pending);
    return await postInput(tid, pending);
  } finally {
    activeSends.delete(tid);
    window.dispatchEvent(new Event("session-inbox-pending"));
  }
}
export async function recoverSessionInput(tid: string) {
  const raw = pendingInputSnapshot(tid);
  if (!raw) return null;
  const pending = JSON.parse(raw) as PendingSummary;
  try {
    const e = await request<SessionInput>(
      `${path(tid)}/${encodeURIComponent(pending.id)}`,
    );
    clearPending(tid, pending.id);
    return {
      entry: e,
      draft: pending.draft,
      attachmentIds: pending.attachment_ids,
    };
  } catch (e) {
    if (!(e instanceof InboxError) || e.status !== 404) throw e;
  }
  const payload = await payloadTransaction<PendingInput | undefined>(
    tid,
    false,
    (store) =>
      store.get(`${tid}:${pending.id}`) as IDBRequest<PendingInput | undefined>,
  );
  if (payload?.id !== pending.id)
    throw new Error(
      "The server has not received this message and its saved request is unavailable. Your draft is still preserved.",
    );
  return {
    entry: await postInput(tid, payload),
    draft: pending.draft,
    attachmentIds: pending.attachment_ids,
  };
}
