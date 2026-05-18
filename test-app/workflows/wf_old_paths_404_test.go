//go:build integration
// +build integration

package workflows

import (
	"net/http"
	"testing"
)

// TestOldAPIPaths_Return404 confirms that the v1 and v2 prefixes are gone
// after plan E (route consolidation to v3). Any unauthenticated GET to
// /api/v1/* or /api/v2/* must hit the v3 router's NoRoute catch-all and
// return 404. Smoke tests a representative slice (auth, clients,
// v2-only options route) — exhaustive coverage isn't needed; if these
// three return 404, all routes return 404.
func TestOldAPIPaths_Return404(t *testing.T) {
	t.Parallel()
	c := newClient()
	for _, path := range []string{
		"/api/v1/clients",
		"/api/v1/auth/login",
		"/api/v2/options/1/orders",
		"/api/v2/securities/stocks",
	} {
		resp, err := c.GET(path)
		if err != nil {
			t.Fatalf("%s: request error: %v", path, err)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: expected 404, got %d", path, resp.StatusCode)
		}
	}
}
