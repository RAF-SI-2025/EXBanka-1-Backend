package service

import (
	"context"
	"log"

	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
)

type ReconciliationService struct {
	db        *gorm.DB
	ledgerSvc *LedgerService
}

func NewReconciliationService(db *gorm.DB, ledgerSvc *LedgerService) *ReconciliationService {
	return &ReconciliationService{db: db, ledgerSvc: ledgerSvc}
}

// CheckAllBalances iterates over all accounts and compares the stored balance
// to the sum of ledger entries. Mismatches are logged as WARNINGs — no
// auto-correction is performed. Returns the number of mismatches found.
func (s *ReconciliationService) CheckAllBalances(ctx context.Context) int {
	var accounts []model.Account
	if err := s.db.Select("account_number").Find(&accounts).Error; err != nil {
		log.Printf("WARNING: reconciliation check failed to load accounts: %v", err)
		return 0
	}

	mismatches := 0
	for _, acct := range accounts {
		if err := s.ledgerSvc.ReconcileBalance(ctx, acct.AccountNumber); err != nil {
			log.Printf("WARNING: %v", err)
			mismatches++
		}
	}

	if mismatches == 0 {
		log.Printf("Ledger reconciliation: all %d accounts are consistent", len(accounts))
	} else {
		log.Printf("WARNING: Ledger reconciliation: %d of %d accounts have mismatches", mismatches, len(accounts))
	}

	return mismatches
}
