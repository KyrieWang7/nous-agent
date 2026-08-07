// Package config loads application configuration. Kernel packages consume
// typed options and never import this package.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/hooks"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/permission"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server        ServerConfig        `yaml:"server"`
	Models        []ModelConfig       `yaml:"models"`
	DefaultModel  string              `yaml:"default_model"`
	Sandbox       SandboxConfig       `yaml:"sandbox"`
	Permissions   PermissionConfig    `yaml:"permissions"`
	Loop          LoopConfig          `yaml:"loop"`
	Runtime       RuntimeConfig       `yaml:"runtime"`
	Skills        SkillsConfig        `yaml:"skills"`
	Subagents     SubagentConfig      `yaml:"subagents"`
	Extensions    ExtensionsConfig    `yaml:"extensions"`
	Memory        MemoryConfig        `yaml:"memory"`
	Title         TitleConfig         `yaml:"title"`
	Swarm         SwarmConfig         `yaml:"swarm"`
	Summarization SummarizationConfig `yaml:"summarization"`
	Guardrails    GuardrailsConfig    `yaml:"guardrails"`
	Hooks         []HookConfig        `yaml:"hooks"`
}

type ServerConfig struct {
	Address string `yaml:"address"`
}
type ModelConfig struct {
	Name             string         `yaml:"name"`
	DisplayName      string         `yaml:"display_name"`
	Description      string         `yaml:"description"`
	Provider         string         `yaml:"provider"`
	Model            string         `yaml:"model"`
	BaseURL          string         `yaml:"base_url"`
	APIKey           string         `yaml:"api_key"`
	MaxTokens        int            `yaml:"max_tokens"`
	ContextLength    int            `yaml:"context_length"`
	Timeout          int            `yaml:"timeout"`
	SupportsThinking bool           `yaml:"supports_thinking"`
	SupportsVision   bool           `yaml:"supports_vision"`
	ExtraBody        map[string]any `yaml:"extra_body"`
}
type SandboxConfig struct {
	Enabled     bool          `yaml:"enabled"`
	Provider    string        `yaml:"provider"`
	BaseDir     string        `yaml:"base_dir"`
	ExecTimeout time.Duration `yaml:"exec_timeout"`
}
type PermissionConfig struct {
	Mode          permission.Mode            `yaml:"mode"`
	ToolOverrides map[string]permission.Mode `yaml:"tool_overrides"`
}
type LoopConfig struct {
	MaxIterations        int           `yaml:"max_iterations"`
	StopReinjectionLimit int           `yaml:"stop_reinjection_limit"`
	Deadline             time.Duration `yaml:"deadline"`
	ToolConcurrency      int           `yaml:"tool_concurrency"`
	TokenBudget          int           `yaml:"token_budget"`
	MiddlewareTimeout    time.Duration `yaml:"middleware_timeout"`
}
type RuntimeConfig struct {
	EventBufferSize   int           `yaml:"event_buffer_size"`
	EventTTL          time.Duration `yaml:"event_ttl"`
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
	RedisURL          string        `yaml:"redis_url"`
	DatabaseURL       string        `yaml:"database_url"`
}
type SkillsConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Path     string   `yaml:"path"`
	Force    []string `yaml:"force"`
	Disabled []string `yaml:"-"`
}
type SubagentConfig struct {
	Enabled       bool          `yaml:"enabled"`
	MaxConcurrent int           `yaml:"max_concurrent"`
	TaskTTL       time.Duration `yaml:"task_ttl"`
}
type ExtensionsConfig struct {
	MCPServers map[string]MCPServerConfig `yaml:"mcp_servers"`
}
type MCPServerConfig struct {
	Enabled bool              `yaml:"enabled"`
	Type    string            `yaml:"type"`
	URL     string            `yaml:"url"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Prefix  string            `yaml:"prefix"`
	Headers map[string]string `yaml:"headers"`
	Env     map[string]string `yaml:"env"`
}
type MemoryConfig struct {
	Enabled             bool    `yaml:"enabled"`
	MaxFacts            int     `yaml:"max_facts"`
	ConfidenceThreshold float64 `yaml:"confidence_threshold"`
	InjectionTokens     int     `yaml:"injection_tokens"`
}
type TitleConfig struct {
	Enabled  bool `yaml:"enabled"`
	MaxWords int  `yaml:"max_words"`
	MaxChars int  `yaml:"max_chars"`
}
type SwarmConfig struct {
	Enabled   bool `yaml:"enabled"`
	PollLimit int  `yaml:"poll_limit"`
}
type SummarizationConfig struct {
	Enabled          bool `yaml:"enabled"`
	TriggerTokens    int  `yaml:"trigger_tokens"`
	KeepMessages     int  `yaml:"keep_messages"`
	MaxSummaryTokens int  `yaml:"max_summary_tokens"`
	MaxInputMessages int  `yaml:"max_input_messages"`
	MaxInputChars    int  `yaml:"max_input_chars"`
}
type GuardrailsConfig struct {
	Enabled    bool `yaml:"enabled"`
	FailClosed bool `yaml:"fail_closed"`
	Input      bool `yaml:"input"`
	Output     bool `yaml:"output"`
}
type HookConfig struct {
	Name    string        `yaml:"name"`
	Enabled bool          `yaml:"enabled"`
	Events  []hooks.Event `yaml:"events"`
	Command string        `yaml:"command"`
	Args    []string      `yaml:"args"`
	Matcher string        `yaml:"matcher"`
	Timeout time.Duration `yaml:"timeout"`
}

func Defaults() Config {
	return Config{
		Server:        ServerConfig{Address: ":7776"},
		Sandbox:       SandboxConfig{Enabled: true, Provider: "local", ExecTimeout: 30 * time.Second},
		Permissions:   PermissionConfig{Mode: permission.ModeWorkspaceWrite},
		Loop:          LoopConfig{MaxIterations: 100, StopReinjectionLimit: 3, Deadline: 30 * time.Minute, ToolConcurrency: 4, MiddlewareTimeout: 30 * time.Second},
		Runtime:       RuntimeConfig{EventBufferSize: 500, EventTTL: 24 * time.Hour, HeartbeatInterval: 15 * time.Second},
		Subagents:     SubagentConfig{Enabled: true, MaxConcurrent: 3, TaskTTL: 24 * time.Hour},
		Memory:        MemoryConfig{MaxFacts: 50, ConfidenceThreshold: .7, InjectionTokens: 1000},
		Title:         TitleConfig{Enabled: true, MaxWords: 8, MaxChars: 80},
		Swarm:         SwarmConfig{PollLimit: 50},
		Summarization: SummarizationConfig{Enabled: true, KeepMessages: 10, MaxSummaryTokens: 1024, MaxInputMessages: 200, MaxInputChars: 120_000},
		Guardrails:    GuardrailsConfig{FailClosed: true, Input: true, Output: true},
	}
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("config: reading %s: %w", path, err)
		}
		data = []byte(os.ExpandEnv(string(data)))
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("config: decoding %s: %w", path, err)
		}
	}
	if err := applyExtensions(&cfg, os.Getenv("NOUS_EXTENSIONS_CONFIG_PATH")); err != nil {
		return Config{}, err
	}
	applyEnv(&cfg)
	if err := cfg.Validate([]string{"openai-compatible", "anthropic"}); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyEnv(c *Config) {
	if v := os.Getenv("NOUS_AGENT_ADDR"); v != "" {
		c.Server.Address = v
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		c.Runtime.DatabaseURL = v
	}
	if v := os.Getenv("REDIS_URL"); v != "" {
		c.Runtime.RedisURL = v
	}
	if v := os.Getenv("NOUS_SKILLS_PATH"); v != "" {
		c.Skills.Path = v
	}
}

func applyExtensions(c *Config, path string) error {
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config: reading extensions %s: %w", path, err)
	}
	var ext struct {
		MCPServers map[string]MCPServerConfig `json:"mcpServers"`
		Skills     map[string]struct {
			Enabled bool `json:"enabled"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(os.ExpandEnv(string(raw))), &ext); err != nil {
		return fmt.Errorf("config: decoding extensions %s: %w", path, err)
	}
	if ext.MCPServers != nil {
		c.Extensions.MCPServers = ext.MCPServers
	}
	c.Skills.Disabled = c.Skills.Disabled[:0]
	for name, state := range ext.Skills {
		if !state.Enabled {
			c.Skills.Disabled = append(c.Skills.Disabled, name)
		}
	}
	slices.Sort(c.Skills.Disabled)
	return nil
}

