package httpx

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

var rateScript = redis.NewScript(`
local hits=redis.call('INCR',KEYS[1])
if hits==1 then redis.call('PEXPIRE',KEYS[1],ARGV[1]) end
return {hits,redis.call('PTTL',KEYS[1])}
`)

// RateLimit uses an atomic, shared fixed window. Redis failures return 503;
// the default deployment does not install this optional middleware.
func RateLimit(service string, client redis.UniversalClient, requests int, window time.Duration) (gin.HandlerFunc, error) {
	if client == nil || requests <= 0 || window < time.Millisecond {
		return nil, fmt.Errorf("HTTP rate limiter requires Redis, positive requests and window >= 1ms")
	}
	return func(c *gin.Context) {
		if c.FullPath() == "/ok" || c.FullPath() == "/health" {
			c.Next()
			return
		}
		key := fmt.Sprintf("nous:http:rate:%s:%x", service, sha256.Sum256([]byte(c.ClientIP())))
		values, err := rateScript.Run(c.Request.Context(), client, []string{key}, window.Milliseconds()).Slice()
		if err != nil || len(values) != 2 {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "rate limiter unavailable"})
			return
		}
		count, countOK := values[0].(int64)
		ttl, ttlOK := values[1].(int64)
		if !countOK || !ttlOK {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "rate limiter unavailable"})
			return
		}
		if count > int64(requests) {
			seconds := (ttl + 999) / 1000
			if seconds < 1 {
				seconds = 1
			}
			c.Header("Retry-After", strconv.FormatInt(seconds, 10))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}, nil
}
