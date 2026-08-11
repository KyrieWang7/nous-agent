package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/hooks"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/permission"
)

func TestLoadDefaultsEnvAndValidation(t *testing.T) {
	isolateTuyooEnv(t)
	t.Setenv("TEST_MODEL_KEY", "secret")
	t.Setenv("NOUS_AGENT_ADDR", ":9000")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := `default_model: primary
models:
  - name: primary
    provider: openai-compatible
    model: test-model
    api_key: $TEST_MODEL_KEY
permissions:
  mode: read_only
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Address != ":9000" {
		t.Fatalf("address=%q", cfg.Server.Address)
	}
	m, _ := cfg.SelectedModel()
	if m.APIKey != "secret" {
		t.Fatalf("api key not expanded")
	}
	if cfg.Permissions.Mode != permission.ModeReadOnly {
		t.Fatalf("mode=%q", cfg.Permissions.Mode)
	}
}

func TestDefaultsUseHarnessPort(t *testing.T) {
	cfg := Defaults()
	if got := cfg.Server.Address; got != ":7776" {
		t.Fatalf("address=%q, want :7776", got)
	}
	if cfg.Subagents.Timeout != 15*time.Minute || cfg.Swarm.TeammateTimeout != 15*time.Minute {
		t.Fatalf("timeouts = subagent:%s swarm:%s", cfg.Subagents.Timeout, cfg.Swarm.TeammateTimeout)
	}
	if cfg.Subagents.MaxConcurrent != 3 || cfg.Subagents.MaxTurns != 50 {
		t.Fatalf("subagent limits = %#v", cfg.Subagents)
	}
	if cfg.Swarm.MaxTeamSize != 5 || cfg.Swarm.MessagePollInterval != 2*time.Second {
		t.Fatalf("swarm defaults = %#v", cfg.Swarm)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	isolateTuyooEnv(t)
	path := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(path, []byte("mystery: true\n"), 0o600)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field mystery not found") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadModelRuntimeOptions(t *testing.T) {
	isolateTuyooEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := `default_model: primary
models:
  - name: primary
    provider: openai-compatible
    model: deepseek
    temperature: 0
    extra_body:
      response_format:
        type: json_object
    pricing:
      input_per_million_micros: 1000000
      output_per_million_micros: 2000000
      cached_input_per_million_micros: 100000
    when_thinking_enabled:
      extra_body:
        thinking:
          type: enabled
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := cfg.SelectedModel()
	if err != nil {
		t.Fatal(err)
	}
	if m.Temperature == nil || *m.Temperature != 0 {
		t.Fatalf("temperature = %v, want an explicit zero", m.Temperature)
	}
	format, ok := m.ExtraBody["response_format"].(map[string]any)
	if !ok || format["type"] != "json_object" {
		t.Fatalf("extra_body = %#v", m.ExtraBody)
	}
	if m.WhenThinkingEnabled == nil {
		t.Fatal("when_thinking_enabled was not decoded")
	}
	thinking, ok := m.WhenThinkingEnabled.ExtraBody["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("thinking extra_body = %#v", m.WhenThinkingEnabled.ExtraBody)
	}
	if m.Pricing.InputPerMillionMicros != 1_000_000 || m.Pricing.OutputPerMillionMicros != 2_000_000 || m.Pricing.CachedInputPerMillionMicros != 100_000 {
		t.Fatalf("pricing = %#v", m.Pricing)
	}
}

