/**
 * Drama types - TypeScript definitions for drama/short-drama features
 */

export interface DramaProject {
  id: number;
  user_id: number;
  name: string;
  intro: string | null;
  type: string | null;
  art_style: string | null;
  video_ratio: string | null;
  status: string;
  projectType?: string;
  created_at: string;
  updated_at?: string;
}

export interface DramaNovel {
  id: number;
  project_id: number;
  chapter: string;
  chapter_data: string;
  chapter_index: number;
  created_at?: string;
}

export interface DramaOutline {
  id: number;
  project_id: number;
  episode: number | null;
  data: Record<string, unknown> | null;
}

export interface DramaStoryline {
  id: number;
  project_id: number;
  name: string | null;
  content: string | null;
  novel_ids: string | null;
}

export interface DramaScript {
  id: number;
  project_id: number;
  outline_id: number | null;
  name: string | null;
  content: string | null;
}

export interface DramaStoryboard {
  id: number;
  project_id: number;
  script_id: number | null;
  data: Record<string, unknown> | null;
  status: string;
}

export interface DramaAsset {
  id: number;
  project_id: number;
  type: string;
  name: string;
  intro: string | null;
  prompt: string | null;
  image_url: string | null;
  segment_id?: string | number;
  shot_index?: number;
}

export interface DramaVideo {
  id: number;
  project_id: number;
  task_type: string;
  status: string;
  result: Record<string, unknown> | null;
  error: string | null;
}

export interface DramaVideoConfig {
  id: number;
  project_id: number;
  script_id: number | null;
  mode: string;
  resolution: string;
  duration: number;
  prompt: string | null;
}

export interface DramaChatMessage {
  id: string;
  identity: "user" | "assistant" | "notice";
  role: string;
  data: ChatMessageContent[];
}

export interface ChatMessageContent {
  type: "text" | "thinking" | "image";
  text?: string;
  imageUrl?: string;
}

// WebSocket event types
export interface WsMessage {
  type: string;
  data?: unknown;
}

export interface StreamMessage extends WsMessage {
  type: "stream" | "subAgentStream" | "response_end";
  data: string;
}

export interface SubAgentMessage extends WsMessage {
  type: "subAgentEnd";
  data: { agent: string };
}

export interface TransferMessage extends WsMessage {
  type: "transfer";
  data: { to: string };
}

export interface RefreshMessage extends WsMessage {
  type: "refresh";
  data: "storyline" | "outline" | "assets";
}

// Art style
export interface ArtStyle {
  catName: string;
  name: string;
  prompt: string;
  promptEn: string;
  fileUrl: string;
}

export interface ArtStyleCategory {
  name: string;
  styles: ArtStyle[];
}
