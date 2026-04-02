package service

import (
	"fmt"
	"log"

	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type RateConfigService struct {
	tierRepo   *repository.InterestRateTierRepository
	marginRepo *repository.BankMarginRepository
	db         *gorm.DB
}

func NewRateConfigService(tierRepo *repository.InterestRateTierRepository, marginRepo *repository.BankMarginRepository, db *gorm.DB) *RateConfigService {
	return &RateConfigService{tierRepo: tierRepo, marginRepo: marginRepo, db: db}
}

// SeedDefaults inserts the 7 rate tiers and 5 margin entries if tables are empty.
func (s *RateConfigService) SeedDefaults() error {
	tierCount, err := s.tierRepo.Count()
	if err != nil {
		return fmt.Errorf("failed to count interest rate tiers: %w", err)
	}
	if tierCount == 0 {
		defaultTiers := []model.InterestRateTier{
			{AmountFrom: decimal.NewFromInt(0), AmountTo: decimal.NewFromInt(500_000), FixedRate: decimal.NewFromFloat(6.25), VariableBase: decimal.NewFromFloat(6.25), Active: true},
			{AmountFrom: decimal.NewFromInt(500_000), AmountTo: decimal.NewFromInt(1_000_000), FixedRate: decimal.NewFromFloat(6.00), VariableBase: decimal.NewFromFloat(6.00), Active: true},
			{AmountFrom: decimal.NewFromInt(1_000_000), AmountTo: decimal.NewFromInt(2_000_000), FixedRate: decimal.NewFromFloat(5.75), VariableBase: decimal.NewFromFloat(5.75), Active: true},
			{AmountFrom: decimal.NewFromInt(2_000_000), AmountTo: decimal.NewFromInt(5_000_000), FixedRate: decimal.NewFromFloat(5.50), VariableBase: decimal.NewFromFloat(5.50), Active: true},
			{AmountFrom: decimal.NewFromInt(5_000_000), AmountTo: decimal.NewFromInt(10_000_000), FixedRate: decimal.NewFromFloat(5.25), VariableBase: decimal.NewFromFloat(5.25), Active: true},
			{AmountFrom: decimal.NewFromInt(10_000_000), AmountTo: decimal.NewFromInt(20_000_000), FixedRate: decimal.NewFromFloat(5.00), VariableBase: decimal.NewFromFloat(5.00), Active: true},
			{AmountFrom: decimal.NewFromInt(20_000_000), AmountTo: decimal.Zero, FixedRate: decimal.NewFromFloat(4.75), VariableBase: decimal.NewFromFloat(4.75), Active: true},
		}
		for i := range defaultTiers {
			if err := s.tierRepo.Create(&defaultTiers[i]); err != nil {
				return fmt.Errorf("failed to seed interest rate tier %d: %w", i, err)
			}
		}
		log.Printf("credit-service: seeded %d default interest rate tiers", len(defaultTiers))
	}

	marginCount, err := s.marginRepo.Count()
	if err != nil {
		return fmt.Errorf("failed to count bank margins: %w", err)
	}
	if marginCount == 0 {
		defaultMargins := []model.BankMargin{
			{LoanType: "cash", Margin: decimal.NewFromFloat(1.75), Active: true},
			{LoanType: "housing", Margin: decimal.NewFromFloat(1.50), Active: true},
			{LoanType: "auto", Margin: decimal.NewFromFloat(1.25), Active: true},
			{LoanType: "refinancing", Margin: decimal.NewFromFloat(1.00), Active: true},
			{LoanType: "student", Margin: decimal.NewFromFloat(0.75), Active: true},
		}
		for i := range defaultMargins {
			if err := s.marginRepo.Create(&defaultMargins[i]); err != nil {
				return fmt.Errorf("failed to seed bank margin %d: %w", i, err)
			}
		}
		log.Printf("credit-service: seeded %d default bank margins", len(defaultMargins))
	}

	return nil
}

// GetNominalRate looks up the current DB rate for a given loan type, interest type, and amount (in RSD equivalent).
func (s *RateConfigService) GetNominalRate(loanType, interestType string, amountRSD decimal.Decimal) (decimal.Decimal, error) {
	tier, err := s.tierRepo.FindByAmount(amountRSD)
	if err != nil {
		return decimal.Zero, fmt.Errorf("no interest rate tier found for amount %s: %w", amountRSD, err)
	}
	margin, err := s.marginRepo.FindByLoanType(loanType)
	if err != nil {
		return decimal.Zero, fmt.Errorf("no bank margin found for loan type %s: %w", loanType, err)
	}

	var baseRate decimal.Decimal
	if interestType == "fixed" {
		baseRate = tier.FixedRate
	} else {
		baseRate = tier.VariableBase
	}
	return baseRate.Add(margin.Margin), nil
}

// GetNominalRateComponents returns the base rate, bank margin, and combined nominal rate
// for a given loan type, interest type, and amount. Used to snapshot rate components on loan approval.
func (s *RateConfigService) GetNominalRateComponents(loanType, interestType string, amountRSD decimal.Decimal) (baseRate, bankMargin, nominalRate decimal.Decimal, err error) {
	tier, findErr := s.tierRepo.FindByAmount(amountRSD)
	if findErr != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("no interest rate tier found for amount %s: %w", amountRSD, findErr)
	}
	margin, marginErr := s.marginRepo.FindByLoanType(loanType)
	if marginErr != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("no bank margin found for loan type %s: %w", loanType, marginErr)
	}

	if interestType == "fixed" {
		baseRate = tier.FixedRate
	} else {
		baseRate = tier.VariableBase
	}
	return baseRate, margin.Margin, baseRate.Add(margin.Margin), nil
}

