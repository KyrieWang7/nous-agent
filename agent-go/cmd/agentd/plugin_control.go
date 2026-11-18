package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/repository"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/config"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/plugin"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/plugincontrol"
)

func (a *reloadableAgent) Snapshot(ctx context.Context) (plugincontrol.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return plugincontrol.Snapshot{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return plugincontrol.Snapshot{}, plugincontrol.ErrUnavailable
	}
	reloadErr := a.refreshLocked()
	cfg, err := config.Load(a.configPath)
	plugins := a.current.plugins
	if err == nil {
		plugins = cfg.Plugins
	} else {
		reloadErr = errors.Join(reloadErr, err)
	}
	fingerprint, err := runtimeConfigFingerprint(a.configPath, a.extensionsPath, runtimeCatalogPaths(cfg), a.pluginStatePath)
	if err != nil {
		fingerprint = a.fingerprint
		reloadErr = errors.Join(reloadErr, err)
	}
	return a.pluginSnapshotLocked(plugins, fingerprint, reloadErr), nil
}
func (a *reloadableAgent) pluginSnapshotLocked(cfg config.PluginsConfig, fp [32]byte, loadErr error) plugincontrol.Snapshot {
	snapshot := plugincontrol.Snapshot{Revision: hex.EncodeToString(fp[:]), Enabled: cfg.Enabled, Plugins: []plugincontrol.Plugin{}}
	dirs, dirErr := cfg.LoadDirectories()
	packages, err := plugin.Inspect(dirs, cfg.States)
	catalog, catalogErr := discoverPackages(cfg.CatalogPath)
	snapshot.Catalog = catalog
	loadErr = errors.Join(loadErr, dirErr, catalogErr)
	if err != nil {
		loadErr = errors.Join(loadErr, err)
	}
	// Even a broken desired manifest must not hide the actual old runtime.
	known := map[string]bool{}
	for _, p := range packages {
		row := plugincontrol.Plugin{Name: p.Name, Installed: true, Version: cfg.Installed[p.Name], Description: p.Description, Tools: p.Tools, Enabled: p.Enabled, Loaded: slices.Contains(a.current.built.plugins, p.Name)}
		snapshot.Plugins = append(snapshot.Plugins, row)
		known[p.Name] = true
	}
	for _, name := range a.current.built.plugins {
		if !known[name] {
			snapshot.Plugins = append(snapshot.Plugins, plugincontrol.Plugin{Name: name, Loaded: true, Tools: []string{}})
			known[name] = true
		}
	}
	for generation := range a.retired {
		for _, name := range generation.built.plugins {
			if !known[name] {
				snapshot.Plugins = append(snapshot.Plugins, plugincontrol.Plugin{Name: name, Tools: []string{}})
				known[name] = true
			}
			for i := range snapshot.Plugins {
				if snapshot.Plugins[i].Name == name {
					snapshot.Plugins[i].RetainedRuns += generation.refs
					if generation.closeError != nil {
						snapshot.Plugins[i].CloseError = generation.closeError.Error()
					}
				}
			}
		}
	}
	if loadErr != nil {
		snapshot.LoadError = loadErr.Error()
	}
	slices.SortFunc(snapshot.Plugins, func(a, b plugincontrol.Plugin) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return snapshot
}

func (a *reloadableAgent) Apply(ctx context.Context, change plugincontrol.Change) (plugincontrol.Snapshot, error) {
	var snapshot plugincontrol.Snapshot
	err := repository.WithPluginSettingsLock(ctx, a.pluginStatePath, func() error {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.closed {
			return plugincontrol.ErrUnavailable
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		cfg, err := config.Load(a.configPath)
		if err != nil {
			return err
		}
		paths := runtimeCatalogPaths(cfg)
		original, err := runtimeConfigFingerprint(a.configPath, a.extensionsPath, paths, a.pluginStatePath)
		if err != nil {
			return err
		}
		if change.Revision == "" || change.Revision != hex.EncodeToString(original[:]) {
			return plugincontrol.ErrConflict
		}
		oldRaw, readErr := os.ReadFile(a.pluginStatePath)
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		state, err := config.ReadPluginState(a.pluginStatePath)
		if err != nil {
			return err
		}
		if !change.Reload {
			if change.PackageAction != "" {
				if err := applyPackageChange(cfg.Plugins, &state, change); err != nil {
					return err
				}
			} else if change.Name == "" {
				state.Enabled = &change.Enabled
			} else {
				dirs, err := cfg.Plugins.LoadDirectories()
				if err != nil {
					return err
				}
				packages, err := plugin.Inspect(dirs, cfg.Plugins.States)
				if err != nil {
					return err
				}
				found := false
				for _, p := range packages {
					if p.Name == change.Name {
						found = true
						break
					}
				}
				if !found {
					return plugincontrol.ErrNotFound
				}
				state.Plugins[change.Name] = change.Enabled
			}
			state.Apply(&cfg.Plugins)
		}
		if change.PackageAction != "" {
			dirs, err := cfg.Plugins.LoadDirectories()
			if err != nil {
				return err
			}
			if _, err := plugin.Inspect(dirs, cfg.Plugins.States); err != nil {
				return err
			}
		}
		if err = cfg.Validate([]string{"openai-compatible", "anthropic", "deepseek"}); err != nil {
			return err
		}
		raw, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return err
		}
		raw = append(raw, '\n')
		candidate := original
		if !change.Reload {
			candidate, err = runtimeFingerprint(a.configPath, a.extensionsPath, paths, []string{a.pluginStatePath}, map[string][]byte{a.pluginStatePath: raw})
			if err != nil {
				return err
			}
		}
		if err = a.validateImmutable(cfg); err != nil {
			return err
		}
		built, err := a.build(cfg)
		if err != nil {
			return fmt.Errorf("plugin change was not applied: %w", errors.Join(err, built.dispose()))
		}
		published := false
		defer func() {
			if !published {
				built.Close()
			}
		}()
		if err = ctx.Err(); err != nil {
			return err
		}
		now, err := runtimeConfigFingerprint(a.configPath, a.extensionsPath, paths, a.pluginStatePath)
		if err != nil {
			return err
		}
		if now != original {
			return plugincontrol.ErrConflict
		}
		if !change.Reload {
			if err = repository.WritePluginSettings(a.pluginStatePath, raw); err != nil {
				return err
			}
			loaded, checkErr := runtimeConfigFingerprint(a.configPath, a.extensionsPath, paths, a.pluginStatePath)
			if checkErr != nil || loaded != candidate {
				// Only undo our own document; a manual concurrent edit is never replaced.
				if current, e := os.ReadFile(a.pluginStatePath); e == nil && bytes.Equal(current, raw) {
					if os.IsNotExist(readErr) {
						checkErr = errors.Join(checkErr, os.Remove(a.pluginStatePath))
					} else {
						checkErr = errors.Join(checkErr, repository.WritePluginSettings(a.pluginStatePath, oldRaw))
					}
				}
				return errors.Join(plugincontrol.ErrConflict, checkErr)
			}
		}
		a.installLocked(built, cfg.Plugins, candidate, paths)
		published = true
		snapshot = a.pluginSnapshotLocked(cfg.Plugins, candidate, nil)
		return nil
	})
	return snapshot, err
}

var _ plugincontrol.Controller = (*reloadableAgent)(nil)
