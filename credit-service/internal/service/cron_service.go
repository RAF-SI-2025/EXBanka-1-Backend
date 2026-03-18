package service

import (
	"context"
	"fmt"
	"log"
	"time"

	accountpb "github.com/exbanka/contract/accountpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/credit-service/internal/kafka"
)

type CronService struct {
	installService *InstallmentService
	loanService    *LoanService
	accountClient  accountpb.AccountServiceClient
	producer       *kafka.Producer
}

func NewCronService(installService *InstallmentService, loanService *LoanService, accountClient accountpb.AccountServiceClient, producer *kafka.Producer) *CronService {
	return &CronService{
		installService: installService,
		loanService:    loanService,
		accountClient:  accountClient,
		producer:       producer,
	}
}

// Start runs the daily installment collection job
func (c *CronService) Start(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	// Run immediately on start
	c.collectDueInstallments(ctx)

	for {
		select {
		case <-ticker.C:
			c.collectDueInstallments(ctx)
		case <-ctx.Done():
			log.Println("CronService: stopping")
			return
		}
	}
}

func (c *CronService) collectDueInstallments(ctx context.Context) {
	log.Println("CronService: checking for due installments")

	// Mark overdue installments
	if err := c.installService.MarkOverdueInstallments(); err != nil {
		log.Printf("CronService: error marking overdue installments: %v", err)
	}

	// Get installments due today (unpaid with expected_date <= now)
	dueInstallments, err := c.installService.GetDueInstallments()
	if err != nil {
		log.Printf("CronService: error fetching due installments: %v", err)
		return
	}

	for _, installment := range dueInstallments {
		c.processInstallment(ctx, installment.ID, installment.LoanID, installment.Amount.StringFixed(4))
	}
}

func (c *CronService) processInstallment(ctx context.Context, installmentID, loanID uint64, amount string) {
	// Fetch the loan to get the account number
	loan, err := c.loanService.loanRepo.GetByID(loanID)
	if err != nil {
		log.Printf("CronService: failed to get loan %d for installment %d: %v", loanID, installmentID, err)
		return
	}

	retryDeadline := time.Now().Add(72 * time.Hour).Format(time.RFC3339)

	// Debit the loan's account via account-service
	debitErr := shared.Retry(ctx, shared.DefaultRetryConfig, func() error {
		if c.accountClient == nil {
			return nil
		}
		// Debit: negative amount
		debitAmt := fmt.Sprintf("-%s", amount)
		_, e := c.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
			AccountNumber:   loan.AccountNumber,
			Amount:          debitAmt,
			UpdateAvailable: true,
		})
		return e
	})

	if debitErr != nil {
		log.Printf("CronService: installment %d debit failed: %v", installmentID, debitErr)
		if c.producer != nil {
			msg := kafkamsg.InstallmentResultMessage{
				LoanID:        loanID,
				Amount:        amount,
				Success:       false,
				Error:         debitErr.Error(),
				RetryDeadline: retryDeadline,
			}
			if pubErr := c.producer.PublishInstallmentFailed(ctx, msg); pubErr != nil {
				log.Printf("CronService: failed to publish installment-failed event: %v", pubErr)
			}
		}
		return
	}

	// Mark installment as paid
	if err := c.installService.MarkInstallmentPaid(installmentID); err != nil {
		log.Printf("CronService: failed to mark installment %d as paid: %v", installmentID, err)
	}

	// Publish success event
	if c.producer != nil {
		msg := kafkamsg.InstallmentResultMessage{
			LoanID:  loanID,
			Amount:  amount,
			Success: true,
		}
		if pubErr := c.producer.PublishInstallmentCollected(ctx, msg); pubErr != nil {
			log.Printf("CronService: failed to publish installment-collected event: %v", pubErr)
		}
	}
}
