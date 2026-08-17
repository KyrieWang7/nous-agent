package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func TestLoadAndExecuteCommandPlugin(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "demo")
	if err := os.MkdirAll(pluginRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"demo","tools":[{"name":"echo_json","description":"Echo input","command":"cat","inputSchema":{"type":"object"},"requiredSandboxMode":"danger-full-access"}]}`
	if err := os.WriteFile(filepath.Join(pluginRoot, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	contributions, err := Load([]string{root}, []string{"read_file"})
	if err != nil {
		t.Fatal(err)
	}
	if len(contributions) != 1 || contributions[0].RequiredSandboxMode != "danger-full-access" {
		t.Fatalf("contributions = %#v", contributions)
	}
	result, err := contributions[0].Definition.Handler(context.Background(), tool.Call{Args: []byte(`{"value":1}`)})
	if err != nil || result.IsError || result.Content != `{"value":1}` {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestLoadRejectsReservedToolConflict(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "demo")
	if err := os.MkdirAll(pluginRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"demo","tools":[{"name":"read_file","command":"cat"}]}`
	if err := os.WriteFile(filepath.Join(pluginRoot, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load([]string{root}, []string{"read_file"})
	if err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadRejectsRemovedManifestFields(t *testing.T) {
	tests := []struct {
		name  string
		field string
	}{
		{name: "input schema", field: `"input_schema":{"type":"object"}`},
		{name: "required permission", field: `"required_permission":"prompt"`},
		{name: "removed camel-case permission", field: `"requiredPermission":"prompt"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			pluginRoot := filepath.Join(root, "demo")
			if err := os.MkdirAll(pluginRoot, 0o750); err != nil {
				t.Fatal(err)
			}
			manifest := `{"name":"demo","tools":[{"name":"echo","command":"cat",` + tc.field + `}]}`
			if err := os.WriteFile(filepath.Join(pluginRoot, "plugin.json"), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load([]string{root}, nil)
			if err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("Load() error = %v, want unknown field rejection", err)
			}
		})
	}
}
