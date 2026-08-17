package capability

import (
	"context"
	"fmt"
)

// ResolveAs resolves from one immutable generation and preserves the domain
// interface at the call site. The registry stays domain-neutral while a bad
// provider registration fails before the capability is used.
func ResolveAs[T any](ctx context.Context, snapshot Snapshot, name string, kind Kind, req ResolveRequest) (T, error) {
	var zero T
	value, err := snapshot.Resolve(ctx, name, kind, req)
	if err != nil {
		return zero, err
	}
	typed, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("capability %q: resolved %T, want requested domain type", name, value)
	}
	return typed, nil
}
