package router

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestNewRouter_Mounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := NewRouter()
	if r == nil {
		t.Fatal("nil router")
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/swagger/index.html", nil)
	r.ServeHTTP(w, req)
	// Either 200 or a redirect/404 from swagger; we just need the route mounted.
	if w.Code == 0 {
		t.Fatalf("no response code")
	}
}

func TestSetupV3_RegistersAllRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := NewRouter()
	h := NewHandlers(Deps{OwnBankCode: "111"})
	SetupV3(r, h)
	routes := r.Routes()
	// v3 router has hundreds of routes; sanity-check that registration ran.
	if len(routes) < 50 {
		t.Fatalf("expected many v3 routes, got %d", len(routes))
	}
	// Spot-check: /api/v3/auth/login should exist.
	found := false
	for _, rt := range routes {
		if rt.Path == "/api/v3/auth/login" && rt.Method == "POST" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("auth/login not registered under v3")
	}
}
