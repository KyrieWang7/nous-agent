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
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/hooks"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/permission"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Deployment    DeploymentConfig          `yaml:"deployment"`
	Server        ServerConfig              `yaml:"server"`
	Models        []ModelConfig             `yaml:"models"`
	DefaultModel  string                    `yaml:"default_model"`
	Sandbox       SandboxConfig             `yaml:"sandbox"`
	Permissions   PermissionConfig          `yaml:"permissions"`
	Plan          PlanConfig                `yaml:"plan"`
	Loop          LoopConfig                `yaml:"loop"`
	Runtime       RuntimeConfig             `yaml:"runtime"`
	Skills        SkillsConfig              `yaml:"skills"`
	Subagents     SubagentConfig            `yaml:"subagents"`
	ToolGroups    []ToolGroupConfig         `yaml:"tool_groups"`
	Tools         []ToolConfig              `yaml:"tools"`
	Plugins       PluginsConfig             `yaml:"plugins"`
	ACPAgents     map[string]ACPAgentConfig `yaml:"acp_agents"`
	Extensions    ExtensionsConfig          `yaml:"extensions"`
	Memory        MemoryConfig              `yaml:"memory"`
	Title         TitleConfig               `yaml:"title"`
	Swarm         SwarmConfig               `yaml:"swarm"`
	Summarization SummarizationConfig       `yaml:"summarization"`
	Guardrails    GuardrailsConfig          `yaml:"guardrails"`
	Hooks         []HookConfig              `yaml:"hooks"`
}

type DeploymentConfig struct {
	Environment string `yaml:"environment"`
}

type ServerConfig struct {
	Address string `yaml:"address"`
}
type ModelConfig struct {
	Name                    string               `yaml:"name"`
	DisplayName             string               `yaml:"display_name"`
	Description             string               `yaml:"description"`
	Provider                string               `yaml:"provider"`
	Model                   string               `yaml:"model"`
	BaseURL                 string               `yaml:"base_url"`
	APIKey                  string               `yaml:"api_key"`
	MaxTokens               int                  `yaml:"max_tokens"`
	Temperature             *float64             `yaml:"temperature"`
	ContextLength           int                  `yaml:"context_length"`
	Timeout                 int                  `yaml:"timeout"`
	SupportsThinking        bool                 `yaml:"supports_thinking"`
	SupportsReasoningEffort bool                 `yaml:"supports_reasoning_effort"`
	SupportsVision          bool                 `yaml:"supports_vision"`
	ExtraBody               map[string]any       `yaml:"extra_body"`
	WhenThinkingEnabled     *ModelThinkingConfig `yaml:"when_thinking_enabled"`
	Pricing                 ModelPricingConfig   `yaml:"pricing"`
}

// ModelThinkingConfig contains provider options that only apply when a run
// explicitly enables thinking. Keeping this separate prevents vendor-specific
// fields from leaking into ordinary requests.
type ModelThinkingConfig struct {
	ExtraBody map[string]any `yaml:"extra_body"`
}

