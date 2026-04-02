package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/account-service/internal/repository"
)

type SpendingCronService struct {
	repo *repository.AccountRepository
}

func NewSpendingCronService(repo *repository.AccountRepository) *SpendingCronService {
	return &SpendingCronService{repo: repo}
}

// Start launches the daily and monthly reset goroutines. They exit when ctx is cancelled.
func (s *SpendingCronService) Start(ctx context.Context) {
	go s.runDailyReset(ctx)
	go s.runMonthlyReset(ctx)
}

func (s *SpendingCronService) runDailyReset(ctx context.Context) {
	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 0, 0, now.Location())
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
		if err := s.repo.ResetDailySpending(); err != nil {
			log.Printf("error resetting daily spending: %v", err)
		} else {
			log.Println("daily spending reset completed")
		}
	}
}

func (s *SpendingCronService) runMonthlyReset(ctx context.Context) {
	for {
		now := time.Now()
		nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
		timer := time.NewTimer(time.Until(nextMonth))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := s.repo.ResetMonthlySpending(); err != nil {
			log.Printf("error resetting monthly spending: %v", err)
		} else {
			log.Println("monthly spending reset completed")
		}
	}
}
