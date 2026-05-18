package handler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	stockpb "github.com/exbanka/contract/stockpb"
	userpb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// ifhBankAccountClient is a minimal stub implementing service.BankAccountClient
// for the investment-fund handler RPC tests. Returns a synthetic auto-incrementing
// account id on every CreateBankAccount call.
type ifhBankAccountClient struct{ nextID uint64 }

func (s *ifhBankAccountClient) CreateBankAccount(_ context.Context, _ *accountpb.CreateBankAccountRequest) (*accountpb.AccountResponse, error) {
	id := atomic.AddUint64(&s.nextID, 1) + 1000
	return &accountpb.AccountResponse{Id: id, AccountNumber: "BANK"}, nil
}

func newInvestmentFundHandlerFixture(t *testing.T) (*InvestmentFundHandler, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.InvestmentFund{}, &model.ClientFundPosition{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.NewFundRepository(db)
	posRepo := repository.NewClientFundPositionRepository(db)
	bac := &ifhBankAccountClient{}
	svc := service.NewFundService(repo, bac, nil)
	h := NewInvestmentFundHandler(svc, repo, posRepo)
	return h, db
}

// ---------------- CreateFund ----------------

func TestInvestmentFundHandler_CreateFund_HappyPath(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	resp, err := h.CreateFund(context.Background(), &stockpb.CreateFundRequest{
		Name: "Alpha", Description: "alpha desc",
		ActorEmployeeId:        25,
		MinimumContributionRsd: "1000",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetId() == 0 || resp.GetName() != "Alpha" {
		t.Errorf("got %+v", resp)
	}
}

func TestInvestmentFundHandler_CreateFund_BadDecimal(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	_, err := h.CreateFund(context.Background(), &stockpb.CreateFundRequest{
		Name:                   "Alpha",
		MinimumContributionRsd: "not-a-decimal",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestInvestmentFundHandler_CreateFund_EmptyMinDefaultsToZero(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	resp, err := h.CreateFund(context.Background(), &stockpb.CreateFundRequest{
		Name: "B",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetMinimumContributionRsd() != "0" {
		t.Errorf("expected min=0, got %s", resp.GetMinimumContributionRsd())
	}
}

func TestInvestmentFundHandler_CreateFund_InvalidNameSurfacesError(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	_, err := h.CreateFund(context.Background(), &stockpb.CreateFundRequest{
		Name: "", // service rejects empty
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------------- ListFunds ----------------

func TestInvestmentFundHandler_ListFunds(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	_, _ = h.CreateFund(context.Background(), &stockpb.CreateFundRequest{Name: "A"})
	_, _ = h.CreateFund(context.Background(), &stockpb.CreateFundRequest{Name: "B"})
	resp, err := h.ListFunds(context.Background(), &stockpb.ListFundsRequest{
		Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetTotal() != 2 {
		t.Errorf("total=%d", resp.GetTotal())
	}
}

func TestInvestmentFundHandler_ListFunds_ActiveOnly(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	_, _ = h.CreateFund(context.Background(), &stockpb.CreateFundRequest{Name: "A"})
	resp, err := h.ListFunds(context.Background(), &stockpb.ListFundsRequest{
		Page: 1, PageSize: 10, ActiveOnly: true,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetTotal() != 1 {
		t.Errorf("total=%d", resp.GetTotal())
	}
}

// ---------------- GetFund ----------------

func TestInvestmentFundHandler_GetFund_HappyPath(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	created, _ := h.CreateFund(context.Background(), &stockpb.CreateFundRequest{Name: "A"})
	resp, err := h.GetFund(context.Background(), &stockpb.GetFundRequest{FundId: created.GetId()})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetFund().GetId() != created.GetId() {
		t.Errorf("id mismatch")
	}
}

func TestInvestmentFundHandler_GetFund_NotFound(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	_, err := h.GetFund(context.Background(), &stockpb.GetFundRequest{FundId: 9999})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestInvestmentFundHandler_GetFund_WithFundDetailDeps(t *testing.T) {
	h, db := newInvestmentFundHandlerFixture(t)
	if err := db.AutoMigrate(&model.FundHolding{}, &model.Listing{}, &model.Stock{}); err != nil {
		t.Fatalf("migrate detail deps: %v", err)
	}
	fhRepo := repository.NewFundHoldingRepository(db)
	listingRepo := repository.NewListingRepository(db)
	stockRepo := repository.NewStockRepository(db)
	h2 := h.WithFundDetailDeps(fhRepo, listingRepo, stockRepo)
	created, _ := h.CreateFund(context.Background(), &stockpb.CreateFundRequest{Name: "Detailed"})
	resp, err := h2.GetFund(context.Background(), &stockpb.GetFundRequest{FundId: created.GetId()})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetFund() == nil {
		t.Errorf("nil fund")
	}
	// holdings list is empty because no FundHolding rows seeded
	if len(resp.GetHoldings()) != 0 {
		t.Errorf("expected 0 holdings, got %d", len(resp.GetHoldings()))
	}
}

// ---------------- UpdateFund ----------------

func TestInvestmentFundHandler_UpdateFund_HappyPath(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	created, _ := h.CreateFund(context.Background(), &stockpb.CreateFundRequest{Name: "A"})
	resp, err := h.UpdateFund(context.Background(), &stockpb.UpdateFundRequest{
		FundId: created.GetId(), Name: "A2", Description: "fresh desc",
		MinimumContributionRsd: "500", ActiveSet: true, Active: false,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetName() != "A2" || resp.GetDescription() != "fresh desc" {
		t.Errorf("got %+v", resp)
	}
	if resp.GetActive() {
		t.Errorf("expected active=false")
	}
}

func TestInvestmentFundHandler_UpdateFund_BadDecimal(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	_, err := h.UpdateFund(context.Background(), &stockpb.UpdateFundRequest{
		FundId: 1, MinimumContributionRsd: "abc",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// ---------------- InvestInFund / RedeemFromFund: input validation only ----------------

func TestInvestmentFundHandler_InvestInFund_BadDecimal(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	_, err := h.InvestInFund(context.Background(), &stockpb.InvestInFundRequest{
		FundId: 1, Amount: "abc",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestInvestmentFundHandler_RedeemFromFund_BadDecimal(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	_, err := h.RedeemFromFund(context.Background(), &stockpb.RedeemFromFundRequest{
		FundId: 1, AmountRsd: "abc",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// Even with a valid decimal, Invest/Redeem need saga deps wired or they
// surface an internal error — that's fine, it exercises the handler path.
func TestInvestmentFundHandler_InvestInFund_NoSagaWired(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	_, err := h.InvestInFund(context.Background(), &stockpb.InvestInFundRequest{
		FundId: 1, Amount: "100", Currency: "RSD",
	})
	if err == nil {
		t.Fatal("expected error from service when saga not wired")
	}
}

func TestInvestmentFundHandler_RedeemFromFund_NoSagaWired(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	_, err := h.RedeemFromFund(context.Background(), &stockpb.RedeemFromFundRequest{
		FundId: 1, AmountRsd: "100",
	})
	if err == nil {
		t.Fatal("expected error from service")
	}
}

func TestInvestmentFundHandler_InvestInFund_OnBehalfOf(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	// Even though it'll fail downstream, the handler should parse OnBehalfOf.
	_, err := h.InvestInFund(context.Background(), &stockpb.InvestInFundRequest{
		FundId:     1,
		Amount:     "100",
		Currency:   "RSD",
		OnBehalfOf: &stockpb.OnBehalfOf{Type: "client"},
	})
	if err == nil {
		t.Fatal("expected error (saga not wired)")
	}
}

func TestInvestmentFundHandler_RedeemFromFund_OnBehalfOf(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	_, err := h.RedeemFromFund(context.Background(), &stockpb.RedeemFromFundRequest{
		FundId:     1,
		AmountRsd:  "100",
		OnBehalfOf: &stockpb.OnBehalfOf{Type: "client"},
	})
	if err == nil {
		t.Fatal("expected error (saga not wired)")
	}
}

// ---------------- ListMyPositions / ListBankPositions ----------------

func TestInvestmentFundHandler_ListMyPositions(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	resp, err := h.ListMyPositions(context.Background(), &stockpb.ListMyPositionsRequest{
		ActorUserId: 7, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetPositions()) != 0 {
		t.Errorf("expected empty initial list")
	}
}

func TestInvestmentFundHandler_ListBankPositions(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	resp, err := h.ListBankPositions(context.Background(), &stockpb.ListBankPositionsRequest{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetPositions()) != 0 {
		t.Errorf("expected empty list")
	}
}

// ---------------- GetActuaryPerformance ----------------

func TestInvestmentFundHandler_GetActuaryPerformance_NoDepsReturnsEmpty(t *testing.T) {
	h, _ := newInvestmentFundHandlerFixture(t)
	resp, err := h.GetActuaryPerformance(context.Background(), &stockpb.GetActuaryPerformanceRequest{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetActuaries()) != 0 {
		t.Errorf("expected empty when actuary deps not wired")
	}
}

// ifhFakeUserClient stubs userpb.UserServiceClient just enough for
// GetActuaryPerformance.
type ifhFakeUserClient struct {
	userpb.UserServiceClient
	resp *userpb.ListEmployeeFullNamesResponse
	err  error
}

func (f *ifhFakeUserClient) ListEmployeeFullNames(_ context.Context, _ *userpb.ListEmployeeFullNamesRequest, _ ...grpc.CallOption) (*userpb.ListEmployeeFullNamesResponse, error) {
	return f.resp, f.err
}

// ifhFakeExchangeClient stubs exchangepb.ExchangeServiceClient.
type ifhFakeExchangeClient struct {
	exchangepb.ExchangeServiceClient
	resp *exchangepb.ConvertResponse
	err  error
}

func (f *ifhFakeExchangeClient) Convert(_ context.Context, _ *exchangepb.ConvertRequest, _ ...grpc.CallOption) (*exchangepb.ConvertResponse, error) {
	return f.resp, f.err
}

func TestInvestmentFundHandler_GetActuaryPerformance_WithDepsButNoData(t *testing.T) {
	h, db := newInvestmentFundHandlerFixture(t)
	if err := db.AutoMigrate(&model.CapitalGain{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cg := repository.NewCapitalGainRepository(db)
	uc := &ifhFakeUserClient{resp: &userpb.ListEmployeeFullNamesResponse{NamesById: map[int64]string{}}}
	ec := &ifhFakeExchangeClient{}
	h2 := h.WithActuaryDeps(cg, uc, ec)
	resp, err := h2.GetActuaryPerformance(context.Background(), &stockpb.GetActuaryPerformanceRequest{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetActuaries()) != 0 {
		t.Errorf("expected empty (no capital gains)")
	}
}

// ifhFailingUserClient surfaces an error from ListEmployeeFullNames.
type ifhFailingUserClient struct {
	userpb.UserServiceClient
}

func (f *ifhFailingUserClient) ListEmployeeFullNames(_ context.Context, _ *userpb.ListEmployeeFullNamesRequest, _ ...grpc.CallOption) (*userpb.ListEmployeeFullNamesResponse, error) {
	return nil, errors.New("user-service unavailable")
}

func TestInvestmentFundHandler_GetActuaryPerformance_UserClientErrSwallowed(t *testing.T) {
	h, db := newInvestmentFundHandlerFixture(t)
	if err := db.AutoMigrate(&model.CapitalGain{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cg := repository.NewCapitalGainRepository(db)
	uc := &ifhFailingUserClient{}
	h2 := h.WithActuaryDeps(cg, uc, nil)
	if _, err := h2.GetActuaryPerformance(context.Background(), &stockpb.GetActuaryPerformanceRequest{}); err != nil {
		t.Errorf("err: %v", err)
	}
}

// TestInvestmentFundHandler_GetActuaryPerformance_WithDataAndExchangeConvert
// seeds capital gains across multiple currencies, verifying the exchange
// conversion path runs (USD → RSD).
func TestInvestmentFundHandler_GetActuaryPerformance_WithDataAndExchangeConvert(t *testing.T) {
	h, db := newInvestmentFundHandlerFixture(t)
	if err := db.AutoMigrate(&model.CapitalGain{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cg := repository.NewCapitalGainRepository(db)
	emp1 := uint64(7)
	for _, ccy := range []string{"RSD", "USD"} {
		_ = cg.Create(&model.CapitalGain{
			OwnerType: model.OwnerClient, OwnerID: &emp1,
			SecurityType: "stock", Ticker: "X", Quantity: 1,
			BuyPricePerUnit: decimal.NewFromInt(100), SellPricePerUnit: decimal.NewFromInt(150),
			TotalGain: decimal.NewFromInt(50), Currency: ccy,
			AccountID: 1, TaxYear: 2026, TaxMonth: 5,
			ActingEmployeeID: &emp1,
		})
	}
	uc := &ifhFakeUserClient{resp: &userpb.ListEmployeeFullNamesResponse{NamesById: map[int64]string{7: "Jane"}}}
	ec := &ifhFakeExchangeClient{resp: &exchangepb.ConvertResponse{ConvertedAmount: "5000"}}
	h2 := h.WithActuaryDeps(cg, uc, ec)
	resp, err := h2.GetActuaryPerformance(context.Background(), &stockpb.GetActuaryPerformanceRequest{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetActuaries()) != 1 {
		t.Errorf("expected 1 actuary, got %d", len(resp.GetActuaries()))
	}
	if resp.GetActuaries()[0].FullName != "Jane" {
		t.Errorf("name=%s", resp.GetActuaries()[0].FullName)
	}
}
