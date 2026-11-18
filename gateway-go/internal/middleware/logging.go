package middleware

import (
	"github.com/KyrieWang7/nous-agent/platform-go/httpx"
	"github.com/gin-gonic/gin"
)

func RequestLog() gin.HandlerFunc { return httpx.RequestLog() }
