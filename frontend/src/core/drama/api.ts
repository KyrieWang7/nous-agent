"use client";

import { authFetch } from "../api/auth-fetch";

/**
 * Nous-Drama API Client
 * Short drama generation service API client
 */

const DRAMA_API_BASE = "/drama-api";

// Types
export interface DramaProject {
  id: number;
  user_id: number;
  name: string;
  intro: string | null;
  type: string | null;
  art_style: string | null;
  video_ratio: string | null;
  status: string;
  created_at: string;
  updated_at?: string;
}

export interface DramaTask {
  id: number;
  project_id: number;
  task_type: string;
  status: string;
  name: string | null;
  project_name: string | null;
  result: Record<string, unknown> | null;
  error: string | null;
  start_time: string | null;
  end_time: string | null;
  created_at: string;
}

export interface DramaPrompt {
  id: number;
  code: string;
  name: string | null;
  default_value: string | null;
  custom_value: string | null;
  description: string | null;
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

export interface DramaUser {
  id: number;
  name: string;
  password?: string;
  created_at?: string;
}

export interface DramaChatMessage {
  role: "user" | "assistant" | "system";
  content: string;
}

// API Functions

// User API
export const dramaUserApi = {
  getUser: async (userId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/user/${userId}`);
    if (!res.ok) throw new Error("Failed to get user");
    return res.json();
  },

  saveUser: async (data: { name: string; password?: string }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/user`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to save user");
    return res.json();
  },
};

// Task API
export const dramaTaskApi = {
  getTasks: async (params?: {
    project_name?: string;
    task_name?: string;
    task_type?: string;
    status?: string;
    page?: number;
    limit?: number;
  }) => {
    const searchParams = new URLSearchParams();
    if (params?.project_name) searchParams.set("project_name", params.project_name);
    if (params?.task_name) searchParams.set("task_name", params.task_name);
    if (params?.task_type) searchParams.set("task_type", params.task_type);
    if (params?.status) searchParams.set("status", params.status);
    if (params?.page) searchParams.set("page", String(params.page));
    if (params?.limit) searchParams.set("limit", String(params.limit));

    const res = await authFetch(`${DRAMA_API_BASE}/task?${searchParams}`);
    if (!res.ok) throw new Error("Failed to get tasks");
    return res.json();
  },

  getTaskDetails: async (taskId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/task/${taskId}`);
    if (!res.ok) throw new Error("Failed to get task details");
    return res.json();
  },
};

// Prompt API
export const dramaPromptApi = {
  getPrompts: async () => {
    const res = await authFetch(`${DRAMA_API_BASE}/prompt/prompts`);
    if (!res.ok) throw new Error("Failed to get prompts");
    return res.json();
  },

  updatePrompt: async (promptId: number, data: { custom_value?: string; default_value?: string }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/prompt/prompts/${promptId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to update prompt");
    return res.json();
  },
};

// Outline API
export const dramaOutlineApi = {
  getOutlines: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/outline/${projectId}`);
    if (!res.ok) throw new Error("Failed to get outlines");
    return res.json();
  },

  generateOutline: async (projectId: number, data: { chapters?: number[] }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/outline/${projectId}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to generate outline");
    return res.json();
  },

