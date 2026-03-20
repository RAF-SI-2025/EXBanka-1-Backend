package service

import (
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

func (s *SpendingCronService) Start() {
	go s.runDailyReset()
	go s.runMonthlyReset()
}

func (s *SpendingCronService) runDailyReset() {
	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 0, 0, now.Location())
		if now.After(next) {
			next = next.Add(24 * time.Hour)
		}
		time.Sleep(time.Until(next))
		if err := s.repo.ResetDailySpending(); err != nil {
			log.Printf("error resetting daily spending: %v", err)
		} else {
			log.Println("daily spending reset completed")
		}
	}
}

func (s *SpendingCronService) runMonthlyReset() {
	for {
		now := time.Now()
		nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
		time.Sleep(time.Until(nextMonth))
		if err := s.repo.ResetMonthlySpending(); err != nil {
			log.Printf("error resetting monthly spending: %v", err)
		} else {
			log.Println("monthly spending reset completed")
		}
	}
}
