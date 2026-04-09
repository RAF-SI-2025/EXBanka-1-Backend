package service

import (
	"context"
	"log"
	"time"
)

type TaxCronService struct {
	taxSvc *TaxService
}

func NewTaxCronService(taxSvc *TaxService) *TaxCronService {
	return &TaxCronService{taxSvc: taxSvc}
}

// StartMonthlyCron starts a background goroutine that triggers tax collection
// at the end of each month (last day, 23:59).
func (s *TaxCronService) StartMonthlyCron(ctx context.Context) {
	go func() {
		for {
			now := time.Now()
			// Calculate next month boundary
			nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
			// Trigger 1 minute before month ends
			triggerTime := nextMonth.Add(-1 * time.Minute)

			if triggerTime.Before(now) {
				// Already past trigger time this month, wait for next month
				triggerTime = time.Date(now.Year(), now.Month()+2, 1, 0, 0, 0, 0, now.Location()).Add(-1 * time.Minute)
			}

			waitDuration := triggerTime.Sub(now)
			log.Printf("tax cron: next collection scheduled for %s (in %s)", triggerTime.Format(time.RFC3339), waitDuration)

			select {
			case <-time.After(waitDuration):
				s.runCollection()
			case <-ctx.Done():
				log.Println("tax cron: shutting down")
				return
			}
		}
	}()
}

func (s *TaxCronService) runCollection() {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	log.Printf("tax cron: starting monthly collection for %d-%02d", year, month)

	collected, totalRSD, failed, err := s.taxSvc.CollectTax(year, month)
	if err != nil {
		log.Printf("ERROR: tax cron: collection failed: %v", err)
		return
	}

	log.Printf("tax cron: collection complete — collected=%d total_rsd=%s failed=%d",
		collected, totalRSD.StringFixed(2), failed)
}
