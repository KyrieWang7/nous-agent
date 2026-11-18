package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestViperSourcesPreserveStrictAndOpaqueYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("name: from-file\nheaders:\n  X-API-Key: value\nwindow: 15s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := struct {
		Name    string            `yaml:"name"`
		Headers map[string]string `yaml:"headers"`
		Window  time.Duration     `yaml:"window"`
	}{Name: "default"}
	t.Setenv("TEST_PLATFORM_NAME", "from-env")
	v, err := LoadYAML(path, &cfg, map[string]string{"name": "TEST_PLATFORM_NAME"})
	if err != nil {
		t.Fatal(err)
	}
	if Env(v, "name", cfg.Name) != "from-env" || cfg.Headers["X-API-Key"] != "value" || cfg.Window != 15*time.Second {
		t.Fatalf("config=%+v", cfg)
	}
	if err := os.WriteFile(path, []byte("typo: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadYAML(path, &cfg, nil); err == nil {
		t.Fatal("unknown field accepted")
	}
}
func TestRateLimitEnvironmentIsTypedAndDefaultOff(t *testing.T) {
	v, err := LoadYAML("", nil, map[string]string{"rate.enabled": "TEST_RATE_ENABLED", "rate.requests": "TEST_RATE_REQUESTS", "rate.window": "TEST_RATE_WINDOW"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := RateLimit{Requests: 60, Window: time.Minute}
	if err := ApplyRateLimit(v, "rate", &cfg); err != nil || cfg.Enabled {
		t.Fatalf("default=%+v err=%v", cfg, err)
	}
	t.Setenv("TEST_RATE_ENABLED", "true")
	t.Setenv("TEST_RATE_REQUESTS", "2")
	t.Setenv("TEST_RATE_WINDOW", "10s")
	if err := ApplyRateLimit(v, "rate", &cfg); err != nil || !cfg.Enabled || cfg.Requests != 2 || cfg.Window != 10*time.Second {
		t.Fatalf("config=%+v err=%v", cfg, err)
	}
	t.Setenv("TEST_RATE_REQUESTS", "invalid")
	if err := ApplyRateLimit(v, "rate", &cfg); err == nil {
		t.Fatal("invalid request limit accepted")
	}
}
