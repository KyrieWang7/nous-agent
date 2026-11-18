package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// PluginState is a separate operator settings document. It cannot change
// commands or package paths, and does not race Gateway's MCP/skill document.
type PluginState struct {
	Enabled   *bool             `json:"enabled,omitempty"`
	Plugins   map[string]bool   `json:"plugins"`
	Installed map[string]string `json:"installed,omitempty"`
}

func PluginStatePath() string {
	if path := strings.TrimSpace(os.Getenv("NOUS_PLUGIN_STATE_PATH")); path != "" {
		return path
	}
	if path := strings.TrimSpace(os.Getenv("NOUS_EXTENSIONS_CONFIG_PATH")); path != "" {
		return filepath.Join(filepath.Dir(path), "plugins.json")
	}
	return filepath.Join(".nous", "plugins.json")
}
func ReadPluginState(path string) (PluginState, error) {
	state := PluginState{Plugins: map[string]bool{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&state); err != nil {
		return state, fmt.Errorf("config: decoding plugin settings: %w", err)
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return state, errors.New("config: plugin settings must contain exactly one JSON object")
	}
	if state.Plugins == nil {
		state.Plugins = map[string]bool{}
	}
	return state, nil
}
func (s PluginState) Apply(c *PluginsConfig) {
	if s.Enabled != nil {
		c.Enabled = *s.Enabled
	}
	if c.States == nil {
		c.States = map[string]bool{}
	}
	for name, enabled := range s.Plugins {
		c.States[name] = enabled
	}
	c.Installed = s.Installed
}

// PluginCatalogSegment is a package or version directory name, never a path.
func PluginCatalogSegment(value string) bool {
	if len(value) == 0 || len(value) > 128 || strings.HasPrefix(value, ".") {
		return false
	}
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
		case char == '-', char == '_', char == '.', char == '+':
		default:
			return false
		}
	}
	return true
}

func (c PluginsConfig) LoadDirectories() ([]string, error) {
	dirs := slices.Clone(c.Directories)
	if len(c.Installed) > 0 && c.CatalogPath == "" {
		return nil, errors.New("installed plugins require a configured plugins.catalog_path")
	}
	for name, version := range c.Installed {
		if !PluginCatalogSegment(name) || !PluginCatalogSegment(version) {
			return nil, errors.New("invalid installed plugin name or version")
		}
		path := filepath.Join(c.CatalogPath, name, version)
		if stat, err := os.Lstat(path); err != nil || !stat.IsDir() {
			return nil, fmt.Errorf("installed plugin %s version %s is unavailable", name, version)
		}
		dirs = append(dirs, path)
	}
	slices.Sort(dirs)
	return dirs, nil
}
