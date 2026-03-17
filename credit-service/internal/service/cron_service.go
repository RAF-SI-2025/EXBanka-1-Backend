package service

import (
	"context"
	"log"
	"time"
)

type CronService struct {
	installService *InstallmentService
	loanService    *LoanService
}

func NewCronService(installService *InstallmentService, loanService *LoanService) *CronService {
	return &CronService{installService: installService, loanService: loanService}
}

// Start runs the daily installment collection job
func (c *CronService) Start(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	// Run immediately on start
	c.collectDueInstallments()

	for {
		select {
		case <-ticker.C:
			c.collectDueInstallments()
		case <-ctx.Done():
			log.Println("CronService: stopping")
			return
		}
	}
}

func (c *CronService) collectDueInstallments() {
	log.Println("CronService: checking for due installments")
	if err := c.installService.MarkOverdueInstallments(); err != nil {
		log.Printf("CronService: error marking overdue installments: %v", err)
	}
}
