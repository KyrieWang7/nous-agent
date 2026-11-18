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

	domain "github.com/KyrieWang7/nous-agent/gateway-go/internal/model"
	"gopkg.in/yaml.v3"
)

type Catalog struct {
	harnessPath    string
	extensionsPath string
	skillsRoot     string
	mu             sync.Mutex
}

func New(harnessPath, extensionsPath, skillsRoot string) *Catalog {
	return &Catalog{harnessPath: harnessPath, extensionsPath: extensionsPath, skillsRoot: skillsRoot}
}

func (c *Catalog) Models() ([]domain.Model, error) {
	raw, err := os.ReadFile(c.harnessPath)
	if err != nil {
		return nil, fmt.Errorf("reading harness config: %w", err)
	}
	var document struct {
		Models []domain.Model `yaml:"models"`
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

func (c *Catalog) SaveMCP(servers map[string]domain.MCPServer) (domain.Extensions, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ext, err := c.loadExtensions()
	if err != nil {
		return domain.Extensions{}, err
	}
	if servers == nil {
		servers = map[string]domain.MCPServer{}
	}
	ext.MCPServers = servers
	return ext, c.writeExtensions(ext)
}

func (c *Catalog) SetSkillEnabled(name string, enabled bool) (domain.Skill, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	skills, err := c.loadSkills()
	if err != nil {
		return domain.Skill{}, err
	}
	var found *domain.Skill
	for i := range skills {
		if skills[i].Name == name {
			found = &skills[i]
			break
		}
	}
	if found == nil {
		return domain.Skill{}, os.ErrNotExist
	}
	ext, err := c.loadExtensions()
	if err != nil {
		return domain.Skill{}, err
	}
	if ext.Skills == nil {
		ext.Skills = make(map[string]domain.SkillState)
	}
	ext.Skills[name] = domain.SkillState{Enabled: enabled}
	if err := c.writeExtensions(ext); err != nil {
		return domain.Skill{}, err
	}
	found.Enabled = enabled
	return *found, nil
}

func (c *Catalog) Skills() ([]domain.Skill, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadSkills()
}

func (c *Catalog) loadSkills() ([]domain.Skill, error) {
	ext, err := c.loadExtensions()
	if err != nil {
		return nil, err
	}
	var skills []domain.Skill
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
			skills = append(skills, domain.Skill{Name: name, Description: stringValue(meta["description"], ""), License: license, Category: category, Enabled: enabled, Content: string(content), Path: filepath.Dir(path)})
		}
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, nil
}

func (c *Catalog) loadExtensions() (domain.Extensions, error) {
	ext := domain.Extensions{MCPServers: map[string]domain.MCPServer{}, Skills: map[string]domain.SkillState{}}
	raw, err := os.ReadFile(c.extensionsPath)
	if errors.Is(err, os.ErrNotExist) {
		return ext, nil
	}
	if err != nil {
		return domain.Extensions{}, err
	}
	if err := json.Unmarshal(raw, &ext); err != nil {
		return domain.Extensions{}, fmt.Errorf("decoding extensions config: %w", err)
	}
	if ext.MCPServers == nil {
		ext.MCPServers = map[string]domain.MCPServer{}
	}
	if ext.Skills == nil {
		ext.Skills = map[string]domain.SkillState{}
	}
	return ext, nil
}

func (c *Catalog) writeExtensions(ext domain.Extensions) error {
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

func (c *Catalog) Extensions() (domain.Extensions, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadExtensions()
}

func (c *Catalog) Skill(name string) (domain.Skill, error) {
	skills, err := c.Skills()
	if err != nil {
		return domain.Skill{}, err
	}
	for _, skill := range skills {
		if skill.Name == name {
			return skill, nil
		}
	}
	return domain.Skill{}, os.ErrNotExist
}
