package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/account-service/internal/repository"
	"github.com/exbanka/contract/cronreg"
)

type SpendingCronService struct {
	repo         *repository.AccountRepository
	dailyEntry   *cronreg.Entry
	monthlyEntry *cronreg.Entry
}

func NewSpendingCronService(repo *repository.AccountRepository, registry *cronreg.Registry) *SpendingCronService {
	s := &SpendingCronService{repo: repo}
	s.dailyEntry = registry.Register("daily-spending-reset", "Reset account daily spending counters at 23:59 UTC", 0)
	s.monthlyEntry = registry.Register("monthly-spending-reset", "Reset account monthly spending counters on 1st of month", 0)
	return s
}

// Start launches the daily and monthly reset goroutines. They exit when ctx is cancelled.
func (s *SpendingCronService) Start(ctx context.Context) {
	go s.runDailyReset(ctx)
	go s.runMonthlyReset(ctx)
}

func (s *SpendingCronService) runDailyReset(ctx context.Context) {
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
			timer.Stop()
			if !s.dailyEntry.BeginRun() {
				continue
			}
			var runErr error
			if err := s.repo.ResetDailySpending(); err != nil {
				log.Printf("error resetting daily spending: %v", err)
				runErr = err
			} else {
				log.Println("daily spending reset completed")
			}
			s.dailyEntry.EndRun(runErr)
		case <-s.dailyEntry.TriggerChan():
			if !s.dailyEntry.BeginRun() {
				continue
			}
			var runErr error
			if err := s.repo.ResetDailySpending(); err != nil {
				log.Printf("error resetting daily spending: %v", err)
				runErr = err
			} else {
				log.Println("daily spending reset completed")
			}
			s.dailyEntry.EndRun(runErr)
		}
	}
}

func (s *SpendingCronService) runMonthlyReset(ctx context.Context) {
	for {
		now := time.Now()
		nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		timer := time.NewTimer(time.Until(nextMonth))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			timer.Stop()
			if !s.monthlyEntry.BeginRun() {
				continue
			}
			var runErr error
			if err := s.repo.ResetMonthlySpending(); err != nil {
				log.Printf("error resetting monthly spending: %v", err)
				runErr = err
			} else {
				log.Println("monthly spending reset completed")
			}
			s.monthlyEntry.EndRun(runErr)
		case <-s.monthlyEntry.TriggerChan():
			if !s.monthlyEntry.BeginRun() {
				continue
			}
			var runErr error
			if err := s.repo.ResetMonthlySpending(); err != nil {
				log.Printf("error resetting monthly spending: %v", err)
				runErr = err
			} else {
				log.Println("monthly spending reset completed")
			}
			s.monthlyEntry.EndRun(runErr)
		}
	}
}
