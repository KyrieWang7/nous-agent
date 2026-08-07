//go:build docker

package docker

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox"
)

func TestProviderAcquireExecAndReuse(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	provider := NewProvider(Options{BaseDir: t.TempDir(), Image: "alpine:3.20"})
	t.Cleanup(func() { _ = provider.Release(context.Background(), "docker-test") })
	first, err := provider.Acquire(ctx, "docker-test")
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.Acquire(ctx, "docker-test")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() != second.ID() {
		t.Fatalf("thread sandbox was not reused: %q != %q", first.ID(), second.ID())
	}
	result, err := first.Exec(ctx, sandbox.Command{Line: "printf docker-ok"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "docker-ok" {
		t.Fatalf("exec result = %#v", result)
	}
}
