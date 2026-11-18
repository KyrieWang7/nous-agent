export type ChildStatus =
  | "pending"
  | "running"
  | "completed"
  | "failed"
  | "cancelled"
  | "timed_out"
  | "interrupted";

export interface ChildMessage {
  role: "user" | "assistant" | "tool" | "system";
  content?: string;
  content_blocks?: { type: string; text?: string }[];
  tool_calls?: { id: string; name: string; arguments: unknown }[];
  tool_call_id?: string;
  name?: string;
  is_error?: boolean;
}

export interface ChildResult {
  description?: string;
  session_id: string;
  parent_run_id: string;
  thread_id: string;
  run_id: string;
  provider: string;
  stop_reason?: string;
  task_id: string;
  subagent_type: string;
  status: ChildStatus;
  output?: string;
  error?: string;
  coordination_error?: string;
  usage: {
    input_tokens?: number;
    output_tokens?: number;
    cached_input_tokens?: number;
  };
  started_at: string;
  completed_at?: string;
}

export interface ChildSession {
  id: string;
  parent_session_id?: string;
  result: ChildResult;
  history?: ChildMessage[];
  cancel_requested: boolean;
}

export interface ChildInput {
  id: string;
  content: string;
  status: "pending" | "consumed";
}

export function isChildActive(status: ChildStatus): boolean {
  return status === "pending" || status === "running";
}

// A persisted catalog may contain an orphan or a cycle after an interrupted
// migration. Keep every session reachable without recursing forever.
export function childTree(
  sessions: ChildSession[],
): { session: ChildSession; depth: number }[] {
  const children = new Map<string, ChildSession[]>();
  const ids = new Set(sessions.map((s) => s.id));
  for (const session of sessions) {
    const parent =
      session.parent_session_id && ids.has(session.parent_session_id)
        ? session.parent_session_id
        : "";
    children.set(parent, [...(children.get(parent) ?? []), session]);
  }
  const rows: { session: ChildSession; depth: number }[] = [];
  const visited = new Set<string>();
  const visit = (session: ChildSession, depth: number) => {
    if (visited.has(session.id)) return;
    visited.add(session.id);
    rows.push({ session, depth });
    for (const child of children.get(session.id) ?? []) visit(child, depth + 1);
  };
  for (const session of children.get("") ?? []) visit(session, 0);
  for (const session of sessions) visit(session, 0);
  return rows;
}
