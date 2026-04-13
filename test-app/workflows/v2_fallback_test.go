//go:build integration
// +build integration

package workflows

import (
	"encoding/json"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestV2_FallsBackToV1 verifies that routes not explicitly defined under /api/v2
// fall back to the /api/v1 implementation and return byte-identical responses.
//
// This test uses GET /api/v2/securities/stocks (a read-only, no-destructive-side-effect
// endpoint) so it is safe to run in the normal integration suite.
func TestV2_FallsBackToV1(t *testing.T) {
	admin := loginAsAdmin(t)

	v1, err := admin.GET("/api/v1/securities/stocks?page=1&page_size=3")
	if err != nil {
		t.Fatalf("v1 call: %v", err)
	}
	helpers.RequireStatus(t, v1, 200)

	v2, err := admin.GET("/api/v2/securities/stocks?page=1&page_size=3")
	if err != nil {
		t.Fatalf("v2 call: %v", err)
	}
	helpers.RequireStatus(t, v2, 200)

	// Compare the two responses — they should be byte-identical because v2
	// falls back to v1 for routes not explicitly overridden.
	var v1Decoded, v2Decoded interface{}
	if err := json.Unmarshal(v1.RawBody, &v1Decoded); err != nil {
		t.Fatalf("decode v1 body: %v", err)
	}
	if err := json.Unmarshal(v2.RawBody, &v2Decoded); err != nil {
		t.Fatalf("decode v2 body: %v", err)
	}

	// Re-marshal to canonical JSON form for comparison (eliminates whitespace/key-order differences).
	a, err := json.Marshal(v1Decoded)
	if err != nil {
		t.Fatalf("re-marshal v1: %v", err)
	}
	b, err := json.Marshal(v2Decoded)
	if err != nil {
		t.Fatalf("re-marshal v2: %v", err)
	}

	if string(a) != string(b) {
		t.Fatalf("v2 fallback should return identical body to v1\nv1: %s\nv2: %s", a, b)
	}
	t.Logf("v2 fallback confirmed identical to v1 (%d bytes)", len(a))
}
