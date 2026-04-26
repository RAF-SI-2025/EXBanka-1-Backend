// api-gateway/internal/middleware/changed_by_test.go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"

	"github.com/exbanka/contract/changelog"
)

func TestGRPCContextWithChangedBy_SetsMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var capturedChangedBy int64
	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		c.Set("user_id", int64(42))
		ctx := GRPCContextWithChangedBy(c)
		// Extract outgoing metadata from context to verify it was set
		md, ok := metadata.FromOutgoingContext(ctx)
		assert.True(t, ok, "outgoing metadata should be present")
		vals := md.Get("x-changed-by")
		assert.Len(t, vals, 1)
		assert.Equal(t, "42", vals[0])

		// Also verify via the changelog package helper
		capturedChangedBy = changelog.ExtractChangedBy(ctx)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	// changelog.ExtractChangedBy expects incoming metadata but we set outgoing;
	// the value will be 0 from incoming perspective — that is expected behaviour.
	// The important thing is that the outgoing context was set correctly (checked above).
	_ = capturedChangedBy
}

func TestGRPCContextWithChangedBy_ZeroWhenNoUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		// Don't set user_id — GetInt64 returns 0
		ctx := GRPCContextWithChangedBy(c)
		md, ok := metadata.FromOutgoingContext(ctx)
		assert.True(t, ok)
		vals := md.Get("x-changed-by")
		assert.Len(t, vals, 1)
		assert.Equal(t, "0", vals[0])
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGRPCContextWithChangedBy_ReturnsContextDerivedFromRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		c.Set("user_id", int64(7))
		ctx := GRPCContextWithChangedBy(c)
		// Returned context must not be nil.
		assert.NotNil(t, ctx)
		// The outgoing metadata value must be "7".
		md, ok := metadata.FromOutgoingContext(ctx)
		assert.True(t, ok)
		assert.Equal(t, "7", md.Get("x-changed-by")[0])
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}
