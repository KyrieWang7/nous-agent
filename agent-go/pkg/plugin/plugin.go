// Package plugin loads command-backed tools from DeerFlow-compatible
// plugin.json manifests.
package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

const (
	defaultTimeout = 120 * time.Second
	maxOutputBytes = 4 << 20
)

type Contribution struct {
	Definition         tool.Definition
	RequiredPermission string
}

type manifest struct {
	Name        string `json:"name"`
	Enabled     *bool  `json:"enabled,omitempty"`
	Description string `json:"description,omitempty"`
	Tools       []struct {
		Name               string          `json:"name"`
		Description        string          `json:"description,omitempty"`
		Command            string          `json:"command"`
		InputSchema        json.RawMessage `json:"inputSchema,omitempty"`
		InputSchemaLegacy  json.RawMessage `json:"input_schema,omitempty"`
		RequiredPermission string          `json:"requiredPermission,omitempty"`
		PermissionLegacy   string          `json:"required_permission,omitempty"`
	} `json:"tools"`
}

func Load(directories []string, reserved []string) ([]Contribution, error) {
	reservedSet := make(map[string]struct{}, len(reserved))
	for _, name := range reserved {
		reservedSet[name] = struct{}{}
	}
	seen := make(map[string]string)
	var contributions []Contribution
	for _, configuredDir := range directories {
		base, err := expandHome(configuredDir)
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(base)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("plugin: reading %s: %w", base, err)
		}
		slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(base, entry.Name(), "plugin.json")
			raw, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			var item manifest
			if err := json.Unmarshal(raw, &item); err != nil {
				return nil, fmt.Errorf("plugin: decoding %s: %w", path, err)
			}
			if strings.TrimSpace(item.Name) == "" {
				return nil, fmt.Errorf("plugin: %s requires name", path)
			}
			if item.Enabled != nil && !*item.Enabled {
				continue
			}
			for _, declared := range item.Tools {
				if strings.TrimSpace(declared.Name) == "" || strings.TrimSpace(declared.Command) == "" {
					return nil, fmt.Errorf("plugin: %s tool requires name and command", item.Name)
				}
				if _, exists := reservedSet[declared.Name]; exists {
					return nil, fmt.Errorf("plugin: tool %q conflicts with a built-in tool", declared.Name)
				}
				if owner, exists := seen[declared.Name]; exists {
					return nil, fmt.Errorf("plugin: tool %q conflicts with plugin %q", declared.Name, owner)
				}
				seen[declared.Name] = item.Name
				schema := declared.InputSchema
				if len(schema) == 0 {
					schema = declared.InputSchemaLegacy
				}
				if len(schema) == 0 {
					schema = json.RawMessage(`{"type":"object","additionalProperties":true}`)
				}
				permission := declared.RequiredPermission
				if permission == "" {
					permission = declared.PermissionLegacy
				}
				if permission == "" {
					permission = "danger_full_access"
				}
				contributions = append(contributions, Contribution{
					Definition:         commandTool(item.Name, filepath.Dir(path), declared.Name, declared.Description, declared.Command, schema, permission),
					RequiredPermission: permission,
				})
			}
		}
	}
	return contributions, nil
}

func commandTool(pluginName, root, name, description, command string, schema json.RawMessage, requiredPermission string) tool.Definition {
	if description == "" {
		description = "Plugin tool: " + name
	}
	return tool.Definition{
		Name: name, Group: "plugin", Description: description, Parameters: schema,
		Metadata: tool.Metadata{RequiredPermission: requiredPermission},
		Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
			runCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
			defer cancel()
			cmd := exec.CommandContext(runCtx, "/bin/sh", "-c", command)
			cmd.Dir = root
			cmd.Env = append(os.Environ(),
				"DEERFLOW_PLUGIN_NAME="+pluginName,
				"DEERFLOW_PLUGIN_ROOT="+root,
				"DEERFLOW_TOOL_NAME="+name,
			)
			cmd.Stdin = bytes.NewReader(call.Args)
			stdout := &limitedBuffer{limit: maxOutputBytes}
			stderr := &limitedBuffer{limit: 8 << 10}
			cmd.Stdout = stdout
			cmd.Stderr = stderr
			err := cmd.Run()
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				return &tool.Result{Content: fmt.Sprintf("Error: Plugin tool %q timed out after %s", name, defaultTimeout), IsError: true}, nil
			}
			if err != nil {
				return &tool.Result{Content: fmt.Sprintf("Error: Plugin tool %q failed: %v: %s", name, err, strings.TrimSpace(stderr.String())), IsError: true}, nil
			}
			if stdout.truncated {
				return &tool.Result{Content: fmt.Sprintf("Error: Plugin tool %q output exceeds %d bytes", name, maxOutputBytes), IsError: true}, nil
			}
			return &tool.Result{Content: strings.TrimSpace(stdout.String())}, nil
		},
	}
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int64
	written   int64
	truncated bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := b.limit - b.written
	if remaining <= 0 {
		b.truncated = true
		return original, nil
	}
	if int64(len(data)) > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	n, err := b.buffer.Write(data)
	b.written += int64(n)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, err
	}
	return original, nil
}

func (b *limitedBuffer) String() string { return b.buffer.String() }

func expandHome(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}
