package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/config"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/plugin"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/plugincontrol"
)

// Packages are supplied by the operator. Clients select named versions from
// this catalog, and never provide paths, commands or network install sources.
func discoverPackages(path string) ([]plugincontrol.CatalogPlugin, error) {
	out := []plugincontrol.CatalogPlugin{}
	if path == "" {
		return out, nil
	}
	names, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if !name.IsDir() || !config.PluginCatalogSegment(name.Name()) {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(path, name.Name()))
		if err != nil {
			return nil, err
		}
		for _, version := range versions {
			if !version.IsDir() || !config.PluginCatalogSegment(version.Name()) {
				continue
			}
			packages, err := plugin.Inspect([]string{filepath.Join(path, name.Name(), version.Name())}, nil)
			if err != nil {
				return nil, err
			}
			if len(packages) != 1 || packages[0].Name != name.Name() {
				return nil, fmt.Errorf("catalog version %s/%s must contain exactly one package named %s", name.Name(), version.Name(), name.Name())
			}
			p := packages[0]
			out = append(out, plugincontrol.CatalogPlugin{Name: p.Name, Version: version.Name(), Description: p.Description, Tools: p.Tools})
		}
	}
	return out, nil
}

func applyPackageChange(cfg config.PluginsConfig, state *config.PluginState, change plugincontrol.Change) error {
	if !config.PluginCatalogSegment(change.Name) {
		return plugincontrol.ErrNotFound
	}
	if change.PackageAction == "uninstall" {
		if state.Installed[change.Name] == "" {
			return plugincontrol.ErrNotFound
		}
		delete(state.Installed, change.Name)
		delete(state.Plugins, change.Name)
		return nil
	}
	if !config.PluginCatalogSegment(change.Version) {
		return plugincontrol.ErrNotFound
	}
	catalog, err := discoverPackages(cfg.CatalogPath)
	if err != nil {
		return err
	}
	for _, candidate := range catalog {
		if candidate.Name != change.Name || candidate.Version != change.Version {
			continue
		}
		if state.Installed == nil {
			state.Installed = make(map[string]string)
		}
		if state.Installed[change.Name] == "" {
			state.Plugins[change.Name] = false
		}
		state.Installed[change.Name] = change.Version
		return nil
	}
	return plugincontrol.ErrNotFound
}
