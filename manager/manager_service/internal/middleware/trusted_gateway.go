package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

const TokenHeader = "X-Nous-Manager-Token"

func TrustedGateway(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader(TokenHeader)
		if subtle.ConstantTimeCompare([]byte(header), []byte(token)) != 1 || header == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"detail": "Manager requires the trusted gateway"})
			return
		}
		c.Request.Header.Del(TokenHeader)
		c.Next()
	}
}
