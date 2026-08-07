// Package skill implements progressive SKILL.md disclosure.
package skill

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

const MaxDescriptionLength = 4096

type Metadata struct {
	Name         string   `yaml:"name" json:"name"`
	Description  string   `yaml:"description" json:"description"`
	AllowedTools []string `yaml:"allowed-tools" json:"allowed_tools,omitempty"`
	Priority     int      `yaml:"priority" json:"priority,omitempty"`
	MutexKey     string   `yaml:"mutex_key" json:"mutex_key,omitempty"`
}

type Skill struct {
	Metadata
	Path   string
	mu     sync.Mutex
	body   string
	loaded bool
	mtime  time.Time
}

func (s *Skill) Body() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := os.Stat(s.Path)
	if err != nil {
		return "", err
	}
	if s.loaded && info.ModTime().Equal(s.mtime) {
		return s.body, nil
	}
	body, err := readBody(s.Path)
	if err != nil {
		return "", err
	}
	s.body = body
	s.loaded = true
	s.mtime = info.ModTime()
	return body, nil
}
func (s *Skill) Invalidate() { s.mu.Lock(); s.loaded = false; s.body = ""; s.mu.Unlock() }

type Registry struct {
	mu     sync.RWMutex
	byName map[string]*Skill
}

func Load(root string) (*Registry, error) {
	return LoadFiltered(root, nil)
}

func LoadFiltered(root string, disabled []string) (*Registry, error) {
	r := &Registry{byName: map[string]*Skill{}}
	disabledSet := make(map[string]struct{}, len(disabled))
	for _, name := range disabled {
		disabledSet[name] = struct{}{}
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		meta, err := readMetadata(path)
		if err != nil {
			return err
		}
		if _, disabled := disabledSet[meta.Name]; disabled {
			return nil
		}
		if _, ok := r.byName[meta.Name]; ok {
			return fmt.Errorf("skill: duplicate name %q", meta.Name)
		}
		r.byName[meta.Name] = &Skill{Metadata: meta, Path: path}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}
func (r *Registry) Get(name string) (*Skill, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("skill: %q not found", name)
	}
	return s, nil
}
func (r *Registry) List() []Metadata {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Metadata, 0, len(r.byName))
	for _, s := range r.byName {
		out = append(out, s.Metadata)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority == out[j].Priority {
			return out[i].Name < out[j].Name
		}
		return out[i].Priority > out[j].Priority
	})
	return out
}
func (r *Registry) Invalidate(name string) error {
	s, err := r.Get(name)
	if err != nil {
		return err
	}
	s.Invalidate()
	return nil
}

type ActivationContext struct {
	Prompt      string
	Summary     string
	ForceSkills []string
}

func (r *Registry) Match(ctx ActivationContext) ([]*Skill, error) {
	if len(ctx.ForceSkills) > 0 {
		out := make([]*Skill, 0, len(ctx.ForceSkills))
		for _, name := range ctx.ForceSkills {
			s, err := r.Get(name)
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return orderAndMutex(out), nil
	}
	hay := tokenSet(ctx.Prompt + " " + ctx.Summary)
	r.mu.RLock()
	var matches []*Skill
	for _, s := range r.byName {
		needles := tokenSet(s.Name + " " + s.Description)
		score := 0
		for n := range needles {
			if _, ok := hay[n]; ok && len(n) > 2 {
				score++
			}
		}
		if score > 0 {
			matches = append(matches, s)
		}
	}
	r.mu.RUnlock()
	return orderAndMutex(matches), nil
}
func orderAndMutex(in []*Skill) []*Skill {
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].Priority == in[j].Priority {
			return in[i].Name < in[j].Name
		}
		return in[i].Priority > in[j].Priority
	})
	seen := map[string]struct{}{}
	out := in[:0]
	for _, s := range in {
		if s.MutexKey != "" {
			if _, ok := seen[s.MutexKey]; ok {
				continue
			}
			seen[s.MutexKey] = struct{}{}
		}
		out = append(out, s)
	}
	return out
}

// NarrowTools intersects the current permission-approved set with skill
// declarations. Deferred tools are disclosed only when both registered and
// already permission-approved.
func NarrowTools(current []string, active []*Skill, registered, deferred map[string]bool) (allow, disclosed []string) {
	if len(active) == 0 {
		return append([]string(nil), current...), nil
	}
	approved := map[string]bool{}
	for _, n := range current {
		approved[n] = true
	}
	declared := map[string]bool{}
	for _, s := range active {
		for _, n := range s.AllowedTools {
			declared[n] = true
		}
	}
	if len(declared) == 0 {
		return append([]string(nil), current...), nil
	}
	for _, n := range current {
		if declared[n] && !deferred[n] {
			allow = append(allow, n)
		}
	}
	for n := range declared {
		if approved[n] && registered[n] && deferred[n] {
			allow = append(allow, n)
			disclosed = append(disclosed, n)
		}
	}
	sort.Strings(disclosed)
	return allow, disclosed
}

func readMetadata(path string) (Metadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return Metadata{}, err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return Metadata{}, fmt.Errorf("skill: %s missing YAML frontmatter", path)
	}
	var lines []string
	closed := false
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "---" {
			closed = true
			break
		}
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return Metadata{}, err
	}
	if !closed {
		return Metadata{}, fmt.Errorf("skill: %s has unterminated YAML frontmatter", path)
	}
	var m Metadata
	if err := yaml.Unmarshal([]byte(strings.Join(lines, "\n")), &m); err != nil {
		return Metadata{}, fmt.Errorf("skill: parsing %s: %w", path, err)
	}
	if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Description) == "" {
		return Metadata{}, fmt.Errorf("skill: %s requires name and description", path)
	}
	if len(m.Description) > MaxDescriptionLength {
		return Metadata{}, fmt.Errorf("skill: %s description exceeds %d bytes", path, MaxDescriptionLength)
	}
	return m, nil
}
func readBody(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	delims := 0
	for {
		line, err := r.ReadString('\n')
		if strings.TrimSpace(line) == "---" {
			delims++
			if delims == 2 {
				break
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", errors.New("skill: missing closing frontmatter delimiter")
			}
			return "", err
		}
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}
func tokenSet(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, part := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' }) {
		out[part] = struct{}{}
	}
	return out
}
