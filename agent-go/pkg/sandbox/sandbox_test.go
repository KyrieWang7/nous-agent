package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReadAllLimitedAcceptsExactBoundary(t *testing.T) {
	t.Parallel()

	data, err := ReadAllLimited(context.Background(), strings.NewReader("1234"), 4)
	if err != nil || string(data) != "1234" {
		t.Fatalf("ReadAllLimited() = %q, %v", data, err)
	}
}

func TestReadAllLimitedRejectsSentinelByte(t *testing.T) {
	t.Parallel()

	data, err := ReadAllLimited(context.Background(), strings.NewReader("12345"), 4)
	if !errors.Is(err, ErrReadLimit) || data != nil {
		t.Fatalf("ReadAllLimited() = %q, %v; want nil, ErrReadLimit", data, err)
	}
}

func TestReadAllLimitedObservesCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadAllLimited(ctx, strings.NewReader("data"), 4); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadAllLimited() error = %v, want context.Canceled", err)
	}
}
