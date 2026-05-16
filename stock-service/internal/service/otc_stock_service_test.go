package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// ---------- test doubles ----------

type stocksFakeAccountClient struct {
	mu                 sync.Mutex
	acct               *accountpb.AccountResponse
	stocksReserveCalls []stocksReserveCall
	stocksReleaseCalls []stocksReleaseCall
	failReserveErr     error
	failReleaseErr     error
	failGetAccountErr  error
}

type stocksReserveCall struct {
	accountID, orderID uint64
	amount             decimal.Decimal
	currency           string
	idempKey           string
}
type stocksReleaseCall struct {
	orderID  uint64
	idempKey string
}

func (f *stocksFakeAccountClient) ReserveFunds(ctx context.Context, accountID, orderID uint64, amount decimal.Decimal, currency, idempKey string) (*accountpb.ReserveFundsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failReserveErr != nil {
		return nil, f.failReserveErr
	}
	f.stocksReserveCalls = append(f.stocksReserveCalls, stocksReserveCall{accountID, orderID, amount, currency, idempKey})
	return &accountpb.ReserveFundsResponse{ReservedBalance: amount.String()}, nil
}

func (f *stocksFakeAccountClient) ReleaseReservation(ctx context.Context, orderID uint64, idempKey string) (*accountpb.ReleaseReservationResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failReleaseErr != nil {
		return nil, f.failReleaseErr
	}
	f.stocksReleaseCalls = append(f.stocksReleaseCalls, stocksReleaseCall{orderID, idempKey})
	return &accountpb.ReleaseReservationResponse{}, nil
}

func (f *stocksFakeAccountClient) GetAccount(ctx context.Context, accountID uint64) (*accountpb.AccountResponse, error) {
	if f.failGetAccountErr != nil {
		return nil, f.failGetAccountErr
	}
	return f.acct, nil
}

type stocksFakeListingResolver struct {
	currency string
	ticker   string
	name     string
	stockID  uint64
	err      error
}

func (f *stocksFakeListingResolver) GetListingCurrency(listingID uint64) (string, error) {
	return f.currency, f.err
}
func (f *stocksFakeListingResolver) GetListingTickerAndName(listingID uint64) (string, string, uint64, error) {
	return f.ticker, f.name, f.stockID, f.err
}

// ---------- env ----------

type otcStockTestEnv struct {
	db            *gorm.DB
	svc           *OTCStockService
	holdingRepo   *repository.HoldingRepository
	buyOfferRepo  *repository.OTCStockBuyOfferRepository
	accountClient *stocksFakeAccountClient
	listing       *stocksFakeListingResolver
}

