package httpx

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func CORS(origins []string, headers, methods string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(origins))
	for _, origin := range origins {
		allowed[origin] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if allowed["*"] {
			c.Header("Access-Control-Allow-Origin", "*")
		} else if allowed[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Add("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Headers", headers)
		c.Header("Access-Control-Allow-Methods", methods)
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
func RequestLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.InfoContext(c.Request.Context(), "HTTP request", "method", c.Request.Method, "route", c.FullPath(), "status", c.Writer.Status(), "duration", time.Since(start))
	}
}
