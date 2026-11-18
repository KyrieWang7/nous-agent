package handler

import (
	"net/http"

	"github.com/KyrieWang7/nous-agent/gateway-go/internal/middleware"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/router"
	"github.com/KyrieWang7/nous-agent/gateway-go/internal/service"
)

type testHTTPServer struct {
	*Handler
	origins []string
}

func newTestHTTPServer(catalog service.Catalog, files service.Files, repository interface {
	service.Repository
	service.SwarmQueries
}, origins []string) *testHTTPServer {
	app := service.New(service.Options{Catalog: catalog, Files: files, Repository: repository, Swarm: repository})
	return &testHTTPServer{Handler: New(app), origins: origins}
}
func (s *testHTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	router.New(s.Endpoints(), middleware.CORS(s.origins), middleware.RequestLog()).ServeHTTP(w, r)
}
