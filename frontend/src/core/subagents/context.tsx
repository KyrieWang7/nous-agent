"use client";

import { createContext, useContext } from "react";

import type { ChildSession } from "./types";

export const ChildSessionsContext = createContext<{
  sessions: ChildSession[];
  openSession: (id: string) => void;
} | null>(null);

export function useChildSessions() {
  return useContext(ChildSessionsContext);
}
