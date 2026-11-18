// Package plugincontrol defines the application's plugin management port.
package plugincontrol

import (
	"context"
	"errors"
)

var (
	ErrUnavailable = errors.New("plugin management is unavailable")
	ErrConflict    = errors.New("runtime configuration changed; refresh before retrying")
	ErrNotFound    = errors.New("installed plugin not found")
)

type Plugin struct {
	Installed    bool     `json:"installed"`
	Version      string   `json:"version,omitempty"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Tools        []string `json:"tools"`
	Enabled      bool     `json:"enabled"`
	Loaded       bool     `json:"loaded"`
	RetainedRuns int      `json:"retained_runs"`
	CloseError   string   `json:"close_error,omitempty"`
}
type Snapshot struct {
	Catalog   []CatalogPlugin `json:"catalog"`
	Revision  string          `json:"revision"`
	Enabled   bool            `json:"enabled"`
	Plugins   []Plugin        `json:"plugins"`
	LoadError string          `json:"load_error,omitempty"`
}

type CatalogPlugin struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Version     string   `json:"version"`
	Tools       []string `json:"tools"`
}
type Change struct {
	PackageAction string
	Version       string
	Name          string
	Enabled       bool
	Revision      string
	Reload        bool
}
type Controller interface {
	Snapshot(context.Context) (Snapshot, error)
	Apply(context.Context, Change) (Snapshot, error)
}
