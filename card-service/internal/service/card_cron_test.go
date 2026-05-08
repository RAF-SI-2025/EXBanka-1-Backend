package service

import (
	"context"
	"testing"
	"time"

	"github.com/exbanka/card-service/internal/repository"
)

// TestRunCardCronTick_NoExpiredItems exercises the happy-path no-op branch.
func TestRunCardCronTick_NoExpiredItems(t *testing.T) {
	db := newCardTestDB(t)
	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)
	// No blocks, no virtual cards, no panic.
	runCardCronTick(context.Background(), cardRepo, blockRepo, db)
}

// TestStartCardCron_CancellableViaContext starts the cron loop and immediately
// cancels the context; Start must return promptly.
func TestStartCardCron_CancellableViaContext(t *testing.T) {
	db := newCardTestDB(t)
	cardRepo := repository.NewCardRepository(db)
	blockRepo := repository.NewCardBlockRepository(db)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		StartCardCron(ctx, cardRepo, blockRepo, db)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartCardCron did not exit within 2s of context cancel")
	}
}
