package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	accountpb "github.com/exbanka/contract/accountpb"
	clientpb "github.com/exbanka/contract/clientpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/credit-service/internal/kafka"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type CronService struct {
	installService    *InstallmentService
	loanService       *LoanService
	accountClient     accountpb.AccountServiceClient
	bankAccountClient accountpb.BankAccountServiceClient
	clientClient      clientpb.ClientServiceClient
	producer          *kafka.Producer
	bankRSDAccount    string
	db                *gorm.DB
}

func NewCronService(
	installService *InstallmentService,
	loanService *LoanService,
	accountClient accountpb.AccountServiceClient,
	bankAccountClient accountpb.BankAccountServiceClient,
	clientClient clientpb.ClientServiceClient,
	producer *kafka.Producer,
	bankRSDAccount string,
	db *gorm.DB,
) *CronService {
	return &CronService{
		installService:    installService,
		loanService:       loanService,
		accountClient:     accountClient,
		bankAccountClient: bankAccountClient,
		clientClient:      clientClient,
		producer:          producer,
		bankRSDAccount:    bankRSDAccount,
		db:                db,
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

				// Wrap both writes in a single transaction to prevent partial updates.
				txErr := c.db.Transaction(func(tx *gorm.DB) error {
					if e := tx.Save(loan).Error; e != nil {
						return e
					}
					return tx.Session(&gorm.Session{SkipHooks: true}).
						Model(&model.Installment{}).
						Where("loan_id = ? AND status = ?", loanID, "unpaid").
						Updates(map[string]interface{}{
							"amount":        newMonthlyAmount,
							"interest_rate": newNominalRate,
						}).Error
				})
				if txErr != nil {
					log.Printf("CronService: failed to apply late penalty transaction for loan %d: %v", loanID, txErr)
				} else {
					log.Printf("CronService: applied +0.05%% late payment penalty to variable loan %d (new rate: %s%%)", loanID, newNominalRate.StringFixed(4))
				}
			}
		}

		return
	}

	// Credit the bank's own RSD account with the installment amount.
	// If this fails, compensate by reversing the client debit — do NOT mark paid.
	if c.accountClient != nil && c.bankRSDAccount != "" {
		_, creditErr := c.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
			AccountNumber:   c.bankRSDAccount,
			Amount:          amount,
			UpdateAvailable: true,
		})
		if creditErr != nil {
			log.Printf("CronService: failed to credit bank RSD account for installment %d: %v — reversing client debit", installmentID, creditErr)
			_, compErr := c.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber:   loan.AccountNumber,
				Amount:          amount, // positive = credit back to client
				UpdateAvailable: true,
			})
			if compErr != nil {
				log.Printf("CronService: CRITICAL: compensation for installment %d failed: %v — manual intervention required", installmentID, compErr)
			}
			return // Do NOT mark installment as paid.
		}
	}

	// Mark installment as paid.
	// If this DB write fails, the money already moved — compensate both steps
	// to prevent double-charging on the next cron cycle.
	amtDecimal, parseErr := decimal.NewFromString(amount)
	if parseErr != nil {
		log.Printf("CronService: CRITICAL: failed to parse installment amount %q for compensation of installment %d: %v — manual review required", amount, installmentID, parseErr)
		return
	}
	if err := c.installService.MarkInstallmentPaid(installmentID); err != nil {
		if errors.Is(err, repository.ErrInstallmentAlreadyPaid) {
			log.Printf("CronService: installment %d already paid — skipping compensation", installmentID)
			return
		}
		log.Printf("CronService: mark-paid failed for installment %d: %v — compensating debit+credit", installmentID, err)
		// Reverse bank credit (debit the bank account back)
		if c.accountClient != nil && c.bankRSDAccount != "" {
			if _, compErr := c.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber:   c.bankRSDAccount,
				Amount:          amtDecimal.Neg().StringFixed(4),
				UpdateAvailable: true,
			}); compErr != nil {
				log.Printf("CronService: CRITICAL: bank compensation failed for installment %d: %v — manual review required", installmentID, compErr)
			}
		}
		// Reverse client debit (credit the borrower account back)
		if c.accountClient != nil {
			if _, compErr := c.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber:   loan.AccountNumber,
				Amount:          amtDecimal.StringFixed(4),
				UpdateAvailable: true,
			}); compErr != nil {
				log.Printf("CronService: CRITICAL: client compensation failed for installment %d: %v — manual review required", installmentID, compErr)
			}
		}
		return
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