  // SSE streaming outline generation
  streamOutline: async (
    projectId: number,
    message: string,
    onMessage: (data: string) => void,
    onError?: (error: string) => void,
    threadId?: string
  ) => {
    const response = await authFetch(`${DRAMA_API_BASE}/outline/${projectId}/stream`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message, thread_id: threadId }),
    });

    if (!response.ok) {
      throw new Error("Failed to stream outline");
    }

    const reader = response.body?.getReader();
    if (!reader) throw new Error("No response body");

    const decoder = new TextDecoder();
    let buffer = "";

    while (true) {
      const { done, value } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split("\n");
      buffer = lines.pop() || "";

      for (const line of lines) {
        if (line.startsWith("data: ")) {
          const data = line.slice(6);
          if (data === "[DONE]") {
            return;
          }
          try {
            const parsed = JSON.parse(data);
            if (parsed.event === "error") {
              onError?.(parsed.data);
            } else {
              onMessage(parsed.data);
            }
          } catch {
            onMessage(data);
          }
        }
      }
    }
  },

  addOutline: async (projectId: number, data: { episode?: number; data?: Record<string, unknown> }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/outline/${projectId}/add`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ project_id: projectId, ...data }),
    });
    if (!res.ok) throw new Error("Failed to add outline");
    return res.json();
  },

  updateOutline: async (projectId: number, outlineId: number, data: { episode?: number; data?: Record<string, unknown> }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/outline/${projectId}/outline?outline_id=${outlineId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to update outline");
    return res.json();
  },

  updateScript: async (projectId: number, scriptId: number, data: { name?: string; content?: string }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/outline/${projectId}/script?script_id=${scriptId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to update script");
    return res.json();
  },

  deleteOutline: async (outlineId: number, projectId?: number) => {
    const searchParams = projectId ? `?projectId=${projectId}` : "";
    const res = await authFetch(`${DRAMA_API_BASE}/outline/${outlineId}${searchParams}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error("Failed to delete outline");
    return res.json();
  },

  getHistory: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/outline/${projectId}/history`);
    if (!res.ok) throw new Error("Failed to get history");
    return res.json();
  },

  setHistory: async (projectId: number, data: { data: unknown[] }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/outline/${projectId}/history`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to save history");
    return res.json();
  },

  getPartScript: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/outline/${projectId}/script/part`);
    if (!res.ok) throw new Error("Failed to get part script");
    return res.json();
  },

  updateStoryline: async (projectId: number, data: { content?: string; name?: string; novelIds?: string[] }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/outline/${projectId}/storyline`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to update storyline");
    return res.json();
  },

  getStoryline: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/storyline/${projectId}`);
    if (!res.ok) throw new Error("Failed to get storyline");
    return res.json();
  },
};

// Storyboard API
export const dramaStoryboardApi = {
  getStoryboards: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/storyboard/${projectId}`);
    if (!res.ok) throw new Error("Failed to get storyboards");
    return res.json();
  },

  createStoryboard: async (projectId: number, data: { script_id?: number; data?: Record<string, unknown>; status?: string }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/storyboard/${projectId}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to create storyboard");
    return res.json();
  },

  // SSE streaming storyboard generation
  streamStoryboard: async (
    projectId: number,
    scriptId: number,
    message: string,
    onMessage: (data: string) => void,
    onError?: (error: string) => void,
    threadId?: string
  ) => {
    const response = await authFetch(`${DRAMA_API_BASE}/storyboard/${projectId}/${scriptId}/stream`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message, thread_id: threadId }),
    });

    if (!response.ok) {
      throw new Error("Failed to stream storyboard");
    }

    const reader = response.body?.getReader();
    if (!reader) throw new Error("No response body");

    const decoder = new TextDecoder();
    let buffer = "";

    while (true) {
      const { done, value } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split("\n");
      buffer = lines.pop() || "";

      for (const line of lines) {
        if (line.startsWith("data: ")) {
          const data = line.slice(6);
          if (data === "[DONE]") {
            return;
          }
          try {
            const parsed = JSON.parse(data);
            if (parsed.event === "error") {
              onError?.(parsed.data);
            } else {
              onMessage(parsed.data);
            }
          } catch {
            onMessage(data);
          }
        }
      }
    }
  },

  generateStoryboard: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/storyboard/${projectId}/generate`, {
      method: "POST",
    });
    if (!res.ok) throw new Error("Failed to generate storyboard");
    return res.json();
  },

  updateStoryboard: async (storyboardId: number, data: { data?: Record<string, unknown>; status?: string }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/storyboard/${storyboardId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to update storyboard");
    return res.json();
  },

  deleteStoryboard: async (storyboardId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/storyboard/${storyboardId}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error("Failed to delete storyboard");
    return res.json();
  },

  // Batch super score image
  batchSuperScoreImage: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/storyboard/${projectId}/batch-superscore`, {
      method: "POST",
    });
    if (!res.ok) throw new Error("Failed to batch super score image");
    return res.json();
  },

  // Get chat history
  getChatHistory: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/storyboard/${projectId}/chat`);
    if (!res.ok) throw new Error("Failed to get chat history");
    return res.json();
  },

  // Chat with storyboard agent
  chatStoryboard: async (projectId: number, data: Record<string, unknown>) => {
    const res = await authFetch(`${DRAMA_API_BASE}/storyboard/${projectId}/chat`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to chat with storyboard");
    return res.json();
  },

  // Generate shot image
  generateShotImage: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/storyboard/${projectId}/shot-image`, {
      method: "POST",
    });
    if (!res.ok) throw new Error("Failed to generate shot image");
    return res.json();
  },

  // Generate video prompt
  generateVideoPrompt: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/storyboard/${projectId}/video-prompt`, {
      method: "POST",
    });
    if (!res.ok) throw new Error("Failed to generate video prompt");
    return res.json();
  },

  // Keep storyboard
  keepStoryboard: async (projectId: number, data: Record<string, unknown>) => {
    const res = await authFetch(`${DRAMA_API_BASE}/storyboard/${projectId}/keep`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to keep storyboard");
    return res.json();
  },

  // Upload image
  uploadImage: async (projectId: number, formData: FormData) => {
    const res = await authFetch(`${DRAMA_API_BASE}/storyboard/${projectId}/upload`, {
      method: "POST",
      body: formData,
    });
    if (!res.ok) throw new Error("Failed to upload image");
    return res.json();
  },

  getVideoStoryboards: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/video/${projectId}/storyboards`);
    if (!res.ok) throw new Error("Failed to get video storyboards");
    return res.json();
  },

  reviseVideoStoryboards: async (projectId: number, data: Record<string, unknown>) => {
    const res = await authFetch(`${DRAMA_API_BASE}/video/${projectId}/storyboards`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to revise video storyboards");
    return res.json();
  },
};

