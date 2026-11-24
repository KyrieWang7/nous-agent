package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/config"
	gatewaymiddleware "github.com/KyrieWang7/nous-agent/gateway-go/internal/middleware"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/repository"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/repository/catalog"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/repository/files"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/router"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/server"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/service"
	"github.com/KyrieWang7/nous-agent/platform-go/httpx"
	"github.com/KyrieWang7/nous-agent/platform-go/logging"
	"github.com/KyrieWang7/nous-agent/platform-go/redisclient"
	"github.com/gin-gonic/gin"
)

func main() {
	flush, err := logging.Install(logging.Options{Service: "gateway", Level: os.Getenv("LOG_LEVEL")})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = flush() }()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	database, err := repository.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("opening database", "error", err)
		os.Exit(1)
	}
	defer database.Close()
	var middleware []gin.HandlerFunc
	if cfg.RateLimit.Enabled {
		client, err := redisclient.Open(ctx, cfg.RedisURL)
		if err != nil || client == nil {
			slog.Error("HTTP rate limit requires reachable REDIS_URL", "error", err)
			os.Exit(1)
		}
		defer func() { _ = client.Close() }()
		limiter, err := httpx.RateLimit("gateway", client, cfg.RateLimit.Requests, cfg.RateLimit.Window)
		if err != nil {
			slog.Error("invalid HTTP rate limit", "error", err)
			os.Exit(1)
		}
		middleware = append(middleware, limiter)
	}
	handler := server.New(service.Options{Catalog: catalog.New(cfg.HarnessConfig, cfg.ExtensionsConfig, cfg.SkillsRoot), Files: files.New(cfg.WorkspaceRoot, cfg.SkillsRoot), Repository: database, Swarm: database}, cfg.CORSOrigins, middleware...)
	frontend, manager, err := gatewaymiddleware.NewWebUpstreams(gatewaymiddleware.WebUpstreams{
		FrontendURL: cfg.FrontendURL, ManagerURL: cfg.ManagerURL,
		ManagerToken: cfg.ManagerToken, PublicOrigin: cfg.PublicOrigin,
	})
	if err != nil {
		slog.Error("configuring gateway routes", "error", err)
		os.Exit(1)
	}
	handler = router.WithWeb(handler, frontend, manager)
	httpServer := server.HTTP(cfg.Address, handler)
	go func() {
		slog.Info("Go Gateway listening", "address", cfg.Address)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("gateway stopped", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown, httpServer); err != nil {
		slog.Error("graceful shutdown", "error", err)
	}
}
