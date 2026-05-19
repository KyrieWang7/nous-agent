export interface SwarmTeam {
  id: string;
  name: string;
  description: string | null;
  created_at: string;
}

export interface SwarmTeamMember {
  name: string;
  status: "active" | "running" | "removed";
  model: string | null;
  joined_at: string | null;
}

export type TeammateDisplayStatus =
  | "waiting"
  | "active"
  | "running"
  | "completed"
  | "failed"
  | "timeout";

export interface SwarmMessage {
  id: number;
  from: string;
  to: string;
  content: string;
  created_at: string;
}

export interface SwarmTeamUpdateEvent {
  members: SwarmTeamMember[];
}

export interface SwarmStatusEvent {
  agent_name: string;
  status: string;
  detail?: string;
}
