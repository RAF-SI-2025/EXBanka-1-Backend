package service

import (
	"context"
	"log"
	"time"

	"github.com/shopspring/decimal"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/stock-service/internal/model"
)

type TaxService struct {
	capitalGainRepo   CapitalGainRepo
	taxCollectionRepo TaxCollectionRepo
	holdingRepo       HoldingRepo
	accountClient     accountpb.AccountServiceClient
	exchangeClient    exchangepb.ExchangeServiceClient
	stateAccountNo    string
}

func NewTaxService(
	capitalGainRepo CapitalGainRepo,
	taxCollectionRepo TaxCollectionRepo,
	holdingRepo HoldingRepo,
	accountClient accountpb.AccountServiceClient,
	exchangeClient exchangepb.ExchangeServiceClient,
	stateAccountNo string,
) *TaxService {
	return &TaxService{
		capitalGainRepo:   capitalGainRepo,
		taxCollectionRepo: taxCollectionRepo,
		holdingRepo:       holdingRepo,
		accountClient:     accountClient,
		exchangeClient:    exchangeClient,
		stateAccountNo:    stateAccountNo,
	}
}

// ListTaxRecords returns users with their tax debt for supervisor portal.
func (s *TaxService) ListTaxRecords(year, month int, filter TaxFilter) ([]TaxUserSummary, int64, error) {
	return s.taxCollectionRepo.ListUsersWithGains(year, month, filter)
}

// GetUserTaxSummary returns tax summary for a (user_id, system_type) owner's
// portfolio page.
func (s *TaxService) GetUserTaxSummary(userID uint64, systemType string) (taxPaidThisYear, taxUnpaidThisMonth decimal.Decimal, err error) {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// Tax paid this year
	taxPaidThisYear, err = s.taxCollectionRepo.SumByUserYear(userID, systemType, year)
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}

	// Tax owed this month (uncollected)
	gainSummaries, err := s.capitalGainRepo.SumByUserMonth(userID, systemType, year, month)
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}

	// Already collected this month
	collectedThisMonth, err := s.taxCollectionRepo.SumByUserMonth(userID, systemType, year, month)
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}

	// Compute uncollected tax in RSD
	taxRate := decimal.NewFromFloat(0.15)
	totalUnpaidRSD := decimal.Zero
	for _, gs := range gainSummaries {
		if gs.TotalGain.IsPositive() {
			taxInCurrency := gs.TotalGain.Mul(taxRate)
			taxInRSD := taxInCurrency
			if gs.Currency != "RSD" {
				converted, convErr := s.convertToRSD(gs.Currency, taxInCurrency)
				if convErr == nil {
					taxInRSD = converted
				}
			}
			totalUnpaidRSD = totalUnpaidRSD.Add(taxInRSD)
		}
	}
	taxUnpaidThisMonth = totalUnpaidRSD.Sub(collectedThisMonth).Round(2)
	if taxUnpaidThisMonth.IsNegative() {
		taxUnpaidThisMonth = decimal.Zero
	}

	return taxPaidThisYear, taxUnpaidThisMonth, nil
}

// ListUserTaxRecords returns paginated capital gain records for a single
// (user_id, system_type) owner.
func (s *TaxService) ListUserTaxRecords(userID uint64, systemType string, page, pageSize int) ([]model.CapitalGain, int64, error) {
	return s.capitalGainRepo.ListByUser(userID, systemType, page, pageSize)
}

