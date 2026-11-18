package middleware

import (
	"github.com/KyrieWang7/nous-agent/platform-go/httpx"
	"github.com/gin-gonic/gin"
)

func CORS(origins []string) gin.HandlerFunc {
	return httpx.CORS(origins, "Content-Type, Authorization, Last-Event-ID", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
}
