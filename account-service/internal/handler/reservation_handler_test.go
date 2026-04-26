package handler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	"github.com/exbanka/account-service/internal/service"
	pb "github.com/exbanka/contract/accountpb"
)

// newReservationHandlerFixture spins up an in-memory SQLite DB, migrates the
// tables the reservation lifecycle touches, seeds one non-bank RSD account
// with balance=1000 / available=1000, and returns a wired ReservationHandler
// plus the seeded account ID.
func newReservationHandlerFixture(t *testing.T) (h *ReservationHandler, accountID uint64, cleanup func()) {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.Account{},
		&model.LedgerEntry{},
		&model.AccountReservation{},
		&model.AccountReservationSettlement{},
	))

	accountRepo := repository.NewAccountRepository(db)
	ledgerRepo := repository.NewLedgerRepository(db)
	resRepo := repository.NewAccountReservationRepository(db)
	svc := service.NewReservationService(db, accountRepo, resRepo, ledgerRepo)

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

	return NewReservationHandler(svc), acc.ID, func() { _ = sqlDB.Close() }
}

func TestReservationHandler_ReserveAndRelease(t *testing.T) {
	h, accountID, cleanup := newReservationHandlerFixture(t)
	defer cleanup()

	ctx := context.Background()
	resp, err := h.ReserveFunds(ctx, &pb.ReserveFundsRequest{
		AccountId:    accountID,
		OrderId:      10_001,
		Amount:       "100",
		CurrencyCode: "RSD",
	})
	if err != nil {
		t.Fatalf("ReserveFunds: %v", err)
	}
	if resp.ReservationId == 0 {
		t.Fatal("no reservation id returned")
	}

	// Sanity-check the exposed balances on the response.
	if got, want := resp.ReservedBalance, "100"; !decimalEqual(got, want) {
		t.Errorf("ReservedBalance: got %s want %s", got, want)
	}
	if got, want := resp.AvailableBalance, "900"; !decimalEqual(got, want) {
		t.Errorf("AvailableBalance: got %s want %s", got, want)
	}

	relResp, err := h.ReleaseReservation(ctx, &pb.ReleaseReservationRequest{OrderId: 10_001})
	if err != nil {
		t.Fatalf("ReleaseReservation: %v", err)
	}
	if !decimalEqual(relResp.ReleasedAmount, "100") {
		t.Errorf("ReleasedAmount: got %s want 100", relResp.ReleasedAmount)
	}

	// Post-release GetReservation should report the reservation as released
	// with no settlements.
	getResp, err := h.GetReservation(ctx, &pb.GetReservationRequest{OrderId: 10_001})
	if err != nil {
		t.Fatalf("GetReservation: %v", err)
	}
	if !getResp.Exists {
		t.Fatal("GetReservation: expected exists=true after release")
	}
	if getResp.Status != model.ReservationStatusReleased {
		t.Errorf("GetReservation Status: got %s want %s", getResp.Status, model.ReservationStatusReleased)
	}
	if len(getResp.SettledTransactionIds) != 0 {
		t.Errorf("SettledTransactionIds: got %v want empty", getResp.SettledTransactionIds)
	}
}

func TestReservationHandler_InvalidAmount(t *testing.T) {
	h, accountID, cleanup := newReservationHandlerFixture(t)
	defer cleanup()

	_, err := h.ReserveFunds(context.Background(), &pb.ReserveFundsRequest{
		AccountId:    accountID,
		OrderId:      10_002,
		Amount:       "not-a-number",
		CurrencyCode: "RSD",
	})
	if err == nil {
		t.Fatal("expected InvalidArgument, got nil")
	}
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("status code: got %s want InvalidArgument", code)
	}
}