// Video API
export const dramaVideoApi = {
  getVideos: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/video/${projectId}`);
    if (!res.ok) throw new Error("Failed to get videos");
    return res.json();
  },

  addVideo: async (data: { project_id: number; script_id?: number; storyboard_ids?: number[]; config_id?: number }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/video`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to add video");
    return res.json();
  },

  saveVideo: async (videoId: number, data: { result?: Record<string, unknown>; status?: string }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/video/${videoId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to save video");
    return res.json();
  },

  getVideoConfigs: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/video/configs?project_id=${projectId}`);
    if (!res.ok) throw new Error("Failed to get video configs");
    return res.json();
  },

  addVideoConfig: async (data: {
    project_id: number;
    script_id?: number;
    mode?: string;
    resolution?: string;
    duration?: number;
    prompt?: string;
    audio_enabled?: boolean;
  }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/video/config`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to add video config");
    return res.json();
  },

  updateVideoConfig: async (configId: number, data: {
    mode?: string;
    resolution?: string;
    duration?: number;
    prompt?: string;
    audio_enabled?: boolean;
  }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/video/config/${configId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to update video config");
    return res.json();
  },

  deleteVideoConfig: async (configId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/video/config/${configId}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error("Failed to delete video config");
    return res.json();
  },

  getManufacturers: async () => {
    const res = await authFetch(`${DRAMA_API_BASE}/video/manufacturer`);
    if (!res.ok) throw new Error("Failed to get manufacturers");
    return res.json();
  },

  getVideoModels: async (manufacturer?: string) => {
    const searchParams = manufacturer ? `?manufacturer=${manufacturer}` : "";
    const res = await authFetch(`${DRAMA_API_BASE}/video/models${searchParams}`);
    if (!res.ok) throw new Error("Failed to get video models");
    return res.json();
  },

  generateVideoPrompt: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/video/${projectId}/prompt`, {
      method: "POST",
    });
    if (!res.ok) throw new Error("Failed to generate video prompt");
    return res.json();
  },

  // Generate video
  generateVideo: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/video/${projectId}/generate`, {
      method: "POST",
    });
    if (!res.ok) throw new Error("Failed to generate video");
    return res.json();
  },

  // Get video task
  getVideoTask: async (taskId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/video/task/${taskId}`);
    if (!res.ok) throw new Error("Failed to get video task");
    return res.json();
  },
};

// Assets API
export const dramaAssetsApi = {
  getAssets: async (projectId: number, assetType?: string) => {
    const searchParams = assetType ? `?asset_type=${assetType}` : "";
    const res = await authFetch(`${DRAMA_API_BASE}/assets/${projectId}${searchParams}`);
    if (!res.ok) throw new Error("Failed to get assets");
    return res.json();
  },

  // Generate assets from outline
  generateAssets: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/assets/${projectId}/generate`, {
      method: "POST",
    });
    if (!res.ok) throw new Error("Failed to generate assets");
    return res.json();
  },

  createAsset: async (projectId: number, data: {
    type: string;
    name: string;
    intro?: string;
    prompt?: string;
    image_url?: string;
  }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/assets/${projectId}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to create asset");
    return res.json();
  },

  updateAsset: async (assetId: number, data: {
    name?: string;
    intro?: string;
    prompt?: string;
    image_url?: string;
  }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/assets/${assetId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to update asset");
    return res.json();
  },

  deleteAsset: async (assetId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/assets/${assetId}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error("Failed to delete asset");
    return res.json();
  },

  getImage: async (imageId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/assets/image/${imageId}`);
    if (!res.ok) throw new Error("Failed to get image");
    return res.json();
  },

  deleteImage: async (imageId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/assets/image/${imageId}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error("Failed to delete image");
    return res.json();
  },

  getStoryboardAsset: async (storyboardId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/assets/storyboard/${storyboardId}`);
    if (!res.ok) throw new Error("Failed to get storyboard asset");
    return res.json();
  },

  polishPrompt: async (prompt: string) => {
    const res = await authFetch(`${DRAMA_API_BASE}/assets/prompt/polish`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ prompt }),
    });
    if (!res.ok) throw new Error("Failed to polish prompt");
    return res.json();
  },
};

