package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type Package struct {
	Name        string
	Description string
	Enabled     bool
	Tools       []string
}

// Inspect reads installed manifests, including disabled packages, without
// executing commands or returning their command lines to the HTTP boundary.
func Inspect(directories []string, states map[string]bool) ([]Package, error) {
	out := []Package{}
	seen := map[string]bool{}
	for _, directory := range directories {
		root, err := expandHome(directory)
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(root, entry.Name(), "plugin.json")
			raw, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			var item manifest
			if err = decodeManifest(raw, &item); err != nil {
				return nil, fmt.Errorf("plugin: decoding %s: %w", path, err)
			}
			if strings.TrimSpace(item.Name) == "" {
				return nil, fmt.Errorf("plugin: %s requires name", path)
			}
			if seen[item.Name] {
				return nil, fmt.Errorf("plugin: duplicate installed name %q", item.Name)
			}
			seen[item.Name] = true
			enabled := item.Enabled == nil || *item.Enabled
			if value, ok := states[item.Name]; ok {
				enabled = value
			}
			names := make([]string, 0, len(item.Tools))
			for _, definition := range item.Tools {
				names = append(names, definition.Name)
			}
			slices.Sort(names)
			out = append(out, Package{Name: item.Name, Description: item.Description, Enabled: enabled, Tools: names})
		}
	}
	slices.SortFunc(out, func(a, b Package) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}