// --- Interest Rate Tier CRUD ---

func (s *RateConfigService) ListTiers() ([]model.InterestRateTier, error) {
	tiers, err := s.tierRepo.ListAll()
	if err != nil {
		return nil, fmt.Errorf("failed to list interest rate tiers: %w", err)
	}
	return tiers, nil
}

func (s *RateConfigService) CreateTier(tier *model.InterestRateTier) error {
	if tier.FixedRate.IsNegative() {
		return fmt.Errorf("fixed_rate must not be negative")
	}
	if tier.VariableBase.IsNegative() {
		return fmt.Errorf("variable_base must not be negative")
	}
	if tier.AmountFrom.IsNegative() {
		return fmt.Errorf("amount_from must not be negative")
	}
	if tier.AmountTo.IsNegative() {
		return fmt.Errorf("amount_to must not be negative")
	}
	if !tier.AmountTo.IsZero() && tier.AmountTo.LessThanOrEqual(tier.AmountFrom) {
		return fmt.Errorf("amount_to must be greater than amount_from (or 0 for unlimited)")
	}
	tier.Active = true
	if err := s.tierRepo.Create(tier); err != nil {
		return fmt.Errorf("failed to create interest rate tier: %w", err)
	}
	return nil
}

func (s *RateConfigService) UpdateTier(tier *model.InterestRateTier) error {
	existing, err := s.tierRepo.GetByID(tier.ID)
	if err != nil {
		return fmt.Errorf("interest rate tier %d not found: %w", tier.ID, err)
	}
	if tier.FixedRate.IsNegative() {
		return fmt.Errorf("fixed_rate must not be negative")
	}
	if tier.VariableBase.IsNegative() {
		return fmt.Errorf("variable_base must not be negative")
	}
	if tier.AmountFrom.IsNegative() {
		return fmt.Errorf("amount_from must not be negative")
	}
	if tier.AmountTo.IsNegative() {
		return fmt.Errorf("amount_to must not be negative")
	}
	if !tier.AmountTo.IsZero() && tier.AmountTo.LessThanOrEqual(tier.AmountFrom) {
		return fmt.Errorf("amount_to must be greater than amount_from (or 0 for unlimited)")
	}

	existing.AmountFrom = tier.AmountFrom
	existing.AmountTo = tier.AmountTo
	existing.FixedRate = tier.FixedRate
	existing.VariableBase = tier.VariableBase

	if err := s.tierRepo.Update(existing); err != nil {
		return fmt.Errorf("failed to update interest rate tier %d: %w", tier.ID, err)
	}
	// Copy back full record so caller has all fields (timestamps, active, etc.)
	*tier = *existing
	return nil
}

