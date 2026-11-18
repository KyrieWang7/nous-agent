package handler

import (
	"net/http"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/router"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/service"
)

type testHTTPServer struct {
	*Handler
	route http.Handler
}

func newHTTPServer(opts service.Options) (*testHTTPServer, error) {
	opts.ProjectEvent = ProjectEvent
	app, err := service.New(opts)
	if err != nil {
		return nil, err
	}
	h := New(app)
	return &testHTTPServer{Handler: h, route: router.New(h.Endpoints(), middleware.CORS())}, nil
}
func (s *testHTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.route.ServeHTTP(w, r) }
func (s *testHTTPServer) Close()                                           { s.app.Close() }
