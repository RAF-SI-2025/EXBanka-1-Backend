package handler

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

func newDividendFundHandler(t *testing.T) (*InvestmentFundHandler, *gorm.DB) {
	t.Helper()
	db := testutil.SetupTestDB(t,
		&model.DividendPayment{}, &model.DividendPayout{}, &model.FundDividendPayment{},
		&model.Holding{}, &model.FundHolding{}, &model.InvestmentFund{}, &model.ClientFundPosition{},
	)
	divSvc := service.NewDividendService(
		db,
		repository.NewDividendPaymentRepository(db),
		repository.NewDividendPayoutRepository(db),
		repository.NewFundDividendPaymentRepository(db),
		repository.NewHoldingRepository(db),
		repository.NewFundHoldingRepository(db),
		repository.NewFundRepository(db),
		repository.NewClientFundPositionRepository(db),
		nil,
	)
	h := NewInvestmentFundHandler(nil, nil, nil).WithDividendService(divSvc)
	return h, db
}

func TestInvestmentFundHandler_DeclareDividend_Success(t *testing.T) {
	h, _ := newDividendFundHandler(t)
	resp, err := h.DeclareDividend(context.Background(), &stockpb.DeclareDividendRequest{
		SecurityId: 10, Ticker: "aapl", AmountPerShareRsd: "1.50",
		PaymentDate: "2026-06-01", DeclaredByEmployeeId: 99,
	})
	testutil.RequireNoGRPCError(t, err)
	if resp.GetId() == 0 {
		t.Fatal("expected dividend payment id")
	}
	if resp.GetTicker() != "AAPL" {
		t.Fatalf("want uppercased ticker AAPL, got %q", resp.GetTicker())
	}
	if resp.GetStatus() != "declared" {
		t.Fatalf("want declared, got %q", resp.GetStatus())
	}
	if resp.GetPaymentDate() != "2026-06-01" {
		t.Fatalf("want payment date 2026-06-01, got %q", resp.GetPaymentDate())
	}
}

func TestInvestmentFundHandler_DeclareDividend_Errors(t *testing.T) {
	h, _ := newDividendFundHandler(t)
	ctx := context.Background()

	// Invalid amount.
	_, err := h.DeclareDividend(ctx, &stockpb.DeclareDividendRequest{SecurityId: 1, Ticker: "X", AmountPerShareRsd: "abc", PaymentDate: "2026-06-01"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Invalid date.
	_, err = h.DeclareDividend(ctx, &stockpb.DeclareDividendRequest{SecurityId: 1, Ticker: "X", AmountPerShareRsd: "1", PaymentDate: "06/01/2026"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Service rejects security_id 0 → Internal.
	_, err = h.DeclareDividend(ctx, &stockpb.DeclareDividendRequest{SecurityId: 0, Ticker: "X", AmountPerShareRsd: "1", PaymentDate: "2026-06-01"})
	testutil.RequireGRPCCode(t, err, codes.Internal)

	// Nil dividend service → Unimplemented.
	bare := NewInvestmentFundHandler(nil, nil, nil)
	_, err = bare.DeclareDividend(ctx, &stockpb.DeclareDividendRequest{SecurityId: 1, Ticker: "X", AmountPerShareRsd: "1", PaymentDate: "2026-06-01"})
	testutil.RequireGRPCCode(t, err, codes.Unimplemented)
}

func TestInvestmentFundHandler_PayoutDividend(t *testing.T) {
	h, _ := newDividendFundHandler(t)
	ctx := context.Background()

	declared, err := h.DeclareDividend(ctx, &stockpb.DeclareDividendRequest{
		SecurityId: 10, Ticker: "AAPL", AmountPerShareRsd: "2", PaymentDate: "2026-06-01",
	})
	testutil.RequireNoGRPCError(t, err)

	// No holders → 0 payouts but a clean success.
	out, err := h.PayoutDividend(ctx, &stockpb.PayoutDividendRequest{DividendPaymentId: declared.GetId()})
	testutil.RequireNoGRPCError(t, err)
	if out.GetPayoutsCreated() != 0 || out.GetFundPayouts() != 0 {
		t.Fatalf("expected zero payouts, got %+v", out)
	}

	// Unknown id → Internal.
	_, err = h.PayoutDividend(ctx, &stockpb.PayoutDividendRequest{DividendPaymentId: 999999})
	testutil.RequireGRPCCode(t, err, codes.Internal)

	// Nil service → Unimplemented.
	bare := NewInvestmentFundHandler(nil, nil, nil)
	_, err = bare.PayoutDividend(ctx, &stockpb.PayoutDividendRequest{DividendPaymentId: 1})
	testutil.RequireGRPCCode(t, err, codes.Unimplemented)
}

func TestInvestmentFundHandler_ListDividends(t *testing.T) {
	h, _ := newDividendFundHandler(t)
	ctx := context.Background()

	// Empty list, page/pageSize defaults applied.
	my, err := h.ListMyDividends(ctx, &stockpb.ListMyDividendsRequest{OwnerType: "client", OwnerId: 7})
	testutil.RequireNoGRPCError(t, err)
	if my.GetTotal() != 0 || len(my.GetPayouts()) != 0 {
		t.Fatalf("expected empty payouts, got %+v", my)
	}

	fund, err := h.ListFundDividends(ctx, &stockpb.ListFundDividendsRequest{FundId: 1, Page: 2, PageSize: 5})
	testutil.RequireNoGRPCError(t, err)
	if fund.GetTotal() != 0 || len(fund.GetPayments()) != 0 {
		t.Fatalf("expected empty fund payments, got %+v", fund)
	}

	// Nil service → empty responses (no error).
	bare := NewInvestmentFundHandler(nil, nil, nil)
	myNil, err := bare.ListMyDividends(ctx, &stockpb.ListMyDividendsRequest{OwnerType: "client", OwnerId: 7})
	testutil.RequireNoGRPCError(t, err)
	if len(myNil.GetPayouts()) != 0 {
		t.Fatal("expected empty payouts for nil service")
	}
	fundNil, err := bare.ListFundDividends(ctx, &stockpb.ListFundDividendsRequest{FundId: 1})
	testutil.RequireNoGRPCError(t, err)
	if len(fundNil.GetPayments()) != 0 {
		t.Fatal("expected empty fund payments for nil service")
	}
}

func TestInvestmentFundHandler_WithDividendService_ReturnsCopy(t *testing.T) {
	h := NewInvestmentFundHandler(nil, nil, nil)
	cp := h.WithDividendService(nil)
	if cp == nil {
		t.Fatal("nil copy")
	}
	if cp == h {
		t.Error("WithDividendService should return a copy")
	}
}
