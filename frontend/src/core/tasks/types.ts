import type { AIMessage } from "@/core/types";

export interface Subtask {
  id: string;
  session_id?: string;
  final_status?:
    | "completed"
    | "failed"
    | "cancelled"
    | "timed_out"
    | "polling_timed_out";
  status: "in_progress" | "completed" | "failed";
  subagent_type: string;
  description: string;
  latestMessage?: AIMessage;
  prompt: string;
  result?: string;
  error?: string;
}
