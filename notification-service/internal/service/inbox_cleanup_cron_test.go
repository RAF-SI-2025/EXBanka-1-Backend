package service

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/exbanka/contract/cronreg"
	"github.com/exbanka/notification-service/internal/model"
	"github.com/exbanka/notification-service/internal/repository"
)

// TestNewInboxCleanupService_TriggerDeletesExpired exercises the public
// constructor (wiring a real repository + registry) and the manual-trigger
// branch of StartCleanupCron: an admin trigger fires a cleanup pass that
// deletes expired mobile inbox items.
func TestNewInboxCleanupService_TriggerDeletesExpired(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.MobileInboxItem{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.NewMobileInboxRepository(db)

	// Seed one expired and one live item.
	if err := repo.Create(&model.MobileInboxItem{
		UserID: 1, ChallengeID: 1, Method: "code_pull", Status: "pending",
		ExpiresAt: time.Now().Add(-time.Hour), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed expired: %v", err)
	}
	if err := repo.Create(&model.MobileInboxItem{
		UserID: 1, ChallengeID: 2, Method: "code_pull", Status: "pending",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed live: %v", err)
	}

	registry := cronreg.NewRegistry("test", nil)
	svc := NewInboxCleanupService(repo, registry)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.StartCleanupCron(ctx)

	// Fire a manual trigger; the cron loop should run one cleanup pass.
	if err := registry.Trigger("inbox-cleanup", false, 0); err != nil {
		t.Fatalf("trigger: %v", err)
	}

	// Poll until the expired item is gone (live item remains).
	deadline := time.After(2 * time.Second)
	for {
		var count int64
		db.Model(&model.MobileInboxItem{}).Count(&count)
		if count == 1 {
			break // expired removed, live remains
		}
		select {
		case <-deadline:
			t.Fatalf("expired item was not deleted after trigger (count=%d)", count)
		case <-time.After(10 * time.Millisecond):
		}
	}

	// The surviving row is the live (future-expiry) one.
	var rows []model.MobileInboxItem
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(rows) != 1 || rows[0].ChallengeID != 2 {
		t.Fatalf("expected only the live item (challenge 2) to survive, got %+v", rows)
	}
}