// Script API
export const dramaScriptApi = {
  getScripts: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/script/${projectId}`);
    if (!res.ok) throw new Error("Failed to get scripts");
    return res.json();
  },

  createScript: async (projectId: number, data: { outline_id?: number; name?: string; content?: string }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/script/${projectId}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to create script");
    return res.json();
  },

  updateScript: async (scriptId: number, data: { name?: string; content?: string }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/script/${scriptId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to update script");
    return res.json();
  },

  deleteScript: async (scriptId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/script/${scriptId}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error("Failed to delete script");
    return res.json();
  },
};

// Novel API
export interface DramaNovel {
  id: number;
  project_id: number;
  chapter: string;
  chapter_data: string;
  chapter_index: number;
}

export const dramaNovelApi = {
  getNovels: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/novels/${projectId}`);
    if (!res.ok) throw new Error("Failed to get novels");
    return res.json();
  },

  createNovel: async (projectId: number, data: { chapter?: string; chapter_data?: string; chapter_index?: number }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/novels/${projectId}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to create novel");
    return res.json();
  },

  updateNovel: async (novelId: number, data: { chapter?: string; chapter_data?: string }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/novels/${novelId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to update novel");
    return res.json();
  },

  deleteNovel: async (novelId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/novels/${novelId}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error("Failed to delete novel");
    return res.json();
  },
};

// Storyline API
export const dramaStorylineApi = {
  getStoryline: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/storyline/${projectId}`);
    if (!res.ok) throw new Error("Failed to get storyline");
    return res.json();
  },

  updateStoryline: async (projectId: number, data: { content?: string; name?: string; novelIds?: string[] }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/outline/${projectId}/storyline`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to update storyline");
    return res.json();
  },
};

