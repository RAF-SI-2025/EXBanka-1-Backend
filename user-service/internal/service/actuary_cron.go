package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/contract/cronreg"
	"github.com/exbanka/user-service/internal/repository"
)

// ActuaryCronService runs the daily actuary used_limit reset at 23:59.
type ActuaryCronService struct {
	repo  *repository.ActuaryRepository
	entry *cronreg.Entry
}

func NewActuaryCronService(repo *repository.ActuaryRepository, registry *cronreg.Registry) *ActuaryCronService {
	s := &ActuaryCronService{repo: repo}
	s.entry = registry.Register("actuary-daily-limit-reset", "Reset actuary daily used-limit counters at 23:59 UTC", 0)
	return s
}

func (s *ActuaryCronService) Start(ctx context.Context) {
	go s.runDailyReset(ctx)
}

func (s *ActuaryCronService) runDailyReset(ctx context.Context) {
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
			if !s.entry.BeginRun() {
				continue
			}
			var runErr error
			if err := s.repo.ResetAllUsedLimits(); err != nil {
				log.Printf("error resetting actuary daily used limits: %v", err)
				runErr = err
			} else {
				log.Println("actuary daily used limits reset completed")
			}
			s.entry.EndRun(runErr)
		case <-s.entry.TriggerChan():
			if !s.entry.BeginRun() {
				continue
			}
			var runErr error
			if err := s.repo.ResetAllUsedLimits(); err != nil {
				log.Printf("error resetting actuary daily used limits: %v", err)
				runErr = err
			} else {
				log.Println("actuary daily used limits reset completed")
			}
			s.entry.EndRun(runErr)
		}
	}
}
