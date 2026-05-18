package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	authpb "github.com/exbanka/contract/authpb"
)

func runAuthMW(t *testing.T, m *mockAuthClient, req *http.Request) int {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", AuthMiddleware(m), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{})
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestAuthMiddleware_MissingHeaderExtra(t *testing.T) {
	if got := runAuthMW(t, &mockAuthClient{}, httptest.NewRequest("GET", "/x", nil)); got != http.StatusUnauthorized {
		t.Fatalf("got %d", got)
	}
}

func TestAuthMiddleware_BadFormat(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "NotBearer abc")
	if got := runAuthMW(t, &mockAuthClient{}, req); got != http.StatusUnauthorized {
		t.Fatalf("got %d", got)
	}
}

func TestAuthMiddleware_ValidateErr(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer t")
	mock := &mockAuthClient{err: errors.New("rpc down")}
	if got := runAuthMW(t, mock, req); got != http.StatusUnauthorized {
		t.Fatalf("got %d", got)
	}
}

func TestAuthMiddleware_TokenInvalid(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer t")
	mock := &mockAuthClient{resp: &authpb.ValidateTokenResponse{Valid: false}}
	if got := runAuthMW(t, mock, req); got != http.StatusUnauthorized {
		t.Fatalf("got %d", got)
	}
}

func TestAnyAuthMiddleware_BadFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	mock := &mockAuthClient{}
	r.GET("/x", AnyAuthMiddleware(mock), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{})
	})
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "garbage")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", w.Code)
	}
}

func TestAnyAuthMiddleware_MissingHeaderExtra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	mock := &mockAuthClient{}
	r.GET("/x", AnyAuthMiddleware(mock), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{})
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", w.Code)
	}
}

func TestAnyAuthMiddleware_ValidateErr(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	mock := &mockAuthClient{err: errors.New("rpc down")}
	r.GET("/x", AnyAuthMiddleware(mock), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{})
	})
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer t")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", w.Code)
	}
}
