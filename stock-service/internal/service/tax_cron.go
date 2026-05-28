package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/contract/cronreg"
)

type TaxCronService struct {
	taxSvc   *TaxService
	registry *cronreg.Registry
	entry    *cronreg.Entry
}

func NewTaxCronService(taxSvc *TaxService, registry *cronreg.Registry) *TaxCronService {
	s := &TaxCronService{taxSvc: taxSvc, registry: registry}
	s.entry = registry.Register("tax-collection", "Collect capital-gains tax monthly (fires ~1 min before month end)", 0)
	return s
}

// StartMonthlyCron starts a background goroutine that triggers tax collection
// at the end of each month (last day, 23:59).
func (s *TaxCronService) StartMonthlyCron(ctx context.Context) {
	go func() {
		for {
			now := time.Now()
			// Calculate next month boundary
			nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
			// Trigger 1 minute before month ends
			triggerTime := nextMonth.Add(-1 * time.Minute)

			if triggerTime.Before(now) {
				// Already past trigger time this month, wait for next month
				triggerTime = time.Date(now.Year(), now.Month()+2, 1, 0, 0, 0, 0, time.UTC).Add(-1 * time.Minute)
			}

			waitDuration := triggerTime.Sub(now)
			log.Printf("tax cron: next collection scheduled for %s (in %s)", triggerTime.Format(time.RFC3339), waitDuration)

			select {
			case <-time.After(waitDuration):
				if !s.entry.BeginRun() {
					log.Println("tax cron: paused, skipping this tick")
					continue
				}
				err := s.runCollection()
				s.entry.EndRun(err)
			case <-s.entry.TriggerChan():
				if !s.entry.BeginRun() {
					continue
				}
				err := s.runCollection()
				s.entry.EndRun(err)
			case <-ctx.Done():
				log.Println("tax cron: shutting down")
				return
			}
		}
	}()
}

func (s *TaxCronService) runCollection() error {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	log.Printf("tax cron: starting monthly collection for %d-%02d", year, month)

	collected, totalRSD, failed, err := s.taxSvc.CollectTax(year, month)
	if err != nil {
		log.Printf("ERROR: tax cron: collection failed: %v", err)
		return err
	}

	log.Printf("tax cron: collection complete — collected=%d total_rsd=%s failed=%d",
		collected, totalRSD.StringFixed(2), failed)
	return nil
}
