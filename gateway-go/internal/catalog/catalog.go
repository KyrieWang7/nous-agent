package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Model struct {
	Name                    string  `json:"name" yaml:"name"`
	DisplayName             string  `json:"display_name" yaml:"display_name"`
	Description             *string `json:"description" yaml:"description"`
	SupportsThinking        bool    `json:"supports_thinking" yaml:"supports_thinking"`
	SupportsReasoningEffort bool    `json:"supports_reasoning_effort" yaml:"supports_reasoning_effort"`
}

type MCPServer struct {
	Enabled     bool              `json:"enabled"`
	Type        string            `json:"type,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers"`
	Description string            `json:"description"`
}

type Extensions struct {
	MCPServers map[string]MCPServer  `json:"mcpServers"`
	Skills     map[string]SkillState `json:"skills"`
}

type SkillState struct {
	Enabled bool `json:"enabled"`
}

type Skill struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	License     *string `json:"license"`
	Category    string  `json:"category"`
	Enabled     bool    `json:"enabled"`
	Content     string  `json:"content,omitempty"`
	Path        string  `json:"-"`
}

type Catalog struct {
	harnessPath    string
	extensionsPath string
	skillsRoot     string
	mu             sync.Mutex
}

func New(harnessPath, extensionsPath, skillsRoot string) *Catalog {
	return &Catalog{harnessPath: harnessPath, extensionsPath: extensionsPath, skillsRoot: skillsRoot}
}

func (c *Catalog) Models() ([]Model, error) {
	raw, err := os.ReadFile(c.harnessPath)
	if err != nil {
		return nil, fmt.Errorf("reading harness config: %w", err)
	}
	var document struct {
		Models []Model `yaml:"models"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("decoding harness config: %w", err)
	}
	seen := make(map[string]struct{}, len(document.Models))
	for i := range document.Models {
		model := &document.Models[i]
		if model.Name == "" {
			return nil, errors.New("harness model requires a name")
		}
		if _, duplicate := seen[model.Name]; duplicate {
			return nil, fmt.Errorf("duplicate harness model %q", model.Name)
		}
		seen[model.Name] = struct{}{}
		if model.DisplayName == "" {
			model.DisplayName = model.Name
		}
	}
	return document.Models, nil
}

func (c *Catalog) Extensions() (Extensions, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadExtensions()
}

func (c *Catalog) SaveMCP(servers map[string]MCPServer) (Extensions, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ext, err := c.loadExtensions()
	if err != nil {
		return Extensions{}, err
	}
	if servers == nil {
		servers = map[string]MCPServer{}
	}
	ext.MCPServers = servers
	return ext, c.writeExtensions(ext)
}

func (c *Catalog) SetSkillEnabled(name string, enabled bool) (Skill, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	skills, err := c.loadSkills()
	if err != nil {
		return Skill{}, err
	}
	var found *Skill
	for i := range skills {
		if skills[i].Name == name {
			found = &skills[i]
			break
		}
	}
	if found == nil {
		return Skill{}, os.ErrNotExist
	}
	ext, err := c.loadExtensions()
	if err != nil {
		return Skill{}, err
	}
	if ext.Skills == nil {
		ext.Skills = make(map[string]SkillState)
	}
	ext.Skills[name] = SkillState{Enabled: enabled}
	if err := c.writeExtensions(ext); err != nil {
		return Skill{}, err
	}
	found.Enabled = enabled
	return *found, nil
}

func (c *Catalog) Skills() ([]Skill, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadSkills()
}

func (c *Catalog) Skill(name string) (Skill, error) {
	skills, err := c.Skills()
	if err != nil {
		return Skill{}, err
	}
	for _, skill := range skills {
		if skill.Name == name {
			return skill, nil
		}
	}
	return Skill{}, os.ErrNotExist
}

func (c *Catalog) loadSkills() ([]Skill, error) {
	ext, err := c.loadExtensions()
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, category := range []string{"public", "custom"} {
		root := filepath.Join(c.skillsRoot, category)
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(root, entry.Name(), "SKILL.md")
			content, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			meta := parseFrontmatter(content)
			name := stringValue(meta["name"], entry.Name())
			enabled := true
			if state, ok := ext.Skills[name]; ok {
				enabled = state.Enabled
			}
			var license *string
			if value := stringValue(meta["license"], ""); value != "" {
				license = &value
			}
			skills = append(skills, Skill{Name: name, Description: stringValue(meta["description"], ""), License: license, Category: category, Enabled: enabled, Content: string(content), Path: filepath.Dir(path)})
		}
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, nil
}

func (c *Catalog) loadExtensions() (Extensions, error) {
	ext := Extensions{MCPServers: map[string]MCPServer{}, Skills: map[string]SkillState{}}
	raw, err := os.ReadFile(c.extensionsPath)
	if errors.Is(err, os.ErrNotExist) {
		return ext, nil
	}
	if err != nil {
		return Extensions{}, err
	}
	if err := json.Unmarshal(raw, &ext); err != nil {
		return Extensions{}, fmt.Errorf("decoding extensions config: %w", err)
	}
	if ext.MCPServers == nil {
		ext.MCPServers = map[string]MCPServer{}
	}
	if ext.Skills == nil {
		ext.Skills = map[string]SkillState{}
	}
	return ext, nil
}

func (c *Catalog) writeExtensions(ext Extensions) error {
	if err := os.MkdirAll(filepath.Dir(c.extensionsPath), 0o750); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(ext, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(c.extensionsPath), ".extensions-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, c.extensionsPath)
}

func parseFrontmatter(content []byte) map[string]any {
	parts := bytes.SplitN(content, []byte("---"), 3)
	if len(parts) != 3 || len(bytes.TrimSpace(parts[0])) != 0 {
		return map[string]any{}
	}
	var values map[string]any
	if yaml.Unmarshal(parts[1], &values) != nil {
		return map[string]any{}
	}
	return values
}

func stringValue(value any, fallback string) string {
	if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text)
	}
	return fallback
}
