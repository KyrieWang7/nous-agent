import { webPath } from "@/core/config";

import type { Session } from "./types";

export async function getSession(signal?: AbortSignal): Promise<Session> {
  const response = await fetch(webPath("/api/auth/session"), { cache: "no-store", signal });
  if (!response.ok) throw new Error("Unable to verify the shared session");
  return (await response.json()) as Session;
}

export async function logout(): Promise<void> {
  const response = await fetch(webPath("/api/auth/logout"), { method: "POST" });
  if (!response.ok) throw new Error("Unable to sign out");
}
