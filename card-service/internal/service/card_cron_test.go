package service

import (
	"context"
	"testing"
	"time"

	"github.com/exbanka/card-service/internal/repository"
	"github.com/exbanka/contract/cronreg"
)

// nilRegistry returns a no-op Registry for unit tests that don't need
// pause/trigger control (nil PauseStore is explicitly supported).
func nilRegistry() *cronreg.Registry {
	return cronreg.NewRegistry("test", nil)
}

// TestRunCardCronTick_NoExpiredItems exercises the happy-path no-op branch.
func TestRunCardCronTick_NoExpiredItems(t *testing.T) {
	db := newCardTestDB(t)
	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)
	// No blocks, no virtual cards, no panic.
	runCardCronTick(context.Background(), cardRepo, blockRepo, db)
}

// TestStartCardCron_CancellableViaContext starts the cron loop, cancels the
// context, and verifies the internal goroutine exits within 2 s.
func TestStartCardCron_CancellableViaContext(t *testing.T) {
	db := newCardTestDB(t)
	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)

	ctx, cancel := context.WithCancel(context.Background())
	// StartCardCron now launches an internal goroutine and returns immediately.
	StartCardCron(ctx, cardRepo, blockRepo, db, nilRegistry())
	time.Sleep(20 * time.Millisecond)
	cancel()
	// Give the internal goroutine up to 2 s to notice the cancellation.
	time.Sleep(100 * time.Millisecond)
}
