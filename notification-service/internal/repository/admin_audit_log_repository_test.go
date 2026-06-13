package repository

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/exbanka/notification-service/internal/model"
)

func newAdminAuditDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.AdminAuditLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestAdminAuditLogRepository_ListAll_FiltersAndOrder(t *testing.T) {
	db := newAdminAuditDB(t)
	r := NewAdminAuditLogRepository(db)
	base := time.Now().UTC().Truncate(time.Second)

	seed := []model.AdminAuditLog{
		{Action: "pause", Service: "credit-service", CronName: "installment", EmployeeID: 1, Reason: "maint", Timestamp: base},
		{Action: "resume", Service: "credit-service", CronName: "installment", EmployeeID: 2, Timestamp: base.Add(time.Second)},
		{Action: "pause", Service: "card-service", CronName: "block-sweep", EmployeeID: 2, Timestamp: base.Add(2 * time.Second)},
	}
	for i := range seed {
		if err := db.Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// No filters → all 3, newest first (block-sweep).
	rows, total, err := r.ListAll(AdminAuditLogFilters{}, 1, 50)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if total != 3 || len(rows) != 3 {
		t.Fatalf("want 3 rows, got total=%d len=%d", total, len(rows))
	}
	if rows[0].CronName != "block-sweep" {
		t.Errorf("expected newest first (block-sweep), got %q", rows[0].CronName)
	}

	// Filter by action.
	_, total, err = r.ListAll(AdminAuditLogFilters{Action: "pause"}, 1, 50)
	if err != nil {
		t.Fatalf("by action: %v", err)
	}
	if total != 2 {
		t.Fatalf("action=pause: want 2, got %d", total)
	}

	// Filter by actor (employee_id).
	_, total, err = r.ListAll(AdminAuditLogFilters{ActorID: 2}, 1, 50)
	if err != nil {
		t.Fatalf("by actor: %v", err)
	}
	if total != 2 {
		t.Fatalf("actor=2: want 2, got %d", total)
	}

	// Combined action + actor → only the card-service pause by employee 2.
	rows, total, err = r.ListAll(AdminAuditLogFilters{Action: "pause", ActorID: 2}, 1, 50)
	if err != nil {
		t.Fatalf("combined: %v", err)
	}
	if total != 1 || rows[0].Service != "card-service" {
		t.Fatalf("combined: want 1 card-service row, got total=%d rows=%+v", total, rows)
	}
}

func TestAdminAuditLogRepository_ListAll_SinceUntilAndPaging(t *testing.T) {
	db := newAdminAuditDB(t)
	r := NewAdminAuditLogRepository(db)
	// Seed in local time so it matches the bound the repo builds with
	// time.Unix(...) (which is local); sqlite compares the serialized strings.
	base := time.Now().Truncate(time.Second)

	for i := 0; i < 5; i++ {
		row := model.AdminAuditLog{
			Action: "trigger", Service: "exchange-service", CronName: "fx-sync",
			EmployeeID: 1, Timestamp: base.Add(time.Duration(i) * time.Minute),
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Since lower bound excludes the first two rows (keep i=2,3,4).
	_, total, err := r.ListAll(AdminAuditLogFilters{Since: base.Add(2 * time.Minute).Unix()}, 1, 50)
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if total != 3 {
		t.Fatalf("since: want 3, got %d", total)
	}

	// Until upper bound keeps i=0,1,2.
	_, total, err = r.ListAll(AdminAuditLogFilters{Until: base.Add(2 * time.Minute).Unix()}, 1, 50)
	if err != nil {
		t.Fatalf("until: %v", err)
	}
	if total != 3 {
		t.Fatalf("until: want 3, got %d", total)
	}

	// Pagination: total still reflects the full set, page returns a slice.
	page1, total, err := r.ListAll(AdminAuditLogFilters{}, 1, 2)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if total != 5 || len(page1) != 2 {
		t.Fatalf("page1: want total=5 len=2, got total=%d len=%d", total, len(page1))
	}
	page3, _, err := r.ListAll(AdminAuditLogFilters{}, 3, 2)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("page3: want 1 trailing row, got %d", len(page3))
	}
}

func TestBusinessAuditLogRepository_ListAll_SinceUntil(t *testing.T) {
	db := newBusinessAuditDB(t)
	r := NewBusinessAuditLogRepository(db)
	base := time.Now().Truncate(time.Second)

	for i := 0; i < 4; i++ {
		row := model.BusinessAuditLog{
			Action: "limit.set", ActorID: 1, TargetType: "employee", TargetID: "1",
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Since keeps i=1,2,3.
	_, total, err := r.ListAll(BusinessAuditLogFilters{Since: base.Add(time.Minute).Unix()}, 1, 50)
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if total != 3 {
		t.Fatalf("since: want 3, got %d", total)
	}

	// Until keeps i=0,1.
	_, total, err = r.ListAll(BusinessAuditLogFilters{Until: base.Add(time.Minute).Unix()}, 1, 50)
	if err != nil {
		t.Fatalf("until: %v", err)
	}
	if total != 2 {
		t.Fatalf("until: want 2, got %d", total)
	}
}
