package service

import (
	"context"
	"fmt"
	"log"
	"time"

	accountpb "github.com/exbanka/contract/accountpb"
	clientpb "github.com/exbanka/contract/clientpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/credit-service/internal/kafka"
	"github.com/shopspring/decimal"
)

type CronService struct {
	installService    *InstallmentService
	loanService       *LoanService
	accountClient     accountpb.AccountServiceClient
	bankAccountClient accountpb.BankAccountServiceClient
	clientClient      clientpb.ClientServiceClient
	producer          *kafka.Producer
	bankRSDAccount    string
}

func NewCronService(
	installService *InstallmentService,
	loanService *LoanService,
	accountClient accountpb.AccountServiceClient,
	bankAccountClient accountpb.BankAccountServiceClient,
	clientClient clientpb.ClientServiceClient,
	producer *kafka.Producer,
	bankRSDAccount string,
) *CronService {
	return &CronService{
		installService:    installService,
		loanService:       loanService,
		accountClient:     accountClient,
		bankAccountClient: bankAccountClient,
		clientClient:      clientClient,
		producer:          producer,
		bankRSDAccount:    bankRSDAccount,
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
		c.processInstallment(
			ctx,
			installment.ID,
			installment.LoanID,
			installment.Amount.StringFixed(4),
			installment.CurrencyCode,
			installment.ExpectedDate.Format("2006-01-02"),
		)
	}
}

func (c *CronService) processInstallment(ctx context.Context, installmentID, loanID uint64, amount, currencyCode, dueDate string) {
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

		// Send failure email notification to the client
		if c.clientClient != nil && c.producer != nil {
			clientResp, clientErr := c.clientClient.GetClient(ctx, &clientpb.GetClientRequest{Id: loan.ClientID})
			if clientErr == nil {
				emailErr := c.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
					To:        clientResp.Email,
					EmailType: kafkamsg.EmailTypeInstallmentFailed,
					Data: map[string]string{
						"loan_number":    loan.LoanNumber,
						"amount":         amount,
						"currency":       currencyCode,
						"due_date":       dueDate,
						"retry_deadline": retryDeadline,
					},
				})
				if emailErr != nil {
					log.Printf("CronService: failed to send installment failure email for loan %d: %v", loanID, emailErr)
				}
			} else {
				log.Printf("CronService: failed to fetch client for loan %d: %v", loanID, clientErr)
			}
		}

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

		// Apply late payment interest penalty for variable-rate loans
		if loan.InterestType == "variable" {
			penalty := decimal.NewFromFloat(0.05)
			loan.BaseRate = loan.BaseRate.Add(penalty)
			newNominalRate := loan.BaseRate.Add(loan.BankMargin)
			loan.NominalInterestRate = newNominalRate
			loan.CurrentRate = newNominalRate

			// Count remaining unpaid installments to recalculate monthly amount
			remainingCount, countErr := c.installService.installRepo.CountUnpaidByLoan(loanID)
			if countErr != nil {
				log.Printf("CronService: failed to count unpaid installments for loan %d: %v", loanID, countErr)
			} else {
				remainingMonths := int(remainingCount)
				newMonthlyAmount := CalculateMonthlyInstallment(loan.RemainingDebt, newNominalRate, remainingMonths)
				loan.NextInstallmentAmount = newMonthlyAmount

				if updateErr := c.loanService.loanRepo.Update(loan); updateErr != nil {
					log.Printf("CronService: failed to update loan %d after late penalty: %v", loanID, updateErr)
				}
				if updateErr := c.installService.installRepo.UpdateUnpaidByLoan(loanID, newMonthlyAmount, newNominalRate); updateErr != nil {
					log.Printf("CronService: failed to update unpaid installments for loan %d after late penalty: %v", loanID, updateErr)
				}
				log.Printf("CronService: applied +0.05%% late payment penalty to variable loan %d (new rate: %s%%)", loanID, newNominalRate.StringFixed(4))
			}
		}

		return
	}

	// Credit the bank's own RSD account with the installment amount
	if c.accountClient != nil && c.bankRSDAccount != "" {
		_, creditErr := c.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
			AccountNumber:   c.bankRSDAccount,
			Amount:          amount,
			UpdateAvailable: true,
		})
		if creditErr != nil {
			log.Printf("CronService: failed to credit bank RSD account for installment %d: %v", installmentID, creditErr)
		}
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