func (c *Config) Validate(providers []string) error {
	if c.Server.Address == "" {
		return errors.New("config: server.address is required")
	}
	if len(c.Models) == 0 {
		return errors.New("config: at least one model is required")
	}
	seen := map[string]struct{}{}
	for _, m := range c.Models {
		if m.Name == "" || m.Model == "" || m.Provider == "" {
			return errors.New("config: every model requires name, provider and model")
		}
		if _, ok := seen[m.Name]; ok {
			return fmt.Errorf("config: duplicate model %q", m.Name)
		}
		seen[m.Name] = struct{}{}
		if !slices.Contains(providers, m.Provider) {
			return fmt.Errorf("config: unknown provider %q for model %q; available: %v", m.Provider, m.Name, providers)
		}
	}
	if c.DefaultModel == "" {
		c.DefaultModel = c.Models[0].Name
	} else if _, ok := seen[c.DefaultModel]; !ok {
		return fmt.Errorf("config: default_model %q is not declared", c.DefaultModel)
	}
	if c.Permissions.Mode != "" && !c.Permissions.Mode.Valid() {
		return fmt.Errorf("config: invalid permission mode %q", c.Permissions.Mode)
	}
	if c.Sandbox.Enabled && c.Sandbox.Provider != "local" && c.Sandbox.Provider != "docker" {
		return fmt.Errorf("config: unsupported sandbox provider %q; available: [local docker]", c.Sandbox.Provider)
	}
	for name, server := range c.Extensions.MCPServers {
		if !server.Enabled {
			continue
		}
		switch server.Type {
		case "http", "sse":
			if server.URL == "" {
				return fmt.Errorf("config: MCP server %q requires url", name)
			}
		case "stdio":
			if server.Command == "" {
				return fmt.Errorf("config: MCP server %q requires command", name)
			}
		default:
			return fmt.Errorf("config: MCP server %q has unsupported type %q", name, server.Type)
		}
	}
	for i, hook := range c.Hooks {
		if !hook.Enabled {
			continue
		}
		if hook.Name == "" || hook.Command == "" {
			return fmt.Errorf("config: hooks[%d] requires name and command", i)
		}
		if len(hook.Events) == 0 {
			return fmt.Errorf("config: hook %q requires at least one event", hook.Name)
		}
		for _, event := range hook.Events {
			if !event.Valid() {
				return fmt.Errorf("config: hook %q has unknown event %q; available: %v", hook.Name, event, hooks.Events())
			}
		}
		if hook.Matcher != "" {
			if _, err := regexp.Compile(hook.Matcher); err != nil {
				return fmt.Errorf("config: hook %q matcher: %w", hook.Name, err)
			}
		}
	}
	return nil
}

func (c Config) SelectedModel() (ModelConfig, error) {
	name := c.DefaultModel
	if name == "" && len(c.Models) > 0 {
		name = c.Models[0].Name
	}
	for _, m := range c.Models {
		if m.Name == name {
			return m, nil
		}
	}
	return ModelConfig{}, fmt.Errorf("config: model %q not found", name)
}