// Project API
export const dramaProjectApi = {
  getProjects: async () => {
    const res = await authFetch(`${DRAMA_API_BASE}/project/getProject`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({}),
    });
    if (!res.ok) throw new Error("Failed to get projects");
    return res.json();
  },

  getProject: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/project/getProject`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: projectId }),
    });
    if (!res.ok) throw new Error("Failed to get project");
    return res.json();
  },

  createProject: async (data: {
    name: string;
    intro?: string;
    type?: string;
    art_style?: string;
    video_ratio?: string;
  }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/project/addProject`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: data.name,
        intro: data.intro || "",
        type: data.type || "短剧",
        artStyle: data.art_style || "2D动漫风格",
        videoRatio: data.video_ratio || "16:9",
      }),
    });
    if (!res.ok) throw new Error("Failed to create project");
    return res.json();
  },

  updateProject: async (projectId: number, data: {
    name?: string;
    intro?: string;
    type?: string;
    art_style?: string;
    video_ratio?: string;
    status?: string;
  }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/project/updateProject`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        id: projectId,
        intro: data.intro,
        type: data.type,
        artStyle: data.art_style,
        videoRatio: data.video_ratio,
      }),
    });
    if (!res.ok) throw new Error("Failed to update project");
    return res.json();
  },

  deleteProject: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/project/delProject`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: projectId }),
    });
    if (!res.ok) throw new Error("Failed to delete project");
    return res.json();
  },

  getProjectStats: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/project/getProjectCount`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ projectId }),
    });
    if (!res.ok) throw new Error("Failed to get project stats");
    return res.json();
  },

  // Task API
  getTaskCategories: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/task/getTaskCategories`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ projectId }),
    });
    if (!res.ok) throw new Error("Failed to get task categories");
    return res.json();
  },

  getTaskList: async (params: {
    page: number;
    limit: number;
    taskClass: string;
    state: string;
    projectId: number;
  }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/task/getMyTaskApi`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(params),
    });
    if (!res.ok) throw new Error("Failed to get task list");
    return res.json();
  },

  // Novel API
  getNovel: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/novel/getNovel`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ projectId }),
    });
    if (!res.ok) throw new Error("Failed to get novel");
    return res.json();
  },

  addNovel: async (
    projectId: number,
    chapters: Array<{
      index: number;
      reel: string;
      chapter: string;
      chapterData: string;
    }>
  ) => {
    const res = await authFetch(`${DRAMA_API_BASE}/novel/addNovel`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ projectId, data: chapters }),
    });
    if (!res.ok) throw new Error("Failed to add novel");
    return res.json();
  },

  updateNovel: async (data: {
    id: number;
    index: number;
    reel: string;
    chapter: string;
    chapterData: string;
  }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/novel/updateNovel`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to update novel");
    return res.json();
  },

  delNovel: async (id: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/novel/delNovel`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id }),
    });
    if (!res.ok) throw new Error("Failed to delete novel");
    return res.json();
  },

  // Outline API
  getStoryline: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/outline/getStoryline`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ projectId }),
    });
    if (!res.ok) throw new Error("Failed to get storyline");
    return res.json();
  },

  updateStoryline: async (projectId: number, content: string) => {
    const res = await authFetch(`${DRAMA_API_BASE}/outline/updateStoryline`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ projectId, content }),
    });
    if (!res.ok) throw new Error("Failed to update storyline");
    return res.json();
  },

  getOutline: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/outline/getOutline`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ projectId }),
    });
    if (!res.ok) throw new Error("Failed to get outline");
    return res.json();
  },

  addOutline: async (projectId: number, data: string) => {
    const res = await authFetch(`${DRAMA_API_BASE}/outline/addOutline`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ projectId, data }),
    });
    if (!res.ok) throw new Error("Failed to add outline");
    return res.json();
  },

  updateOutline: async (id: number, projectId: number, data: string) => {
    const res = await authFetch(`${DRAMA_API_BASE}/outline/updateOutline`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id, projectId, data }),
    });
    if (!res.ok) throw new Error("Failed to update outline");
    return res.json();
  },

  deleteOutline: async (id: number, projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/outline/delOutline`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id, projectId }),
    });
    if (!res.ok) throw new Error("Failed to delete outline");
    return res.json();
  },

  // Script API
  getScripts: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/script/geScriptApi`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ projectId }),
    });
    if (!res.ok) throw new Error("Failed to get scripts");
    return res.json();
  },

  generateScript: async (outlineId: number, scriptId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/script/generateScriptApi`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ outlineId, scriptId }),
    });
    if (!res.ok) throw new Error("Failed to generate script");
    return res.json();
  },

  saveScript: async (outlineId: number, scriptId: number, content: string) => {
    const res = await authFetch(`${DRAMA_API_BASE}/script/generateScriptSave`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ outlineId, scriptId, content }),
    });
    if (!res.ok) throw new Error("Failed to save script");
    return res.json();
  },

  // Asset API
  getScriptList: async (projectId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/script/geScriptApi`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ projectId }),
    });
    if (!res.ok) throw new Error("Failed to get script list");
    return res.json();
  },

  getAssets: async (projectId: number, type: string) => {
    const res = await authFetch(`${DRAMA_API_BASE}/assets/getAssets`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ projectId, type }),
    });
    if (!res.ok) throw new Error("Failed to get assets");
    return res.json();
  },

  addAsset: async (projectId: number, data: Record<string, unknown>) => {
    const res = await authFetch(`${DRAMA_API_BASE}/assets/addAssets`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ projectId, ...data }),
    });
    if (!res.ok) throw new Error("Failed to add asset");
    return res.json();
  },

  updateAsset: async (id: number, data: Record<string, unknown>) => {
    const res = await authFetch(`${DRAMA_API_BASE}/assets/updateAssets`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id, ...data }),
    });
    if (!res.ok) throw new Error("Failed to update asset");
    return res.json();
  },

  deleteAsset: async (id: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/assets/delAssets`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id }),
    });
    if (!res.ok) throw new Error("Failed to delete asset");
    return res.json();
  },

  generateAsset: async (
    projectId: number,
    type: string,
    name: string,
    prompt: string,
    id: number
  ) => {
    const res = await authFetch(`${DRAMA_API_BASE}/assets/generateAssets`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ projectId, type, name, prompt, id }),
    });
    if (!res.ok) throw new Error("Failed to generate asset");
    return res.json();
  },

  getStoryboard: async (projectId: number, scriptId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/assets/getAssets`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ projectId, type: "storyboard", scriptId }),
    });
    if (!res.ok) throw new Error("Failed to get storyboard");
    return res.json();
  },
};

