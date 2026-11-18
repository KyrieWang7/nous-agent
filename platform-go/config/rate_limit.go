package config

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/viper"
)

type RateLimit struct {
	Enabled  bool          `yaml:"enabled"`
	Requests int           `yaml:"requests"`
	Window   time.Duration `yaml:"window"`
}

func ApplyRateLimit(v *viper.Viper, prefix string, c *RateLimit) error {
	if v.IsSet(prefix + ".enabled") {
		value, err := strconv.ParseBool(v.GetString(prefix + ".enabled"))
		if err != nil {
			return fmt.Errorf("config: rate limit enabled: %w", err)
		}
		c.Enabled = value
	}
	if v.IsSet(prefix + ".requests") {
		value, err := strconv.Atoi(v.GetString(prefix + ".requests"))
		if err != nil {
			return fmt.Errorf("config: rate limit requests: %w", err)
		}
		c.Requests = value
	}
	if v.IsSet(prefix + ".window") {
		value, err := time.ParseDuration(v.GetString(prefix + ".window"))
		if err != nil {
			return fmt.Errorf("config: rate limit window: %w", err)
		}
		c.Window = value
	}
	if c.Enabled && (c.Requests <= 0 || c.Window < time.Millisecond) {
		return fmt.Errorf("config: enabled rate limiter requires positive requests and window >= 1ms")
	}
	return nil
}
