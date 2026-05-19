import { createContext, useContext } from "react";

import type { AgentThreadState } from "@/core/threads";
import type { ThreadStream } from "@/core/types/thread";

export interface ThreadContextType {
  threadId: string;
  thread: ThreadStream<AgentThreadState>;
}

export const ThreadContext = createContext<ThreadContextType | undefined>(
  undefined,
);

export function useThread() {
  const context = useContext(ThreadContext);
  if (context === undefined) {
    throw new Error("useThread must be used within a ThreadContext");
  }
  return context;
}
