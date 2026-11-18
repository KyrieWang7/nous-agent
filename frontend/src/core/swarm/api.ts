import { getBackendBaseURL } from "../config";

import type { SwarmTeam, SwarmTeamMember } from "./types";

export async function getTeamsByThread(threadId: string): Promise<SwarmTeam[]> {
  const base = getBackendBaseURL();
  const res = await fetch(
    `${base}/api/swarm/teams?thread_id=${encodeURIComponent(threadId)}`,
  );
  if (!res.ok) throw new Error(`Failed to fetch teams: ${res.status}`);
  const data = await res.json();
  return data.teams;
}

export async function getTeamMembers(
  teamId: string,
): Promise<SwarmTeamMember[]> {
  const base = getBackendBaseURL();
  const res = await fetch(`${base}/api/swarm/teams/${teamId}/members`);
  if (!res.ok) throw new Error(`Failed to fetch members: ${res.status}`);
  const data = await res.json();
  return data.members;
}

export function getSwarmStreamURL(teamId: string): string {
  const gateway = getBackendBaseURL();
  return `${gateway}/api/swarm/teams/${encodeURIComponent(teamId)}/stream`;
}