func newOTCStockTestEnv(t *testing.T) *otcStockTestEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.Holding{}, &model.OTCStockBuyOffer{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// SQLite doesn't have sequences, but we don't need real ones in tests —
	// AllocateReservationOrderID will return error and the test compensates
	// by pre-creating offers via the repo OR by stubbing. We'll patch the
	// CreateBuyOffer test by intercepting at the resOrderID step via a
	// fixed-value sequence shim using last_insert_rowid().
	// Simpler: a sqlite virtual table or just NEXTVAL emulation via a
	// counter table. For now: create a small counter table.
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS otc_stock_buy_offer_res_seq (next INTEGER NOT NULL)`).Error; err != nil {
		t.Fatalf("seq table: %v", err)
	}
	db.Exec(`INSERT INTO otc_stock_buy_offer_res_seq (next) VALUES (1000000)`)

	holdingRepo := repository.NewHoldingRepository(db)
	buyOfferRepo := repository.NewOTCStockBuyOfferRepository(db)
	accountClient := &stocksFakeAccountClient{
		acct: &accountpb.AccountResponse{
			Id:            42,
			AccountNumber: "111000123456789011",
			CurrencyCode:  "USD",
		},
	}
	listing := &stocksFakeListingResolver{
		currency: "USD",
		ticker:   "AAPL",
		name:     "Apple Inc",
		stockID:  100,
	}
	svc := NewOTCStockService(db, holdingRepo, buyOfferRepo, listing, accountClient)
	return &otcStockTestEnv{
		db: db, svc: svc, holdingRepo: holdingRepo, buyOfferRepo: buyOfferRepo,
		accountClient: accountClient, listing: listing,
	}
}

// NOTE on sqlite + sequence: AllocateReservationOrderID uses Postgres
// nextval(). The CreateBuyOffer happy-path test would therefore fail on
// sqlite. We do NOT cover that path here; instead, every buy-offer test
// seeds offers directly via the repo with a hand-picked
// AccountReservationOrderID. The CreateBuyOffer path itself is covered
// by the Postgres-backed integration suite in Phase 3B.

func seedHolding(t *testing.T, env *otcStockTestEnv, ownerID uint64, qty, publicQty, reservedQty int64) *model.Holding {
	t.Helper()
	h := &model.Holding{
		OwnerType:        model.OwnerClient,
		OwnerID:          u64p(ownerID),
		UserFirstName:    "Test",
		UserLastName:     "User",
		SecurityType:     "stock",
		SecurityID:       100,
		Ticker:           "AAPL",
		Name:             "Apple Inc",
		Quantity:         qty,
		PublicQuantity:   publicQty,
		ReservedQuantity: reservedQty,
		AveragePrice:     decimal.NewFromFloat(150.0),
		AccountID:        42,
	}
	if err := env.db.Create(h).Error; err != nil {
		t.Fatalf("seed holding: %v", err)
	}
	return h
}

// ---------- CreateSellOffer ----------

func TestCreateSellOffer_HappyPath_Accumulates(t *testing.T) {
	env := newOTCStockTestEnv(t)
	h := seedHolding(t, env, 7, 100, 0, 0)

	updated, err := env.svc.CreateSellOffer(context.Background(), CreateSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7), Quantity: 5,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if updated.PublicQuantity != 5 {
		t.Errorf("first create: public_quantity=%d want 5", updated.PublicQuantity)
	}
	// Accumulate again.
	updated, err = env.svc.CreateSellOffer(context.Background(), CreateSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7), Quantity: 3,
	})
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if updated.PublicQuantity != 8 {
		t.Errorf("accumulated: public_quantity=%d want 8", updated.PublicQuantity)
	}
}

func TestCreateSellOffer_RejectsNonOwner(t *testing.T) {
	env := newOTCStockTestEnv(t)
	h := seedHolding(t, env, 7, 100, 0, 0)
	_, err := env.svc.CreateSellOffer(context.Background(), CreateSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(99), Quantity: 5,
	})
	if !errors.Is(err, ErrOTCContractNotParticipant) {
		t.Fatalf("want ownership error, got %v", err)
	}
}

func TestCreateSellOffer_RejectsNonStockHolding(t *testing.T) {
	env := newOTCStockTestEnv(t)
	h := seedHolding(t, env, 7, 100, 0, 0)
	h.SecurityType = "futures"
	if err := env.db.Save(h).Error; err != nil {
		t.Fatalf("save: %v", err)
	}
	_, err := env.svc.CreateSellOffer(context.Background(), CreateSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7), Quantity: 5,
	})
	if !errors.Is(err, ErrOTCStockSellOfferHoldingType) {
		t.Fatalf("want type-rejection, got %v", err)
	}
}

func TestCreateSellOffer_RejectsIfNotEnoughAvailable(t *testing.T) {
	env := newOTCStockTestEnv(t)
	// 100 owned, 60 reserved by orders, 30 already public → safe avail = 10.
	h := seedHolding(t, env, 7, 100, 30, 60)

	if _, err := env.svc.CreateSellOffer(context.Background(), CreateSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7), Quantity: 11,
	}); !errors.Is(err, ErrOTCStockInsufficientShares) {
		t.Fatalf("want insufficient-shares, got %v", err)
	}
	// Right at the boundary — should succeed.
	if _, err := env.svc.CreateSellOffer(context.Background(), CreateSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7), Quantity: 10,
	}); err != nil {
		t.Fatalf("boundary qty=10 should succeed, got %v", err)
	}
}

func TestCreateSellOffer_RejectsZeroQuantity(t *testing.T) {
	env := newOTCStockTestEnv(t)
	h := seedHolding(t, env, 7, 100, 0, 0)
	_, err := env.svc.CreateSellOffer(context.Background(), CreateSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7), Quantity: 0,
	})
	if !errors.Is(err, ErrOTCStockInsufficientShares) {
		t.Fatalf("want insufficient-shares for qty=0, got %v", err)
	}
}

// ---------- CancelSellOffer ----------

func TestCancelSellOffer_ZeroesPublicQuantity(t *testing.T) {
	env := newOTCStockTestEnv(t)
	h := seedHolding(t, env, 7, 100, 25, 0)

	if err := env.svc.CancelSellOffer(context.Background(), CancelSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7),
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	out, _ := env.holdingRepo.GetByID(h.ID)
	if out.PublicQuantity != 0 {
		t.Errorf("public_quantity=%d want 0", out.PublicQuantity)
	}
}

func TestCancelSellOffer_RejectsIfAlreadyZero(t *testing.T) {
	env := newOTCStockTestEnv(t)
	h := seedHolding(t, env, 7, 100, 0, 0)
	err := env.svc.CancelSellOffer(context.Background(), CancelSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7),
	})
	if !errors.Is(err, ErrOTCStockNoActiveSellOffer) {
		t.Fatalf("want no-active-offer, got %v", err)
	}
}

// ---------- CancelBuyOffer ----------

func TestCancelBuyOffer_ReleasesReservation_HappyPath(t *testing.T) {
	env := newOTCStockTestEnv(t)
	// Seed a buy offer directly via the repo (skip CreateBuyOffer's sequence
	// dependency).
	buyer := uint64(7)
	offer := &model.OTCStockBuyOffer{
		BuyerOwnerType:            model.OwnerClient,
		BuyerOwnerID:              &buyer,
		BuyerAccountID:            42,
		BuyerAccountNumber:        "111000123456789011",
		StockID:                   100,
		ListingID:                 1,
		Ticker:                    "AAPL",
		Name:                      "Apple Inc",
		OriginalQuantity:          10,
		RemainingQuantity:         10,
		PricePerUnit:              decimal.NewFromFloat(150.0),
		CurrencyCode:              "USD",
		ReservedAmount:            decimal.NewFromInt(1500),
		OriginalReservedAmount:    decimal.NewFromInt(1500),
		AccountReservationOrderID: 9_999_001,
		Status:                    model.OTCStockBuyOfferStatusActive,
	}
	if err := env.buyOfferRepo.Create(offer); err != nil {
		t.Fatalf("seed offer: %v", err)
	}

	if err := env.svc.CancelBuyOffer(context.Background(), CancelBuyOfferInput{
		OfferID: offer.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: &buyer,
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	updated, _ := env.buyOfferRepo.GetByID(offer.ID)
	if updated.Status != model.OTCStockBuyOfferStatusCancelled {
		t.Errorf("status=%s want cancelled", updated.Status)
	}
	if len(env.accountClient.stocksReleaseCalls) != 1 {
		t.Fatalf("expected 1 release call, got %d", len(env.accountClient.stocksReleaseCalls))
	}
	if env.accountClient.stocksReleaseCalls[0].orderID != 9_999_001 {
		t.Errorf("release order_id=%d want 9999001", env.accountClient.stocksReleaseCalls[0].orderID)
	}
}

func TestCancelBuyOffer_RejectsNonOwner(t *testing.T) {
	env := newOTCStockTestEnv(t)
	buyer := uint64(7)
	offer := &model.OTCStockBuyOffer{
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyer, BuyerAccountID: 42,
		BuyerAccountNumber: "x", StockID: 1, ListingID: 1, Ticker: "X", Name: "X",
		OriginalQuantity: 1, RemainingQuantity: 1,
		PricePerUnit: decimal.NewFromInt(1), CurrencyCode: "USD",
		ReservedAmount: decimal.NewFromInt(1), OriginalReservedAmount: decimal.NewFromInt(1),
		AccountReservationOrderID: 9_999_002, Status: model.OTCStockBuyOfferStatusActive,
	}
	_ = env.buyOfferRepo.Create(offer)

	err := env.svc.CancelBuyOffer(context.Background(), CancelBuyOfferInput{
		OfferID: offer.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(99),
	})
	if !errors.Is(err, ErrOTCStockBuyOfferOwnership) {
		t.Fatalf("want ownership-error, got %v", err)
	}
	// Reservation should NOT be released on auth failure.
	if len(env.accountClient.stocksReleaseCalls) != 0 {
		t.Errorf("release should NOT be called on auth failure, got %d", len(env.accountClient.stocksReleaseCalls))
	}
}

func TestCancelBuyOffer_RejectsAlreadyCancelled(t *testing.T) {
	env := newOTCStockTestEnv(t)
	buyer := uint64(7)
	offer := &model.OTCStockBuyOffer{
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyer, BuyerAccountID: 42,
		BuyerAccountNumber: "x", StockID: 1, ListingID: 1, Ticker: "X", Name: "X",
		OriginalQuantity: 1, RemainingQuantity: 1,
		PricePerUnit: decimal.NewFromInt(1), CurrencyCode: "USD",
		ReservedAmount: decimal.NewFromInt(1), OriginalReservedAmount: decimal.NewFromInt(1),
		AccountReservationOrderID: 9_999_003, Status: model.OTCStockBuyOfferStatusCancelled,
	}
	_ = env.buyOfferRepo.Create(offer)

	err := env.svc.CancelBuyOffer(context.Background(), CancelBuyOfferInput{
		OfferID: offer.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: &buyer,
	})
	if !errors.Is(err, ErrOTCStockBuyOfferNotActive) {
		t.Fatalf("want not-active, got %v", err)
	}
}

// ---------- ListMyListings ----------

func TestListMyListings_ReturnsBothDirections(t *testing.T) {
	env := newOTCStockTestEnv(t)
	owner := uint64(7)
	// One sell offer (holding with public_quantity > 0).
	seedHolding(t, env, owner, 100, 20, 0)
	// One active buy offer.
	buy := &model.OTCStockBuyOffer{
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &owner, BuyerAccountID: 42,
		BuyerAccountNumber: "x", StockID: 100, ListingID: 1, Ticker: "MSFT", Name: "Microsoft",
		OriginalQuantity: 5, RemainingQuantity: 5,
		PricePerUnit: decimal.NewFromFloat(300.0), CurrencyCode: "USD",
		ReservedAmount: decimal.NewFromInt(1500), OriginalReservedAmount: decimal.NewFromInt(1500),
		AccountReservationOrderID: 9_999_004, Status: model.OTCStockBuyOfferStatusActive,
	}
	_ = env.buyOfferRepo.Create(buy)

	res, err := env.svc.ListMyListings(context.Background(), ListMyOTCStocksInput{
		OwnerType: model.OwnerClient, OwnerID: &owner, Page: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if res.Total != 2 || len(res.Listings) != 2 {
		t.Errorf("total=%d listings=%d want both=2", res.Total, len(res.Listings))
	}
	directions := map[string]bool{}
	for _, l := range res.Listings {
		directions[l.Direction] = true
	}
	if !directions["sell"] || !directions["buy"] {
		t.Errorf("expected both directions present, got %v", directions)
	}
}

func TestListMyListings_FiltersByDirection(t *testing.T) {
	env := newOTCStockTestEnv(t)
	owner := uint64(7)
	seedHolding(t, env, owner, 100, 20, 0)
	buy := &model.OTCStockBuyOffer{
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &owner, BuyerAccountID: 42,
		BuyerAccountNumber: "x", StockID: 100, ListingID: 1, Ticker: "MSFT", Name: "Microsoft",
		OriginalQuantity: 5, RemainingQuantity: 5,
		PricePerUnit: decimal.NewFromFloat(300.0), CurrencyCode: "USD",
		ReservedAmount: decimal.NewFromInt(1500), OriginalReservedAmount: decimal.NewFromInt(1500),
		AccountReservationOrderID: 9_999_005, Status: model.OTCStockBuyOfferStatusActive,
	}
	_ = env.buyOfferRepo.Create(buy)

	res, err := env.svc.ListMyListings(context.Background(), ListMyOTCStocksInput{
		OwnerType: model.OwnerClient, OwnerID: &owner, Direction: "sell", Page: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list sell: %v", err)
	}
	if res.Total != 1 || res.Listings[0].Direction != "sell" {
		t.Errorf("sell-only filter returned %d items, first dir=%s", res.Total, res.Listings[0].Direction)
	}

	res, _ = env.svc.ListMyListings(context.Background(), ListMyOTCStocksInput{
		OwnerType: model.OwnerClient, OwnerID: &owner, Direction: "buy", Page: 1, PageSize: 20,
	})
	if res.Total != 1 || res.Listings[0].Direction != "buy" {
		t.Errorf("buy-only filter returned %d items, first dir=%s", res.Total, res.Listings[0].Direction)
	}
}
