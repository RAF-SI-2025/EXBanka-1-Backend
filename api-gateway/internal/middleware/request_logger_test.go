package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestLogger_SetsRequestIDHeaderAndContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestLogger())
	var seen string
	r.GET("/x", func(c *gin.Context) {
		seen = RequestID(c)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))

	require.NotEmpty(t, seen)
	require.Equal(t, seen, w.Header().Get("X-Request-Id"))
}

func TestRequestLogger_HonorsInboundRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestLogger())
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Request-Id", "abc-123")
	r.ServeHTTP(w, req)

	require.Equal(t, "abc-123", w.Header().Get("X-Request-Id"))
}
