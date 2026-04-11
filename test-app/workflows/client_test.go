//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
	"github.com/exbanka/test-app/internal/kafka"
)

// --- WF3: Client CRUD + Activation ---

func TestClient_CreateMultipleClients(t *testing.T) {
	c := loginAsAdmin(t)
	el := kafka.NewEventListener(cfg.KafkaBrokers)
	el.Start()
	defer el.Stop()

	genders := []string{"male", "female", "other"}
	for _, gender := range genders {
		t.Run("gender_"+gender, func(t *testing.T) {
			resp, err := c.POST("/api/clients", map[string]interface{}{
				"first_name":    helpers.RandomName("Client"),
				"last_name":     helpers.RandomName(gender),
				"date_of_birth": helpers.DateOfBirthUnix(),
				"gender":        gender,
				"email":         helpers.RandomEmail(),
				"phone":         helpers.RandomPhone(),
				"address":       "100 Client St",
				"jmbg":          helpers.RandomJMBG(),
			})
			if err != nil {
				t.Fatalf("create client error: %v", err)
			}
			helpers.RequireStatus(t, resp, 201)
			helpers.RequireField(t, resp, "id")
		})
	}

	// Verify at least one client.created event
	_, found := el.WaitForEvent("client.created", 10*time.Second, nil)
	if !found {
		t.Fatal("expected client.created Kafka event")
	}
}

func TestClient_CreateWithInvalidJMBG(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/clients", map[string]interface{}{
		"first_name":    "Bad",
		"last_name":     "Client",
		"date_of_birth": helpers.DateOfBirthUnix(),
		"email":         helpers.RandomEmail(),
		"jmbg":          "123",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 201 {
		t.Fatal("expected failure with invalid JMBG")
	}
}

func TestClient_CreateWithMissingRequiredFields(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)

	// Missing email
	resp, err := c.POST("/api/clients", map[string]interface{}{
		"first_name": "No",
		"last_name":  "Email",
		"jmbg":       helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 201 {
		t.Fatal("expected failure with missing email")
	}
}

func TestClient_ListAndGet(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)

	// Create a client first
	email := helpers.RandomEmail()
	createResp, err := c.POST("/api/clients", map[string]interface{}{
		"first_name":    "List",
		"last_name":     "Test",
		"date_of_birth": helpers.DateOfBirthUnix(),
		"email":         email,
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	clientID := helpers.GetNumberField(t, createResp, "id")

	// List
	resp, err := c.GET("/api/clients")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	// Get by ID
	resp, err = c.GET(fmt.Sprintf("/api/clients/%d", int(clientID)))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "id")
}

func TestClient_Update(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)

	createResp, err := c.POST("/api/clients", map[string]interface{}{
		"first_name":    "Update",
		"last_name":     "Client",
		"date_of_birth": helpers.DateOfBirthUnix(),
		"email":         helpers.RandomEmail(),
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	clientID := helpers.GetNumberField(t, createResp, "id")

	resp, err := c.PUT(fmt.Sprintf("/api/clients/%d", int(clientID)), map[string]interface{}{
		"last_name": "UpdatedClient",
		"phone":     helpers.RandomPhone(),
		"address":   "New Address 123",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestClient_GetNonExistent(t *testing.T) {
	t.Parallel()
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/clients/999999")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
