package config

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	platformconfig "github.com/KyrieWang7/nous-agent/platform-go/config"
)

type Config struct {
	RedisURL         string                   `yaml:"redis_url"`
	RateLimit        platformconfig.RateLimit `yaml:"rate_limit"`
	Address          string                   `yaml:"address"`
	DatabaseURL      string                   `yaml:"database_url"`
	HarnessConfig    string                   `yaml:"harness_config"`
	ExtensionsConfig string                   `yaml:"extensions_config"`
	SkillsRoot       string                   `yaml:"skills_root"`
	WorkspaceRoot    string                   `yaml:"workspace_root"`
	CORSOrigins      []string                 `yaml:"cors_origins"`
	FrontendURL      string                   `yaml:"frontend_url"`
	CreativeURL      string                   `yaml:"creative_api_url"`
	CreativeWebURL   string                   `yaml:"creative_web_url"`
	PublicOrigin     string                   `yaml:"public_origin"`
	WebBasePath      string                   `yaml:"web_base_path"`
}

var webBasePathPattern = regexp.MustCompile(`^(/[A-Za-z0-9_-]+)+$`)

func Load() (Config, error) {
	cfg := Config{RateLimit: platformconfig.RateLimit{Requests: 60, Window: time.Minute}, Address: ":7777", HarnessConfig: "/etc/nous-agent/config.yaml", ExtensionsConfig: "/etc/nous-agent/extensions.json", SkillsRoot: "/opt/nous/skills", WorkspaceRoot: "/data/workspaces", CORSOrigins: []string{"*"}}
	v, err := platformconfig.LoadYAML(os.Getenv("NOUS_GATEWAY_CONFIG_PATH"), &cfg, map[string]string{
		"redis_url": "REDIS_URL", "rate_limit.enabled": "HTTP_RATE_LIMIT_ENABLED", "rate_limit.requests": "HTTP_RATE_LIMIT_REQUESTS", "rate_limit.window": "HTTP_RATE_LIMIT_WINDOW",
		"address": "GATEWAY_ADDR", "database_url": "DATABASE_URL", "harness_config": "NOUS_HARNESS_CONFIG_PATH", "extensions_config": "NOUS_EXTENSIONS_CONFIG_PATH", "skills_root": "NOUS_SKILLS_ROOT", "workspace_root": "NOUS_WORKSPACE_ROOT", "cors_origins": "CORS_ORIGINS",
		"frontend_url": "FRONTEND_BASE_URL", "creative_api_url": "CREATIVE_API_URL", "creative_web_url": "CREATIVE_WEB_URL", "public_origin": "PUBLIC_WEB_ORIGIN",
		"web_base_path": "NOUS_WEB_BASE_PATH",
	})
	if err != nil {
		return Config{}, err
	}
	cfg.RedisURL = platformconfig.Env(v, "redis_url", cfg.RedisURL)
	if err := platformconfig.ApplyRateLimit(v, "rate_limit", &cfg.RateLimit); err != nil {
		return Config{}, err
	}
	cfg.Address = platformconfig.Env(v, "address", cfg.Address)
	cfg.DatabaseURL = platformconfig.Env(v, "database_url", cfg.DatabaseURL)
	cfg.HarnessConfig = platformconfig.Env(v, "harness_config", cfg.HarnessConfig)
	cfg.ExtensionsConfig = platformconfig.Env(v, "extensions_config", cfg.ExtensionsConfig)
	cfg.SkillsRoot = platformconfig.Env(v, "skills_root", cfg.SkillsRoot)
	cfg.WorkspaceRoot = platformconfig.Env(v, "workspace_root", cfg.WorkspaceRoot)
	cfg.FrontendURL = platformconfig.Env(v, "frontend_url", cfg.FrontendURL)
	cfg.CreativeURL = platformconfig.Env(v, "creative_api_url", cfg.CreativeURL)
	cfg.CreativeWebURL = platformconfig.Env(v, "creative_web_url", cfg.CreativeWebURL)
	cfg.PublicOrigin = platformconfig.Env(v, "public_origin", cfg.PublicOrigin)
	cfg.WebBasePath = platformconfig.Env(v, "web_base_path", cfg.WebBasePath)
	if cfg.WebBasePath != "" && !webBasePathPattern.MatchString(cfg.WebBasePath) {
		return Config{}, errors.New("NOUS_WEB_BASE_PATH must be a path prefix without a trailing slash")
	}
	if value := os.Getenv("CORS_ORIGINS"); strings.TrimSpace(value) != "" {
		cfg.CORSOrigins = split(v.GetString("cors_origins"))
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
