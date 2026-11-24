package bootstrap

import (
	"context"
	"errors"
	"net"
	"net/http"
	"syscall"
	"time"

	easelhandler "github.com/KyrieWang7/nous-agent/manager/manager_service/internal/handler/easel"
	ideashandler "github.com/KyrieWang7/nous-agent/manager/manager_service/internal/handler/ideas"
	"github.com/KyrieWang7/nous-agent/manager/manager_service/internal/middleware"
	ideasrepository "github.com/KyrieWang7/nous-agent/manager/manager_service/internal/repository/ideas"
	"github.com/KyrieWang7/nous-agent/manager/manager_service/internal/router"
	ideasservice "github.com/KyrieWang7/nous-agent/manager/manager_service/internal/service/ideas"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"
)

var ideasModule = fx.Module("ideas", fx.Provide(
	fx.Annotate(ideasrepository.New, fx.As(new(ideasservice.Repository))),
	ideasservice.New, ideashandler.New,
))

var Module = fx.Module("manager", ideasModule,
	fx.Provide(LoadConfig, newLogger, newDatabase, newLegacyEndpoint, easelhandler.New, newRouter),
	fx.Invoke(serve),
)

func New(options ...fx.Option) *fx.App {
	defaults := []fx.Option{
		Module,
		fx.StartTimeout(30 * time.Second),
		fx.WithLogger(func(logger *zap.Logger) fxevent.Logger { return &fxevent.ZapLogger{Logger: logger} }),
	}
	return fx.New(append(defaults, options...)...)
}

func newLogger(lc fx.Lifecycle, config Config) (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	if err := cfg.Level.UnmarshalText([]byte(config.LogLevel)); err != nil {
		return nil, err
	}
	logger, err := cfg.Build(zap.Fields(zap.String("service", "manager")))
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error {
		if err := logger.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) &&
			!errors.Is(err, syscall.ENOTTY) && !errors.Is(err, syscall.EBADF) {
			return err
		}
		return nil
	}})
	return logger, nil
}

func newRouter(cfg Config, handler *ideashandler.Handler, legacy *easelhandler.Handler) http.Handler {
	return router.New(handler, legacy, middleware.TrustedGateway(cfg.GatewayToken))
}

func serve(lc fx.Lifecycle, shutdown fx.Shutdowner, cfg Config, handler http.Handler, logger *zap.Logger) {
	server := &http.Server{Addr: cfg.Address, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			var listenerConfig net.ListenConfig
			listener, err := listenerConfig.Listen(ctx, "tcp", server.Addr)
			if err != nil {
				return err
			}
			go func() {
				if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
					logger.Error("Manager HTTP server stopped", zap.Error(err))
					_ = shutdown.Shutdown(fx.ExitCode(1))
				}
			}()
			logger.Info("Manager listening", zap.String("address", listener.Addr().String()))
			return nil
		},
		OnStop: server.Shutdown,
	})
}
