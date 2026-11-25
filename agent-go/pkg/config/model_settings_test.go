package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSavedModelSettings(t *testing.T) {
	isolateTuyooEnv(t)
	root := t.TempDir()
	t.Setenv("NOUS_EXTENSIONS_CONFIG_PATH", filepath.Join(root, "extensions.json"))
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("models:\n  - name: original\n    provider: openai-compatible\n    model: original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "models.json"), []byte(`{"default_model":"saved","models":[{"name":"saved","provider":"openai-compatible","model":"actual","api_key":"secret-$literal-\"quote\"","base_url":"https://example.test/v1","max_tokens":8192}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel != "saved" || len(cfg.Models) != 1 || cfg.Models[0].Model != "actual" || cfg.Models[0].APIKey != `secret-$literal-"quote"` {
		t.Fatalf("saved model settings were not loaded correctly")
	}
}