// ModelPricingConfig stores integer micro-currency rates per million tokens.
// A zero-valued config keeps cost accounting disabled for that model while
// token accounting remains active.
type ModelPricingConfig struct {
	InputPerMillionMicros       int64 `yaml:"input_per_million_micros"`
	OutputPerMillionMicros      int64 `yaml:"output_per_million_micros"`
	CachedInputPerMillionMicros int64 `yaml:"cached_input_per_million_micros"`
}
type SandboxConfig struct {
	Enabled        bool              `yaml:"enabled"`
	Provider       string            `yaml:"provider"`
	BaseDir        string            `yaml:"base_dir"`
	ExecTimeout    time.Duration     `yaml:"exec_timeout"`
	RemoteURL      string            `yaml:"remote_url"`
	RemoteHeaders  map[string]string `yaml:"remote_headers"`
	TenantID       string            `yaml:"tenant_id"`
	ProvisionerURL string            `yaml:"provisioner_url"`
}
type PermissionConfig struct {
	Preset        permission.Preset                           `yaml:"preset"`
	Presets       map[permission.Preset]permission.PresetSpec `yaml:"presets"`
	PromptTimeout time.Duration                               `yaml:"prompt_timeout"`
	ApprovalTTL   time.Duration                               `yaml:"approval_ttl"`
}
type PlanConfig struct {
	Guidance string `yaml:"guidance"`
}
type LoopConfig struct {
	MaxIterations        int           `yaml:"max_iterations"`
	StopReinjectionLimit int           `yaml:"stop_reinjection_limit"`
	Deadline             time.Duration `yaml:"deadline"`
	ToolConcurrency      int           `yaml:"tool_concurrency"`
	TokenBudget          int           `yaml:"token_budget"`
	CostBudgetMicros     int64         `yaml:"cost_budget_micros"`
	ToolCallBudget       int           `yaml:"tool_call_budget"`
	SubagentBudget       int           `yaml:"subagent_budget"`
	MaxRecursionDepth    int           `yaml:"max_recursion_depth"`
	LifecycleTimeout     time.Duration `yaml:"lifecycle_timeout"`
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
	Enabled       bool                             `yaml:"enabled"`
	MaxConcurrent int                              `yaml:"max_concurrent"`
	MaxTurns      int                              `yaml:"max_turns"`
	TaskTTL       time.Duration                    `yaml:"task_ttl"`
	Timeout       time.Duration                    `yaml:"timeout"`
	Agents        map[string]SubagentProfileConfig `yaml:"agents"`
}
type SubagentProfileConfig struct {
	Timeout time.Duration `yaml:"timeout"`
}
type ToolGroupConfig struct {
	Name string `yaml:"name"`
}
type ToolConfig struct {
	Name       string        `yaml:"name"`
	Group      string        `yaml:"group"`
	Use        string        `yaml:"use"`
	APIKey     string        `yaml:"api_key"`
	MaxResults int           `yaml:"max_results"`
	Timeout    time.Duration `yaml:"timeout"`
}
type PluginsConfig struct {
	Enabled     bool     `yaml:"enabled"`
	Directories []string `yaml:"directories"`
}
type ACPAgentConfig struct {
	Command                string            `yaml:"command"`
	Args                   []string          `yaml:"args"`
	Env                    map[string]string `yaml:"env"`
	Description            string            `yaml:"description"`
	Model                  string            `yaml:"model"`
	AutoApprovePermissions bool              `yaml:"auto_approve_permissions"`
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
	Enabled             bool          `yaml:"enabled"`
	MaxTeamSize         int           `yaml:"max_team_size"`
	MessagePollInterval time.Duration `yaml:"message_poll_interval"`
	TeammateTimeout     time.Duration `yaml:"teammate_timeout"`
	PollLimit           int           `yaml:"poll_limit"`
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
		Deployment:  DeploymentConfig{Environment: "development"},
		Server:      ServerConfig{Address: ":7776"},
		Sandbox:     SandboxConfig{Enabled: true, Provider: "local", ExecTimeout: 30 * time.Second},
		Permissions: PermissionConfig{Preset: permission.PresetWorkspaceWrite},
		Plan: PlanConfig{Guidance: "You are in plan mode. Think through the request, identify constraints, and prepare a concrete plan without using execution capabilities. " +
			"Call write_todos before any work with every executable step set to pending, then call exit_plan_mode with the complete Markdown plan starting with a # heading. " +
			"Do not begin implementation until the user approves the plan; if they keep planning, revise it using their feedback and present it again."},
		Loop:          LoopConfig{MaxIterations: 100, StopReinjectionLimit: 3, Deadline: 30 * time.Minute, ToolConcurrency: 4, ToolCallBudget: 200, SubagentBudget: 20, MaxRecursionDepth: 1, LifecycleTimeout: 30 * time.Second},
		Runtime:       RuntimeConfig{EventBufferSize: 500, EventTTL: 24 * time.Hour, HeartbeatInterval: 15 * time.Second},
		Subagents:     SubagentConfig{Enabled: true, MaxConcurrent: 3, MaxTurns: 50, TaskTTL: 24 * time.Hour, Timeout: 15 * time.Minute},
		Memory:        MemoryConfig{MaxFacts: 50, ConfidenceThreshold: .7, InjectionTokens: 1000},
		Title:         TitleConfig{Enabled: true, MaxWords: 8, MaxChars: 80},
		Swarm:         SwarmConfig{MaxTeamSize: 5, MessagePollInterval: 2 * time.Second, TeammateTimeout: 15 * time.Minute, PollLimit: 50},
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
	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate([]string{"openai-compatible", "anthropic"}); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyEnv(c *Config) error {
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

	gatewayBaseURL := strings.TrimSpace(os.Getenv("TUYOO_BASE_URL"))
	gatewayAPIKey := strings.TrimSpace(os.Getenv("TUYOO_API_KEY"))
	if (gatewayBaseURL == "") != (gatewayAPIKey == "") {
		return errors.New("config: TUYOO_BASE_URL and TUYOO_API_KEY must be set together")
	}
	if gatewayBaseURL != "" {
		for index := range c.Models {
			if c.Models[index].Provider != "openai-compatible" {
				continue
			}
			c.Models[index].BaseURL = gatewayBaseURL
			c.Models[index].APIKey = gatewayAPIKey
		}
	}
	return nil
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
		if m.Pricing.InputPerMillionMicros < 0 || m.Pricing.OutputPerMillionMicros < 0 || m.Pricing.CachedInputPerMillionMicros < 0 {
			return fmt.Errorf("config: model %q pricing cannot be negative", m.Name)
		}
	}
	if c.DefaultModel == "" {
		c.DefaultModel = c.Models[0].Name
	} else if _, ok := seen[c.DefaultModel]; !ok {
		return fmt.Errorf("config: default_model %q is not declared", c.DefaultModel)
	}
	if _, err := permission.NewPolicy(permission.Config{Preset: c.Permissions.Preset, Presets: c.Permissions.Presets}); err != nil {
		return fmt.Errorf("config: invalid permission policy: %w", err)
	}
	if c.Permissions.PromptTimeout < 0 || c.Permissions.ApprovalTTL < 0 {
		return errors.New("config: permissions prompt_timeout and approval_ttl cannot be negative")
	}
	if strings.TrimSpace(c.Plan.Guidance) == "" {
		return errors.New("config: plan.guidance is required")
	}
	if c.Loop.TokenBudget < 0 || c.Loop.CostBudgetMicros < 0 {
		return errors.New("config: loop token_budget and cost_budget_micros cannot be negative")
	}
	if c.Sandbox.Enabled && c.Sandbox.Provider != "local" && c.Sandbox.Provider != "docker" && c.Sandbox.Provider != "remote" && c.Sandbox.Provider != "aio" {
		return fmt.Errorf("config: unsupported sandbox provider %q; available: [local docker remote aio]", c.Sandbox.Provider)
	}
	if c.Sandbox.Enabled && c.Sandbox.Provider == "remote" && strings.TrimSpace(c.Sandbox.RemoteURL) == "" {
		return errors.New("config: sandbox.remote_url is required for the remote provider")
	}
	if c.Sandbox.Enabled && c.Sandbox.Provider == "remote" && strings.TrimSpace(c.Sandbox.TenantID) == "" {
		return errors.New("config: sandbox.tenant_id is required for the remote provider")
	}
	if c.Sandbox.Enabled && c.Sandbox.Provider == "aio" && strings.TrimSpace(c.Sandbox.ProvisionerURL) == "" {
		return errors.New("config: sandbox.provisioner_url is required for the aio provider")
	}
	environment := strings.ToLower(strings.TrimSpace(c.Deployment.Environment))
	if environment != "development" && environment != "test" && environment != "production" {
		return fmt.Errorf("config: deployment.environment must be development, test, or production, got %q", c.Deployment.Environment)
	}
	if environment == "production" && (!c.Sandbox.Enabled || c.Sandbox.Provider != "remote") {
		return errors.New("config: production requires sandbox.enabled=true and sandbox.provider=remote")
	}
	groups := make(map[string]struct{}, len(c.ToolGroups))
	for i, group := range c.ToolGroups {
		name := strings.TrimSpace(group.Name)
		if name == "" {
			return fmt.Errorf("config: tool_groups[%d] requires name", i)
		}
		if _, exists := groups[name]; exists {
			return fmt.Errorf("config: duplicate tool group %q", name)
		}
		groups[name] = struct{}{}
	}
	toolNames := make(map[string]struct{}, len(c.Tools))
	for i, configuredTool := range c.Tools {
		name := strings.TrimSpace(configuredTool.Name)
		if name == "" {
			return fmt.Errorf("config: tools[%d] requires name", i)
		}
		if _, exists := toolNames[name]; exists {
			return fmt.Errorf("config: duplicate tool %q", name)
		}
		toolNames[name] = struct{}{}
		if configuredTool.Group != "" {
			if _, exists := groups[configuredTool.Group]; !exists {
				return fmt.Errorf("config: tool %q references unknown group %q", name, configuredTool.Group)
			}
		}
	}
	if c.Plugins.Enabled && len(c.Plugins.Directories) == 0 {
		return errors.New("config: plugins.directories is required when plugins are enabled")
	}
	for name, agent := range c.ACPAgents {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(agent.Command) == "" || strings.TrimSpace(agent.Description) == "" {
			return fmt.Errorf("config: ACP agent %q requires name, command and description", name)
		}
	}
	if c.Subagents.MaxConcurrent <= 0 || c.Subagents.MaxConcurrent > 4 {
		return errors.New("config: subagents.max_concurrent must be between 1 and 4")
	}
	if c.Loop.ToolCallBudget <= 0 {
		return errors.New("config: loop.tool_call_budget must be greater than zero")
	}
	if c.Loop.SubagentBudget <= 0 {
		return errors.New("config: loop.subagent_budget must be greater than zero")
	}
	if c.Loop.MaxRecursionDepth <= 0 {
		return errors.New("config: loop.max_recursion_depth must be greater than zero")
	}
	if c.Subagents.MaxTurns <= 0 || c.Subagents.MaxTurns > 100 {
		return errors.New("config: subagents.max_turns must be between 1 and 100")
	}
	if c.Subagents.TaskTTL <= 0 {
		return errors.New("config: subagents.task_ttl must be greater than zero")
	}
	if c.Subagents.Timeout <= 0 {
		return errors.New("config: subagents.timeout must be greater than zero")
	}
	knownSubagents := []string{"bash", "explore", "general-purpose", "plan", "verification"}
	for name, profile := range c.Subagents.Agents {
		if !slices.Contains(knownSubagents, name) {
			return fmt.Errorf("config: unknown subagent profile %q; available: %v", name, knownSubagents)
		}
		if profile.Timeout < 0 {
			return fmt.Errorf("config: subagents.agents.%s.timeout cannot be negative", name)
		}
	}
	if c.Swarm.MaxTeamSize < 2 {
		return errors.New("config: swarm.max_team_size must be at least 2")
	}
	if c.Swarm.MessagePollInterval <= 0 {
		return errors.New("config: swarm.message_poll_interval must be greater than zero")
	}
	if c.Swarm.TeammateTimeout <= 0 {
		return errors.New("config: swarm.teammate_timeout must be greater than zero")
	}
	if c.Swarm.PollLimit <= 0 {
		return errors.New("config: swarm.poll_limit must be greater than zero")
	}
	if c.Swarm.Enabled && strings.TrimSpace(c.Runtime.DatabaseURL) == "" {
		return errors.New("config: swarm.enabled requires runtime.database_url")
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

// TimeoutFor returns the trusted execution deadline for one built-in profile.
// A missing or zero-valued profile override inherits the global deadline.
func (c SubagentConfig) TimeoutFor(name string) time.Duration {
	if profile, ok := c.Agents[strings.TrimSpace(name)]; ok && profile.Timeout > 0 {
		return profile.Timeout
	}
	return c.Timeout
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
