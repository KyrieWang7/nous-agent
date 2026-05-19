import { getBackendBaseURL } from "@/core/config";
import { authFetch } from "@/core/api/auth-fetch";

import type { MCPConfig } from "./types";

export async function loadMCPConfig() {
  const response = await authFetch(`${getBackendBaseURL()}/api/mcp/config`);
  if (!response.ok) {
    const text = await response.text().catch(() => "");
    throw new Error(`Failed to load MCP config: ${response.status} ${text}`);
  }
  return response.json() as Promise<MCPConfig>;
}

export async function updateMCPConfig(config: MCPConfig) {
  const response = await authFetch(`${getBackendBaseURL()}/api/mcp/config`,
    {
      method: "PUT",
      headers: {
        "Content-Type": "application/json",
      },
      body: JSON.stringify(config),
    },
  );
  return response.json();
}
