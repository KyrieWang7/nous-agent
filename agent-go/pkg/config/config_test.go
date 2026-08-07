package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/hooks"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/permission"
)

func TestLoadDefaultsEnvAndValidation(t *testing.T) {
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
	if got := Defaults().Server.Address; got != ":7776" {
		t.Fatalf("address=%q, want :7776", got)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(path, []byte("mystery: true\n"), 0o600)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field mystery not found") {
		t.Fatalf("error=%v", err)
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