func TestLoadSubagentProfileTimeouts(t *testing.T) {
	isolateTuyooEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := `models:
  - name: primary
    provider: openai-compatible
    model: test-model
subagents:
  timeout: 15m
  agents:
    explore:
      timeout: 5m
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Subagents.TimeoutFor("explore"); got != 5*time.Minute {
		t.Fatalf("explore timeout = %s", got)
	}
	if got := cfg.Subagents.TimeoutFor("bash"); got != 15*time.Minute {
		t.Fatalf("bash timeout = %s", got)
	}
}

func TestValidateRejectsInvalidRuntimeLimits(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "subagent concurrency", mutate: func(c *Config) { c.Subagents.MaxConcurrent = 0 }, want: "subagents.max_concurrent"},
		{name: "subagent concurrency upper bound", mutate: func(c *Config) { c.Subagents.MaxConcurrent = 5 }, want: "between 1 and 4"},
		{name: "subagent turns", mutate: func(c *Config) { c.Subagents.MaxTurns = 0 }, want: "subagents.max_turns"},
		{name: "subagent turns upper bound", mutate: func(c *Config) { c.Subagents.MaxTurns = 101 }, want: "between 1 and 100"},
		{name: "subagent timeout", mutate: func(c *Config) { c.Subagents.Timeout = 0 }, want: "subagents.timeout"},
		{name: "unknown subagent profile", mutate: func(c *Config) { c.Subagents.Agents = map[string]SubagentProfileConfig{"typo": {Timeout: time.Minute}} }, want: "unknown subagent profile"},
		{name: "negative profile timeout", mutate: func(c *Config) {
			c.Subagents.Agents = map[string]SubagentProfileConfig{"explore": {Timeout: -time.Second}}
		}, want: "cannot be negative"},
		{name: "team size", mutate: func(c *Config) { c.Swarm.MaxTeamSize = 1 }, want: "swarm.max_team_size"},
		{name: "poll interval", mutate: func(c *Config) { c.Swarm.MessagePollInterval = 0 }, want: "swarm.message_poll_interval"},
		{name: "teammate timeout", mutate: func(c *Config) { c.Swarm.TeammateTimeout = 0 }, want: "swarm.teammate_timeout"},
		{name: "swarm database", mutate: func(c *Config) { c.Swarm.Enabled = true }, want: "swarm.enabled requires runtime.database_url"},
		{name: "negative pricing", mutate: func(c *Config) { c.Models[0].Pricing.InputPerMillionMicros = -1 }, want: "pricing cannot be negative"},
		{name: "negative token budget", mutate: func(c *Config) { c.Loop.TokenBudget = -1 }, want: "token_budget"},
		{name: "negative cost budget", mutate: func(c *Config) { c.Loop.CostBudgetMicros = -1 }, want: "cost_budget_micros"},
		{name: "remote sandbox url", mutate: func(c *Config) { c.Sandbox.Enabled = true; c.Sandbox.Provider = "remote"; c.Sandbox.RemoteURL = "" }, want: "sandbox.remote_url"},
		{name: "plugin directories", mutate: func(c *Config) { c.Plugins.Enabled = true; c.Plugins.Directories = nil }, want: "plugins.directories"},
		{name: "ACP command", mutate: func(c *Config) { c.ACPAgents = map[string]ACPAgentConfig{"codex": {Description: "coding"}} }, want: "command and description"},
		{name: "ACP description", mutate: func(c *Config) { c.ACPAgents = map[string]ACPAgentConfig{"codex": {Command: "codex"}} }, want: "command and description"},
		{name: "duplicate tool group", mutate: func(c *Config) { c.ToolGroups = []ToolGroupConfig{{Name: "web"}, {Name: "web"}} }, want: "duplicate tool group"},
		{name: "duplicate tool", mutate: func(c *Config) { c.Tools = []ToolConfig{{Name: "web_search"}, {Name: "web_search"}} }, want: "duplicate tool"},
		{name: "unknown tool group", mutate: func(c *Config) { c.Tools = []ToolConfig{{Name: "web_search", Group: "web"}} }, want: "unknown group"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Models = []ModelConfig{{Name: "first", Provider: "openai-compatible", Model: "m"}}
			tc.mutate(&cfg)
			err := cfg.Validate([]string{"openai-compatible"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadPythonCompatibleToolsPluginsAndACP(t *testing.T) {
	isolateTuyooEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := `models:
  - name: primary
    provider: openai-compatible
    model: test-model
tool_groups:
  - name: web
tools:
  - name: web_search
    group: web
    use: src.community.tavily.tools:web_search_tool
    max_results: 7
    timeout: 12s
plugins:
  enabled: true
  directories: [./plugins]
acp_agents:
  codex:
    command: codex-acp
    args: [--stdio]
    description: Coding agent
    auto_approve_permissions: true
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tools) != 1 || cfg.Tools[0].Use == "" || cfg.Tools[0].MaxResults != 7 || cfg.Tools[0].Timeout != 12*time.Second {
		t.Fatalf("tools = %#v", cfg.Tools)
	}
	if !cfg.Plugins.Enabled || len(cfg.Plugins.Directories) != 1 {
		t.Fatalf("plugins = %#v", cfg.Plugins)
	}
	if got := cfg.ACPAgents["codex"]; got.Command != "codex-acp" || got.Description != "Coding agent" || !got.AutoApprovePermissions {
		t.Fatalf("ACP agent = %#v", got)
	}
}

