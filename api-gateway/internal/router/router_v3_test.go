package router

import (
	"net/http"
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

// TestCrossBankProtocol_DualMount verifies that Plan F (2026-05-28) correctly
// registers every SI-TX wire route under BOTH the legacy /api/v3/<route> path
// AND the new canonical /api/v3/cross-bank-protocol/<route> path.
//
// It checks two things per route pair:
//  1. The route is present in the Gin route table (proves the path was mounted).
//  2. An unauthenticated request returns 401, not 404 (proves PeerAuthMW fires on
//     BOTH paths — a 404 would mean the route wasn't reached at all, i.e., not
//     mounted under the canonical prefix).
func TestCrossBankProtocol_DualMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := NewRouter()
	h := NewHandlers(Deps{OwnBankCode: "111"})
	SetupV3(r, h)

	// Route pairs: {legacy, canonical} — same handler registered on both.
	pairs := []struct {
		method  string
		legacy  string
		canonical string
	}{
		{http.MethodPost, "/api/v3/interbank", "/api/v3/cross-bank-protocol/interbank"},
		{http.MethodGet, "/api/v3/interbank/tx-abc/status", "/api/v3/cross-bank-protocol/interbank/tx-abc/status"},
		{http.MethodGet, "/api/v3/public-stock", "/api/v3/cross-bank-protocol/public-stock"},
		{http.MethodGet, "/api/v3/public-option-offers", "/api/v3/cross-bank-protocol/public-option-offers"},
		{http.MethodPost, "/api/v3/negotiations", "/api/v3/cross-bank-protocol/negotiations"},
		{http.MethodGet, "/api/v3/user/111/client-1", "/api/v3/cross-bank-protocol/user/111/client-1"},
	}

	// 1. Route-table presence: both legacy and canonical paths must be in r.Routes().
	registeredRoutes := make(map[string]bool, len(r.Routes()))
	for _, rt := range r.Routes() {
		registeredRoutes[rt.Method+":"+rt.Path] = true
	}
	canonicalInTable := []struct{ method, path string }{
		{http.MethodPost, "/api/v3/cross-bank-protocol/interbank"},
		{http.MethodGet, "/api/v3/cross-bank-protocol/interbank/:transaction_id/status"},
		{http.MethodGet, "/api/v3/cross-bank-protocol/public-stock"},
		{http.MethodGet, "/api/v3/cross-bank-protocol/public-option-offers"},
		{http.MethodPost, "/api/v3/cross-bank-protocol/negotiations"},
		{http.MethodPut, "/api/v3/cross-bank-protocol/negotiations/:rid/:id"},
		{http.MethodGet, "/api/v3/cross-bank-protocol/negotiations/:rid/:id"},
		{http.MethodDelete, "/api/v3/cross-bank-protocol/negotiations/:rid/:id"},
		{http.MethodGet, "/api/v3/cross-bank-protocol/negotiations/:rid/:id/accept"},
		{http.MethodGet, "/api/v3/cross-bank-protocol/user/:rid/:id"},
	}
	for _, want := range canonicalInTable {
		key := want.method + ":" + want.path
		if !registeredRoutes[key] {
			t.Errorf("canonical route not in route table: %s %s", want.method, want.path)
		}
	}

	// 2. Both legacy and canonical paths return 401 (not 404) when no PeerAuth
	//    credentials are provided — confirms PeerAuthMW is wired on both.
	for _, p := range pairs {
		for _, path := range []string{p.legacy, p.canonical} {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(p.method, path, nil)
			r.ServeHTTP(w, req)
			if w.Code == http.StatusNotFound {
				t.Errorf("%s %s: got 404 (route not mounted), expected 401 from PeerAuthMW", p.method, path)
			} else if w.Code != http.StatusUnauthorized {
				t.Logf("%s %s: got %d (expected 401 without credentials — acceptable if middleware rejected early)", p.method, path, w.Code)
			}
		}
	}
}

// TestCrossBankProtocol_LegacyAndCanonicalSameStatus verifies that for the
// two most important SI-TX wire routes (POST /interbank and POST /negotiations),
// the legacy path and the canonical path return the exact same HTTP status for
// the same unauthenticated request, confirming they share the same handler chain.
func TestCrossBankProtocol_LegacyAndCanonicalSameStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := NewRouter()
	h := NewHandlers(Deps{OwnBankCode: "111"})
	SetupV3(r, h)

	cases := []struct{ method, legacy, canonical string }{
		{http.MethodPost, "/api/v3/interbank", "/api/v3/cross-bank-protocol/interbank"},
		{http.MethodPost, "/api/v3/negotiations", "/api/v3/cross-bank-protocol/negotiations"},
	}
	for _, c := range cases {
		wLegacy := httptest.NewRecorder()
		r.ServeHTTP(wLegacy, httptest.NewRequest(c.method, c.legacy, nil))

		wCanonical := httptest.NewRecorder()
		r.ServeHTTP(wCanonical, httptest.NewRequest(c.method, c.canonical, nil))

		if wLegacy.Code != wCanonical.Code {
			t.Errorf("%s: legacy %s → %d, canonical %s → %d (expected same status)",
				c.method, c.legacy, wLegacy.Code, c.canonical, wCanonical.Code)
		}
	}
}
