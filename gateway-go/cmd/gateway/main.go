package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/catalog"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/config"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/files"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/httpapi"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	database, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("opening database", "error", err)
		os.Exit(1)
	}
	defer database.Close()
	handler := httpapi.New(catalog.New(cfg.HarnessConfig, cfg.ExtensionsConfig, cfg.SkillsRoot), files.New(cfg.WorkspaceRoot, cfg.SkillsRoot), database, cfg.CORSOrigins)
	server := &http.Server{Addr: cfg.Address, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		slog.Info("Go Gateway listening", "address", cfg.Address)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("gateway stopped", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpapi.Shutdown(shutdown, server); err != nil {
		slog.Error("graceful shutdown", "error", err)
	}
}
