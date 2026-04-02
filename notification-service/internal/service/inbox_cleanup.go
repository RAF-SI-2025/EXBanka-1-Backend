package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/notification-service/internal/repository"
)

type InboxCleanupService struct {
	inboxRepo *repository.MobileInboxRepository
}

func NewInboxCleanupService(inboxRepo *repository.MobileInboxRepository) *InboxCleanupService {
	return &InboxCleanupService{inboxRepo: inboxRepo}
}

// StartCleanupCron runs every minute and deletes expired mobile inbox items.
func (s *InboxCleanupService) StartCleanupCron(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				deleted, err := s.inboxRepo.DeleteExpired()
				if err != nil {
					log.Printf("inbox cleanup: error deleting expired items: %v", err)
				} else if deleted > 0 {
					log.Printf("inbox cleanup: deleted %d expired items", deleted)
				}
			case <-ctx.Done():
				log.Println("inbox cleanup: stopped")
				return
			}
		}
	}()
	log.Println("inbox cleanup: started (every 1 minute)")
}
