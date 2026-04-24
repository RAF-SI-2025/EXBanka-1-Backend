package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
)

// newReservationFixture builds a ReservationService backed by an in-memory
// SQLite DB, seeds a single non-bank RSD account with balance=1000,
// available=1000, reserved=0 and returns the service, repos, and the
// seeded account's ID.
func newReservationFixture(t *testing.T) (svc *ReservationService, accountRepo *repository.AccountRepository, ledgerRepo *repository.LedgerRepository, accountID uint64, accountNumber string) {
	t.Helper()
	db := newTestDB(t)
	accountRepo = repository.NewAccountRepository(db)
	ledgerRepo = repository.NewLedgerRepository(db)
	resRepo := repository.NewAccountReservationRepository(db)
	svc = NewReservationService(db, accountRepo, resRepo, ledgerRepo)

	acc := &model.Account{
		AccountNumber:    "111000100000099011",
		OwnerID:          10,
		CurrencyCode:     "RSD",
		AccountKind:      "current",
		AccountType:      "standard",
		Status:           "active",
		Balance:          decimal.NewFromInt(1000),
		AvailableBalance: decimal.NewFromInt(1000),
		ReservedBalance:  decimal.Zero,
		DailyLimit:       decimal.NewFromInt(1_000_000),
		MonthlyLimit:     decimal.NewFromInt(10_000_000),
		IsBankAccount:    false,
		ExpiresAt:        time.Now().AddDate(5, 0, 0),
		Version:          1,
	}
	require.NoError(t, accountRepo.Create(acc))
	return svc, accountRepo, ledgerRepo, acc.ID, acc.AccountNumber
}

func TestReserveFunds_HappyPath(t *testing.T) {
	svc, accountRepo, _, accountID, _ := newReservationFixture(t)

	_, err := svc.ReserveFunds(context.Background(), 500, accountID, decimal.NewFromInt(400), "RSD")
	if err != nil {
		t.Fatalf("ReserveFunds: %v", err)
	}
	acc, _ := accountRepo.GetByID(accountID)
	if !acc.Balance.Equal(decimal.NewFromInt(1000)) {
		t.Errorf("balance unchanged expected: got %s", acc.Balance)
	}
	if !acc.ReservedBalance.Equal(decimal.NewFromInt(400)) {
		t.Errorf("reserved_balance: got %s want 400", acc.ReservedBalance)
	}
	if !acc.AvailableBalance.Equal(decimal.NewFromInt(600)) {
		t.Errorf("available_balance: got %s want 600", acc.AvailableBalance)
	}
}

func TestReserveFunds_Idempotent(t *testing.T) {
	svc, accountRepo, _, accountID, _ := newReservationFixture(t)
	ctx := context.Background()

	r1, err := svc.ReserveFunds(ctx, 500, accountID, decimal.NewFromInt(400), "RSD")
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	r2, err := svc.ReserveFunds(ctx, 500, accountID, decimal.NewFromInt(400), "RSD")
	if err != nil {
		t.Fatalf("second reserve: %v", err)
	}
	if r1.ReservationID != r2.ReservationID {
		t.Errorf("retry produced different reservation id (%d vs %d)", r1.ReservationID, r2.ReservationID)
	}
	acc, _ := accountRepo.GetByID(accountID)
	if !acc.ReservedBalance.Equal(decimal.NewFromInt(400)) {
		t.Errorf("retry double-counted reserved_balance: %s", acc.ReservedBalance)
	}
	if !acc.AvailableBalance.Equal(decimal.NewFromInt(600)) {
		t.Errorf("retry double-counted available_balance: %s", acc.AvailableBalance)
	}
}

func TestReserveFunds_InsufficientAvailable(t *testing.T) {
	svc, _, _, accountID, _ := newReservationFixture(t)
	_, err := svc.ReserveFunds(context.Background(), 500, accountID, decimal.NewFromInt(5000), "RSD")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %s", st.Code())
	}
}

func TestReserveFunds_CurrencyMismatch(t *testing.T) {
	svc, _, _, accountID, _ := newReservationFixture(t)
	_, err := svc.ReserveFunds(context.Background(), 500, accountID, decimal.NewFromInt(100), "USD")
	if err == nil {
		t.Fatal("expected currency-mismatch error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %s", st.Code())
	}
}

func TestPartialSettle_HappyPath_LedgerEntryWritten(t *testing.T) {
	svc, accountRepo, ledgerRepo, accountID, accountNumber := newReservationFixture(t)
	ctx := context.Background()
	_, err := svc.ReserveFunds(ctx, 500, accountID, decimal.NewFromInt(400), "RSD")
	require.NoError(t, err)

	resp, err := svc.PartialSettleReservation(ctx, 500, 9001, decimal.NewFromInt(150), "order 500 fill 9001")
	if err != nil {
		t.Fatalf("PartialSettle: %v", err)
	}
	if !resp.SettledAmount.Equal(decimal.NewFromInt(150)) {
		t.Errorf("settled amount: got %s", resp.SettledAmount)
	}
	acc, _ := accountRepo.GetByID(accountID)
	if !acc.Balance.Equal(decimal.NewFromInt(850)) {
		t.Errorf("balance after settle: got %s want 850", acc.Balance)
	}
	if !acc.ReservedBalance.Equal(decimal.NewFromInt(250)) {
		t.Errorf("reserved after settle: got %s want 250", acc.ReservedBalance)
	}
	if !acc.AvailableBalance.Equal(decimal.NewFromInt(600)) {
		t.Errorf("available unchanged by settle expected: got %s want 600", acc.AvailableBalance)
	}
	entries, _, err := ledgerRepo.GetEntriesByAccount(accountNumber, 1, 10)
	require.NoError(t, err)
	if len(entries) == 0 {
		t.Fatal("expected at least one ledger entry after partial settle")
	}
	// Sanity-check the entry fields.
	entry := entries[0]
	if entry.EntryType != "debit" {
		t.Errorf("entry_type: got %s want debit", entry.EntryType)
	}
	if !entry.Amount.Equal(decimal.NewFromInt(150)) {
		t.Errorf("entry.Amount: got %s want 150", entry.Amount)
	}
}

