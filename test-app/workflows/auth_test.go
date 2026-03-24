//go:build integration

package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	kafkalib "github.com/segmentio/kafka-go"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/internal/config"
	"github.com/exbanka/test-app/internal/helpers"
)

var cfg *config.Config

func TestMain(m *testing.M) {
	cfg = config.Load()
	ensureAdminActivated()
	os.Exit(m.Run())
}

// ensureAdminActivated bootstraps and activates the admin account if needed.
//
// Flow:
//  1. Try login — if the admin is already active, return immediately.
//  2. Call POST /api/bootstrap (with retry) to create the admin employee and trigger
//     an ACTIVATION email onto the notification.send-email Kafka topic.
//  3. Scan that topic (from offset 0) for the token and activate the account.
func ensureAdminActivated() {
	adminEmail := cfg.AdminEmail()
	adminPassword := cfg.Password

	c := newClient()
	if resp, err := c.Login(adminEmail, adminPassword); err == nil && resp.StatusCode == 200 {
		return // already active
	}

	fmt.Println("[test-app] Admin not active — calling bootstrap endpoint...")

	// Retry bootstrap until the API gateway and upstream services are ready.
	deadline := time.Now().Add(3 * time.Minute)
	bootstrapped := false
	for time.Now().Before(deadline) {
		resp, err := c.POST("/api/bootstrap", map[string]string{
			"secret": cfg.BootstrapSecret,
			"email":  adminEmail,
		})
		if err == nil && resp.StatusCode == 200 {
			bootstrapped = true
			break
		}
		if err == nil && resp.StatusCode == 404 {
			log.Fatalf("[test-app] FATAL: bootstrap endpoint returned 404. " +
				"Ensure BOOTSTRAP_SECRET is set in the api-gateway environment and " +
				"matches the BOOTSTRAP_SECRET used by test-app (default: dev-bootstrap-secret).")
		}
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}
		fmt.Printf("[test-app] bootstrap not ready yet (status=%d, err=%v), retrying in 3s...\n", statusCode, err)
		time.Sleep(3 * time.Second)
	}
	if !bootstrapped {
		log.Fatalf("[test-app] FATAL: bootstrap endpoint did not return 200 within timeout.")
	}

	fmt.Println("[test-app] Bootstrap succeeded — scanning Kafka for activation token...")

	// No GroupID: use direct partition reader so Kafka never redirects us to
	// the group coordinator (which advertises the internal kafka:9092 address
	// unreachable from outside Docker).
	r := kafkalib.NewReader(kafkalib.ReaderConfig{
		Brokers:     []string{cfg.KafkaBrokers},
		Topic:       "notification.send-email",
		Partition:   0,
		StartOffset: kafkalib.FirstOffset,
		MaxWait:     500 * time.Millisecond,
	})
	defer r.Close()

	outerCtx, outerCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer outerCancel()

	var latestToken string
	for {
		// Once a token is found, only wait 1 more second for any newer message.
		readCtx := outerCtx
		readCancel := func() {}
		if latestToken != "" {
			readCtx, readCancel = context.WithTimeout(outerCtx, 1*time.Second)
		}
		msg, err := r.ReadMessage(readCtx)
		readCancel()
		if err != nil {
			break
		}
		var body struct {
			To        string            `json:"to"`
			EmailType string            `json:"email_type"`
			Data      map[string]string `json:"data"`
		}
		if json.Unmarshal(msg.Value, &body) != nil {
			continue
		}
		if body.To == adminEmail && body.EmailType == "ACTIVATION" {
			if token := body.Data["token"]; token != "" {
				latestToken = token // keep scanning for the latest token
			}
		}
	}

	if latestToken == "" {
		log.Fatalf("[test-app] FATAL: no ACTIVATION token found in Kafka for admin email %q. "+
			"Check that auth-service received the bootstrap call and published to notification.send-email.",
			adminEmail)
	}

	resp, err := c.ActivateAccount(latestToken, adminPassword)
	if err != nil || resp.StatusCode != 200 {
		body, _ := json.Marshal(resp.Body)
		fmt.Printf("[test-app] Warning: admin activation failed (status=%d, body=%s). Tests requiring admin login will fail.\n",
			resp.StatusCode, body)
		return
	}
	fmt.Println("[test-app] Admin account activated successfully.")
}

func newClient() *client.APIClient {
	return client.New(cfg.GatewayURL)
}

// loginAsAdmin authenticates as the seeded admin employee and returns an authenticated client.
func loginAsAdmin(t *testing.T) *client.APIClient {
	t.Helper()
	c := newClient()
	resp, err := c.Login(cfg.AdminEmail(), cfg.Password)
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
	resp, err := c.Login(cfg.AdminEmail(), cfg.Password)
	if err != nil {
		t.Fatalf("login error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "access_token")
	helpers.RequireField(t, resp, "refresh_token")
}

func TestAuth_LoginWithInvalidPassword(t *testing.T) {
	c := newClient()
	resp, err := c.POST("/api/auth/login", map[string]string{
		"email":    cfg.AdminEmail(),
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
	resp, err := c.POST("/api/auth/login", map[string]string{
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
	resp, err := c.POST("/api/auth/login", map[string]string{
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
	resp, err = c.POST("/api/auth/login", map[string]string{
		"email":    cfg.AdminEmail(),
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
	loginResp, err := c.Login(cfg.AdminEmail(), cfg.Password)
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
	loginResp, err := c.Login(cfg.AdminEmail(), cfg.Password)
	if err != nil {
		t.Fatalf("login error: %v", err)
	}
	helpers.RequireStatus(t, loginResp, 200)

	refreshToken := helpers.GetStringField(t, loginResp, "refresh_token")

	resp, err := c.POST("/api/auth/logout", map[string]string{
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
	resp, err := c.POST("/api/auth/password/reset-request", map[string]string{
		"email": cfg.AdminEmail(),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	// Request for non-existent email (should also succeed — no info leak)
	resp, err = c.POST("/api/auth/password/reset-request", map[string]string{
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
	resp, err := c.GET("/api/employees")
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
	resp, err := c.GET("/api/employees")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 Unauthorized, got %d", resp.StatusCode)
	}
}
