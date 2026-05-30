package service

import (
	"context"
	"testing"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// stubFundHoldingUpserter is a no-op upserter for wiring smoke tests.
type stubFundHoldingUpserter struct{}

func (stubFundHoldingUpserter) Upsert(_ *model.FundHolding) error { return nil }
func (stubFundHoldingUpserter) UpsertIdempotent(_ *model.FundHolding, _ string) error {
	return nil
}

// stubBankCommissionRecipient returns a fixed account number.
type stubBankCommissionRecipient struct{}

func (stubBankCommissionRecipient) BankCommissionAccountNumber(_ context.Context) (string, error) {
	return "BANK-COMM", nil
}

// stubHoldingTxRepo returns no rows.
type stubHoldingTxRepo struct{}

func (stubHoldingTxRepo) ListByHolding(_ model.OwnerType, _ *uint64, _ string, _ uint64, _ string, _, _ int) ([]repository.HoldingTransactionRow, int64, error) {
	return nil, 0, nil
}

// TestPortfolioService_WiringMethods covers WithFundHoldings,
// WithBankCommissionRecipient, WithHoldingTxRepo wiring helpers.
func TestPortfolioService_WiringMethods(t *testing.T) {
	svc := NewPortfolioService(nil, nil, nil, nil, nil, nil, nil, "S")
	withFH := svc.WithFundHoldings(stubFundHoldingUpserter{})
	if withFH.fundHoldingRepo == nil {
		t.Errorf("WithFundHoldings did not wire repo")
	}
	withBR := svc.WithBankCommissionRecipient(stubBankCommissionRecipient{})
	if withBR.bankCommissionRecipient == nil {
		t.Errorf("WithBankCommissionRecipient did not wire recipient")
	}
	withTx := svc.WithHoldingTxRepo(stubHoldingTxRepo{})
	if withTx.holdingTxRepo == nil {
		t.Errorf("WithHoldingTxRepo did not wire tx repo")
	}
}

// TestPortfolioService_ListHoldingTransactions_NilTxRepo returns an empty
// result when the read-side repo is not wired (legacy path).
func TestPortfolioService_ListHoldingTransactions_NilTxRepo(t *testing.T) {
	svc := buildPortfolioServiceWithEmptyMocks()
	uid := uint64(7)
	rows, total, err := svc.ListHoldingTransactions(1, model.OwnerClient, &uid, "", 1, 10)
	if err == nil {
		t.Fatal("expected error (holding not found)")
	}
	if total != 0 || len(rows) != 0 {
		t.Errorf("got total=%d len=%d", total, len(rows))
	}
}

// buildPortfolioServiceWithEmptyMocks returns a portfolio service backed by
// an empty mock holding repo so ListHoldingTransactions can run the
// "holding not found" branch without a real DB.
func buildPortfolioServiceWithEmptyMocks() *PortfolioService {
	holdingRepo := newMockHoldingRepo()
	return NewPortfolioService(holdingRepo, nil, nil, nil, nil, nil, nil, "STATE-RSD-001")
}

// TestFundService_GetByIDAndList exercises the FundService getter pass-throughs.
func TestFundService_GetByIDAndList(t *testing.T) {
	fx := newFundFixture(t)
	created, err := fx.svc.Create(context.Background(), CreateFundInput{Name: "Pass"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := fx.svc.GetByID(created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Pass" {
		t.Errorf("got %s", got.Name)
	}
	rows, total, err := fx.svc.List("", nil, 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Errorf("got total=%d len=%d", total, len(rows))
	}
}

// TestFundService_WithPositionReadsAndOutbox covers the WithPositionReads /
// WithOutbox wiring helpers (smoke).
func TestFundService_WithPositionReadsAndOutbox(t *testing.T) {
	fx := newFundFixture(t)
	withPR := fx.svc.WithPositionReads(nil)
	if withPR == nil {
		t.Error("WithPositionReads returned nil")
	}
	withOB := fx.svc.WithOutbox(nil, nil)
	if withOB == nil {
		t.Error("WithOutbox returned nil")
	}
}
