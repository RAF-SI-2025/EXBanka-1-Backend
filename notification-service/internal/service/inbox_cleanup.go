package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/contract/cronreg"
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
	entry     *cronreg.Entry
}

func NewInboxCleanupService(inboxRepo *repository.MobileInboxRepository, registry *cronreg.Registry) *InboxCleanupService {
	s := &InboxCleanupService{inboxRepo: inboxRepo}
	s.entry = registry.Register("inbox-cleanup", "Delete expired mobile inbox items (every 1 min)", time.Minute)
	return s
}

// newInboxCleanupServiceWithDeleter is a test-only constructor accepting any
// implementation of expiredItemDeleter. Kept package-private so the public
// API stays unchanged.
func newInboxCleanupServiceWithDeleter(d expiredItemDeleter, registry *cronreg.Registry) *InboxCleanupService {
	s := &InboxCleanupService{inboxRepo: d}
	s.entry = registry.Register("inbox-cleanup", "Delete expired mobile inbox items (every 1 min)", time.Minute)
	return s
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
// Each tick is gated by the cronreg Entry so the job can be paused / manually
// triggered via the AdminCron gRPC API.
func (s *InboxCleanupService) StartCleanupCron(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		log.Println("inbox cleanup: started (every 1 minute)")
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !s.entry.BeginRun() {
					continue
				}
				_, err := s.runOnce()
				s.entry.EndRun(err)
			case <-s.entry.TriggerChan():
				if !s.entry.BeginRun() {
					continue
				}
				_, err := s.runOnce()
				s.entry.EndRun(err)
			}
		}
	}()
}