func TestLoadRejectsUnknownThinkingOption(t *testing.T) {
	isolateTuyooEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := `models:
  - name: primary
    provider: openai-compatible
    model: deepseek
    when_thinking_enabled:
      unexpected: true
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateRejectsUnknownProvider(t *testing.T) {
	cfg := Defaults()
	cfg.Models = []ModelConfig{{Name: "x", Provider: "missing", Model: "m"}}
	if err := cfg.Validate([]string{"openai-compatible"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidatePersistsDefaultModel(t *testing.T) {
	cfg := Defaults()
	cfg.Models = []ModelConfig{{Name: "first", Provider: "openai-compatible", Model: "m"}}
	if err := cfg.Validate([]string{"openai-compatible"}); err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel != "first" {
		t.Fatalf("default model = %q", cfg.DefaultModel)
	}
}

func TestValidateMCPTransportRequirements(t *testing.T) {
	cfg := Defaults()
	cfg.Models = []ModelConfig{{Name: "first", Provider: "openai-compatible", Model: "m"}}
	cfg.Extensions.MCPServers = map[string]MCPServerConfig{"files": {Enabled: true, Type: "stdio"}}
	if err := cfg.Validate([]string{"openai-compatible"}); err == nil || !strings.Contains(err.Error(), "requires command") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateRejectsInvalidHookBeforeStartup(t *testing.T) {
	cfg := Defaults()
	cfg.Models = []ModelConfig{{Name: "first", Provider: "openai-compatible", Model: "m"}}
	cfg.Hooks = []HookConfig{{Name: "protect", Enabled: true, Events: []hooks.Event{"unknown"}, Command: "check"}}
	if err := cfg.Validate([]string{"openai-compatible"}); err == nil || !strings.Contains(err.Error(), "unknown event") {
		t.Fatalf("error = %v", err)
	}
	cfg.Hooks = []HookConfig{{Name: "protect", Enabled: true, Events: []hooks.Event{hooks.EventPreToolUse}, Command: "check", Matcher: "["}}
	if err := cfg.Validate([]string{"openai-compatible"}); err == nil || !strings.Contains(err.Error(), "matcher") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadAppliesGatewayExtensions(t *testing.T) {
	isolateTuyooEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := "models:\n  - name: test\n    provider: openai-compatible\n    model: test\nskills:\n  enabled: true\n  path: ./skills\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	extensions := filepath.Join(dir, "extensions.json")
	data = `{"mcpServers":{"docs":{"enabled":true,"type":"http","url":"https://example.test/mcp","headers":{"Authorization":"Bearer token"}}},"skills":{"disabled-skill":{"enabled":false},"enabled-skill":{"enabled":true}}}`
	if err := os.WriteFile(extensions, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOUS_EXTENSIONS_CONFIG_PATH", extensions)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Extensions.MCPServers["docs"].Headers["Authorization"] != "Bearer token" {
		t.Fatalf("MCP headers not loaded: %#v", cfg.Extensions.MCPServers)
	}
	if !slices.Equal(cfg.Skills.Disabled, []string{"disabled-skill"}) {
		t.Fatalf("disabled skills = %#v", cfg.Skills.Disabled)
	}
}

func TestLoadRoutesOpenAICompatibleModelsThroughTuyoo(t *testing.T) {
	t.Setenv("TUYOO_BASE_URL", "https://gateway.example.test/v1")
	t.Setenv("TUYOO_API_KEY", "gateway-test-key")
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := `models:
  - name: openai-model
    provider: openai-compatible
    model: model-a
    base_url: https://original.example.test/v1
    api_key: original-key
  - name: anthropic-model
    provider: anthropic
    model: model-b
    base_url: https://anthropic.example.test
    api_key: anthropic-key
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Models[0].BaseURL != "https://gateway.example.test/v1" || cfg.Models[0].APIKey != "gateway-test-key" {
		t.Fatalf("OpenAI-compatible gateway override = %#v", cfg.Models[0])
	}
	if cfg.Models[1].BaseURL != "https://anthropic.example.test" || cfg.Models[1].APIKey != "anthropic-key" {
		t.Fatalf("Anthropic model was unexpectedly overridden: %#v", cfg.Models[1])
	}
}

func TestLoadRejectsPartialTuyooConfiguration(t *testing.T) {
	t.Setenv("TUYOO_BASE_URL", "https://gateway.example.test/v1")
	t.Setenv("TUYOO_API_KEY", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := "models:\n  - name: test\n    provider: openai-compatible\n    model: test\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "must be set together") {
		t.Fatalf("Load() error = %v, want paired gateway configuration failure", err)
	}
}

func isolateTuyooEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TUYOO_BASE_URL", "")
	t.Setenv("TUYOO_API_KEY", "")
}
