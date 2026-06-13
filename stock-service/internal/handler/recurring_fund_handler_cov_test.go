package handler

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

func newRecurringFundHandlerForCov(t *testing.T) (*RecurringFundHandler, *gorm.DB) {
	t.Helper()
	db := testutil.SetupTestDB(t, &model.RecurringFundInvestment{}, &model.InvestmentFund{})
	svc := service.NewRecurringFundService(
		repository.NewRecurringFundInvestmentRepository(db),
		repository.NewFundRepository(db),
		nil, nil,
	)
	return NewRecurringFundHandler(svc), db
}

func seedOpenFund(t *testing.T, db *gorm.DB, id, rsdAccount uint64, minContribution decimal.Decimal) {
	t.Helper()
	if err := db.Create(&model.InvestmentFund{
		ID: id, Name: "Fund", ManagerEmployeeID: 1,
		RSDAccountID: rsdAccount, Active: true,
		MinimumContributionRSD: minContribution,
		FundType:               model.FundTypeOpen, FundStatus: model.FundStatusOpen,
	}).Error; err != nil {
		t.Fatalf("seed fund: %v", err)
	}
}

func TestRecurringFundHandler_CreateGetLifecycle(t *testing.T) {
	h, db := newRecurringFundHandlerForCov(t)
	seedOpenFund(t, db, 1, 500, decimal.Zero)
	ctx := context.Background()

	created, err := h.Create(ctx, &pb.CreateRecurringFundRequest{
		ClientId: 7, FundId: 1, SourceAccountId: 9,
		AmountRsd: "1000", DayOfMonth: 15,
	})
	testutil.RequireNoGRPCError(t, err)
	if created.GetId() == 0 || created.GetFundId() != 1 {
		t.Fatalf("unexpected create: %+v", created)
	}
	if created.GetAmountRsd() != "1000" {
		t.Fatalf("want amount 1000, got %s", created.GetAmountRsd())
	}
	if !created.GetActive() || created.GetDayOfMonth() != 15 {
		t.Fatalf("unexpected fields: %+v", created)
	}

	got, err := h.Get(ctx, &pb.GetRecurringFundRequest{Id: created.GetId(), ClientId: 7})
	testutil.RequireNoGRPCError(t, err)
	if got.GetId() != created.GetId() {
		t.Fatalf("get mismatch")
	}

	paused, err := h.Pause(ctx, &pb.GetRecurringFundRequest{Id: created.GetId(), ClientId: 7})
	testutil.RequireNoGRPCError(t, err)
	if paused.GetActive() {
		t.Fatal("expected inactive after pause")
	}

	resumed, err := h.Resume(ctx, &pb.GetRecurringFundRequest{Id: created.GetId(), ClientId: 7})
	testutil.RequireNoGRPCError(t, err)
	if !resumed.GetActive() {
		t.Fatal("expected active after resume")
	}

	list, err := h.ListMy(ctx, &pb.ListMyRecurringFundsRequest{ClientId: 7})
	testutil.RequireNoGRPCError(t, err)
	if len(list.GetItems()) != 1 {
		t.Fatalf("want 1, got %d", len(list.GetItems()))
	}

	cancel, err := h.Cancel(ctx, &pb.GetRecurringFundRequest{Id: created.GetId(), ClientId: 7})
	testutil.RequireNoGRPCError(t, err)
	if !cancel.GetCancelled() {
		t.Fatal("expected cancelled=true")
	}

	// Gone after cancel.
	_, err = h.Get(ctx, &pb.GetRecurringFundRequest{Id: created.GetId(), ClientId: 7})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestRecurringFundHandler_CreateErrors(t *testing.T) {
	h, db := newRecurringFundHandlerForCov(t)
	seedOpenFund(t, db, 1, 500, decimal.NewFromInt(2000))
	ctx := context.Background()

	// Missing required ids.
	_, err := h.Create(ctx, &pb.CreateRecurringFundRequest{FundId: 1, SourceAccountId: 9, AmountRsd: "1000"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Bad amount string.
	_, err = h.Create(ctx, &pb.CreateRecurringFundRequest{ClientId: 7, FundId: 1, SourceAccountId: 9, AmountRsd: "abc", DayOfMonth: 1})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Fund not found.
	_, err = h.Create(ctx, &pb.CreateRecurringFundRequest{ClientId: 7, FundId: 999, SourceAccountId: 9, AmountRsd: "1000", DayOfMonth: 1})
	testutil.RequireGRPCCode(t, err, codes.NotFound)

	// Below the fund minimum (2000) → FailedPrecondition.
	_, err = h.Create(ctx, &pb.CreateRecurringFundRequest{ClientId: 7, FundId: 1, SourceAccountId: 9, AmountRsd: "1000", DayOfMonth: 1})
	testutil.RequireGRPCCode(t, err, codes.FailedPrecondition)
}

func TestRecurringFundHandler_GetNotFoundAndOwnership(t *testing.T) {
	h, db := newRecurringFundHandlerForCov(t)
	seedOpenFund(t, db, 1, 500, decimal.Zero)
	ctx := context.Background()

	created, err := h.Create(ctx, &pb.CreateRecurringFundRequest{ClientId: 7, FundId: 1, SourceAccountId: 9, AmountRsd: "1000", DayOfMonth: 5})
	testutil.RequireNoGRPCError(t, err)

	// Unknown id → NotFound.
	_, err = h.Get(ctx, &pb.GetRecurringFundRequest{Id: 9999, ClientId: 7})
	testutil.RequireGRPCCode(t, err, codes.NotFound)

	// Wrong client cannot see another client's row → NotFound.
	_, err = h.Get(ctx, &pb.GetRecurringFundRequest{Id: created.GetId(), ClientId: 8})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestNewRecurringFundHandler_Constructor(t *testing.T) {
	if NewRecurringFundHandler(nil) == nil {
		t.Fatal("nil handler")
	}
}
