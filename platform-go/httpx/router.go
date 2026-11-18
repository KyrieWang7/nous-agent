// Package httpx provides the common Gin boundary used by both Go services.
package httpx

import (
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

var modeOnce sync.Once
var segment = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)(\.\.\.)?\}`)

func New(middleware ...gin.HandlerFunc) *gin.Engine {
	modeOnce.Do(func() { gin.SetMode(gin.ReleaseMode) })
	engine := gin.New()
	engine.HandleMethodNotAllowed = true
	// Direct peer addresses are used until a deployment explicitly configures trusted proxies.
	_ = engine.SetTrustedProxies(nil)
	engine.Use(middleware...)
	return engine
}

// Handle adapts existing strict HTTP/SSE handlers to Gin. Path values remain a
// transport contract; the application and Agent kernel do not import Gin.
func Handle(engine *gin.Engine, method, path string, handler http.HandlerFunc) {
	pattern := segment.ReplaceAllStringFunc(path, func(s string) string {
		match := segment.FindStringSubmatch(s)
		if match[2] != "" {
			return "*" + match[1]
		}
		return ":" + match[1]
	})
	adapted := func(ctx *gin.Context) {
		for _, param := range ctx.Params {
			ctx.Request.SetPathValue(param.Key, strings.TrimPrefix(param.Value, "/"))
		}
		handler(ctx.Writer, ctx.Request)
	}
	engine.Handle(method, pattern, adapted)
	if method == http.MethodGet {
		engine.Handle(http.MethodHead, pattern, adapted)
	}
}
