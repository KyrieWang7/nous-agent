package middleware

import (
	"github.com/KyrieWang7/nous-agent/platform-go/httpx"
	"github.com/gin-gonic/gin"
)

func CORS() gin.HandlerFunc {
	return httpx.CORS([]string{"*"}, "Content-Type, Last-Event-ID", "GET, POST, PATCH, DELETE, OPTIONS")
}
