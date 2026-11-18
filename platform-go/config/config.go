// Package config unifies configuration sources while preserving strict YAML schemas.
package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// LoadYAML uses Viper for source loading and environment bindings. The original
// YAML is decoded strictly to preserve case-sensitive maps, explicit zeroes,
// duration values and the application's existing typed defaults.
func LoadYAML(path string, target any, bindings map[string]string) (*viper.Viper, error) {
	v := viper.New()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("config: reading %s: %w", path, err)
		}
		raw = []byte(os.ExpandEnv(string(raw)))
		v.SetConfigType("yaml")
		if err := v.ReadConfig(bytes.NewReader(raw)); err != nil {
			return nil, fmt.Errorf("config: reading YAML: %w", err)
		}
		decoder := yaml.NewDecoder(bytes.NewReader(raw))
		decoder.KnownFields(true)
		if err := decoder.Decode(target); err != nil {
			return nil, fmt.Errorf("config: decoding %s: %w", path, err)
		}
	}
	for key, env := range bindings {
		if err := v.BindEnv(key, env); err != nil {
			return nil, err
		}
	}
	return v, nil
}

// Env reads explicitly supported overrides through Viper. Blank values retain
// the existing application fallback rather than silently clearing a setting.
func Env(v *viper.Viper, key, fallback string) string {
	value := strings.TrimSpace(v.GetString(key))
	if value == "" {
		return fallback
	}
	return value
}
