package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/notification-service/internal/repository"
)

// expiredItemDeleter is the minimal contract InboxCleanupService needs.
// *repository.MobileInboxRepository satisfies this interface in production;
// tests inject a stub.
type expiredItemDeleter interface {
	DeleteExpired() (int64, error)
}

type InboxCleanupService struct {
	inboxRepo expiredItemDeleter
}

func NewInboxCleanupService(inboxRepo *repository.MobileInboxRepository) *InboxCleanupService {
	return &InboxCleanupService{inboxRepo: inboxRepo}
}

// newInboxCleanupServiceWithDeleter is a test-only constructor accepting any
// implementation of expiredItemDeleter. Kept package-private so the public
// API stays unchanged.
func newInboxCleanupServiceWithDeleter(d expiredItemDeleter) *InboxCleanupService {
	return &InboxCleanupService{inboxRepo: d}
}

// runOnce performs a single cleanup pass and returns the count + error.
// Extracted so tests can exercise the cleanup logic without a goroutine.
func (s *InboxCleanupService) runOnce() (int64, error) {
	deleted, err := s.inboxRepo.DeleteExpired()
	if err != nil {
		log.Printf("inbox cleanup: error deleting expired items: %v", err)
		return 0, err
	}
	if deleted > 0 {
		log.Printf("inbox cleanup: deleted %d expired items", deleted)
	}
	return deleted, nil
}

// StartCleanupCron runs every minute and deletes expired mobile inbox items.
func (s *InboxCleanupService) StartCleanupCron(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_, _ = s.runOnce()
			case <-ctx.Done():
				log.Println("inbox cleanup: stopped")
				return
			}
		}
	}()
	log.Println("inbox cleanup: started (every 1 minute)")
}