// Setting API
export const dramaSettingApi = {
  getSetting: async (projectId?: number) => {
    const searchParams = projectId ? `?project_id=${projectId}` : "";
    const res = await authFetch(`${DRAMA_API_BASE}/setting${searchParams}`);
    if (!res.ok) throw new Error("Failed to get setting");
    return res.json();
  },

  getAiConfigs: async () => {
    const res = await authFetch(`${DRAMA_API_BASE}/setting/ai-configs`);
    if (!res.ok) throw new Error("Failed to get AI configs");
    return res.json();
  },

  addAiConfig: async (data: {
    name: string;
    manufacturer?: string;
    model?: string;
    model_type?: string;
    api_key?: string;
    base_url?: string;
  }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/setting/ai-configs`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to add AI config");
    return res.json();
  },

  updateAiConfig: async (configId: number, data: {
    name?: string;
    manufacturer?: string;
    model?: string;
    model_type?: string;
    api_key?: string;
    base_url?: string;
  }) => {
    const res = await authFetch(`${DRAMA_API_BASE}/setting/ai-configs/${configId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to update AI config");
    return res.json();
  },

  deleteAiConfig: async (configId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/setting/ai-configs/${configId}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error("Failed to delete AI config");
    return res.json();
  },

  getAiModelMap: async () => {
    const res = await authFetch(`${DRAMA_API_BASE}/setting/ai-model-map`);
    if (!res.ok) throw new Error("Failed to get AI model map");
    return res.json();
  },

  configureAiModel: async (data: Record<string, unknown>) => {
    const res = await authFetch(`${DRAMA_API_BASE}/setting/ai-model-map/configure`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to configure AI model");
    return res.json();
  },

  getAiManufacturers: async () => {
    const res = await authFetch(`${DRAMA_API_BASE}/setting/ai-manufacturers`);
    if (!res.ok) throw new Error("Failed to get AI manufacturers");
    return res.json();
  },

  getAiModelList: async (modelType?: string) => {
    const searchParams = modelType ? `?model_type=${modelType}` : "";
    const res = await authFetch(`${DRAMA_API_BASE}/setting/ai-model-list${searchParams}`);
    if (!res.ok) throw new Error("Failed to get AI model list");
    return res.json();
  },

  getTextModelList: async () => {
    const res = await authFetch(`${DRAMA_API_BASE}/setting/text-model-list`);
    if (!res.ok) throw new Error("Failed to get text model list");
    return res.json();
  },

  getImageModelList: async () => {
    const res = await authFetch(`${DRAMA_API_BASE}/setting/image-model-list`);
    if (!res.ok) throw new Error("Failed to get image model list");
    return res.json();
  },

  getVideoModelList: async (manufacturer?: string) => {
    const searchParams = manufacturer ? `?manufacturer=${manufacturer}` : "";
    const res = await authFetch(`${DRAMA_API_BASE}/setting/video-model-list${searchParams}`);
    if (!res.ok) throw new Error("Failed to get video model list");
    return res.json();
  },

  getVideoModelDetail: async (modelId: number) => {
    const res = await authFetch(`${DRAMA_API_BASE}/setting/video-model-detail?model_id=${modelId}`);
    if (!res.ok) throw new Error("Failed to get video model detail");
    return res.json();
  },

  getLog: async () => {
    const res = await authFetch(`${DRAMA_API_BASE}/setting/log`);
    if (!res.ok) throw new Error("Failed to get log");
    return res.json();
  },

  configurationModel: async (data: Record<string, unknown>) => {
    const res = await authFetch(`${DRAMA_API_BASE}/setting/configuration`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error("Failed to configure model");
    return res.json();
  },
};
