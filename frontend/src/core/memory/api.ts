import { getBackendBaseURL } from "../config";
import { authFetch } from "../api/auth-fetch";

import type { UserMemory } from "./types";

export async function loadMemory() {
  const response = await authFetch(`${getBackendBaseURL()}/api/memory`);
  if (!response.ok) {
    const text = await response.text().catch(() => "");
    throw new Error(`Failed to load memory: ${response.status} ${text}`);
  }
  const json = await response.json();
  return json as UserMemory;
}
