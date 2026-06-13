package repository

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/exbanka/notification-service/internal/model"
)

// newIdemDB gives idempotency_key a unique index so the ON CONFLICT DO NOTHING
// dedup path can be exercised. Production uses a PARTIAL unique index (WHERE
// idempotency_key <> ”), but sqlite's ON CONFLICT inference will not match a
// partial index, so the test uses a full unique index — the dedup semantics on
// non-empty keys are identical.
func newIdemDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.GeneralNotification{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_general_notif_idem_key
		 ON general_notifications (idempotency_key)`,
	).Error; err != nil {
		t.Fatalf("unique index: %v", err)
	}
	return db
}

func TestCreateWithIdempotency_FirstInsertThenDedup(t *testing.T) {
	db := newIdemDB(t)
	r := NewGeneralNotificationRepository(db)

	created, err := r.CreateWithIdempotency(&model.GeneralNotification{
		UserID: 1, Type: "WATCHLIST_PRICE_MOVE", Title: "AAPL", Message: "moved",
	}, "watchlist-1-AAPL-20260613")
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if !created {
		t.Fatal("first insert should report created=true")
	}

	// Same key again → dedup hit, created=false, no error, no second row.
	created, err = r.CreateWithIdempotency(&model.GeneralNotification{
		UserID: 1, Type: "WATCHLIST_PRICE_MOVE", Title: "AAPL", Message: "moved",
	}, "watchlist-1-AAPL-20260613")
	if err != nil {
		t.Fatalf("dedup insert: %v", err)
	}
	if created {
		t.Fatal("duplicate key should report created=false")
	}

	var count int64
	db.Model(&model.GeneralNotification{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected exactly 1 row after dedup, got %d", count)
	}
}

func TestCreateWithIdempotency_EmptyKeyFallsBackToPlainCreate(t *testing.T) {
	db := newIdemDB(t)
	r := NewGeneralNotificationRepository(db)

	// Empty key → plain Create (no ON CONFLICT), always created=true.
	created, err := r.CreateWithIdempotency(&model.GeneralNotification{
		UserID: 2, Type: "INFO", Title: "t", Message: "m",
	}, "")
	if err != nil {
		t.Fatalf("empty-key create: %v", err)
	}
	if !created {
		t.Fatal("empty-key create should report created=true")
	}

	var count int64
	db.Model(&model.GeneralNotification{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 empty-key row, got %d", count)
	}
}

func TestCreateWithIdempotency_DistinctKeysBothInserted(t *testing.T) {
	db := newIdemDB(t)
	r := NewGeneralNotificationRepository(db)

	for _, key := range []string{"k-a", "k-b"} {
		created, err := r.CreateWithIdempotency(&model.GeneralNotification{
			UserID: 3, Type: "INFO", Title: "t", Message: "m",
		}, key)
		if err != nil {
			t.Fatalf("create %s: %v", key, err)
		}
		if !created {
			t.Fatalf("distinct key %s should be created", key)
		}
	}

	var count int64
	db.Model(&model.GeneralNotification{}).Count(&count)
	if count != 2 {
		t.Fatalf("expected 2 distinct-key rows, got %d", count)
	}
}
