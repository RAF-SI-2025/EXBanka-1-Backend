//go:build integration

package workflows

import (
	"fmt"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
	"github.com/exbanka/test-app/internal/kafka"
)

// --- WF2: Employee CRUD + Activation ---

func TestEmployee_CreateWithBasicRole(t *testing.T) {
	c := loginAsAdmin(t)
	el := kafka.NewEventListener(cfg.KafkaBrokers)
	el.Start()
	defer el.Stop()

	resp, err := c.POST("/api/employees", map[string]interface{}{
		"first_name":    helpers.RandomName("Emp"),
		"last_name":     helpers.RandomName("Basic"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         helpers.RandomEmail(),
		"phone":         helpers.RandomPhone(),
		"address":       "123 Test St",
		"username":      helpers.RandomUsername(),
		"position":      "Teller",
		"department":    "Operations",
		"role":          "EmployeeBasic",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("create employee error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	helpers.RequireField(t, resp, "id")
	helpers.RequireFieldEquals(t, resp, "role", "EmployeeBasic")

	// Verify Kafka event
	evt, found := el.WaitForEvent("user.employee-created", 10*time.Second, nil)
	if !found {
		t.Fatal("expected user.employee-created Kafka event within 10s")
	}
	if evt.Value["email"] == nil {
		t.Fatal("event missing email field")
	}

	// Verify activation email event
	_, found = el.WaitForEvent("notification.send-email", 15*time.Second, func(e kafka.Event) bool {
		if e.Value == nil {
			return false
		}
		return e.Value["email_type"] == "ACTIVATION"
	})
	if !found {
		t.Fatal("expected notification.send-email ACTIVATION event within 15s")
	}
}

func TestEmployee_CreateWithAgentRole(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/employees", map[string]interface{}{
		"first_name":    helpers.RandomName("Emp"),
		"last_name":     helpers.RandomName("Agent"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "female",
		"email":         helpers.RandomEmail(),
		"phone":         helpers.RandomPhone(),
		"address":       "456 Test Ave",
		"username":      helpers.RandomUsername(),
		"position":      "Agent",
		"department":    "Trading",
		"role":          "EmployeeAgent",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	helpers.RequireFieldEquals(t, resp, "role", "EmployeeAgent")
}

func TestEmployee_CreateWithSupervisorRole(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/employees", map[string]interface{}{
		"first_name":    helpers.RandomName("Emp"),
		"last_name":     helpers.RandomName("Supervisor"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         helpers.RandomEmail(),
		"phone":         helpers.RandomPhone(),
		"address":       "789 Test Blvd",
		"username":      helpers.RandomUsername(),
		"position":      "Supervisor",
		"department":    "Management",
		"role":          "EmployeeSupervisor",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
}

func TestEmployee_CreateWithAdminRole(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.POST("/api/employees", map[string]interface{}{
		"first_name":    helpers.RandomName("Emp"),
		"last_name":     helpers.RandomName("Admin"),
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "other",
		"email":         helpers.RandomEmail(),
		"phone":         helpers.RandomPhone(),
		"address":       "321 Admin St",
		"username":      helpers.RandomUsername(),
		"position":      "Administrator",
		"department":    "IT",
		"role":          "EmployeeAdmin",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
}

func TestEmployee_CreateWithInvalidJMBG(t *testing.T) {
	c := loginAsAdmin(t)
	// Too short
	resp, err := c.POST("/api/employees", map[string]interface{}{
		"first_name":    "Bad",
		"last_name":     "JMBG",
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         helpers.RandomEmail(),
		"username":      helpers.RandomUsername(),
		"role":          "EmployeeBasic",
		"jmbg":          "12345",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 201 {
		t.Fatal("expected failure with invalid JMBG")
	}
}

func TestEmployee_CreateWithDuplicateEmail(t *testing.T) {
	c := loginAsAdmin(t)
	email := helpers.RandomEmail()

	// First creation
	resp, err := c.POST("/api/employees", map[string]interface{}{
		"first_name":    "First",
		"last_name":     "Employee",
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         email,
		"username":      helpers.RandomUsername(),
		"role":          "EmployeeBasic",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)

	// Duplicate email
	resp, err = c.POST("/api/employees", map[string]interface{}{
		"first_name":    "Duplicate",
		"last_name":     "Employee",
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "female",
		"email":         email,
		"username":      helpers.RandomUsername(),
		"role":          "EmployeeBasic",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode == 201 {
		t.Fatal("expected failure with duplicate email")
	}
}

func TestEmployee_ListAndGet(t *testing.T) {
	c := loginAsAdmin(t)

	// List employees
	resp, err := c.GET("/api/employees")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	// Get specific employee (ID 1 = seeded admin)
	resp, err = c.GET("/api/employees/1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
	helpers.RequireField(t, resp, "id")
	helpers.RequireField(t, resp, "email")
}

func TestEmployee_Update(t *testing.T) {
	c := loginAsAdmin(t)

	// Create employee first
	email := helpers.RandomEmail()
	createResp, err := c.POST("/api/employees", map[string]interface{}{
		"first_name":    "Update",
		"last_name":     "Test",
		"date_of_birth": helpers.DateOfBirthUnix(),
		"gender":        "male",
		"email":         email,
		"username":      helpers.RandomUsername(),
		"role":          "EmployeeBasic",
		"jmbg":          helpers.RandomJMBG(),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	empID := helpers.GetNumberField(t, createResp, "id")

	// Update
	resp, err := c.PUT(fmt.Sprintf("/api/employees/%d", int(empID)), map[string]interface{}{
		"last_name":  "Updated",
		"department": "HR",
		"position":   "Manager",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)
}

func TestEmployee_GetNonExistent(t *testing.T) {
	c := loginAsAdmin(t)
	resp, err := c.GET("/api/employees/999999")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
