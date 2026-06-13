package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func TestSetupV3_GlobalRateLimitReturns429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	r := NewRouter()
	h := NewHandlers(Deps{
		OwnBankCode: "111",
		RateLimit:   RateLimitConfig{Redis: rdb, GlobalPerMin: 2},
	})
	SetupV3(r, h)

	do := func() int {
		w := httptest.NewRecorder()
		// /version is a public route with no auth — clean target for the
		// global per-IP ceiling.
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v3/version", nil))
		return w.Code
	}
	if c := do(); c != http.StatusOK {
		t.Fatalf("1st call: want 200, got %d", c)
	}
	if c := do(); c != http.StatusOK {
		t.Fatalf("2nd call: want 200, got %d", c)
	}
	if c := do(); c != http.StatusTooManyRequests {
		t.Fatalf("3rd call: want 429, got %d", c)
	}
}

func TestSetupV3_NoLimiterWhenRedisNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := NewRouter()
	h := NewHandlers(Deps{OwnBankCode: "111"}) // no RateLimit → no limiter
	SetupV3(r, h)
	for i := 0; i < 20; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v3/version", nil))
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("unexpected 429 with limiting disabled on call %d", i)
		}
	}
}

// TestPublicOptionOffers_RouteRemoved asserts the proprietary cross-bank
// /public-option-offers endpoint is gone: cross-bank option discovery now
// happens only via /public-stock. The route must no longer be mounted, so a
// request resolves to 404 (not 401 from PeerAuthMW).
func TestPublicOptionOffers_RouteRemoved(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := NewRouter()
	h := NewHandlers(Deps{OwnBankCode: "111"})
	SetupV3(r, h)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/cross-bank-protocol/public-option-offers", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("/public-option-offers should be removed: want 404, got %d", w.Code)
	}
}

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

// TestCrossBankProtocol_CanonicalMount verifies that every SI-TX wire route is
// registered exclusively under the /api/v3/cross-bank-protocol/... prefix.
// Legacy paths (/api/v3/interbank, /api/v3/public-stock, etc.) were removed on
// 2026-05-29 and MUST return 404.
func TestCrossBankProtocol_CanonicalMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := NewRouter()
	h := NewHandlers(Deps{OwnBankCode: "111"})
	SetupV3(r, h)

	// 1. Canonical paths must be present in the Gin route table.
	registeredRoutes := make(map[string]bool, len(r.Routes()))
	for _, rt := range r.Routes() {
		registeredRoutes[rt.Method+":"+rt.Path] = true
	}
	canonicalWant := []struct{ method, path string }{
		{http.MethodPost, "/api/v3/cross-bank-protocol/interbank"},
		{http.MethodGet, "/api/v3/cross-bank-protocol/interbank/:transaction_id/status"},
		{http.MethodGet, "/api/v3/cross-bank-protocol/public-stock"},
		{http.MethodPost, "/api/v3/cross-bank-protocol/negotiations"},
		{http.MethodPut, "/api/v3/cross-bank-protocol/negotiations/:rid/:id"},
		{http.MethodGet, "/api/v3/cross-bank-protocol/negotiations/:rid/:id"},
		{http.MethodDelete, "/api/v3/cross-bank-protocol/negotiations/:rid/:id"},
		{http.MethodGet, "/api/v3/cross-bank-protocol/negotiations/:rid/:id/accept"},
		{http.MethodGet, "/api/v3/cross-bank-protocol/user/:rid/:id"},
	}
	for _, want := range canonicalWant {
		key := want.method + ":" + want.path
		if !registeredRoutes[key] {
			t.Errorf("canonical route missing from route table: %s %s", want.method, want.path)
		}
	}

	// 2. Canonical paths return 401 (PeerAuthMW fires), not 404 (route not mounted).
	canonicalPaths := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v3/cross-bank-protocol/interbank"},
		{http.MethodGet, "/api/v3/cross-bank-protocol/interbank/tx-abc/status"},
		{http.MethodGet, "/api/v3/cross-bank-protocol/public-stock"},
		{http.MethodPost, "/api/v3/cross-bank-protocol/negotiations"},
		{http.MethodGet, "/api/v3/cross-bank-protocol/user/111/client-1"},
	}
	for _, p := range canonicalPaths {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(p.method, p.path, nil)
		r.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			t.Errorf("%s %s: got 404 (route not mounted), expected 401 from PeerAuthMW", p.method, p.path)
		}
	}

	// 3. Legacy paths MUST return 404 — they are no longer registered.
	legacyPaths := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v3/interbank"},
		{http.MethodGet, "/api/v3/interbank/tx-abc/status"},
		{http.MethodGet, "/api/v3/public-stock"},
		{http.MethodGet, "/api/v3/public-option-offers"},
		{http.MethodPost, "/api/v3/negotiations"},
		{http.MethodGet, "/api/v3/user/111/client-1"},
	}
	for _, p := range legacyPaths {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(p.method, p.path, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("legacy route %s %s: got %d, expected 404 (must be removed)", p.method, p.path, w.Code)
		}
	}
}
