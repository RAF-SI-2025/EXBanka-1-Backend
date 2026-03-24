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
	c := newClient()
	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/employees"},
		{"GET", "/api/employees/1"},
		{"POST", "/api/employees"},
		{"GET", "/api/clients"},
		{"POST", "/api/clients"},
		{"GET", "/api/accounts"},
		{"POST", "/api/accounts"},
		{"GET", "/api/roles"},
		{"POST", "/api/roles"},
		{"GET", "/api/permissions"},
		{"GET", "/api/fees"},
		{"POST", "/api/fees"},
		{"GET", "/api/bank-accounts"},
		{"POST", "/api/bank-accounts"},
		{"GET", "/api/interest-rate-tiers"},
		{"GET", "/api/bank-margins"},
		{"GET", "/api/loan-requests"},
		{"GET", "/api/loans"},
		{"GET", "/api/currencies"},
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
	c := newClient()
	c.SetToken("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.invalid.payload")

	resp, err := c.GET("/api/employees")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 with invalid token, got %d", resp.StatusCode)
	}
}

func TestNeg_ExpiredToken(t *testing.T) {
	// Use a clearly expired JWT (we can't easily forge one, but an obviously malformed one should fail)
	c := newClient()
	c.SetToken("expired.token.here")
	resp, err := c.GET("/api/employees")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// --- Client-Only Routes with Employee Token ---

func TestNeg_EmployeeCannotAccessClientOnlyRoutes(t *testing.T) {
	c := loginAsAdmin(t)
	clientOnlyRoutes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/me"},
		{"POST", "/api/me/payments"},
		{"POST", "/api/me/transfers"},
		{"POST", "/api/me/verification"},
		{"POST", "/api/me/loan-requests"},
		{"POST", "/api/me/cards/virtual"},
		{"POST", "/api/me/cards/1/pin"},
		{"POST", "/api/me/cards/1/verify-pin"},
		{"POST", "/api/me/cards/1/temporary-block"},
		{"POST", "/api/me/payment-recipients"},
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
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/employees", map[string]interface{}{
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
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/clients", map[string]interface{}{
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
	c := loginAsAdmin(t)
	notFoundRoutes := []string{
		"/api/employees/999999",
		"/api/clients/999999",
		"/api/accounts/999999",
		"/api/cards/999999",
		"/api/loans/999999",
		"/api/roles/999999",
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
	c := newClient()

	// Auth routes (public)
	resp, err := c.POST("/api/auth/password/reset-request", map[string]string{"email": "test@test.com"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 401 {
		t.Fatal("password reset request should be public")
	}

	// Exchange rates (public)
	resp, err = c.GET("/api/exchange-rates")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 401 {
		t.Fatal("exchange rates should be public")
	}
}