func TestPartialSettle_IdempotentOnTxnID(t *testing.T) {
	svc, accountRepo, _, accountID, _ := newReservationFixture(t)
	ctx := context.Background()
	_, err := svc.ReserveFunds(ctx, 500, accountID, decimal.NewFromInt(400), "RSD")
	require.NoError(t, err)

	if _, err := svc.PartialSettleReservation(ctx, 500, 9001, decimal.NewFromInt(150), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PartialSettleReservation(ctx, 500, 9001, decimal.NewFromInt(150), ""); err != nil {
		t.Fatal("expected idempotent no-op, got error:", err)
	}
	acc, _ := accountRepo.GetByID(accountID)
	if !acc.Balance.Equal(decimal.NewFromInt(850)) {
		t.Errorf("retry double-debited: balance %s want 850", acc.Balance)
	}
}

func TestPartialSettle_OverReservation(t *testing.T) {
	svc, _, _, accountID, _ := newReservationFixture(t)
	ctx := context.Background()
	_, err := svc.ReserveFunds(ctx, 500, accountID, decimal.NewFromInt(400), "RSD")
	require.NoError(t, err)

	_, err = svc.PartialSettleReservation(ctx, 500, 9001, decimal.NewFromInt(500), "")
	if err == nil {
		t.Fatal("expected over-reservation error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %s", st.Code())
	}
}

func TestPartialSettle_FullyFills_TransitionsToSettled(t *testing.T) {
	svc, _, _, accountID, _ := newReservationFixture(t)
	ctx := context.Background()
	_, err := svc.ReserveFunds(ctx, 500, accountID, decimal.NewFromInt(400), "RSD")
	require.NoError(t, err)

	_, err = svc.PartialSettleReservation(ctx, 500, 9001, decimal.NewFromInt(400), "")
	require.NoError(t, err)
	// Reservation should be SETTLED now — releasing should be a no-op returning 0.
	resp, err := svc.ReleaseReservation(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.ReleasedAmount.IsZero() {
		t.Errorf("expected 0 released after full settle, got %s", resp.ReleasedAmount)
	}
}

func TestRelease_ActiveReservation(t *testing.T) {
	svc, accountRepo, _, accountID, _ := newReservationFixture(t)
	ctx := context.Background()
	_, err := svc.ReserveFunds(ctx, 500, accountID, decimal.NewFromInt(400), "RSD")
	require.NoError(t, err)

	resp, err := svc.ReleaseReservation(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.ReleasedAmount.Equal(decimal.NewFromInt(400)) {
		t.Errorf("released: got %s want 400", resp.ReleasedAmount)
	}
	acc, _ := accountRepo.GetByID(accountID)
	if !acc.ReservedBalance.IsZero() {
		t.Errorf("reserved after release: %s", acc.ReservedBalance)
	}
	if !acc.AvailableBalance.Equal(decimal.NewFromInt(1000)) {
		t.Errorf("available after full release: got %s want 1000", acc.AvailableBalance)
	}
}

func TestRelease_Idempotent(t *testing.T) {
	svc, _, _, accountID, _ := newReservationFixture(t)
	ctx := context.Background()
	_, err := svc.ReserveFunds(ctx, 500, accountID, decimal.NewFromInt(400), "RSD")
	require.NoError(t, err)
	if _, err := svc.ReleaseReservation(ctx, 500); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReleaseReservation(ctx, 500); err != nil {
		t.Fatal("second release should be no-op")
	}
}

func TestRelease_NonexistentOrder_NoOp(t *testing.T) {
	svc, _, _, _, _ := newReservationFixture(t)
	resp, err := svc.ReleaseReservation(context.Background(), 99999)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.ReleasedAmount.IsZero() {
		t.Errorf("expected 0 released, got %s", resp.ReleasedAmount)
	}
}

func TestGetReservation_ReturnsSettledTxnIDs(t *testing.T) {
	svc, _, _, accountID, _ := newReservationFixture(t)
	ctx := context.Background()
	_, err := svc.ReserveFunds(ctx, 500, accountID, decimal.NewFromInt(400), "RSD")
	require.NoError(t, err)
	_, err = svc.PartialSettleReservation(ctx, 500, 9001, decimal.NewFromInt(100), "")
	require.NoError(t, err)
	_, err = svc.PartialSettleReservation(ctx, 500, 9002, decimal.NewFromInt(100), "")
	require.NoError(t, err)

	st, amount, settled, txnIDs, exists, err := svc.GetReservation(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("expected exists=true")
	}
	if st != model.ReservationStatusActive {
		t.Errorf("status: got %s", st)
	}
	if !amount.Equal(decimal.NewFromInt(400)) {
		t.Errorf("amount: got %s want 400", amount)
	}
	if !settled.Equal(decimal.NewFromInt(200)) {
		t.Errorf("settled_total: got %s want 200", settled)
	}
	if len(txnIDs) != 2 {
		t.Errorf("expected 2 settled txn ids, got %d", len(txnIDs))
	}
}
