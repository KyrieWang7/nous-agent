import { getBackendBaseURL } from "@/core/config";
import { authFetch } from "@/core/api/auth-fetch";

import type { Skill, SkillDetail } from "./type";

export async function loadSkills() {
  const response = await authFetch(`${getBackendBaseURL()}/api/skills`);
  if (!response.ok) {
    const text = await response.text().catch(() => "");
    throw new Error(`Failed to load skills: ${response.status} ${text}`);
  }
  const json = await response.json();
  return json.skills as Skill[];
}

export async function loadSkillDetail(skillName: string): Promise<SkillDetail> {
  const response = await authFetch(
    `${getBackendBaseURL()}/api/skills/${encodeURIComponent(skillName)}`,
  );
  if (!response.ok) {
    const text = await response.text().catch(() => "");
    throw new Error(`Failed to load skill detail: ${response.status} ${text}`);
  }
  return response.json();
}

export async function enableSkill(skillName: string, enabled: boolean) {
  const response = await authFetch(
    `${getBackendBaseURL()}/api/skills/${skillName}`,
    {
      method: "PUT",
      headers: {
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        enabled,
      }),
    },
  );
  return response.json();
}

export interface InstallSkillRequest {
  thread_id: string;
  path: string;
}

export interface InstallSkillResponse {
  success: boolean;
  skill_name: string;
  message: string;
}

export async function installSkill(
  request: InstallSkillRequest,
): Promise<InstallSkillResponse> {
  const response = await authFetch(`${getBackendBaseURL()}/api/skills/install`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });

  if (!response.ok) {
    // Handle HTTP error responses (4xx, 5xx)
    const errorData = await response.json().catch(() => ({}));
    const errorMessage =
      errorData.detail ?? `HTTP ${response.status}: ${response.statusText}`;
    return {
      success: false,
      skill_name: "",
      message: errorMessage,
    };
  }

  return response.json();
}
