//go:build integration

package workflows

import (
	"os"
	"testing"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/config"
	"github.com/exbanka/test-app/internal/helpers"
)

var cfg *config.Config

func TestMain(m *testing.M) {
	cfg = config.Load()
	os.Exit(m.Run())
}

func newClient() *client.APIClient {
	return client.New(cfg.GatewayURL)
}

// loginAsAdmin authenticates as the seeded admin employee and returns an authenticated client.
func loginAsAdmin(t *testing.T) *client.APIClient {
	t.Helper()
	c := newClient()
	resp, err := c.Login(cfg.AdminEmail, cfg.AdminPassword)
	if err != nil {
		t.Fatalf("admin login failed: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	return c
}

// loginAsClient authenticates as a client user using POST /api/auth/login.
func loginAsClient(t *testing.T, email, password string) *client.APIClient {
	t.Helper()
	c := newClient()
	resp, err := c.Login(email, password)
	if err != nil {
		t.Fatalf("client login failed: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	return c
}

// --- WF1: Authentication Lifecycle ---

func TestAuth_LoginWithValidCredentials(t *testing.T) {
	c := newClient()
	resp, err := c.Login(cfg.AdminEmail, cfg.AdminPassword)
	if err != nil {
		t.Fatalf("login error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "access_token")
	helpers.RequireField(t, resp, "refresh_token")
}

func TestAuth_LoginWithInvalidPassword(t *testing.T) {
	c := newClient()
	resp, err := c.POST("/api/v3/auth/login", map[string]string{
		"email":    cfg.AdminEmail,
		"password": "wrongpassword",
	})
	if err != nil {
		t.Fatalf("login error: %v", err)
	}
	if resp.StatusCode == 200 {
		t.Fatal("expected login to fail with wrong password")
	}
}

func TestAuth_LoginWithNonexistentEmail(t *testing.T) {
	c := newClient()
	resp, err := c.POST("/api/v3/auth/login", map[string]string{
		"email":    "nonexistent@exbanka.com",
		"password": "SomePass12",
	})
	if err != nil {
		t.Fatalf("login error: %v", err)
	}
	if resp.StatusCode == 200 {
		t.Fatal("expected login to fail with non-existent email")
	}
}

func TestAuth_LoginWithEmptyFields(t *testing.T) {
	c := newClient()

	// Empty email
	resp, err := c.POST("/api/v3/auth/login", map[string]string{
		"email":    "",
		"password": "SomePass12",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 200 {
		t.Fatal("expected failure with empty email")
	}

	// Empty password
	resp, err = c.POST("/api/v3/auth/login", map[string]string{
		"email":    cfg.AdminEmail,
		"password": "",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 200 {
		t.Fatal("expected failure with empty password")
	}
}

func TestAuth_RefreshToken(t *testing.T) {
	c := newClient()
	loginResp, err := c.Login(cfg.AdminEmail, cfg.AdminPassword)
	if err != nil {
		t.Fatalf("login error: %v", err)
	}
	helpers.RequireStatus(t, loginResp, 200)

	refreshToken := helpers.GetStringField(t, loginResp, "refresh_token")

	resp, err := c.RefreshToken(refreshToken)
	if err != nil {
		t.Fatalf("refresh error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "access_token")
	helpers.RequireField(t, resp, "refresh_token")

	// New access token should differ from old one
	newAccess := helpers.GetStringField(t, resp, "access_token")
	oldAccess := helpers.GetStringField(t, loginResp, "access_token")
	if newAccess == oldAccess {
		t.Log("warning: new access token same as old (possible if within same second)")
	}
}

func TestAuth_RefreshTokenInvalid(t *testing.T) {
	c := newClient()
	resp, err := c.RefreshToken("invalid-refresh-token")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 200 {
		t.Fatal("expected refresh to fail with invalid token")
	}
}

func TestAuth_Logout(t *testing.T) {
	c := newClient()
	loginResp, err := c.Login(cfg.AdminEmail, cfg.AdminPassword)
	if err != nil {
		t.Fatalf("login error: %v", err)
	}
	helpers.RequireStatus(t, loginResp, 200)

	refreshToken := helpers.GetStringField(t, loginResp, "refresh_token")

	resp, err := c.POST("/api/v3/auth/logout", map[string]string{
		"refresh_token": refreshToken,
	})
	if err != nil {
		t.Fatalf("logout error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	// Refresh token should no longer work after logout
	resp, err = c.RefreshToken(refreshToken)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 200 {
		t.Fatal("expected refresh to fail after logout")
	}
}

func TestAuth_PasswordResetRequest(t *testing.T) {
	c := newClient()

	// Request for existing email (should succeed silently)
	resp, err := c.POST("/api/v3/auth/password/reset-request", map[string]string{
		"email": cfg.AdminEmail,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	// Request for non-existent email (should also succeed — no info leak)
	resp, err = c.POST("/api/v3/auth/password/reset-request", map[string]string{
		"email": "nonexistent@exbanka.com",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestAuth_ActivateAccountInvalidToken(t *testing.T) {
	c := newClient()
	resp, err := c.ActivateAccount("invalid-token-1234", "NewPass12")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 200 {
		t.Fatal("expected activation to fail with invalid token")
	}
}

func TestAuth_AccessProtectedRouteWithoutToken(t *testing.T) {
	c := newClient() // no token set
	resp, err := c.GET("/api/v3/employees")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 Unauthorized, got %d", resp.StatusCode)
	}
}

func TestAuth_AccessProtectedRouteWithInvalidToken(t *testing.T) {
	c := newClient()
	c.SetToken("invalid-jwt-token")
	resp, err := c.GET("/api/v3/employees")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 Unauthorized, got %d", resp.StatusCode)
	}
}