func (s *RateConfigService) DeleteTier(id uint64) error {
	_, err := s.tierRepo.GetByID(id)
	if err != nil {
		return fmt.Errorf("interest rate tier %d not found: %w", id, err)
	}
	if err := s.tierRepo.Delete(id); err != nil {
		return fmt.Errorf("failed to delete interest rate tier %d: %w", id, err)
	}
	return nil
}

// --- Bank Margin CRUD ---

func (s *RateConfigService) ListMargins() ([]model.BankMargin, error) {
	margins, err := s.marginRepo.ListAll()
	if err != nil {
		return nil, fmt.Errorf("failed to list bank margins: %w", err)
	}
	return margins, nil
}

// ApplyVariableRateUpdate propagates a changed variable base rate from a tier to
// all active variable-rate loans whose original amount falls in that tier's range.
// Unpaid installments are recalculated with the new nominal rate. Already-paid
// installments remain unchanged. Returns the number of affected loans.
func (s *RateConfigService) ApplyVariableRateUpdate(tierID uint64, loanRepo *repository.LoanRepository, installRepo *repository.InstallmentRepository) (int, error) {
	tier, err := s.tierRepo.GetByID(tierID)
	if err != nil {
		return 0, fmt.Errorf("interest rate tier %d not found: %w", tierID, err)
	}

	loans, err := loanRepo.FindActiveVariableLoansInAmountRange(tier.AmountFrom, tier.AmountTo)
	if err != nil {
		return 0, fmt.Errorf("failed to find variable-rate loans for tier %d: %w", tierID, err)
	}

	count := 0
	for i := range loans {
		loan := &loans[i]
		newBaseRate := tier.VariableBase
		newNominalRate := newBaseRate.Add(loan.BankMargin)
		newEffectiveRate := CalculateEffectiveInterestRate(newNominalRate, 12)

		// Count remaining unpaid installments to recalculate monthly payment.
		unpaidCount, countErr := installRepo.CountUnpaidByLoan(loan.ID)
		if countErr != nil {
			log.Printf("warn: could not count unpaid installments for loan %d: %v", loan.ID, countErr)
			continue
		}
		if unpaidCount == 0 {
			continue
		}

		newMonthly := CalculateMonthlyInstallment(loan.RemainingDebt, newNominalRate, int(unpaidCount))

		// Wrap loan rate update + installment update in a single transaction so
		// they are always consistent — a partial update would leave the loan rate
		// changed but installment amounts unchanged.
		loanID := loan.ID
		txErr := s.db.Transaction(func(tx *gorm.DB) error {
			loan.BaseRate = newBaseRate
			loan.CurrentRate = newNominalRate
			loan.NominalInterestRate = newNominalRate
			loan.EffectiveInterestRate = newEffectiveRate
			loan.NextInstallmentAmount = newMonthly

			result := tx.Save(loan)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return fmt.Errorf("optimistic lock conflict: loan %d was modified concurrently", loanID)
			}

			// Bulk-update all unpaid installments; SkipHooks intentional (bulk sweep).
			return tx.Session(&gorm.Session{SkipHooks: true}).
				Model(&model.Installment{}).
				Where("loan_id = ? AND status = ?", loanID, "unpaid").
				Updates(map[string]interface{}{
					"amount":        newMonthly,
					"interest_rate": newNominalRate,
				}).Error
		})
		if txErr != nil {
			log.Printf("warn: could not update loan %d for variable rate change: %v", loanID, txErr)
			continue
		}

		count++
	}
	return count, nil
}

func (s *RateConfigService) UpdateMargin(margin *model.BankMargin) error {
	existing, err := s.marginRepo.GetByID(margin.ID)
	if err != nil {
		return fmt.Errorf("bank margin %d not found: %w", margin.ID, err)
	}
	if margin.Margin.IsNegative() {
		return fmt.Errorf("margin must not be negative")
	}

	existing.Margin = margin.Margin

	if err := s.marginRepo.Update(existing); err != nil {
		return fmt.Errorf("failed to update bank margin %d: %w", margin.ID, err)
	}
	// Copy back full record so caller has all fields (timestamps, active, loan_type, etc.)
	*margin = *existing
	return nil
}
