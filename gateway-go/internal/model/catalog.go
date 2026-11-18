package model

import "errors"

var ErrModelNotFound = errors.New("model not found")

type Model struct {
	Name                    string  `json:"name" yaml:"name"`
	DisplayName             string  `json:"display_name" yaml:"display_name"`
	Description             *string `json:"description" yaml:"description"`
	SupportsThinking        bool    `json:"supports_thinking" yaml:"supports_thinking"`
	SupportsReasoningEffort bool    `json:"supports_reasoning_effort" yaml:"supports_reasoning_effort"`
}

type MCPServer struct {
	Enabled     bool              `json:"enabled"`
	Type        string            `json:"type,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers"`
	Description string            `json:"description"`
}

type Extensions struct {
	MCPServers map[string]MCPServer  `json:"mcpServers"`
	Skills     map[string]SkillState `json:"skills"`
}

type SkillState struct {
	Enabled bool `json:"enabled"`
}

type Skill struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	License     *string `json:"license"`
	Category    string  `json:"category"`
	Enabled     bool    `json:"enabled"`
	Content     string  `json:"content,omitempty"`
	Path        string  `json:"-"`
}