// decimalEqual compares two decimal strings by value (so "100" == "100.0").
func decimalEqual(a, b string) bool {
	da, err1 := decimal.NewFromString(a)
	dbv, err2 := decimal.NewFromString(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return da.Equal(dbv)
}

// ---------------------------------------------------------------------------
// Mock-backed unit tests (cover code paths the integration fixture does not).
// ---------------------------------------------------------------------------

type mockReservationSvc struct {
	reserveFn func(ctx context.Context, orderID, accountID uint64, amount decimal.Decimal, currencyCode string) (*service.ReserveFundsResult, error)
	releaseFn func(ctx context.Context, orderID uint64) (*service.ReleaseResult, error)
	partialFn func(ctx context.Context, orderID, orderTransactionID uint64, amount decimal.Decimal, memo string) (*service.PartialSettleResult, error)
	getFn     func(ctx context.Context, orderID uint64) (string, decimal.Decimal, decimal.Decimal, []uint64, bool, error)
}

func (m *mockReservationSvc) ReserveFunds(ctx context.Context, orderID, accountID uint64, amount decimal.Decimal, currencyCode string) (*service.ReserveFundsResult, error) {
	if m.reserveFn != nil {
		return m.reserveFn(ctx, orderID, accountID, amount, currencyCode)
	}
	return &service.ReserveFundsResult{ReservationID: 1, ReservedBalance: amount, AvailableBalance: decimal.NewFromInt(1000)}, nil
}

func (m *mockReservationSvc) ReleaseReservation(ctx context.Context, orderID uint64) (*service.ReleaseResult, error) {
	if m.releaseFn != nil {
		return m.releaseFn(ctx, orderID)
	}
	return &service.ReleaseResult{ReleasedAmount: decimal.NewFromInt(100), ReservedBalance: decimal.Zero}, nil
}

func (m *mockReservationSvc) PartialSettleReservation(ctx context.Context, orderID, orderTransactionID uint64, amount decimal.Decimal, memo string) (*service.PartialSettleResult, error) {
	if m.partialFn != nil {
		return m.partialFn(ctx, orderID, orderTransactionID, amount, memo)
	}
	return &service.PartialSettleResult{
		SettledAmount: amount, RemainingReserved: decimal.Zero,
		BalanceAfter: decimal.NewFromInt(900), LedgerEntryID: 1,
	}, nil
}

func (m *mockReservationSvc) GetReservation(ctx context.Context, orderID uint64) (string, decimal.Decimal, decimal.Decimal, []uint64, bool, error) {
	if m.getFn != nil {
		return m.getFn(ctx, orderID)
	}
	return "active", decimal.NewFromInt(1000), decimal.NewFromInt(500), []uint64{1, 2}, true, nil
}

func TestReservationHandler_PartialSettle_Success(t *testing.T) {
	svc := &mockReservationSvc{
		partialFn: func(_ context.Context, _, _ uint64, amt decimal.Decimal, _ string) (*service.PartialSettleResult, error) {
			return &service.PartialSettleResult{
				SettledAmount: amt, RemainingReserved: decimal.NewFromInt(20),
				BalanceAfter: decimal.NewFromInt(900), LedgerEntryID: 5,
			}, nil
		},
	}
	h := newReservationHandlerForTest(svc)
	resp, err := h.PartialSettleReservation(context.Background(), &pb.PartialSettleReservationRequest{
		OrderId: 1, OrderTransactionId: 100, Amount: "30", Memo: "trade",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.LedgerEntryId != 5 {
		t.Errorf("expected LedgerEntryId=5, got %d", resp.LedgerEntryId)
	}
}

func TestReservationHandler_PartialSettle_InvalidAmount(t *testing.T) {
	h := newReservationHandlerForTest(&mockReservationSvc{})
	_, err := h.PartialSettleReservation(context.Background(), &pb.PartialSettleReservationRequest{Amount: "not-a-number"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestReservationHandler_PartialSettle_ServiceError(t *testing.T) {
	svc := &mockReservationSvc{
		partialFn: func(_ context.Context, _, _ uint64, _ decimal.Decimal, _ string) (*service.PartialSettleResult, error) {
			return nil, status.Error(codes.FailedPrecondition, "settle failed")
		},
	}
	h := newReservationHandlerForTest(svc)
	_, err := h.PartialSettleReservation(context.Background(), &pb.PartialSettleReservationRequest{Amount: "30"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", status.Code(err))
	}
}

func TestReservationHandler_GetReservation_Empty(t *testing.T) {
	svc := &mockReservationSvc{
		getFn: func(_ context.Context, _ uint64) (string, decimal.Decimal, decimal.Decimal, []uint64, bool, error) {
			return "", decimal.Zero, decimal.Zero, nil, false, nil
		},
	}
	h := newReservationHandlerForTest(svc)
	resp, err := h.GetReservation(context.Background(), &pb.GetReservationRequest{OrderId: 99})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Exists {
		t.Error("expected Exists=false")
	}
}

func TestReservationHandler_GetReservation_ServiceError(t *testing.T) {
	svc := &mockReservationSvc{
		getFn: func(_ context.Context, _ uint64) (string, decimal.Decimal, decimal.Decimal, []uint64, bool, error) {
			return "", decimal.Zero, decimal.Zero, nil, false, status.Error(codes.Internal, "db down")
		},
	}
	h := newReservationHandlerForTest(svc)
	_, err := h.GetReservation(context.Background(), &pb.GetReservationRequest{OrderId: 1})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

func TestReservationHandler_ReleaseReservation_ServiceError(t *testing.T) {
	svc := &mockReservationSvc{
		releaseFn: func(_ context.Context, _ uint64) (*service.ReleaseResult, error) {
			return nil, status.Error(codes.Internal, "release failed")
		},
	}
	h := newReservationHandlerForTest(svc)
	_, err := h.ReleaseReservation(context.Background(), &pb.ReleaseReservationRequest{OrderId: 1})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}
