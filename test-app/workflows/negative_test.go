//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/helpers"
)

// --- WF15: Negative & Error Path Tests ---

// --- Authorization Failures ---

func TestNeg_NoTokenOnProtectedRoutes(t *testing.T) {
	t.Parallel()
	c := newClient()
	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/employees"},
		{"GET", "/api/v1/employees/1"},
		{"POST", "/api/v1/employees"},
		{"GET", "/api/v1/clients"},
		{"POST", "/api/v1/clients"},
		{"GET", "/api/v1/accounts"},
		{"POST", "/api/v1/accounts"},
		{"GET", "/api/v1/roles"},
		{"POST", "/api/v1/roles"},
		{"GET", "/api/v1/permissions"},
		{"GET", "/api/v1/fees"},
		{"POST", "/api/v1/fees"},
		{"GET", "/api/v1/bank-accounts"},
		{"POST", "/api/v1/bank-accounts"},
		{"GET", "/api/v1/interest-rate-tiers"},
		{"GET", "/api/v1/bank-margins"},
		{"GET", "/api/v1/loan-requests"},
		{"GET", "/api/v1/loans"},
		{"GET", "/api/v1/currencies"},
	}

	for _, r := range routes {
		t.Run(r.method+"_"+r.path, func(t *testing.T) {
			var resp *client.Response
			var err error
			switch r.method {
			case "GET":
				resp, err = c.GET(r.path)
			case "POST":
				resp, err = c.POST(r.path, map[string]interface{}{})
			}
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if resp.StatusCode != 401 {
				t.Fatalf("%s %s: expected 401, got %d", r.method, r.path, resp.StatusCode)
			}
		})
	}
}

func TestNeg_InvalidTokenOnProtectedRoutes(t *testing.T) {
	t.Parallel()
	c := newClient()
	c.SetToken("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.invalid.payload")

	resp, err := c.GET("/api/v1/employees")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 with invalid token, got %d", resp.StatusCode)
	}
}

func TestNeg_ExpiredToken(t *testing.T) {
	t.Parallel()
	// Use a clearly expired JWT (we can't easily forge one, but an obviously malformed one should fail)
	c := newClient()
	c.SetToken("expired.token.here")
	resp, err := c.GET("/api/v1/employees")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// --- Client-Only Routes with Employee Token ---

func TestNeg_EmployeeCannotAccessClientOnlyRoutes(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	clientOnlyRoutes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/me"},
		{"POST", "/api/v1/me/payments"},
		{"POST", "/api/v1/me/transfers"},
		{"POST", "/api/v1/me/loan-requests"},
		{"POST", "/api/v1/me/cards/virtual"},
		{"POST", "/api/v1/me/cards/1/pin"},
		{"POST", "/api/v1/me/cards/1/verify-pin"},
		{"POST", "/api/v1/me/cards/1/temporary-block"},
		{"POST", "/api/v1/me/payment-recipients"},
	}

	for _, r := range clientOnlyRoutes {
		t.Run(r.method+"_"+r.path, func(t *testing.T) {
			var resp *client.Response
			var err error
			switch r.method {
			case "GET":
				resp, err = c.GET(r.path)
			case "POST":
				resp, err = c.POST(r.path, map[string]interface{}{})
			}
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			// Should be 401 or 403 (not 200)
			if resp.StatusCode == 200 || resp.StatusCode == 201 {
				t.Fatalf("%s %s: expected auth failure, got %d", r.method, r.path, resp.StatusCode)
			}
		})
	}
}

// --- Validation Failures ---

func TestNeg_EmployeeCreateMissingRequiredFields(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/v1/employees", map[string]interface{}{
		// Missing most required fields
		"first_name": "Only",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 201 {
		t.Fatal("expected validation failure with missing fields")
	}
}

func TestNeg_ClientCreateInvalidEmail(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/v1/clients", map[string]interface{}{
		"first_name": "Bad",
		"last_name":  "Email",
		"email":      "not-an-email",
		"jmbg":       helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 201 {
		t.Fatal("expected validation failure with invalid email")
	}
}

// --- Resource Not Found ---

func TestNeg_GetNonExistentResources(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	notFoundRoutes := []string{
		"/api/v1/employees/999999",
		"/api/v1/clients/999999",
		"/api/v1/accounts/999999",
		"/api/v1/cards/999999",
		"/api/v1/loans/999999",
		"/api/v1/roles/999999",
	}

	for _, path := range notFoundRoutes {
		t.Run("GET_"+path, func(t *testing.T) {
			resp, err := c.GET(path)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if resp.StatusCode != 404 {
				t.Fatalf("GET %s: expected 404, got %d", path, resp.StatusCode)
			}
		})
	}
}

// --- Public routes should be accessible ---

func TestNeg_PublicRoutesNoAuth(t *testing.T) {
	t.Parallel()
	c := newClient()

	// Auth routes (public)
	resp, err := c.POST("/api/v1/auth/password/reset-request", map[string]string{"email": "test@test.com"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 401 {
		t.Fatal("password reset request should be public")
	}

	// Exchange rates (public)
	resp, err = c.GET("/api/v1/exchange/rates")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 401 {
		t.Fatal("exchange rates should be public")
	}
}
