package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/handler"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/repository"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/router"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/service"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/delegation"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/metadata"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/plugincontrol"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/replay"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/runmanager"
	"github.com/KyrieWang7/nous-agent/platform-go/httpx"
	"github.com/gin-gonic/gin"
)

type Options struct {
	Plugins           plugincontrol.Controller
	Subagents         delegation.SessionStore
	Middleware        []gin.HandlerFunc
	Agent             runmanager.Agent
	Store             repository.Store
	Bus               runtime.Bus
	EventStore        runtime.EventStore
	HeartbeatInterval time.Duration
	Logger            *slog.Logger
	Registry          *runtime.Registry
	OwnRegistry       bool
	AllowedTools      []string
	Pricer            *runtime.Pricer
	Approvals         *runtime.ApprovalManager
	Questions         *runtime.QuestionManager
	Snapshots         replay.SnapshotStore
	MetadataStore     metadata.Store
}

// Server composes the HTTP boundary and owns its application lifecycle.
type Server struct {
	app     *service.Service
	handler http.Handler
}

func New(opts Options) (*Server, error) {
	app, err := service.New(service.Options{Plugins: opts.Plugins,
		Subagents: opts.Subagents, Agent: opts.Agent, Store: opts.Store, Bus: opts.Bus, EventStore: opts.EventStore,
		HeartbeatInterval: opts.HeartbeatInterval, Logger: opts.Logger, Registry: opts.Registry, OwnRegistry: opts.OwnRegistry,
		AllowedTools: opts.AllowedTools, Pricer: opts.Pricer, Approvals: opts.Approvals, Questions: opts.Questions,
		Snapshots: opts.Snapshots, MetadataStore: opts.MetadataStore, ProjectEvent: handler.ProjectEvent,
	})
	if err != nil {
		return nil, err
	}
	chain := append([]gin.HandlerFunc{middleware.CORS(), httpx.RequestLog()}, opts.Middleware...)
	return &Server{app: app, handler: router.New(handler.New(app).Endpoints(), chain...)}, nil
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }
func (s *Server) Close()                                           { s.app.Close() }

func HTTP(address string, handler http.Handler) *http.Server {
	return &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
}
