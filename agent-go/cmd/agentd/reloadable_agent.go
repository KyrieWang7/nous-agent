package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/config"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/runmanager"
	"github.com/KyrieWang7/nous-agent/platform-go/modelconfig"
)

type agentBuilder func(config.Config) (builtAgent, error)

type agentGeneration struct {
	built      builtAgent
	refs       int
	retired    bool
	plugins    config.PluginsConfig
	closeError error
}

// reloadableAgent swaps complete production assemblies between runs. Keeping
// each generation intact prevents a model router from observing a tool catalog
// or prompt assembled from a different configuration revision.
type reloadableAgent struct {
	mu              sync.Mutex
	pluginStatePath string
	retired         map[*agentGeneration]struct{}
	configPath      string
	extensionsPath  string
	catalogPaths    []string
	fingerprint     [sha256.Size]byte
	immutable       immutableRuntimeConfig
	current         *agentGeneration
	build           agentBuilder
	closed          bool
}

type immutableRuntimeConfig struct {
	http        config.HTTPConfig
	address     string
	databaseURL string
	redisURL    string
}

func newReloadableAgent(configPath string, cfg config.Config, initial builtAgent, build agentBuilder) (*reloadableAgent, error) {
	if initial.agent == nil {
		return nil, errors.New("agentd: reloadable agent requires an initial agent")
	}
	if build == nil {
		return nil, errors.New("agentd: reloadable agent requires a builder")
	}
	extensionsPath := strings.TrimSpace(os.Getenv("NOUS_EXTENSIONS_CONFIG_PATH"))
	catalogPaths := runtimeCatalogPaths(cfg)
	fingerprint, err := runtimeConfigFingerprint(configPath, extensionsPath, catalogPaths, config.PluginStatePath())
	if err != nil {
		return nil, err
	}
	return &reloadableAgent{
		pluginStatePath: config.PluginStatePath(), retired: make(map[*agentGeneration]struct{}),
		configPath:     configPath,
		extensionsPath: extensionsPath,
		catalogPaths:   catalogPaths,
		fingerprint:    fingerprint,
		immutable: immutableRuntimeConfig{
			address: cfg.Server.Address, databaseURL: cfg.Runtime.DatabaseURL, redisURL: cfg.Runtime.RedisURL, http: cfg.HTTP,
		},
		current: &agentGeneration{built: initial, plugins: cfg.Plugins},
		build:   build,
	}, nil
}

func (a *reloadableAgent) Run(ctx context.Context, req runmanager.AgentRequest) (runmanager.AgentResult, error) {
	prepared, err := a.PrepareRun()
	if err != nil {
		return runmanager.AgentResult{}, err
	}
	defer prepared.Release()
	if run, ok := runtime.RunContextFrom(ctx); ok {
		run.AllowedTools = append([]string(nil), prepared.AllowedTools...)
		run.Journal = runtime.NewJournal(prepared.Pricer)
		ctx = runtime.WithRunContext(ctx, run)
	}
	return prepared.Agent.Run(ctx, req)
}

func (a *reloadableAgent) PrepareRun() (runmanager.PreparedRun, error) {
	generation, err := a.acquire()
	if err != nil {
		return runmanager.PreparedRun{}, err
	}
	var releaseOnce sync.Once
	return runmanager.PreparedRun{
		Agent: generation.built.agent, AllowedTools: append([]string(nil), generation.built.tools...),
		Pricer: generation.built.pricer, BudgetLimits: generation.built.budget,
		AllowedCapabilities: append([]string(nil), generation.built.capabilities...),
		MaxRecursionDepth:   generation.built.maxDepth,
		Release:             func() { releaseOnce.Do(func() { a.release(generation) }) },
	}, nil
}

func (a *reloadableAgent) acquire() (*agentGeneration, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, errors.New("agentd: reloadable agent is closed")
	}
	if err := a.refreshLocked(); err != nil {
		return nil, err
	}
	a.current.refs++
	return a.current, nil
}

func (a *reloadableAgent) refreshLocked() error {
	fingerprint, err := runtimeConfigFingerprint(a.configPath, a.extensionsPath, a.catalogPaths, a.pluginStatePath)
	if err != nil {
		return err
	}
	if fingerprint == a.fingerprint {
		return nil
	}

	cfg, err := config.Load(a.configPath)
	if err != nil {
		return fmt.Errorf("agentd: reloading configuration: %w", err)
	}
	if err := a.validateImmutable(cfg); err != nil {
		return err
	}
	loadedCatalogPaths := runtimeCatalogPaths(cfg)
	candidateFingerprint, err := runtimeConfigFingerprint(a.configPath, a.extensionsPath, loadedCatalogPaths, a.pluginStatePath)
	if err != nil {
		return err
	}
	built, err := a.build(cfg)
	if err != nil {
		return fmt.Errorf("agentd: rebuilding runtime: %w", errors.Join(err, built.dispose()))
	}
	// Recompute after the build so a concurrent atomic Gateway write cannot be
	// recorded as loaded unless that exact revision was assembled.
	loadedFingerprint, err := runtimeConfigFingerprint(a.configPath, a.extensionsPath, loadedCatalogPaths, a.pluginStatePath)
	if err != nil {
		built.Close()
		return err
	}
	if loadedFingerprint != candidateFingerprint {
		built.Close()
		return errors.New("agentd: configuration changed during reload; retry the run")
	}
	a.installLocked(built, cfg.Plugins, loadedFingerprint, loadedCatalogPaths)
	return nil
}

