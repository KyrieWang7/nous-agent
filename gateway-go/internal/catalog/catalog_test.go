package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCatalogModelsAndSkills(t *testing.T) {
	root := t.TempDir()
	harness := filepath.Join(root, "config.yaml")
	extensions := filepath.Join(root, "extensions.json")
	skillDir := filepath.Join(root, "skills", "public", "writer")
	mustWrite(t, harness, []byte("models:\n  - name: test-model\n    supports_thinking: true\n"))
	mustWrite(t, filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: writer\ndescription: Writes clearly\nlicense: MIT\n---\nBody\n"))

	catalog := New(harness, extensions, filepath.Join(root, "skills"))
	models, err := catalog.Models()
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].DisplayName != "test-model" || !models[0].SupportsThinking {
		t.Fatalf("unexpected models: %#v", models)
	}

	skills, err := catalog.Skills()
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Description != "Writes clearly" || !skills[0].Enabled {
		t.Fatalf("unexpected skills: %#v", skills)
	}
	updated, err := catalog.SetSkillEnabled("writer", false)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled {
		t.Fatal("skill remained enabled")
	}
}

func TestSaveMCPPreservesSkillState(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "extensions.json")
	mustWrite(t, path, []byte(`{"mcpServers":{},"skills":{"writer":{"enabled":false}}}`))
	catalog := New(filepath.Join(root, "config.yaml"), path, filepath.Join(root, "skills"))
	ext, err := catalog.SaveMCP(map[string]MCPServer{"docs": {Enabled: true, Type: "http", URL: "https://example.test/mcp", Args: []string{}, Env: map[string]string{}, Headers: map[string]string{}}})
	if err != nil {
		t.Fatal(err)
	}
	if !ext.MCPServers["docs"].Enabled || ext.Skills["writer"].Enabled {
		t.Fatalf("configuration was not preserved: %#v", ext)
	}
}

func mustWrite(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o640); err != nil {
		t.Fatal(err)
	}
}
