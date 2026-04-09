package service

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/transaction-service/internal/model"
)

// mockFeeRepo is a test double for TransferFeeRepository.
type mockFeeRepo struct {
	fees []model.TransferFee
}

func (m *mockFeeRepo) Create(fee *model.TransferFee) error { return nil }
func (m *mockFeeRepo) GetByID(id uint64) (*model.TransferFee, error) {
	return nil, nil
}
func (m *mockFeeRepo) List() ([]model.TransferFee, error)  { return m.fees, nil }
func (m *mockFeeRepo) Update(fee *model.TransferFee) error { return nil }
func (m *mockFeeRepo) Deactivate(id uint64) error          { return nil }
func (m *mockFeeRepo) GetApplicableFees(amount decimal.Decimal, txType, currency string) ([]model.TransferFee, error) {
	var result []model.TransferFee
	for _, f := range m.fees {
		if !f.Active {
			continue
		}
		if f.TransactionType != txType && f.TransactionType != "all" {
			continue
		}
		if f.CurrencyCode != "" && f.CurrencyCode != currency {
			continue
		}
		if f.MinAmount.IsPositive() && amount.LessThan(f.MinAmount) {
			continue
		}
		result = append(result, f)
	}
	return result, nil
}

func TestCalculateFee_DefaultSeedFee(t *testing.T) {
	mock := &mockFeeRepo{fees: []model.TransferFee{
		{
			Active: true, FeeType: "percentage", FeeValue: decimal.NewFromFloat(0.1),
			MinAmount: decimal.NewFromInt(1000), TransactionType: "all",
		},
	}}
	svc := &FeeService{repo: mock}

	fee, err := svc.CalculateFee(decimal.NewFromInt(10000), "payment", "RSD")
	require.NoError(t, err)
	assert.True(t, fee.Equal(decimal.NewFromInt(10)), "0.1% of 10000 = 10")
}

func TestCalculateFee_BelowMinAmount(t *testing.T) {
	mock := &mockFeeRepo{fees: []model.TransferFee{
		{Active: true, FeeType: "percentage", FeeValue: decimal.NewFromFloat(0.1),
			MinAmount: decimal.NewFromInt(1000), TransactionType: "all"},
	}}
	svc := &FeeService{repo: mock}

	fee, err := svc.CalculateFee(decimal.NewFromInt(500), "payment", "RSD")
	require.NoError(t, err)
	assert.True(t, fee.Equal(decimal.Zero), "below min_amount -> zero fee")
}

func TestCalculateFee_PercentageWithCap(t *testing.T) {
	mock := &mockFeeRepo{fees: []model.TransferFee{
		{Active: true, FeeType: "percentage", FeeValue: decimal.NewFromFloat(0.5),
			MaxFee: decimal.NewFromInt(1000), TransactionType: "all"},
	}}
	svc := &FeeService{repo: mock}

	fee, err := svc.CalculateFee(decimal.NewFromInt(1000000), "payment", "RSD")
	require.NoError(t, err)
	assert.True(t, fee.Equal(decimal.NewFromInt(1000)), "capped at 1000")
}

func TestCalculateFee_FixedFee(t *testing.T) {
	mock := &mockFeeRepo{fees: []model.TransferFee{
		{Active: true, FeeType: "fixed", FeeValue: decimal.NewFromInt(50), TransactionType: "all"},
	}}
	svc := &FeeService{repo: mock}

	fee, err := svc.CalculateFee(decimal.NewFromInt(999), "payment", "RSD")
	require.NoError(t, err)
	assert.True(t, fee.Equal(decimal.NewFromInt(50)), "fixed fee regardless of amount")
}

func TestCalculateFee_MultipleFees_Stack(t *testing.T) {
	mock := &mockFeeRepo{fees: []model.TransferFee{
		{Active: true, FeeType: "percentage", FeeValue: decimal.NewFromFloat(0.1),
			MinAmount: decimal.NewFromInt(1000), TransactionType: "all"},
		{Active: true, FeeType: "fixed", FeeValue: decimal.NewFromInt(50), TransactionType: "all"},
	}}
	svc := &FeeService{repo: mock}

	fee, err := svc.CalculateFee(decimal.NewFromInt(10000), "payment", "RSD")
	require.NoError(t, err)
	assert.True(t, fee.Equal(decimal.NewFromInt(60)), "10 percentage + 50 fixed = 60")
}

func TestCalculateFee_CurrencySpecific(t *testing.T) {
	mock := &mockFeeRepo{fees: []model.TransferFee{
		{Active: true, FeeType: "fixed", FeeValue: decimal.NewFromInt(100),
			TransactionType: "all", CurrencyCode: "EUR"},
	}}
	svc := &FeeService{repo: mock}

	feeEUR, err := svc.CalculateFee(decimal.NewFromInt(5000), "payment", "EUR")
	require.NoError(t, err)
	assert.True(t, feeEUR.Equal(decimal.NewFromInt(100)), "EUR fee applies")

	feeRSD, err := svc.CalculateFee(decimal.NewFromInt(5000), "payment", "RSD")
	require.NoError(t, err)
	assert.True(t, feeRSD.Equal(decimal.Zero), "EUR fee does not apply to RSD")
}

func TestCalculateFee_NoMatchingFees_ZeroNotError(t *testing.T) {
	mock := &mockFeeRepo{fees: []model.TransferFee{}}
	svc := &FeeService{repo: mock}

	fee, err := svc.CalculateFee(decimal.NewFromInt(5000), "payment", "RSD")
	require.NoError(t, err)
	assert.True(t, fee.Equal(decimal.Zero))
}
