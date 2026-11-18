// Package server composes the gateway application and HTTP transport.
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/handler"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/middleware"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/router"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/service"
	"github.com/gin-gonic/gin"
)

func New(opts service.Options, origins []string, extra ...gin.HandlerFunc) http.Handler {
	app := service.New(opts)
	chain := append([]gin.HandlerFunc{middleware.CORS(origins), middleware.RequestLog()}, extra...)
	return router.New(handler.New(app).Endpoints(), chain...)
}
func HTTP(address string, handler http.Handler) *http.Server {
	return &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
}
func Shutdown(ctx context.Context, server *http.Server) error { return server.Shutdown(ctx) }
