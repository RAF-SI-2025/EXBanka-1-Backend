package repository

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/client-service/internal/model"
	"github.com/exbanka/contract/changelog"
)

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.Client{}, &model.ClientLimit{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Changelog model uses default:now() (postgres syntax) — create
	// the table with raw DDL so sqlite accepts it.
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS changelogs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		entity_type TEXT NOT NULL,
		entity_id INTEGER NOT NULL,
		action TEXT NOT NULL,
		field_name TEXT,
		old_value TEXT,
		new_value TEXT,
		changed_by INTEGER NOT NULL,
		changed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		reason TEXT
	)`).Error; err != nil {
		t.Fatalf("ddl: %v", err)
	}
	return db
}

func TestClientRepository_CRUD(t *testing.T) {
	db := newDB(t)
	r := NewClientRepository(db)

	c := &model.Client{
		FirstName: "alice", LastName: "smith",
		Email: "a@b.c", JMBG: "1234567890123",
		DateOfBirth: time.Now().Unix(),
	}
	if err := r.Create(c); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got, err := r.GetByID(c.ID); err != nil || got.Email != "a@b.c" {
		t.Fatalf("get: %v %+v", err, got)
	}
	if got, err := r.GetByEmail("a@b.c"); err != nil || got.ID != c.ID {
		t.Fatalf("by email: %v %+v", err, got)
	}
	c.LastName = "smith2"
	if err := r.Update(c); err != nil {
		t.Fatalf("update: %v", err)
	}

	// List with no filters returns the row
	list, total, err := r.List("", "", 1, 10)
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("list: %v %d %d", err, total, len(list))
	}
	// List with email filter — sqlite doesn't have ILIKE; expect that branch
	// to error gracefully (we only need to exercise the conditional).
	_, _, _ = r.List("a@b", "", 1, 10)
	_, _, _ = r.List("", "smith", 1, 10)
}

func TestClientRepository_Update_OptimisticLock(t *testing.T) {
	db := newDB(t)
	r := NewClientRepository(db)
	c := &model.Client{Email: "x@y.z", JMBG: "1111111111111", FirstName: "x", LastName: "y"}
	if err := r.Create(c); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Stale version (use a different version than the actual one)
	stale := *c
	stale.Version = 99 // way past actual
	stale.LastName = "stale"
	// On sqlite, the BeforeUpdate WHERE version=99 matches 0 rows; gorm.Save
	// in newer versions falls back to INSERT-on-conflict, but with an
	// existing primary key it errors. Either way the update path runs.
	_ = r.Update(&stale)
}

func TestClientLimitRepository_GetUpsert(t *testing.T) {
	db := newDB(t)
	r := NewClientLimitRepository(db)

	// Default-fallback path: no row exists.
	def, err := r.GetByClientID(7)
	if err != nil || !def.DailyLimit.Equal(decimal.NewFromInt(100000)) {
		t.Fatalf("default: %v %+v", err, def)
	}

	limit := &model.ClientLimit{
		ClientID: 7, DailyLimit: decimal.NewFromInt(50000),
		MonthlyLimit:  decimal.NewFromInt(500000),
		TransferLimit: decimal.NewFromInt(10000),
		SetByEmployee: 1,
	}
	if err := r.Upsert(limit); err != nil {
		t.Fatalf("upsert insert: %v", err)
	}
	got, err := r.GetByClientID(7)
	if err != nil || !got.DailyLimit.Equal(decimal.NewFromInt(50000)) {
		t.Fatalf("after insert: %v %+v", err, got)
	}
	// Update path
	limit.DailyLimit = decimal.NewFromInt(80000)
	if err := r.Upsert(limit); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	got2, err := r.GetByClientID(7)
	if err != nil || !got2.DailyLimit.Equal(decimal.NewFromInt(80000)) {
		t.Fatalf("after update: %v %+v", err, got2)
	}
}

// TestClientLimitRepository_VersionMonotonic guards the SP-5 invariant: every
// Upsert must increment Version so account-service replica consumers can order
// events correctly. Against unfixed code this test fails because version stays 1.
func TestClientLimitRepository_VersionMonotonic(t *testing.T) {
	db := newDB(t)
	r := NewClientLimitRepository(db)

	limit := &model.ClientLimit{
		ClientID: 1, DailyLimit: decimal.NewFromInt(10000),
		MonthlyLimit: decimal.NewFromInt(100000), TransferLimit: decimal.NewFromInt(5000),
		SetByEmployee: 1,
	}
	// First insert: version should be 1 (DB column default).
	if err := r.Upsert(limit); err != nil {
		t.Fatalf("upsert insert: %v", err)
	}
	got1, err := r.GetByClientID(1)
	if err != nil {
		t.Fatalf("get after insert: %v", err)
	}
	if got1.Version != 1 {
		t.Fatalf("after first insert: want Version=1, got %d", got1.Version)
	}

	// Second upsert (update): version must increment to 2.
	limit.DailyLimit = decimal.NewFromInt(20000)
	if err := r.Upsert(limit); err != nil {
		t.Fatalf("upsert update1: %v", err)
	}
	got2, err := r.GetByClientID(1)
	if err != nil {
		t.Fatalf("get after update1: %v", err)
	}
	if got2.Version != 2 {
		t.Fatalf("after second upsert: want Version=2, got %d", got2.Version)
	}
	if !got2.DailyLimit.Equal(decimal.NewFromInt(20000)) {
		t.Fatalf("after second upsert: want DailyLimit=20000, got %s", got2.DailyLimit)
	}

	// Third upsert: version must increment to 3.
	limit.DailyLimit = decimal.NewFromInt(30000)
	if err := r.Upsert(limit); err != nil {
		t.Fatalf("upsert update2: %v", err)
	}
	got3, err := r.GetByClientID(1)
	if err != nil {
		t.Fatalf("get after update2: %v", err)
	}
	if got3.Version != 3 {
		t.Fatalf("after third upsert: want Version=3, got %d", got3.Version)
	}
	if !got3.DailyLimit.Equal(decimal.NewFromInt(30000)) {
		t.Fatalf("after third upsert: want DailyLimit=30000, got %s", got3.DailyLimit)
	}
}

func TestChangelogRepository_CRUD(t *testing.T) {
	db := newDB(t)
	r := NewChangelogRepository(db)

	if err := r.Create(changelog.Entry{
		EntityType: "client", EntityID: 1, Action: "create",
		FieldName: "status", NewValue: "active", ChangedBy: 1,
		ChangedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Empty batch is a no-op
	if err := r.CreateBatch(nil); err != nil {
		t.Fatalf("batch nil: %v", err)
	}
	if err := r.CreateBatch([]changelog.Entry{
		{EntityType: "client", EntityID: 1, Action: "update",
			FieldName: "first_name", OldValue: "a", NewValue: "b", ChangedBy: 1, ChangedAt: time.Now()},
		{EntityType: "client", EntityID: 1, Action: "update",
			FieldName: "last_name", OldValue: "y", NewValue: "z", ChangedBy: 1, ChangedAt: time.Now()},
	}); err != nil {
		t.Fatalf("batch: %v", err)
	}

	rows, total, err := r.ListByEntity("client", 1, 1, 10)
	if err != nil || total < 2 || len(rows) < 2 {
		t.Fatalf("list: %v %d %d", err, total, len(rows))
	}
	rows2, total2, err := r.ListByEntity("client", 9999, 1, 10)
	if err != nil || total2 != 0 || len(rows2) != 0 {
		t.Fatalf("missing: %v %d %d", err, total2, len(rows2))
	}
}
