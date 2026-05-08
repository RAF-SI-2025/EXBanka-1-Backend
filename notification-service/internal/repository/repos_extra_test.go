package repository

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/exbanka/notification-service/internal/model"
)

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.GeneralNotification{}, &model.MobileInboxItem{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func boolPtr(b bool) *bool { return &b }

func TestGeneralNotificationRepository_CRUD(t *testing.T) {
	db := newDB(t)
	r := NewGeneralNotificationRepository(db)

	if err := r.Create(&model.GeneralNotification{
		UserID: 1, Type: "info", Title: "t", Message: "m", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := r.Create(&model.GeneralNotification{
		UserID: 1, Type: "info", Title: "t2", Message: "m", CreatedAt: time.Now(),
		IsRead: true,
	}); err != nil {
		t.Fatalf("create2: %v", err)
	}

	all, total, err := r.ListByUser(1, nil, 1, 10)
	if err != nil || total != 2 || len(all) != 2 {
		t.Fatalf("list all: %v %d %d", err, total, len(all))
	}
	unread, total, err := r.ListByUser(1, boolPtr(false), 1, 10)
	if err != nil || total != 1 || len(unread) != 1 {
		t.Fatalf("list unread: %v %d %d", err, total, len(unread))
	}
	read, total, err := r.ListByUser(1, boolPtr(true), 1, 10)
	if err != nil || total != 1 || len(read) != 1 {
		t.Fatalf("list read: %v %d %d", err, total, len(read))
	}

	if c, err := r.UnreadCount(1); err != nil || c != 1 {
		t.Fatalf("unread count: %v %d", err, c)
	}

	// MarkRead the unread one
	if err := r.MarkRead(unread[0].ID, 1); err != nil {
		t.Fatalf("markread: %v", err)
	}
	if c, _ := r.UnreadCount(1); c != 0 {
		t.Fatalf("unread should be 0, got %d", c)
	}
	// MarkRead missing
	if err := r.MarkRead(99999, 1); err == nil {
		t.Fatal("want missing err")
	}

	// Reset and test MarkAllRead
	if err := r.Create(&model.GeneralNotification{
		UserID: 2, Title: "x", Message: "m", Type: "info", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create3: %v", err)
	}
	count, err := r.MarkAllRead(2)
	if err != nil || count != 1 {
		t.Fatalf("markallread: %v %d", err, count)
	}
}

func TestMobileInboxRepository_CRUD(t *testing.T) {
	db := newDB(t)
	r := NewMobileInboxRepository(db)

	item := &model.MobileInboxItem{
		UserID: 1, ChallengeID: 7, Method: "code_pull",
		Status: "pending", ExpiresAt: time.Now().Add(time.Hour),
		CreatedAt: time.Now(),
	}
	if err := r.Create(item); err != nil {
		t.Fatalf("create: %v", err)
	}
	pend, err := r.GetPendingByUser(1)
	if err != nil || len(pend) != 1 {
		t.Fatalf("pending: %v %d", err, len(pend))
	}
	if err := r.MarkDelivered(item.ID); err != nil {
		t.Fatalf("delivered: %v", err)
	}
	if err := r.MarkDelivered(item.ID); err == nil {
		t.Fatal("second delivered should be missing-err")
	}
	pend2, _ := r.GetPendingByUser(1)
	if len(pend2) != 0 {
		t.Fatalf("after deliver: %d", len(pend2))
	}

	// Insert expired item, then DeleteExpired removes it.
	if err := r.Create(&model.MobileInboxItem{
		UserID: 2, ChallengeID: 8, Method: "code_pull",
		Status: "pending", ExpiresAt: time.Now().Add(-time.Hour),
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("expired create: %v", err)
	}
	n, err := r.DeleteExpired()
	if err != nil || n != 1 {
		t.Fatalf("delete expired: %v %d", err, n)
	}
}
