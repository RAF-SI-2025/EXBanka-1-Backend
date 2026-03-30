package service

import (
	"fmt"
	"log"
	"time"

	"github.com/exbanka/account-service/internal/repository"
)

type MaintenanceCronService struct {
	accountRepo *repository.AccountRepository
	ledgerSvc   *LedgerService
}

func NewMaintenanceCronService(accountRepo *repository.AccountRepository, ledgerSvc *LedgerService) *MaintenanceCronService {
	return &MaintenanceCronService{accountRepo: accountRepo, ledgerSvc: ledgerSvc}
}

func (s *MaintenanceCronService) Start() {
	go s.runMonthlyCharge()
}

func (s *MaintenanceCronService) runMonthlyCharge() {
	for {
		now := time.Now()
		// Next 1st of month at 00:01
		nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 1, 0, 0, now.Location())
		time.Sleep(time.Until(nextMonth))
		s.chargeMaintenanceFees()
	}
}

func (s *MaintenanceCronService) chargeMaintenanceFees() {
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
		if err := s.ledgerSvc.Transfer(acc.AccountNumber, bankRSDNumber, acc.MaintenanceFee,
			"Monthly maintenance fee", refID, "maintenance"); err != nil {
			log.Printf("error charging maintenance fee for %s: %v", acc.AccountNumber, err)
			continue
		}
		charged++
	}
	log.Printf("maintenance fees charged: %d/%d accounts", charged, len(accounts))
}
