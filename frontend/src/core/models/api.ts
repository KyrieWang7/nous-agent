import { getBackendBaseURL } from "../config";
import { authFetch } from "../api/auth-fetch";

import type { Model } from "./types";

export async function loadModels() {
  const res = authFetch(`${getBackendBaseURL()}/api/models`);
  const { models } = (await (await res).json()) as { models: Model[] };
  return models;
}
