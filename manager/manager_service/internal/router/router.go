package router

import (
	"net/http"
	"strings"

	"github.com/KyrieWang7/nous-agent/manager/manager_service/internal/handler/ideas"
	"github.com/KyrieWang7/nous-agent/platform-go/httpx"
	"github.com/gin-gonic/gin"
)

func New(ideas *ideas.Handler, legacy http.Handler, trustedGateway gin.HandlerFunc) *gin.Engine {
	engine := httpx.New()
	engine.RedirectTrailingSlash = false
	engine.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "manager"})
	})
	engine.Use(trustedGateway)
	engine.GET("/api/ideas", ideas.List)
	engine.POST("/api/ideas", ideas.Create)
	engine.PUT("/api/ideas/:iid", ideas.Update)
	engine.DELETE("/api/ideas/:iid", ideas.Delete)

	engine.NoMethod(func(c *gin.Context) {
		c.JSON(http.StatusMethodNotAllowed, gin.H{"detail": "Method Not Allowed"})
	})
	engine.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if !strings.HasPrefix(path, "/api/ideas/") {
			legacy.ServeHTTP(c.Writer, c.Request)
			return
		}
		suffix := strings.TrimPrefix(path, "/api/ideas/")
		id, trailingSlash := strings.CutSuffix(suffix, "/")
		if suffix == "" || (trailingSlash && id != "" && !strings.Contains(id, "/")) {
			target := *c.Request.URL
			target.Path = strings.TrimSuffix(target.Path, "/")
			target.RawPath = strings.TrimSuffix(target.RawPath, "/")
			c.Redirect(http.StatusTemporaryRedirect, target.RequestURI())
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"detail": "Not Found"})
	})
	return engine
}