func (a *reloadableAgent) installLocked(built builtAgent, plugins config.PluginsConfig, fp [sha256.Size]byte, paths []string) {
	old := a.current
	old.retired = true
	a.retired[old] = struct{}{}
	a.current = &agentGeneration{built: built, plugins: plugins}
	a.fingerprint = fp
	a.catalogPaths = paths
	if old.refs == 0 {
		a.disposeGenerationLocked(old)
	}
}
func (a *reloadableAgent) disposeGenerationLocked(g *agentGeneration) {
	g.closeError = g.built.dispose()
	if g.closeError == nil {
		delete(a.retired, g)
	} else {
		a.retired[g] = struct{}{}
	}
}

func (a *reloadableAgent) validateImmutable(cfg config.Config) error {
	if cfg.HTTP != a.immutable.http {
		return errors.New("agentd: http.rate_limit changed; restart is required")
	}
	if cfg.Server.Address != a.immutable.address {
		return errors.New("agentd: server.address changed; restart is required")
	}
	if cfg.Runtime.DatabaseURL != a.immutable.databaseURL {
		return errors.New("agentd: runtime.database_url changed; restart is required")
	}
	if cfg.Runtime.RedisURL != a.immutable.redisURL {
		return errors.New("agentd: runtime.redis_url changed; restart is required")
	}
	return nil
}

func (a *reloadableAgent) release(generation *agentGeneration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	generation.refs--
	if generation.refs == 0 && generation.retired {
		a.disposeGenerationLocked(generation)
	}
}

func (a *reloadableAgent) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.closed = true
	a.current.retired = true
	if a.current.refs == 0 {
		a.disposeGenerationLocked(a.current)
	}
}

func runtimeCatalogPaths(cfg config.Config) []string {
	paths := make([]string, 0, len(cfg.Plugins.Directories)+1)
	if strings.TrimSpace(cfg.Skills.Path) != "" {
		paths = append(paths, cfg.Skills.Path)
	}
	paths = append(paths, cfg.Plugins.Directories...)
	if cfg.Plugins.CatalogPath != "" {
		paths = append(paths, cfg.Plugins.CatalogPath)
	}
	for i, path := range paths {
		if path == "~" || strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				paths[i] = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
			}
		}
	}
	return paths
}

func runtimeConfigFingerprint(configPath, extensionsPath string, catalogPaths []string, extraPaths ...string) ([sha256.Size]byte, error) {
	return runtimeFingerprint(configPath, extensionsPath, catalogPaths, extraPaths, nil)
}
func runtimeFingerprint(configPath, extensionsPath string, catalogPaths, extraPaths []string, overrides map[string][]byte) ([sha256.Size]byte, error) {
	extraPaths = append(append([]string(nil), extraPaths...), modelconfig.Path(extensionsPath))
	hash := sha256.New()
	for _, path := range append([]string{configPath, extensionsPath}, extraPaths...) {
		if strings.TrimSpace(path) == "" {
			continue
		}
		raw, err := os.ReadFile(path)
		if replacement, ok := overrides[path]; ok {
			raw = replacement
			err = nil
		}
		if errors.Is(err, os.ErrNotExist) && (path == extensionsPath || slices.Contains(extraPaths, path)) {
			_, _ = hash.Write([]byte(path + "\x00missing\x00"))
			continue
		}
		if err != nil {
			return [sha256.Size]byte{}, fmt.Errorf("agentd: fingerprinting %s: %w", path, err)
		}
		_, _ = hash.Write([]byte(path + "\x00"))
		_, _ = hash.Write(raw)
	}
	for _, catalogPath := range catalogPaths {
		if strings.TrimSpace(catalogPath) == "" {
			continue
		}
		err := filepath.WalkDir(catalogPath, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return fs.SkipDir
				}
				return err
			}
			if entry.IsDir() || (entry.Name() != "SKILL.md" && entry.Name() != "plugin.json") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			_, _ = hash.Write([]byte(path + "\x00"))
			_, _ = hash.Write(raw)
			_, _ = hash.Write([]byte("\x00"))
			return nil
		})
		if err != nil {
			return [sha256.Size]byte{}, fmt.Errorf("agentd: fingerprinting catalog %s: %w", catalogPath, err)
		}
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}
