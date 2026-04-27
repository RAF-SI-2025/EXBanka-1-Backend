//go:build integration
// +build integration

package workflows

import (
	"net/http"
	"testing"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
)

// TestLogin_Wrong_Password_Returns_401_Unauthorized — the canonical case.
// Verifies that a bcrypt mismatch on a known account surfaces as 401
// "unauthorized" (collapsed with email-not-found to prevent enumeration).
func TestLogin_Wrong_Password_Returns_401_Unauthorized(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, _, email := setupActivatedClient(t, adminC)

	resp := postLoginRaw(t, email, "WrongPassword12!")
	assertHTTP(t, resp, http.StatusUnauthorized, "unauthorized")
}

// TestLogin_Missing_Email_Returns_401_Unauthorized — collapses to the SAME
// code as wrong-password by design (prevents email enumeration).
func TestLogin_Missing_Email_Returns_401_Unauthorized(t *testing.T) {
	t.Parallel()
	resp := postLoginRaw(t, "missing-"+helpers.RandomEmail(), "AnyPass12!")
	assertHTTP(t, resp, http.StatusUnauthorized, "unauthorized")
}

// TestLogin_Locked_Account_Returns_403_Forbidden — typed sentinel
// distinguishes lockout from wrong-password. Auth-service locks accounts
// after 5 failed attempts within 15 min; we drive 5 wrong attempts then
// the 6th hits the active lock (regardless of password correctness).
func TestLogin_Locked_Account_Returns_403_Forbidden(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, _, email := setupActivatedClient(t, adminC)

	// 5 wrong-password attempts trigger the lockout window.
	for i := 0; i < 5; i++ {
		_ = postLoginRaw(t, email, "WrongAttempt12!")
	}
	// 6th attempt — lockout active regardless of password correctness.
	resp := postLoginRaw(t, email, "WrongAttempt12!")
	assertHTTP(t, resp, http.StatusForbidden, "forbidden")
}

// TestLogin_Pending_Account_Returns_409_BusinessRule — distinct from
// not-found and locked. Creating a client without consuming the activation
// token leaves the account in "pending" status; login surfaces FailedPrecondition.
func TestLogin_Pending_Account_Returns_409_BusinessRule(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	email := createUnactivatedClient(t, adminC)

	resp := postLoginRaw(t, email, "AnyPass12!")
	assertHTTP(t, resp, http.StatusConflict, "business_rule_violation")
}

// --- helpers ---

// postLoginRaw POSTs to the v1 login endpoint without parsing or asserting.
// Returns the parsed Response (StatusCode + Body) so callers can inspect both.
func postLoginRaw(t *testing.T, email, password string) *client.Response {
	t.Helper()
	c := newClient()
	resp, err := c.POST("/api/v3/auth/login", map[string]string{
		"email":    email,
		"password": password,
	})
	if err != nil {
		t.Fatalf("postLoginRaw: %v", err)
	}
	return resp
}

// assertHTTP asserts the response status and the gateway error.code field
// match the expected values. Gateway error body shape is
// {"error": {"code": "...", "message": "..."}}.
func assertHTTP(t *testing.T, resp *client.Response, status int, errCode string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("assertHTTP: expected status %d, got %d. Body: %s", status, resp.StatusCode, string(resp.RawBody))
	}
	if resp.Body == nil {
		t.Fatalf("assertHTTP: response body was not JSON. Raw: %s", string(resp.RawBody))
	}
	errMap, ok := resp.Body["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("assertHTTP: response body missing 'error' object. Body: %s", string(resp.RawBody))
	}
	gotCode, _ := errMap["code"].(string)
	if gotCode != errCode {
		t.Fatalf("assertHTTP: expected error.code %q, got %q. Body: %s", errCode, gotCode, string(resp.RawBody))
	}
}

// createUnactivatedClient creates a bank client via POST /api/v3/clients but
// deliberately does NOT pull the activation token from Kafka. The auth-service
// Account row is created with status=pending; logging in will surface
// ErrAccountPending → FailedPrecondition → 409 business_rule_violation.
//
// Returns the email used.
func createUnactivatedClient(t *testing.T, adminC *client.APIClient) string {
	t.Helper()
	email := helpers.RandomEmail()
	resp, err := adminC.POST("/api/v3/clients", map[string]interface{}{
		"first_name":    helpers.RandomName("Pen"),
		"last_name":     helpers.RandomName("Ding"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "other",
		"email":         email,
		"phone":         helpers.RandomPhone(),
		"address":       "Pending Test St",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("createUnactivatedClient: POST /api/v3/clients: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	return email
}
