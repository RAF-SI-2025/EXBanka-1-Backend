package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// errHoldingPortfolioSvc reuses the package mockPortfolioSvc but overrides
// GetHoldingByID to surface an error, exercising GetHolding's error branch.
type errHoldingPortfolioSvc struct{ mockPortfolioSvc }

func (e *errHoldingPortfolioSvc) GetHoldingByID(uint64) (*model.Holding, error) {
	return nil, errors.New("boom")
}

func TestPortfolioHandler_GetHolding_Success(t *testing.T) {
	h := newPortfolioHandlerForTest(&mockPortfolioSvc{}, &mockTaxSvc{})
	resp, err := h.GetHolding(context.Background(), &pb.GetHoldingRequest{Id: 42})
	testutil.RequireNoGRPCError(t, err)
	if resp.GetHolding().GetId() != 42 {
		t.Fatalf("want holding id 42, got %d", resp.GetHolding().GetId())
	}
	if resp.GetOwnerType() != string(model.OwnerClient) {
		t.Fatalf("want owner_type client, got %q", resp.GetOwnerType())
	}
}

func TestPortfolioHandler_GetHolding_Errors(t *testing.T) {
	h := newPortfolioHandlerForTest(&mockPortfolioSvc{}, &mockTaxSvc{})
	// id == 0 → InvalidArgument before any service call.
	_, err := h.GetHolding(context.Background(), &pb.GetHoldingRequest{Id: 0})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// service error is mapped (passthrough).
	he := newPortfolioHandlerForTest(&errHoldingPortfolioSvc{}, &mockTaxSvc{})
	_, err = he.GetHolding(context.Background(), &pb.GetHoldingRequest{Id: 5})
	if err == nil {
		t.Fatal("expected error from GetHoldingByID")
	}
}

func TestPortfolioHandler_GetUnifiedPortfolio_GuardErrors(t *testing.T) {
	h := newPortfolioHandlerForTest(&mockPortfolioSvc{}, &mockTaxSvc{})
	// owner_type empty → InvalidArgument.
	_, err := h.GetUnifiedPortfolio(context.Background(), &pb.GetUnifiedPortfolioRequest{})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// unifiedSvc not configured → Internal.
	_, err = h.GetUnifiedPortfolio(context.Background(), &pb.GetUnifiedPortfolioRequest{OwnerType: "client", OwnerId: 5})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

func TestPortfolioHandler_GetUnifiedPortfolio_Success(t *testing.T) {
	db := testutil.SetupTestDB(t,
		&model.Holding{}, &model.ClientFundPosition{},
		&model.InvestmentFund{}, &model.FundHolding{}, &model.Listing{},
	)
	unified := service.NewUnifiedPortfolioService(
		repository.NewHoldingRepository(db),
		repository.NewClientFundPositionRepository(db),
		repository.NewFundRepository(db),
		repository.NewFundHoldingRepository(db),
		repository.NewListingRepository(db),
		nil,
	)
	h := newPortfolioHandlerForTest(&mockPortfolioSvc{}, &mockTaxSvc{}).WithUnifiedPortfolioService(unified)

	resp, err := h.GetUnifiedPortfolio(context.Background(), &pb.GetUnifiedPortfolioRequest{OwnerType: "bank"})
	testutil.RequireNoGRPCError(t, err)
	if resp.GetOwnerType() != "bank" {
		t.Fatalf("want bank, got %q", resp.GetOwnerType())
	}
	if resp.GetPortfolioId() != "bank" {
		t.Fatalf("want portfolio_id bank, got %q", resp.GetPortfolioId())
	}
}

func TestPortfolioHandler_WithUnifiedPortfolioService_ReturnsSelf(t *testing.T) {
	h := newPortfolioHandlerForTest(&mockPortfolioSvc{}, &mockTaxSvc{})
	if got := h.WithUnifiedPortfolioService(nil); got != h {
		t.Fatal("WithUnifiedPortfolioService should return the same handler")
	}
}

func TestMapUnifiedToProto_ClientWithPositions(t *testing.T) {
	id := uint64(7)
	settle := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	p := &service.UnifiedPortfolio{
		OwnerType:      "client",
		OwnerID:        &id,
		OwnerName:      "Alice",
		TotalValueRSD:  decimal.NewFromInt(1000),
		TotalProfitRSD: decimal.NewFromInt(100),
		TotalProfitPct: decimal.NewFromFloat(10),
		Securities: service.PortfolioGroup{
			TotalValueRSD: decimal.NewFromInt(1000),
			Positions: []service.PortfolioPosition{
				{AssetType: "option", Symbol: "AAPL", SettlementDate: &settle, CurrentValueRSD: decimal.NewFromInt(1000)},
			},
		},
	}
	out := mapUnifiedToProto(p)
	if out.GetOwnerId() != 7 || out.GetPortfolioId() != "client-7" {
		t.Fatalf("unexpected client mapping: id=%d portfolio=%q", out.GetOwnerId(), out.GetPortfolioId())
	}
	if out.GetOwnerName() != "Alice" {
		t.Fatalf("want owner name Alice, got %q", out.GetOwnerName())
	}
	if len(out.GetSecurities().GetPositions()) != 1 {
		t.Fatalf("want 1 security position, got %d", len(out.GetSecurities().GetPositions()))
	}
	if out.GetSecurities().GetPositions()[0].GetSettlementDate() != "2026-06-01" {
		t.Fatalf("settlement date mismatch: %q", out.GetSecurities().GetPositions()[0].GetSettlementDate())
	}
}

func TestMapUnifiedToProto_InvestmentFundPrefix(t *testing.T) {
	id := uint64(3)
	p := &service.UnifiedPortfolio{OwnerType: "investment_fund", OwnerID: &id}
	out := mapUnifiedToProto(p)
	if out.GetPortfolioId() != "fund-3" {
		t.Fatalf("want fund-3, got %q", out.GetPortfolioId())
	}
}

func TestEncodeOwnerTypePrefix(t *testing.T) {
	if got := encodeOwnerTypePrefix("investment_fund"); got != "fund" {
		t.Fatalf("want fund, got %q", got)
	}
	if got := encodeOwnerTypePrefix("client"); got != "client" {
		t.Fatalf("want client, got %q", got)
	}
}
