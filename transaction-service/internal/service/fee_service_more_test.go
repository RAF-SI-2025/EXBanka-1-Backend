package service

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/transaction-service/internal/model"
)

// failingFeeRepo always returns errors. Used to exercise the fee-service's
// repo-error paths.
type failingFeeRepo struct{}

func (f *failingFeeRepo) Create(*model.TransferFee) error { return errors.New("create boom") }
func (f *failingFeeRepo) GetByID(uint64) (*model.TransferFee, error) {
	return nil, errors.New("get boom")
}
func (f *failingFeeRepo) List() ([]model.TransferFee, error) { return nil, errors.New("list boom") }
func (f *failingFeeRepo) Update(*model.TransferFee) error    { return errors.New("update boom") }
func (f *failingFeeRepo) Deactivate(uint64) error            { return errors.New("deactivate boom") }
func (f *failingFeeRepo) GetApplicableFees(decimal.Decimal, string, string) ([]model.TransferFee, error) {
	return nil, errors.New("applicable boom")
}

// stubFeeRepo collects calls and returns canned responses for verifying CRUD.
type stubFeeRepo struct {
	created     []*model.TransferFee
	updated     []*model.TransferFee
	deactivated []uint64
	fees        []model.TransferFee
}

func (s *stubFeeRepo) Create(f *model.TransferFee) error {
	f.ID = uint64(len(s.created)) + 1
	s.created = append(s.created, f)
	return nil
}
func (s *stubFeeRepo) GetByID(id uint64) (*model.TransferFee, error) {
	for i := range s.created {
		if s.created[i].ID == id {
			return s.created[i], nil
		}
	}
	return nil, errors.New("not found")
}
func (s *stubFeeRepo) List() ([]model.TransferFee, error) { return s.fees, nil }
func (s *stubFeeRepo) Update(f *model.TransferFee) error {
	s.updated = append(s.updated, f)
	return nil
}
func (s *stubFeeRepo) Deactivate(id uint64) error {
	s.deactivated = append(s.deactivated, id)
	return nil
}
func (s *stubFeeRepo) GetApplicableFees(decimal.Decimal, string, string) ([]model.TransferFee, error) {
	return nil, nil
}

func TestNewFeeService_Constructor(t *testing.T) {
	svc := NewFeeService(&stubFeeRepo{})
	if svc == nil {
		t.Fatalf("constructor returned nil")
	}
}

// TestCalculateFee_RepoError verifies that a repo error rejects (returns error)
// — banking rule per the spec.
func TestCalculateFee_RepoError(t *testing.T) {
	svc := NewFeeService(&failingFeeRepo{})
	_, err := svc.CalculateFee(decimal.NewFromInt(100), "payment", "RSD")
	if err == nil {
		t.Fatalf("expected error on repo failure")
	}
}

// TestCalculateFeeDetailed_HappyPath verifies the detailed breakdown shape.
func TestCalculateFeeDetailed_HappyPath(t *testing.T) {
	mock := &mockFeeRepo{fees: []model.TransferFee{
		{Active: true, FeeType: "percentage", FeeValue: decimal.NewFromFloat(0.1),
			MinAmount: decimal.NewFromInt(0), TransactionType: "all", Name: "Pct"},
		{Active: true, FeeType: "fixed", FeeValue: decimal.NewFromInt(50),
			TransactionType: "all", Name: "Fixed"},
	}}
	svc := NewFeeService(mock)
	total, details, err := svc.CalculateFeeDetailed(decimal.NewFromInt(10000), "payment", "RSD")
	require.NoError(t, err)
	assert.True(t, total.Equal(decimal.NewFromInt(60)), "10 + 50 = 60")
	require.Len(t, details, 2)
	assert.Equal(t, "Pct", details[0].Name)
	assert.True(t, details[0].CalculatedAmount.Equal(decimal.NewFromInt(10)))
	assert.Equal(t, "Fixed", details[1].Name)
	assert.True(t, details[1].CalculatedAmount.Equal(decimal.NewFromInt(50)))
}

// TestCalculateFeeDetailed_RepoError verifies the error path.
func TestCalculateFeeDetailed_RepoError(t *testing.T) {
	svc := NewFeeService(&failingFeeRepo{})
	_, _, err := svc.CalculateFeeDetailed(decimal.NewFromInt(100), "payment", "RSD")
	if err == nil {
		t.Fatalf("expected error")
	}
}

// TestCalculateFeeDetailed_PercentageWithCap verifies the per-rule cap branch
// in CalculateFeeDetailed.
func TestCalculateFeeDetailed_PercentageWithCap(t *testing.T) {
	mock := &mockFeeRepo{fees: []model.TransferFee{
		{Active: true, FeeType: "percentage", FeeValue: decimal.NewFromFloat(0.5),
			MaxFee: decimal.NewFromInt(1000), TransactionType: "all", Name: "Capped"},
	}}
	svc := NewFeeService(mock)
	total, details, err := svc.CalculateFeeDetailed(decimal.NewFromInt(1_000_000), "payment", "RSD")
	require.NoError(t, err)
	assert.True(t, total.Equal(decimal.NewFromInt(1000)), "capped at 1000")
	assert.True(t, details[0].CalculatedAmount.Equal(decimal.NewFromInt(1000)))
}

// TestFeeService_CRUD_Pass-through verifies the CRUD methods pass through to
// the repository.
func TestFeeService_CRUD_PassThrough(t *testing.T) {
	repo := &stubFeeRepo{
		fees: []model.TransferFee{{ID: 7, Name: "Repo Fee", Active: true}},
	}
	svc := NewFeeService(repo)

	fee := &model.TransferFee{Name: "New", FeeType: "fixed", FeeValue: decimal.NewFromInt(10)}
	require.NoError(t, svc.CreateFee(fee))
	require.Len(t, repo.created, 1)
	assert.Equal(t, uint64(1), fee.ID)

	list, err := svc.ListFees()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "Repo Fee", list[0].Name)

	got, err := svc.GetFee(1)
	require.NoError(t, err)
	assert.Equal(t, "New", got.Name)

	got.Name = "Renamed"
	require.NoError(t, svc.UpdateFee(got))
	require.Len(t, repo.updated, 1)
	assert.Equal(t, "Renamed", repo.updated[0].Name)

	require.NoError(t, svc.DeactivateFee(1))
	require.Len(t, repo.deactivated, 1)
	assert.Equal(t, uint64(1), repo.deactivated[0])
}
