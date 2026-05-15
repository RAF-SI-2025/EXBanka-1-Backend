package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	kafkamsg "github.com/exbanka/contract/kafka"
)

// maintenanceProducer is the narrow slice of the Kafka producer the cron uses.
// A separate interface (not the concrete *kafka.Producer) so unit tests can
// inject a recording stub. Precedent: Plan B3 introduced cronNotifier on
// credit-service's CronService for the same reason.
type maintenanceProducer interface {
	PublishMaintenanceFeeCharged(ctx context.Context, msg kafkamsg.MaintenanceFeeChargedMessage) error
	PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error
}

type MaintenanceCronService struct {
	accountRepo *repository.AccountRepository
	ledgerSvc   *LedgerService
	producer    maintenanceProducer
}

func NewMaintenanceCronService(accountRepo *repository.AccountRepository, ledgerSvc *LedgerService, producer maintenanceProducer) *MaintenanceCronService {
	s := &MaintenanceCronService{accountRepo: accountRepo, ledgerSvc: ledgerSvc}
	// Typed-nil guard: only assign when non-nil to avoid the
	// non-nil-interface-holding-nil-pointer panic at the call site.
	if producer != nil {
		s.producer = producer
	}
	return s
}

// Start launches the monthly charge goroutine. It exits when ctx is cancelled.
func (s *MaintenanceCronService) Start(ctx context.Context) {
	go s.runMonthlyCharge(ctx)
}

func (s *MaintenanceCronService) runMonthlyCharge(ctx context.Context) {
	for {
		now := time.Now()
		// Next 1st of month at 00:01
		nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 1, 0, 0, time.UTC)
		timer := time.NewTimer(time.Until(nextMonth))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		s.chargeMaintenanceFees(ctx)
	}
}

func (s *MaintenanceCronService) chargeMaintenanceFees(ctx context.Context) {
	accounts, err := s.accountRepo.ListActiveAccountsWithMaintenanceFee()
	if err != nil {
		log.Printf("error listing accounts for maintenance fee: %v", err)
		return
	}

	// Get bank RSD account for crediting fees.
	bankAccounts, _ := s.accountRepo.ListBankAccounts()
	var bankRSDNumber string
	for _, ba := range bankAccounts {
		if ba.CurrencyCode == "RSD" {
			bankRSDNumber = ba.AccountNumber
			break
		}
	}

	charged := 0
	for _, acc := range accounts {
		if acc.MaintenanceFee.IsZero() {
			continue
		}
		// Check sufficient balance (pre-check; authoritative check is inside Transfer).
		if acc.Balance.LessThan(acc.MaintenanceFee) {
			log.Printf("warn: insufficient balance for maintenance fee on %s (balance: %s, fee: %s)",
				acc.AccountNumber, acc.Balance.StringFixed(2), acc.MaintenanceFee.StringFixed(2))
			continue
		}

		if bankRSDNumber == "" {
			log.Printf("warn: no bank RSD account found; skipping maintenance fee for %s", acc.AccountNumber)
			continue
		}

		// Use LedgerService.Transfer for atomic debit+credit in a single transaction.
		// If the credit step fails, the entire TX is rolled back and no money is lost.
		refID := fmt.Sprintf("maint-%s-%s", acc.AccountNumber, time.Now().Format("2006-01"))
		if err := s.ledgerSvc.Transfer(ctx, acc.AccountNumber, bankRSDNumber, acc.MaintenanceFee,
			"Monthly maintenance fee", refID, "maintenance"); err != nil {
			log.Printf("error charging maintenance fee for %s: %v", acc.AccountNumber, err)
			continue
		}
		charged++
		s.notifyMaintenanceFeeCharged(ctx, &acc)
	}
	log.Printf("maintenance fees charged: %d/%d accounts", charged, len(accounts))
}

// notifyMaintenanceFeeCharged publishes the domain MaintenanceFeeCharged
// event and, for non-bank accounts, an in-app MAINTENANCE_FEE_CHARGED
// notification. Errors are intentionally discarded (best-effort): the
// ledger transfer has already committed and a Kafka outage must not
// break the cron loop.
func (s *MaintenanceCronService) notifyMaintenanceFeeCharged(ctx context.Context, acc *model.Account) {
	if s.producer == nil || acc == nil {
		return
	}
	amount := acc.MaintenanceFee.StringFixed(2)
	_ = s.producer.PublishMaintenanceFeeCharged(ctx, kafkamsg.MaintenanceFeeChargedMessage{
		AccountNumber: acc.AccountNumber,
		Amount:        amount,
		CurrencyCode:  acc.CurrencyCode,
	})
	if !acc.IsBankAccount {
		_ = s.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID: acc.OwnerID,
			Type:   "MAINTENANCE_FEE_CHARGED",
			Data: map[string]string{
				"account_number": acc.AccountNumber,
				"amount":         amount,
				"currency":       acc.CurrencyCode,
			},
			RefType: "account",
			RefID:   acc.ID,
		})
	}
}
