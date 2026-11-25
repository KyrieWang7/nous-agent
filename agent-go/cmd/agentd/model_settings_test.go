package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/config"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/runmanager"
)

func TestModelSettingsReloadBetweenRuns(t *testing.T) {
	isolateReloadEnvironment(t)
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	t.Setenv("NOUS_EXTENSIONS_CONFIG_PATH", filepath.Join(root, "extensions.json"))
	writeReloadConfig(t, path, "original")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	reloader, err := newReloadableAgent(path, cfg, reloadBuiltAgent("original", nil, nil, nil), func(cfg config.Config) (builtAgent, error) {
		return reloadBuiltAgent(cfg.DefaultModel, nil, nil, nil), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reloader.Close()
	if err := os.WriteFile(filepath.Join(root, "models.json"), []byte(`{"default_model":"saved","models":[{"name":"saved","provider":"openai-compatible","model":"saved"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := reloader.Run(context.Background(), runmanager.AgentRequest{})
	if err != nil || result.Output != "saved" {
		t.Fatalf("next run = %q, err=%v; saved settings were not reloaded", result.Output, err)
	}
}
