package metadata

import (
	"context"
	"errors"
)

var ErrStaleWriter = errors.New("thread write rejected: a newer run owns the thread")

type writerKey struct{}

func WithWriter(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, writerKey{}, runID)
}
func Writer(ctx context.Context) string { value, _ := ctx.Value(writerKey{}).(string); return value }
