package service

import (
	"context"
	"log"
	"time"
)

// LimitCronService runs scheduled jobs for employee limit management.
type LimitCronService struct {
	repo EmployeeLimitRepo
}

// NewLimitCronService creates a new LimitCronService.
func NewLimitCronService(repo EmployeeLimitRepo) *LimitCronService {
	return &LimitCronService{repo: repo}
}

// Start launches the daily reset goroutine. It exits when ctx is cancelled.
func (s *LimitCronService) Start(ctx context.Context) {
	go s.runDailyReset(ctx)
}

// runDailyReset waits until 23:59 each day and resets employee daily used limits.
func (s *LimitCronService) runDailyReset(ctx context.Context) {
	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 0, 0, time.UTC)
		if !now.Before(next) {
			next = next.Add(24 * time.Hour)
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := s.repo.ResetDailyUsedLimits(); err != nil {
			log.Printf("error resetting employee daily limits: %v", err)
		} else {
			log.Println("employee daily limits reset completed")
		}
	}
}
