import type { Message } from "@/core/types";
import type { Thread, TokenUsage } from "@/core/types/thread";

import type { Todo } from "../todos";

export type { TokenUsage };

export interface AgentThreadState extends Record<string, unknown> {
  title: string;
  messages: Message[];
  artifacts: string[];
  todos?: Todo[];
}

export interface AgentThread extends Thread<AgentThreadState> {}

export interface AgentRunIntent extends Record<string, unknown> {
  model_name: string | undefined;
  mode: "flash" | "thinking" | "pro" | "ultra" | undefined;
  swarm_enabled: boolean;
}

export interface UserQuestionOption {
  label: string;
  description?: string;
}

export interface PendingUserQuestion {
  id: string;
  run_id: string;
  thread_id: string;
  header: string;
  question: string;
  detail?: string;
  options: UserQuestionOption[];
  intent?: string;
  status: "pending";
}

export interface AgentThreadContext extends AgentRunIntent {
  thread_id: string;
}
