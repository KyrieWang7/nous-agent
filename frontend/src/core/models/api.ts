import { authFetch } from "../api/auth-fetch";
import { getBackendBaseURL } from "../config";

import type { Model, ModelSettings, ModelSettingsInput } from "./types";

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

async function requestModelSettings(
  options?: RequestInit,
): Promise<ModelSettings> {
  const response = await authFetch(`${getBackendBaseURL()}/api/models/config`, {
    cache: "no-store",
    ...options,
  });
  if (!response.ok) {
    const body = (await response.json().catch(() => null)) as {
      detail?: string;
    } | null;
    throw new Error(body?.detail ?? `HTTP ${response.status}`);
  }
  const settings = (await response.json()) as ModelSettings;
  if (!Array.isArray(settings?.models)) {
    throw new Error("Invalid model configuration response");
  }
  return settings;
}

export function loadModelSettings() {
  return requestModelSettings();
}

export function saveModelSettings(settings: ModelSettingsInput) {
  return requestModelSettings({
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(settings),
  });
}
