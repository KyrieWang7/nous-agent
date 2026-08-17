package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Address          string
	DatabaseURL      string
	HarnessConfig    string
	ExtensionsConfig string
	SkillsRoot       string
	WorkspaceRoot    string
	CORSOrigins      []string
}

func Load() (Config, error) {
	cfg := Config{
		Address:          env("GATEWAY_ADDR", ":7777"),
		DatabaseURL:      strings.TrimSpace(os.Getenv("DATABASE_URL")),
		HarnessConfig:    env("NOUS_HARNESS_CONFIG_PATH", "/etc/nous-agent/config.yaml"),
		ExtensionsConfig: env("NOUS_EXTENSIONS_CONFIG_PATH", "/etc/nous-agent/extensions.json"),
		SkillsRoot:       env("NOUS_SKILLS_ROOT", "/opt/nous/skills"),
		WorkspaceRoot:    env("NOUS_WORKSPACE_ROOT", "/data/workspaces"),
		CORSOrigins:      split(env("CORS_ORIGINS", "*")),
	}
	if cfg.Address == "" {
		return Config{}, errors.New("gateway: GATEWAY_ADDR must not be empty")
	}
	for _, path := range []*string{&cfg.HarnessConfig, &cfg.ExtensionsConfig, &cfg.SkillsRoot, &cfg.WorkspaceRoot} {
		if !filepath.IsAbs(*path) {
			absolute, err := filepath.Abs(*path)
			if err != nil {
				return Config{}, err
			}
			*path = absolute
		}
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func split(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
