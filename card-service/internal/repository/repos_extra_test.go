package repository

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/contract/changelog"
)

type changelogPkgEntry = changelog.Entry

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Card{}, &model.CardBlock{}, &model.CardRequest{},
		&model.AuthorizedPerson{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestCardRepository_CRUD(t *testing.T) {
	db := newDB(t)
	r := NewCardRepository(db)

	c := &model.Card{
		CardNumber: "4111-aaa", CardNumberFull: "4111111111111111",
		CVV: "123", CardBrand: "visa", AccountNumber: "111-1",
		OwnerID: 1, OwnerType: "client", CardLimit: decimal.NewFromInt(1000),
		Status: "active",
	}
	if err := r.Create(c); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got, err := r.GetByID(c.ID); err != nil || got.CardNumber != "4111-aaa" {
		t.Fatalf("get: %v %+v", err, got)
	}
	if _, err := r.GetByID(99999); err == nil {
		t.Fatal("want missing err")
	}
	tx := db.Begin()
	if got, err := r.GetByIDForUpdate(tx, c.ID); err != nil || got.ID != c.ID {
		t.Fatalf("get for update: %v %+v", err, got)
	}
	if _, err := r.GetByIDForUpdate(tx, 99999); err == nil {
		t.Fatal("want missing for-update err")
	}
	tx.Commit()

	if list, err := r.ListByAccount("111-1"); err != nil || len(list) != 1 {
		t.Fatalf("by account: %v %d", err, len(list))
	}
	if list, err := r.ListByClient(1); err != nil || len(list) != 1 {
		t.Fatalf("by client: %v %d", err, len(list))
	}
	if got, err := r.UpdateStatus(c.ID, "blocked"); err != nil || got.Status != "blocked" {
		t.Fatalf("update status: %v %+v", err, got)
	}
	if _, err := r.UpdateStatus(99999, "x"); err == nil {
		t.Fatal("want missing update")
	}
	// Re-fetch (UpdateStatus bumped Version) for further Updates.
	fresh, err := r.GetByID(c.ID)
	if err != nil {
		t.Fatalf("refetch: %v", err)
	}
	fresh.IsVirtual = true
	fresh.ExpiresAt = time.Now().Add(-time.Hour)
	fresh.Status = "active"
	if err := r.Update(fresh); err != nil {
		t.Fatalf("update virtual: %v", err)
	}
	if list, err := r.FindExpiredVirtual(time.Now()); err != nil || len(list) != 1 {
		t.Fatalf("expired virtual: %v %d", err, len(list))
	}
	if n, err := r.CountByAccount("111-1"); err != nil || n != 1 {
		t.Fatalf("count: %v %d", err, n)
	}
	if n, err := r.CountByAccountAndOwner("111-1", 1); err != nil || n != 1 {
		t.Fatalf("count owner: %v %d", err, n)
	}
}

func TestCardBlockRepository_CRUD(t *testing.T) {
	db := newDB(t)
	r := NewCardBlockRepository(db)

	expired := time.Now().Add(-time.Hour)
	b := &model.CardBlock{
		CardID: 1, Reason: "fraud", BlockedAt: time.Now(),
		ExpiresAt: &expired, Active: true,
	}
	if err := r.Create(b); err != nil {
		t.Fatalf("create: %v", err)
	}
	list, err := r.FindExpiredActive(time.Now())
	if err != nil || len(list) != 1 {
		t.Fatalf("find: %v %d", err, len(list))
	}
	if err := r.Deactivate(b.ID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	list2, _ := r.FindExpiredActive(time.Now())
	if len(list2) != 0 {
		t.Fatalf("after deactivate: %d", len(list2))
	}
}

func TestCardRequestRepository_CRUD(t *testing.T) {
	db := newDB(t)
	r := NewCardRequestRepository(db)

	req := &model.CardRequest{
		ClientID: 1, AccountNumber: "111-1", CardBrand: "visa", Status: "pending",
	}
	if err := r.Create(req); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got, err := r.GetByID(req.ID); err != nil || got.ClientID != 1 {
		t.Fatalf("get: %v %+v", err, got)
	}
	if _, err := r.GetByID(99999); err == nil {
		t.Fatal("missing")
	}
	if list, total, err := r.List("", 1, 10); err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("list all: %v %d %d", err, total, len(list))
	}
	if list, total, err := r.List("pending", 1, 10); err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("list pending: %v %d %d", err, total, len(list))
	}
	if list, total, err := r.ListByClient(1, 1, 10); err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("by client: %v %d %d", err, total, len(list))
	}
	if err := r.UpdateStatus(req.ID, "approved", "ok", 7); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := r.GetByID(req.ID)
	if got.Status != "approved" || got.Reason != "ok" || got.ApprovedBy != 7 {
		t.Fatalf("after update: %+v", got)
	}
}

func TestChangelogRepository_CRUD(t *testing.T) {
	db := newDB(t)
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
	r := NewChangelogRepository(db)
	mk := func(action string) changelogPkgEntry {
		return changelogPkgEntry{
			EntityType: "card", EntityID: 1, Action: action,
			ChangedBy: 1, ChangedAt: time.Now(),
		}
	}
	if err := r.Create(mk("create")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := r.CreateBatch(nil); err != nil {
		t.Fatalf("batch nil: %v", err)
	}
	if err := r.CreateBatch([]changelogPkgEntry{mk("update"), mk("delete")}); err != nil {
		t.Fatalf("batch: %v", err)
	}
	rows, total, err := r.ListByEntity("card", 1, 1, 10)
	if err != nil || total < 2 || len(rows) < 2 {
		t.Fatalf("list: %v %d %d", err, total, len(rows))
	}
}

func TestAuthorizedPersonRepository_CRUD(t *testing.T) {
	db := newDB(t)
	r := NewAuthorizedPersonRepository(db)

	ap := &model.AuthorizedPerson{
		FirstName: "x", LastName: "y", AccountID: 1,
		DateOfBirth: time.Now().Unix(),
	}
	if err := r.Create(ap); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got, err := r.GetByID(ap.ID); err != nil || got.FirstName != "x" {
		t.Fatalf("get: %v %+v", err, got)
	}
	if _, err := r.GetByID(99999); err == nil {
		t.Fatal("missing")
	}
}
