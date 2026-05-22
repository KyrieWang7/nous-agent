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

export interface AgentThreadContext extends Record<string, unknown> {
  thread_id: string;
  model_name: string | undefined;
  thinking_enabled: boolean;
  is_plan_mode: boolean;
  subagent_enabled: boolean;
  swarm_enabled: boolean;
  reasoning_effort?: "low" | "medium" | "high" | undefined;
}

