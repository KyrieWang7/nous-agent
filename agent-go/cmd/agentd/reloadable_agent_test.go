package main

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/config"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/runmanager"
)

type reloadTestAgent struct {
	output  string
	started chan<- struct{}
	release <-chan struct{}
}

func (a reloadTestAgent) Run(context.Context, runmanager.AgentRequest) (runmanager.AgentResult, error) {
	if a.started != nil {
		a.started <- struct{}{}
	}
	if a.release != nil {
		<-a.release
	}
	return runmanager.AgentResult{Output: a.output}, nil
}

func TestReloadableAgentBuildsNextRunFromChangedConfig(t *testing.T) {
	isolateReloadEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeReloadConfig(t, path, "one")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var closes atomic.Int32
	initial := reloadBuiltAgent("one", &closes, nil, nil)
	reloader, err := newReloadableAgent(path, cfg, initial, func(cfg config.Config) (builtAgent, error) {
		return reloadBuiltAgent(cfg.Models[0].Name, &closes, nil, nil), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reloader.Close()

	first, err := reloader.Run(context.Background(), runmanager.AgentRequest{})
	if err != nil || first.Output != "one" {
		t.Fatalf("first run = %#v, err = %v", first, err)
	}
	writeReloadConfig(t, path, "two")
	second, err := reloader.Run(context.Background(), runmanager.AgentRequest{})
	if err != nil || second.Output != "two" {
		t.Fatalf("second run = %#v, err = %v", second, err)
	}
	if closes.Load() != 1 {
		t.Fatalf("retired generation closes = %d, want 1", closes.Load())
	}
	prepared, err := reloader.PrepareRun()
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Release()
	if len(prepared.AllowedTools) != 1 || prepared.AllowedTools[0] != "tool-two" {
		t.Fatalf("prepared tools = %v, want current generation", prepared.AllowedTools)
	}
	if got := prepared.Pricer.CostMicros("two", model.Usage{InputTokens: 1_000_000}); got != 3 {
		t.Fatalf("prepared generation cost = %d, want 3", got)
	}
}

func TestReloadableAgentDefersCloseUntilActiveRunEnds(t *testing.T) {
	isolateReloadEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeReloadConfig(t, path, "one")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var closes atomic.Int32
	reloader, err := newReloadableAgent(path, cfg, reloadBuiltAgent("one", &closes, started, release), func(cfg config.Config) (builtAgent, error) {
		return reloadBuiltAgent(cfg.Models[0].Name, &closes, nil, nil), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reloader.Close()

	done := make(chan error, 1)
	go func() {
		_, runErr := reloader.Run(context.Background(), runmanager.AgentRequest{})
		done <- runErr
	}()
	<-started
	writeReloadConfig(t, path, "two")
	if _, err := reloader.Run(context.Background(), runmanager.AgentRequest{}); err != nil {
		t.Fatal(err)
	}
	if closes.Load() != 0 {
		t.Fatalf("active generation was closed early %d time(s)", closes.Load())
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if closes.Load() != 1 {
		t.Fatalf("retired generation closes = %d, want 1", closes.Load())
	}
}

func TestReloadableAgentRejectsProcessLevelConfigChange(t *testing.T) {
	isolateReloadEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeReloadConfig(t, path, "one")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	reloader, err := newReloadableAgent(path, cfg, reloadBuiltAgent("one", nil, nil, nil), func(config.Config) (builtAgent, error) {
		t.Fatal("builder must not run for an immutable change")
		return builtAgent{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reloader.Close()

	writeReloadConfigWithAddress(t, path, "one", ":9999")
	if _, err := reloader.Run(context.Background(), runmanager.AgentRequest{}); err == nil {
		t.Fatal("server address change was accepted without restart")
	}
}

func TestReloadableAgentCanSwitchCatalogDirectories(t *testing.T) {
	isolateReloadEnvironment(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	firstCatalog := filepath.Join(dir, "skills-one")
	secondCatalog := filepath.Join(dir, "skills-two")
	for _, catalog := range []string{firstCatalog, secondCatalog} {
		if err := os.MkdirAll(filepath.Join(catalog, "demo"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(catalog, "demo", "SKILL.md"), []byte(catalog), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeReloadConfigWithSkills(t, path, "one", firstCatalog)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	reloader, err := newReloadableAgent(path, cfg, reloadBuiltAgent("one", nil, nil, nil), func(cfg config.Config) (builtAgent, error) {
		return reloadBuiltAgent(cfg.Models[0].Name, nil, nil, nil), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reloader.Close()

	writeReloadConfigWithSkills(t, path, "two", secondCatalog)
	result, err := reloader.Run(context.Background(), runmanager.AgentRequest{})
	if err != nil || result.Output != "two" {
		t.Fatalf("run after catalog switch = %#v, err = %v", result, err)
	}
}

func reloadBuiltAgent(output string, closes *atomic.Int32, started chan<- struct{}, release <-chan struct{}) builtAgent {
	var closeFuncs []func() error
	if closes != nil {
		closeFuncs = append(closeFuncs, func() error { closes.Add(1); return nil })
	}
	return builtAgent{agent: reloadTestAgent{output: output, started: started, release: release}, tools: []string{"tool-" + output}, pricer: runtime.NewPricer(map[string]runtime.Price{output: {InputPerMillion: int64(len(output))}}, runtime.Price{}), close: closeFuncs}
}

func writeReloadConfig(t *testing.T, path, modelName string) {
	t.Helper()
	writeReloadConfigWithAddress(t, path, modelName, ":7776")
}

func writeReloadConfigWithAddress(t *testing.T, path, modelName, address string) {
	t.Helper()
	data := "server:\n  address: \"" + address + "\"\nmodels:\n  - name: " + modelName + "\n    provider: openai-compatible\n    model: " + modelName + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeReloadConfigWithSkills(t *testing.T, path, modelName, skillsPath string) {
	t.Helper()
	data := "server:\n  address: \":7776\"\nmodels:\n  - name: " + modelName + "\n    provider: openai-compatible\n    model: " + modelName + "\nskills:\n  enabled: true\n  path: " + skillsPath + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func isolateReloadEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"NOUS_EXTENSIONS_CONFIG_PATH", "NOUS_AGENT_ADDR", "DATABASE_URL", "REDIS_URL", "NOUS_SKILLS_PATH", "TUYOO_BASE_URL", "TUYOO_API_KEY"} {
		t.Setenv(name, "")
	}
}
