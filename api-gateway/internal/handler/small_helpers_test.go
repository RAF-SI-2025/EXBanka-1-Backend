package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestParseInt64Query(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	captured := int64(-1)
	r.GET("/x", func(c *gin.Context) {
		captured = parseInt64Query(c, "id")
	})
	// missing → 0
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	if captured != 0 {
		t.Fatalf("missing: %d", captured)
	}
	// numeric → parsed
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x?id=42", nil))
	if captured != 42 {
		t.Fatalf("parsed: %d", captured)
	}
	// non-numeric → 0
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x?id=abc", nil))
	if captured != 0 {
		t.Fatalf("invalid: %d", captured)
	}
}

func TestEmptyIfNil(t *testing.T) {
	out := emptyIfNil[int](nil)
	if out == nil || len(out) != 0 {
		t.Fatalf("nil case: %#v", out)
	}
	in := []int{1, 2, 3}
	if got := emptyIfNil(in); len(got) != 3 {
		t.Fatalf("non-nil case: %#v", got)
	}
}
