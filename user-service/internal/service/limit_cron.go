package service

import (
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

// Start launches the daily reset goroutine in the background.
func (s *LimitCronService) Start() {
	go s.runDailyReset()
}

// runDailyReset waits until 23:59 each day and resets employee daily used limits.
func (s *LimitCronService) runDailyReset() {
	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 0, 0, now.Location())
		if now.After(next) {
			next = next.Add(24 * time.Hour)
		}
		time.Sleep(time.Until(next))
		if err := s.repo.ResetDailyUsedLimits(); err != nil {
			log.Printf("error resetting employee daily limits: %v", err)
		} else {
			log.Println("employee daily limits reset completed")
		}
	}
}
