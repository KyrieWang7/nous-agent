// Package modelconfig defines persisted model settings shared by Gateway and Agent.
package modelconfig

type Model struct {
	Name                    string         `yaml:"name" json:"name"`
	DisplayName             string         `yaml:"display_name" json:"display_name,omitempty"`
	Description             string         `yaml:"description" json:"description,omitempty"`
	Provider                string         `yaml:"provider" json:"provider"`
	Model                   string         `yaml:"model" json:"model"`
	BaseURL                 string         `yaml:"base_url" json:"base_url,omitempty"`
	APIKey                  string         `yaml:"api_key" json:"api_key,omitempty"`
	MaxTokens               int            `yaml:"max_tokens" json:"max_tokens,omitempty"`
	Temperature             *float64       `yaml:"temperature" json:"temperature,omitempty"`
	ContextLength           int            `yaml:"context_length" json:"context_length,omitempty"`
	Timeout                 int            `yaml:"timeout" json:"timeout,omitempty"`
	StreamIdleTimeout       int            `yaml:"stream_idle_timeout" json:"stream_idle_timeout,omitempty"`
	UseFiles                bool           `yaml:"use_files" json:"use_files,omitempty"`
	SupportsThinking        bool           `yaml:"supports_thinking" json:"supports_thinking,omitempty"`
	SupportsReasoningEffort bool           `yaml:"supports_reasoning_effort" json:"supports_reasoning_effort,omitempty"`
	SupportsVision          bool           `yaml:"supports_vision" json:"supports_vision,omitempty"`
	ExtraBody               map[string]any `yaml:"extra_body" json:"extra_body,omitempty"`
	WhenThinkingEnabled     *Thinking      `yaml:"when_thinking_enabled" json:"when_thinking_enabled,omitempty"`
	Pricing                 Pricing        `yaml:"pricing" json:"pricing,omitempty"`
}

// Thinking contains provider options that only apply when a run
// explicitly enables thinking. Keeping this separate prevents vendor-specific
// fields from leaking into ordinary requests.
type Thinking struct {
	ExtraBody map[string]any `yaml:"extra_body" json:"extra_body,omitempty"`
}

// Pricing stores integer micro-currency rates per million tokens.
// A zero-valued config keeps cost accounting disabled for that model while
// token accounting remains active.
type Pricing struct {
	InputPerMillionMicros       int64 `yaml:"input_per_million_micros" json:"input_per_million_micros,omitempty"`
	OutputPerMillionMicros      int64 `yaml:"output_per_million_micros" json:"output_per_million_micros,omitempty"`
	CachedInputPerMillionMicros int64 `yaml:"cached_input_per_million_micros" json:"cached_input_per_million_micros,omitempty"`
}
type Settings struct {
	DefaultModel string  `json:"default_model" yaml:"default_model"`
	Models       []Model `json:"models" yaml:"models"`
}

// Input replaces the model list; an omitted key retains the previous credential.
type Input struct {
	DefaultModel string       `json:"default_model"`
	Models       []ModelInput `json:"models"`
}

type ModelInput struct {
	Model
	APIKey *string `json:"api_key,omitempty"`
	// Accepted for GET/edit/PUT clients; this read-only status never sets a key.
	APIKeyConfigured *bool `json:"api_key_configured,omitempty"`
}

type PublicModel struct {
	Model
	APIKeyConfigured bool `json:"api_key_configured"`
}

type PublicSettings struct {
	DefaultModel string        `json:"default_model"`
	Models       []PublicModel `json:"models"`
}
