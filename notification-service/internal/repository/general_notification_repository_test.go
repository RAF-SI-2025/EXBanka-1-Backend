package repository

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/exbanka/notification-service/internal/model"
)

// TestGeneralNotification_RecipientScoping is the regression for the leak where a
// client's notifications (including personal data / codes) reached an
// employee/admin with the SAME numeric id. A client and an employee both with id
// 5 must see DISJOINT inboxes: the client sees only client + legacy rows, the
// employee sees only the shared employee inbox.
func TestGeneralNotification_RecipientScoping(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.GeneralNotification{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewGeneralNotificationRepository(db)

	must := func(n *model.GeneralNotification) {
		if err := r.Create(n); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	// client 5's activation code, an employee/bank row, and a legacy (empty) row.
	must(&model.GeneralNotification{UserID: 5, SystemType: "client", Type: "MOBILE_ACTIVATION", Title: "code", Message: "123456"})
	must(&model.GeneralNotification{UserID: 5, SystemType: "employee", Type: "X", Title: "emp", Message: "bank"})
	must(&model.GeneralNotification{UserID: 5, SystemType: "", Type: "Y", Title: "legacy", Message: "old"})

	// Client caller: client + legacy rows; NEVER the employee row.
	cItems, cTotal, err := r.ListByUser(5, "client", nil, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if cTotal != 2 {
		t.Fatalf("client should see 2 (client+legacy), got %d", cTotal)
	}
	for _, it := range cItems {
		if it.SystemType == "employee" {
			t.Errorf("LEAK: client saw an employee notification (%q)", it.Title)
		}
	}

	// Employee caller: ONLY the employee row; never the client's personal code.
	eItems, eTotal, err := r.ListByUser(5, "employee", nil, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if eTotal != 1 {
		t.Fatalf("employee should see 1 (employee inbox), got %d", eTotal)
	}
	for _, it := range eItems {
		if it.SystemType != "employee" {
			t.Errorf("LEAK: employee saw a non-employee notification (%q / %q)", it.Title, it.Message)
		}
	}

	// Unread counts respect the same scoping.
	if cc, _ := r.UnreadCount(5, "client"); cc != 2 {
		t.Errorf("client unread = %d, want 2", cc)
	}
	if ec, _ := r.UnreadCount(5, "employee"); ec != 1 {
		t.Errorf("employee unread = %d, want 1", ec)
	}

	// An employee marking-all-read must NOT touch the client's notifications.
	if _, err := r.MarkAllRead(5, "employee"); err != nil {
		t.Fatal(err)
	}
	if cc, _ := r.UnreadCount(5, "client"); cc != 2 {
		t.Errorf("after employee mark-all-read, client unread = %d, want still 2", cc)
	}
}
