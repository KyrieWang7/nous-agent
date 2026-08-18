//go:build docker

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/transport/sandboxapi"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox/controller"
	dockersandbox "github.com/KyrieWang7/nous-agent/agent-go/pkg/sandbox/docker"
)

func main() {
	if err := run(); err != nil {
		slog.Error("sandboxd stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ttlDefault, err := envDuration("SANDBOX_TTL", 30*time.Minute)
	if err != nil {
		return err
	}
	reapDefault, err := envDuration("SANDBOX_REAP_INTERVAL", time.Minute)
	if err != nil {
		return err
	}
	address := flag.String("address", envOr("SANDBOXD_ADDRESS", ":7780"), "listen address")
	token := flag.String("token", os.Getenv("SANDBOX_CONTROLLER_TOKEN"), "control-plane bearer token")
	baseDir := flag.String("base-dir", envOr("SANDBOX_BASE_DIR", "/var/lib/nous-sandbox"), "controller-visible workspace directory")
	hostBaseDir := flag.String("host-base-dir", os.Getenv("SANDBOX_HOST_BASE_DIR"), "Docker-host-visible workspace directory")
	image := flag.String("image", envOr("SANDBOX_IMAGE", "alpine:3.20"), "sandbox workload image")
	ttl := flag.Duration("ttl", ttlDefault, "idle sandbox lease lifetime")
	reapInterval := flag.Duration("reap-interval", reapDefault, "expired sandbox scan interval")
	flag.Parse()

	provider := dockersandbox.NewProvider(dockersandbox.Options{Image: *image, BaseDir: *baseDir, HostBaseDir: *hostBaseDir, VirtualRoot: "/mnt/user-data", ExecTimeout: 30 * time.Second})
	enforcement := controller.Enforcement{Backend: "docker", Isolation: "container", NetworkDefault: "none", NonRoot: true, ReadOnlyRoot: true, ResourceLimits: true}
	manager, err := controller.New(provider, controller.Options{TTL: *ttl, ReapInterval: *reapInterval, Enforcement: enforcement, Logger: slog.Default()})
	if err != nil {
		return err
	}
	defer manager.Close()
	api, err := sandboxapi.New(manager, strings.TrimSpace(*token))
	if err != nil {
		return err
	}
	server := &http.Server{Addr: *address, Handler: api, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	slog.Info("sandboxd listening", "address", *address, "backend", "docker")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s duration %q: %w", name, value, err)
	}
	return parsed, nil
}
