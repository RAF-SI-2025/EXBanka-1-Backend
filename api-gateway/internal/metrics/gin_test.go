package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGinMiddleware_Matched(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GinMiddleware())
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
}

func TestGinMiddleware_Unmatched(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GinMiddleware())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/never", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d", w.Code)
	}
}
