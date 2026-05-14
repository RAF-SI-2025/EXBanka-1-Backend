package repository

import (
	"testing"

	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/notification-service/internal/model"
)

func TestTemplateRepository_UpsertGetDelete(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.NotificationTemplate{})
	repo := NewTemplateRepository(db)

	// Get on a missing row → gorm.ErrRecordNotFound.
	if _, err := repo.Get("ACTIVATION", "email"); err == nil {
		t.Fatal("expected error for missing override")
	}

	// Upsert inserts.
	tmpl := &model.NotificationTemplate{Type: "ACTIVATION", Channel: "email", Subject: "Hi", Body: "Hello {{first_name}}", UpdatedBy: 7}
	if err := repo.Upsert(tmpl); err != nil {
		t.Fatalf("upsert insert: %v", err)
	}
	got, err := repo.Get("ACTIVATION", "email")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Subject != "Hi" || got.UpdatedBy != 7 {
		t.Errorf("got %+v", got)
	}

	// Upsert again updates the same row (no duplicate).
	tmpl2 := &model.NotificationTemplate{Type: "ACTIVATION", Channel: "email", Subject: "Updated", Body: "x", UpdatedBy: 9}
	if err := repo.Upsert(tmpl2); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	got, _ = repo.Get("ACTIVATION", "email")
	if got.Subject != "Updated" || got.UpdatedBy != 9 {
		t.Errorf("update did not take: %+v", got)
	}
	var count int64
	db.Model(&model.NotificationTemplate{}).Count(&count)
	if count != 1 {
		t.Errorf("expected 1 row after re-upsert, got %d", count)
	}

	// Delete removes it.
	if err := repo.Delete("ACTIVATION", "email"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.Get("ACTIVATION", "email"); err == nil {
		t.Error("expected error after delete")
	}
}
