// Package redisclient owns go-redis connection setup, validation and defaults.
package redisclient

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Open returns nil for a disabled Redis URL. Callers own and close a returned client.
func Open(ctx context.Context, url string) (*redis.Client, error) {
	if url == "" {
		return nil, nil
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis: invalid URL: %w", err)
	}
	client := redis.NewClient(opts)
	check, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(check).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: ping: %w", err)
	}
	return client, nil
}
