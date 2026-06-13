//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- GET /api/v3/version (public) ---

func TestVersion_Public(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.GET("/api/v3/version")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	// The version must be a non-empty semver-ish string so front-end
	// developers can tell which backend build they are talking to.
	version := helpers.GetStringField(t, resp, "version")
	if version == "" {
		t.Fatalf("expected non-empty version, got empty string")
	}
}
