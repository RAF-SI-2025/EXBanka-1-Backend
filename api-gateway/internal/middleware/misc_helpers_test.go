package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetCallerPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("absent returns nil", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		require.Nil(t, GetCallerPermissions(c))
	})

	t.Run("wrong type returns nil", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("permissions", "not-a-slice")
		require.Nil(t, GetCallerPermissions(c))
	})

	t.Run("present returns the list", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("permissions", []string{"a.b", "c.d"})
		require.Equal(t, []string{"a.b", "c.d"}, GetCallerPermissions(c))
	})
}

func TestFaultHeaderForwarder_NoOpInProduction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(FaultHeaderForwarder())
	reached := false
	r.GET("/x", func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Saga-Force-Fail", "true")
	r.ServeHTTP(w, req)

	// saga.FaultsEnabled is a compile-time false in this build, so the
	// middleware is a transparent pass-through.
	require.True(t, reached)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestRateLimitKeyFuncs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var clientKey, routeKey string
	r := gin.New()
	r.GET("/things/:id", func(c *gin.Context) {
		clientKey = ClientIPKey(c)
		routeKey = RouteIPKey(c)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/things/7", nil)
	req.RemoteAddr = "203.0.113.9:5555"
	r.ServeHTTP(w, req)

	require.Equal(t, "203.0.113.9", clientKey)
	// RouteIPKey appends the matched route template, not the concrete path.
	require.Equal(t, "203.0.113.9|/things/:id", routeKey)
}
