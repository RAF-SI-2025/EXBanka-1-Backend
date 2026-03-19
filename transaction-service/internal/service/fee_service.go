package service

import (
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/exbanka/transaction-service/internal/model"
)

// FeeRepo abstracts the TransferFeeRepository for testing.
type FeeRepo interface {
	Create(fee *model.TransferFee) error
	GetByID(id uint64) (*model.TransferFee, error)
	List() ([]model.TransferFee, error)
	Update(fee *model.TransferFee) error
	Deactivate(id uint64) error
	GetApplicableFees(amount decimal.Decimal, txType, currency string) ([]model.TransferFee, error)
}

type FeeService struct {
	repo FeeRepo
}

func NewFeeService(repo FeeRepo) *FeeService {
	return &FeeService{repo: repo}
}

// CalculateFee returns the total applicable fee for a transaction.
// IMPORTANT: Returns an error if DB lookup fails — the transaction MUST be rejected.
// Returns zero fee (not an error) if no rules match.
func (s *FeeService) CalculateFee(amount decimal.Decimal, txType, currency string) (decimal.Decimal, error) {
	fees, err := s.repo.GetApplicableFees(amount, txType, currency)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to determine applicable fees: %w", err)
	}
	total := decimal.Zero
	for _, fee := range fees {
		var thisFee decimal.Decimal
		if fee.FeeType == "percentage" {
			thisFee = amount.Mul(fee.FeeValue).Div(decimal.NewFromInt(100))
		} else {
			thisFee = fee.FeeValue
		}
		if fee.MaxFee.IsPositive() && thisFee.GreaterThan(fee.MaxFee) {
			thisFee = fee.MaxFee
		}
		total = total.Add(thisFee)
	}
	return total, nil
}

func (s *FeeService) CreateFee(fee *model.TransferFee) error {
	return s.repo.Create(fee)
}

func (s *FeeService) ListFees() ([]model.TransferFee, error) {
	return s.repo.List()
}

func (s *FeeService) GetFee(id uint64) (*model.TransferFee, error) {
	return s.repo.GetByID(id)
}

func (s *FeeService) UpdateFee(fee *model.TransferFee) error {
	return s.repo.Update(fee)
}

func (s *FeeService) DeactivateFee(id uint64) error {
	return s.repo.Deactivate(id)
}
