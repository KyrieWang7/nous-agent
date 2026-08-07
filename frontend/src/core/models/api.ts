import { authFetch } from "../api/auth-fetch";
import { getBackendBaseURL } from "../config";

import type { Model } from "./types";

export async function loadModels() {
  const res = await authFetch(`${getBackendBaseURL()}/api/models`);
  if (!res.ok) {
    throw new Error(`Failed to load models (${res.status})`);
  }
  const { models } = (await res.json()) as { models?: Model[] };
  if (!Array.isArray(models)) {
    throw new Error("Invalid models response");
  }
  return models;
}
