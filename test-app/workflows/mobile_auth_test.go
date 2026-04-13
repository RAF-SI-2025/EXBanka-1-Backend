//go:build integration

package workflows

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	kafkalib "github.com/segmentio/kafka-go"

	"github.com/exbanka/test-app/internal/helpers"
)

// --- Mobile Auth Endpoints ---

func TestMobileAuth_RequestActivation_ValidEmail(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)

	// Create an activated client first
	_, _, _, email := setupActivatedClient(t, adminC)

	c := newClient()
	resp, err := c.POST("/api/v1/mobile/auth/request-activation", map[string]interface{}{
		"email": email,
	})
	if err != nil {
		t.Fatalf("request activation error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "success")
}

func TestMobileAuth_RequestActivation_InvalidEmail(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.POST("/api/v1/mobile/auth/request-activation", map[string]interface{}{
		"email": "not-an-email",
	})
	if err != nil {
		t.Fatalf("request activation error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for invalid email, got %d", resp.StatusCode)
	}
}

func TestMobileAuth_RequestActivation_NonexistentEmail(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.POST("/api/v1/mobile/auth/request-activation", map[string]interface{}{
		"email": "nonexistent_user_xyz@example.com",
	})
	if err != nil {
		t.Fatalf("request activation error: %v", err)
	}
	// API returns 200 even for nonexistent emails (prevents email enumeration)
	helpers.RequireStatus(t, resp, 200)
}

func TestMobileAuth_Activate_InvalidCode(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, _, email := setupActivatedClient(t, adminC)

	c := newClient()
	// Request activation code
	reqResp, err := c.POST("/api/v1/mobile/auth/request-activation", map[string]interface{}{
		"email": email,
	})
	if err != nil {
		t.Fatalf("request activation error: %v", err)
	}
	helpers.RequireStatus(t, reqResp, 200)

	// Try with wrong code
	actResp, err := c.POST("/api/v1/mobile/auth/activate", map[string]interface{}{
		"email":       email,
		"code":        "000000",
		"device_name": "Test Device",
	})
	if err != nil {
		t.Fatalf("activate error: %v", err)
	}
	if actResp.StatusCode == 200 {
		t.Fatalf("expected error for invalid code, got 200")
	}
}

func TestMobileAuth_Activate_MissingFields(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.POST("/api/v1/mobile/auth/activate", map[string]interface{}{
		"email": "test@example.com",
		// missing code and device_name
	})
	if err != nil {
		t.Fatalf("activate error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for missing fields, got %d", resp.StatusCode)
	}
}

func TestMobileAuth_Activate_CodeWrongLength(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.POST("/api/v1/mobile/auth/activate", map[string]interface{}{
		"email":       "test@example.com",
		"code":        "123",
		"device_name": "Test Device",
	})
	if err != nil {
		t.Fatalf("activate error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for wrong code length, got %d", resp.StatusCode)
	}
}

func TestMobileAuth_ActivateFullFlow(t *testing.T) {
	// Not parallel: uses Kafka scanning which requires dedicated reader
	adminC := loginAsAdmin(t)
	_, _, _, email := setupActivatedClient(t, adminC)

	c := newClient()

	// Step 1: Request activation
	reqResp, err := c.POST("/api/v1/mobile/auth/request-activation", map[string]interface{}{
		"email": email,
	})
	if err != nil {
		t.Fatalf("request activation error: %v", err)
	}
	helpers.RequireStatus(t, reqResp, 200)

	// Step 2: Scan Kafka for the mobile activation code
	code := scanKafkaForMobileActivationCode(t, email)

	// Step 3: Activate with the code
	actResp, err := c.POST("/api/v1/mobile/auth/activate", map[string]interface{}{
		"email":       email,
		"code":        code,
		"device_name": "Integration Test Device",
	})
	if err != nil {
		t.Fatalf("activate error: %v", err)
	}
	helpers.RequireStatus(t, actResp, 200)
	helpers.RequireField(t, actResp, "access_token")
	helpers.RequireField(t, actResp, "refresh_token")
	helpers.RequireField(t, actResp, "device_id")
	helpers.RequireField(t, actResp, "device_secret")
}

func TestMobileAuth_Refresh_MissingDeviceID(t *testing.T) {
	t.Parallel()
	c := newClient()
	resp, err := c.POST("/api/v1/mobile/auth/refresh", map[string]interface{}{
		"refresh_token": "some-fake-token",
	})
	if err != nil {
		t.Fatalf("refresh error: %v", err)
	}
	// Should fail — missing X-Device-ID header
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for missing X-Device-ID, got %d", resp.StatusCode)
	}
}

func TestMobileAuth_DeviceEndpoints_RequireAuth(t *testing.T) {
	t.Parallel()
	c := newClient()

	// GET /api/mobile/device should require mobile auth
	resp, err := c.GET("/api/v1/mobile/device")
	if err != nil {
		t.Fatalf("get device info error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 for unauthenticated GET /api/mobile/device, got %d", resp.StatusCode)
	}

	// POST /api/mobile/device/deactivate
	resp, err = c.POST("/api/v1/mobile/device/deactivate", nil)
	if err != nil {
		t.Fatalf("deactivate error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 for unauthenticated deactivate, got %d", resp.StatusCode)
	}

	// POST /api/mobile/device/transfer
	resp, err = c.POST("/api/v1/mobile/device/transfer", map[string]interface{}{
		"email": "test@example.com",
	})
	if err != nil {
		t.Fatalf("transfer error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 for unauthenticated transfer, got %d", resp.StatusCode)
	}
}

func TestMobileAuth_DeviceEndpoints_RejectBrowserToken(t *testing.T) {
	t.Parallel()
	// A browser (employee) token should NOT work for mobile device endpoints
	c := loginAsAdmin(t)

	resp, err := c.GET("/api/v1/mobile/device")
	if err != nil {
		t.Fatalf("get device info error: %v", err)
	}
	// Should be 401 or 403 — browser token lacks device_type=mobile
	if resp.StatusCode == 200 {
		t.Fatalf("expected mobile device endpoint to reject browser token, got 200")
	}
}

// scanKafkaForMobileActivationCode reads Kafka for the MOBILE_ACTIVATION email to the given address.
func scanKafkaForMobileActivationCode(t *testing.T, email string) string {
	t.Helper()
	r := kafkalib.NewReader(kafkalib.ReaderConfig{
		Brokers:     []string{cfg.KafkaBrokers},
		Topic:       "notification.send-email",
		Partition:   0,
		StartOffset: kafkalib.FirstOffset,
		MaxWait:     500 * time.Millisecond,
	})
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var latestCode string
	for {
		msg, err := r.ReadMessage(ctx)
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
		if body.To == email && body.EmailType == "MOBILE_ACTIVATION" {
			if code := body.Data["code"]; code != "" {
				latestCode = code
			}
		}
	}

	if latestCode == "" {
		t.Fatalf("scanKafkaForMobileActivationCode: no MOBILE_ACTIVATION code found for %s within 15s", email)
	}
	return latestCode
}
