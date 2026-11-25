package modelconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Path uses the same shared configuration directory as MCP and Skill settings.
func Path(extensionsPath string) string {
	if strings.TrimSpace(extensionsPath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(extensionsPath), "models.json")
}

func Read(path string) (Settings, bool, error) {
	if path == "" {
		return Settings{}, false, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, false, nil
	}
	if err != nil {
		return Settings{}, false, fmt.Errorf("reading model settings: %w", err)
	}
	var settings Settings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return Settings{}, false, errors.New("invalid saved model settings JSON")
	}
	return settings, true, nil
}

func Load(harnessPath, settingsPath string) (Settings, error) {
	if saved, present, err := Read(settingsPath); err != nil || present {
		return saved, err
	}
	raw, err := os.ReadFile(harnessPath)
	if err != nil {
		return Settings{}, fmt.Errorf("reading harness config: %w", err)
	}
	var settings Settings
	if err := yaml.Unmarshal(raw, &settings); err != nil {
		return Settings{}, errors.New("invalid harness model configuration")
	}
	if settings.DefaultModel == "" && len(settings.Models) > 0 {
		settings.DefaultModel = settings.Models[0].Name
	}
	return settings, nil
}

var envReference = regexp.MustCompile(`^\$(?:([A-Za-z_][A-Za-z0-9_]*)|\{([A-Za-z_][A-Za-z0-9_]*)\})$`)

// ResolveKey expands only a complete environment reference, never literal key bytes.
func ResolveKey(key string) string {
	match := envReference.FindStringSubmatch(key)
	if match == nil {
		return key
	}
	return os.Getenv(match[1] + match[2])
}

func (s Settings) Public() PublicSettings {
	result := PublicSettings{DefaultModel: s.DefaultModel, Models: make([]PublicModel, 0, len(s.Models))}
	for _, item := range s.Models {
		configured := ResolveKey(item.APIKey) != ""
		item.APIKey = ""
		result.Models = append(result.Models, PublicModel{Model: item, APIKeyConfigured: configured})
	}
	return result
}

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }
func invalid(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

func (s *Settings) Validate() error {
	if len(s.Models) == 0 {
		return invalid("at least one model is required")
	}
	seen := map[string]bool{}
	for _, m := range s.Models {
		if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Model) == "" {
			return invalid("every model requires name and model")
		}
		if m.Name == "default" || m.Name == "config" {
			return invalid("model name %q is reserved by the runtime or API", m.Name)
		}
		if seen[m.Name] {
			return invalid("duplicate model name %q", m.Name)
		}
		seen[m.Name] = true
		switch m.Provider {
		case "openai-compatible", "anthropic", "deepseek":
		default:
			return invalid("unsupported model provider")
		}
		if m.BaseURL != "" {
			u, err := url.Parse(m.BaseURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return invalid("base_url must be an HTTP(S) URL without credentials, query or fragment")
			}
		}
		if m.MaxTokens < 0 || m.ContextLength < 0 || m.Timeout < 0 || m.StreamIdleTimeout < 0 {
			return invalid("model limits and timeouts cannot be negative")
		}
		// Match the provider constructors, including omitted output reservations.
		window, output := m.ContextLength, m.MaxTokens
		defaultWindow, defaultOutput := 128000, 0
		switch m.Provider {
		case "anthropic":
			defaultWindow, defaultOutput = 200000, 8192
		case "deepseek":
			defaultWindow, defaultOutput = 1000000, 256000
		}
		if window == 0 {
			window = defaultWindow
		}
		if output == 0 {
			output = defaultOutput
		}
		if output >= window {
			return invalid("max_tokens must be smaller than context_length")
		}
		if m.Temperature != nil && (*m.Temperature < 0 || *m.Temperature > 2) {
			return invalid("temperature must be between 0 and 2")
		}
		if m.UseFiles && m.Provider != "deepseek" {
			return invalid("use_files requires the deepseek provider")
		}
		if m.Provider == "deepseek" && (len(m.ExtraBody) > 0 || m.WhenThinkingEnabled != nil) {
			return invalid("deepseek does not support extra_body or when_thinking_enabled")
		}
		if m.Pricing.InputPerMillionMicros < 0 || m.Pricing.OutputPerMillionMicros < 0 || m.Pricing.CachedInputPerMillionMicros < 0 {
			return invalid("model pricing cannot be negative")
		}
	}
	if s.DefaultModel == "" {
		s.DefaultModel = s.Models[0].Name
	}
	if !seen[s.DefaultModel] {
		return invalid("default_model must name a configured model")
	}
	return nil
}

// Replace preserves omitted credentials by name; an explicit empty string clears one.
func Replace(previous Settings, input Input) (Settings, error) {
	keys := map[string]string{}
	for _, item := range previous.Models {
		keys[item.Name] = item.APIKey
	}
	result := Settings{DefaultModel: input.DefaultModel, Models: make([]Model, 0, len(input.Models))}
	for _, item := range input.Models {
		m := item.Model
		m.APIKey = keys[m.Name]
		if item.APIKey != nil {
			m.APIKey = *item.APIKey
		}
		result.Models = append(result.Models, m)
	}
	if err := result.Validate(); err != nil {
		return Settings{}, err
	}
	return result, nil
}

// Write uses rename so Agent sees either the old complete revision or the new one.
func Write(path string, settings Settings) error {
	if path == "" {
		return errors.New("model settings path is not configured")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".models-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	if err := json.NewEncoder(file).Encode(settings); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// Decode rejects trailing JSON and bounds configuration request size.
func Decode(body io.Reader) (Input, error) {
	decoder := json.NewDecoder(io.LimitReader(body, (2<<20)+1))
	decoder.DisallowUnknownFields()
	var input Input
	if err := decoder.Decode(&input); err != nil {
		return Input{}, errors.New("invalid model configuration JSON or unsupported field")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Input{}, errors.New("expected a single model configuration object")
	}
	return input, nil
}
