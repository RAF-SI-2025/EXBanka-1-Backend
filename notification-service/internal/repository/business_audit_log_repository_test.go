package repository

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/exbanka/notification-service/internal/model"
)

func newBusinessAuditDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.BusinessAuditLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestBusinessAuditLogRepository_ListAll_Filters(t *testing.T) {
	db := newBusinessAuditDB(t)
	r := NewBusinessAuditLogRepository(db)
	base := time.Now().UTC()

	seed := []model.BusinessAuditLog{
		{Action: "limit.set", ActorID: 1, TargetType: "employee", TargetID: "7", Detail: "max_single=5000", Timestamp: base},
		{Action: "order.approve", ActorID: 2, TargetType: "order", TargetID: "42", Timestamp: base.Add(time.Second)},
		{Action: "limit.set", ActorID: 2, TargetType: "employee", TargetID: "9", Timestamp: base.Add(2 * time.Second)},
	}
	for i := range seed {
		if err := db.Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Filter by action.
	rows, total, err := r.ListAll(BusinessAuditLogFilters{Action: "limit.set"}, 1, 50)
	if err != nil {
		t.Fatalf("list by action: %v", err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("action=limit.set: want 2 rows, got total=%d len=%d", total, len(rows))
	}

	// Filter by actor.
	_, total, err = r.ListAll(BusinessAuditLogFilters{ActorID: 2}, 1, 50)
	if err != nil {
		t.Fatalf("list by actor: %v", err)
	}
	if total != 2 {
		t.Fatalf("actor=2: want 2, got %d", total)
	}

	// Combined action + actor.
	rows, total, err = r.ListAll(BusinessAuditLogFilters{Action: "limit.set", ActorID: 2}, 1, 50)
	if err != nil {
		t.Fatalf("list combined: %v", err)
	}
	if total != 1 || rows[0].TargetID != "9" {
		t.Fatalf("combined: want 1 row target=9, got total=%d rows=%+v", total, rows)
	}

	// Filter by target_type.
	_, total, err = r.ListAll(BusinessAuditLogFilters{TargetType: "order"}, 1, 50)
	if err != nil {
		t.Fatalf("list by target_type: %v", err)
	}
	if total != 1 {
		t.Fatalf("target_type=order: want 1, got %d", total)
	}

	// Ordered by timestamp DESC.
	rows, _, err = r.ListAll(BusinessAuditLogFilters{}, 1, 50)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(rows) != 3 || rows[0].TargetID != "9" {
		t.Fatalf("expected newest first (target 9), got %+v", rows)
	}
}
