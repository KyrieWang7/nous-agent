package bootstrap

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"

	platformconfig "github.com/KyrieWang7/nous-agent/platform-go/config"
)

type Config struct {
	Address       string `yaml:"address"`
	DatabaseURL   string `yaml:"database_url"`
	Workspace     string `yaml:"workspace"`
	GatewayToken  string `yaml:"gateway_token"`
	LegacyAddress string `yaml:"legacy_address"`
	Python        string `yaml:"python"`
	LogLevel      string `yaml:"log_level"`
}

func LoadConfig() (Config, error) {
	cfg := Config{Address: "127.0.0.1:7860", LegacyAddress: "127.0.0.1:7861", Python: "python3", LogLevel: "info"}
	bindings := map[string]string{
		"address": "MANAGER_HTTP_ADDR", "database_url": "MANAGER_DATABASE_URL",
		"workspace": "MANAGER_WORKSPACE", "gateway_token": "MANAGER_GATEWAY_TOKEN",
		"legacy_address": "MANAGER_LEGACY_ADDR", "python": "MANAGER_PYTHON", "log_level": "LOG_LEVEL",
	}
	v, err := platformconfig.LoadYAML(os.Getenv("MANAGER_CONFIG_PATH"), &cfg, bindings)
	if err != nil {
		return Config{}, err
	}
	cfg.Address = platformconfig.Env(v, "address", cfg.Address)
	cfg.DatabaseURL = platformconfig.Env(v, "database_url", cfg.DatabaseURL)
	cfg.Workspace = platformconfig.Env(v, "workspace", cfg.Workspace)
	cfg.GatewayToken = platformconfig.Env(v, "gateway_token", cfg.GatewayToken)
	cfg.LegacyAddress = platformconfig.Env(v, "legacy_address", cfg.LegacyAddress)
	cfg.Python = platformconfig.Env(v, "python", cfg.Python)
	cfg.LogLevel = platformconfig.Env(v, "log_level", cfg.LogLevel)
	if cfg.DatabaseURL == "" || cfg.GatewayToken == "" {
		return Config{}, errors.New("MANAGER_DATABASE_URL and MANAGER_GATEWAY_TOKEN are required")
	}
	if !filepath.IsAbs(cfg.Workspace) {
		return Config{}, errors.New("MANAGER_WORKSPACE must be an absolute path")
	}
	host, port, err := net.SplitHostPort(cfg.LegacyAddress)
	value, parseErr := strconv.Atoi(port)
	if err != nil || parseErr != nil || value < 1 || value > 65535 || host != "127.0.0.1" {
		return Config{}, errors.New("MANAGER_LEGACY_ADDR must be an IPv4 loopback address and valid port")
	}
	return cfg, nil
}
