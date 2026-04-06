package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestLatestRoutesRewriteToV1(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// Register a known v1 route
	r.GET("/api/v1/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"version": "v1"})
	})

	// Register the latest alias
	SetupLatestRoutes(r)

	// Hit /api/latest/ping — should be served by /api/v1/ping
	req := httptest.NewRequest(http.MethodGet, "/api/latest/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "v1")
}

func TestLatestRoutes404ForUnknownPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// No v1 routes registered
	SetupLatestRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/api/latest/nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
