package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const requestIDKey = "request_id"

// RequestLogger assigns a request id (honoring an inbound X-Request-Id, else a
// fresh UUID), exposes it on the gin context and the response header, and emits
// one structured slog line per request after it completes. Pair with
// gin.Recovery() — it replaces gin's default text Logger.
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-Id")
		if rid == "" {
			rid = uuid.NewString()
		}
		c.Set(requestIDKey, rid)
		c.Header("X-Request-Id", rid)

		start := time.Now()
		c.Next()

		slog.Info("http_request",
			"request_id", rid,
			"method", c.Request.Method,
			"path", c.FullPath(),
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
			"principal_id", c.GetInt64("principal_id"),
		)
	}
}

// RequestID returns the per-request id set by RequestLogger ("" if absent).
func RequestID(c *gin.Context) string { return c.GetString(requestIDKey) }
