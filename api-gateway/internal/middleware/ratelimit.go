package middleware

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RateLimitRule is one fixed-window bucket. A Limit <= 0 disables the rule
// (the middleware becomes a pass-through), so an unconfigured limiter never
// blocks traffic.
type RateLimitRule struct {
	Name   string        // bucket namespace; keeps distinct rules from colliding
	Limit  int           // max requests per window per key
	Window time.Duration // window length
}

// fixedWindowScript atomically increments the window counter and sets the
// expiry only on the first increment, so the window is fixed (not sliding):
// the first request in a window starts the clock; the window resets when the
// key expires.
var fixedWindowScript = redis.NewScript(`
local c = redis.call('INCR', KEYS[1])
if c == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return c
`)

// RateLimit returns a gin middleware enforcing one fixed-window rule, keyed by
// keyFn(c). On exceed it writes 429 with the standard error envelope and a
// Retry-After header. It FAILS OPEN: any Redis error (timeout, outage) lets the
// request through — a limiter outage must never become a global outage for a
// banking gateway.
func RateLimit(rdb *redis.Client, rule RateLimitRule, keyFn func(*gin.Context) string) gin.HandlerFunc {
	windowMillis := strconv.FormatInt(rule.Window.Milliseconds(), 10)
	return func(c *gin.Context) {
		if rule.Limit <= 0 || rdb == nil {
			c.Next()
			return
		}
		key := "ratelimit:" + rule.Name + ":" + keyFn(c)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 100*time.Millisecond)
		defer cancel()
		n, err := fixedWindowScript.Run(ctx, rdb, []string{key}, windowMillis).Int64()
		if err != nil {
			c.Next() // fail open
			return
		}
		if n > int64(rule.Limit) {
			c.Header("Retry-After", strconv.Itoa(int(rule.Window.Seconds())))
			abortWithError(c, http.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
			return
		}
		c.Next()
	}
}

// ClientIPKey keys a bucket by client IP (per-IP limiting).
func ClientIPKey(c *gin.Context) string { return c.ClientIP() }

// RouteIPKey keys by client IP + matched route template, so a strict bucket on
// one route does not consume another route's budget.
func RouteIPKey(c *gin.Context) string { return c.ClientIP() + "|" + c.FullPath() }
