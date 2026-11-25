export interface Model {
  id?: string;
  name: string;
  display_name: string;
  description?: string | null;
  supports_thinking?: boolean;
}

export type ModelProvider = "openai-compatible" | "anthropic" | "deepseek";

export interface ModelConnection {
  name: string;
  display_name?: string;
  provider: ModelProvider;
  model: string;
  base_url?: string;
  api_key_configured?: boolean;
  use_files?: boolean;
  extra_body?: Record<string, unknown>;
  when_thinking_enabled?: { extra_body?: Record<string, unknown> };
}

export interface ModelSettings {
  default_model: string;
  models: ModelConnection[];
}

export interface ModelSettingsInput extends Omit<ModelSettings, "models"> {
  models: (ModelConnection & { api_key?: string })[];
}