// CollectTax collects tax from all users for the given month.
// Returns the number of users collected, total RSD, and failures.
func (s *TaxService) CollectTax(year, month int) (collectedCount int64, totalRSD decimal.Decimal, failedCount int64, err error) {
	StockTaxCollectedTotal.Inc()
	// Get all users with gains this month (broad filter, all pages)
	summaries, _, err := s.taxCollectionRepo.ListUsersWithGains(year, month, TaxFilter{
		Page:     1,
		PageSize: 10000, // collect all
	})
	if err != nil {
		return 0, decimal.Zero, 0, err
	}

	taxRate := decimal.NewFromFloat(0.15)

	for _, summary := range summaries {
		if summary.TotalDebtRSD.IsZero() || summary.TotalDebtRSD.IsNegative() {
			continue
		}

		// Get detailed gains per account, scoped to this (user_id, system_type)
		// pair so employee and client bookkeeping stay separate when IDs collide.
		gainSummaries, gainErr := s.capitalGainRepo.SumByUserMonth(summary.UserID, summary.SystemType, year, month)
		if gainErr != nil {
			failedCount++
			log.Printf("WARN: tax: failed to get gains for user %d (%s): %v", summary.UserID, summary.SystemType, gainErr)
			continue
		}

		userFailed := false
		for _, gs := range gainSummaries {
			if !gs.TotalGain.IsPositive() {
				continue
			}

			taxInCurrency := gs.TotalGain.Mul(taxRate).Round(4)
			taxInRSD := taxInCurrency

			// Convert to RSD if needed
			if gs.Currency != "RSD" {
				converted, convErr := s.convertToRSD(gs.Currency, taxInCurrency)
				if convErr != nil {
					log.Printf("WARN: tax: currency conversion failed for user %d: %v", summary.UserID, convErr)
					userFailed = true
					continue
				}
				taxInRSD = converted
			}

			// Debit user's account
			acctResp, acctErr := s.accountClient.GetAccount(context.Background(), &accountpb.GetAccountRequest{Id: gs.AccountID})
			if acctErr != nil {
				log.Printf("WARN: tax: account lookup failed for user %d account %d: %v", summary.UserID, gs.AccountID, acctErr)
				userFailed = true
				continue
			}

			_, debitErr := s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
				AccountNumber:   acctResp.AccountNumber,
				Amount:          taxInCurrency.Neg().StringFixed(4),
				UpdateAvailable: true,
			})
			if debitErr != nil {
				log.Printf("WARN: tax: debit failed for user %d account %s: %v", summary.UserID, acctResp.AccountNumber, debitErr)
				userFailed = true
				continue
			}

			// Credit state's RSD account
			_, creditErr := s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
				AccountNumber:   s.stateAccountNo,
				Amount:          taxInRSD.StringFixed(4),
				UpdateAvailable: true,
			})
			if creditErr != nil {
				log.Printf("WARN: tax: credit state account failed for user %d: %v", summary.UserID, creditErr)
			}

			// Record collection
			collection := &model.TaxCollection{
				UserID:       summary.UserID,
				SystemType:   summary.SystemType,
				Year:         year,
				Month:        month,
				AccountID:    gs.AccountID,
				Currency:     gs.Currency,
				TotalGain:    gs.TotalGain,
				TaxAmount:    taxInCurrency,
				TaxAmountRSD: taxInRSD,
				CollectedAt:  time.Now(),
			}
			if createErr := s.taxCollectionRepo.Create(collection); createErr != nil {
				log.Printf("WARN: tax: failed to record collection for user %d: %v", summary.UserID, createErr)
			}

			totalRSD = totalRSD.Add(taxInRSD)
		}

		if userFailed {
			failedCount++
		} else {
			collectedCount++
		}
	}

	return collectedCount, totalRSD.Round(2), failedCount, nil
}

// convertToRSD converts an amount from a foreign currency to RSD.
func (s *TaxService) convertToRSD(fromCurrency string, amount decimal.Decimal) (decimal.Decimal, error) {
	resp, err := s.exchangeClient.Convert(context.Background(), &exchangepb.ConvertRequest{
		FromCurrency: fromCurrency,
		ToCurrency:   "RSD",
		Amount:       amount.StringFixed(4),
	})
	if err != nil {
		return decimal.Zero, err
	}
	converted, err := decimal.NewFromString(resp.ConvertedAmount)
	if err != nil {
		return decimal.Zero, err
	}
	return converted, nil
}
