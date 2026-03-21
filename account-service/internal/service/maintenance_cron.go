package service

import (
	"log"
	"time"

	"github.com/exbanka/account-service/internal/repository"
)

type MaintenanceCronService struct {
	accountRepo *repository.AccountRepository
}

func NewMaintenanceCronService(accountRepo *repository.AccountRepository) *MaintenanceCronService {
	return &MaintenanceCronService{accountRepo: accountRepo}
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

	// Get bank RSD account for crediting fees
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
		// Check sufficient balance
		if acc.Balance.LessThan(acc.MaintenanceFee) {
			log.Printf("warn: insufficient balance for maintenance fee on %s (balance: %s, fee: %s)",
				acc.AccountNumber, acc.Balance.StringFixed(2), acc.MaintenanceFee.StringFixed(2))
			continue
		}
		// Debit account (updateAvailable=true so available balance is also adjusted)
		if err := s.accountRepo.UpdateBalance(acc.AccountNumber, acc.MaintenanceFee.Neg(), true); err != nil {
			log.Printf("error charging maintenance fee for %s: %v", acc.AccountNumber, err)
			continue
		}
		// Credit bank RSD account
		if bankRSDNumber != "" {
			if err := s.accountRepo.UpdateBalance(bankRSDNumber, acc.MaintenanceFee, true); err != nil {
				log.Printf("error crediting bank RSD account for maintenance fee on %s: %v", acc.AccountNumber, err)
			}
		}
		charged++
	}
	log.Printf("maintenance fees charged: %d/%d accounts", charged, len(accounts))
}
