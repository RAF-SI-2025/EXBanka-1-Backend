package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

func TestRateLimit_AllowsUnderLimitThenBlocks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newTestRedis(t)

	r := gin.New()
	r.Use(RateLimit(rdb, RateLimitRule{Name: "test", Limit: 3, Window: time.Minute},
		func(c *gin.Context) string { return "fixed-key" }))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	do := func() int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.ServeHTTP(w, req)
		return w.Code
	}

	require.Equal(t, 200, do())
	require.Equal(t, 200, do())
	require.Equal(t, 200, do())
	require.Equal(t, 429, do()) // 4th in the window is blocked
}

func TestRateLimit_DisabledWhenLimitZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newTestRedis(t)
	r := gin.New()
	r.Use(RateLimit(rdb, RateLimitRule{Name: "off", Limit: 0, Window: time.Minute},
		func(c *gin.Context) string { return "k" }))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	for i := 0; i < 50; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, 200, w.Code)
	}
}

func TestRateLimit_FailOpenOnRedisError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr, err := miniredis.Run()
	require.NoError(t, err)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	mr.Close() // redis now unreachable

	r := gin.New()
	r.Use(RateLimit(rdb, RateLimitRule{Name: "t", Limit: 1, Window: time.Minute},
		func(c *gin.Context) string { return "k" }))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	require.Equal(t, 200, w.Code) // fail-open: a Redis outage must not lock everyone out
}
