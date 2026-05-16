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
	settleCalls        []stocksSettleCall
	creditCalls        []stocksCreditCall
	failReserveErr     error
	failReleaseErr     error
	failGetAccountErr  error
	failSettleErr      error
	failCreditErr      error
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

func (f *stocksFakeAccountClient) ReserveFunds(ctx context.Context, accountID, orderID uint64, amount decimal.Decimal, currency, idempKey, _ string) (*accountpb.ReserveFundsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failReserveErr != nil {
		return nil, f.failReserveErr
	}
	f.stocksReserveCalls = append(f.stocksReserveCalls, stocksReserveCall{accountID, orderID, amount, currency, idempKey})
	return &accountpb.ReserveFundsResponse{ReservedBalance: amount.String()}, nil
}

func (f *stocksFakeAccountClient) ReleaseReservation(ctx context.Context, orderID uint64, idempKey, _ string) (*accountpb.ReleaseReservationResponse, error) {
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

// FillBuyOffer dependencies — recorded to assert call ordering.
type stocksSettleCall struct {
	orderID, orderTxID uint64
	amount             decimal.Decimal
	idempKey           string
}
type stocksCreditCall struct {
	accountNumber string
	amount        decimal.Decimal
	idempKey      string
}

func (f *stocksFakeAccountClient) PartialSettleReservation(_ context.Context, orderID, orderTxID uint64, amount decimal.Decimal, _, idempKey, _ string) (*accountpb.PartialSettleReservationResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSettleErr != nil {
		return nil, f.failSettleErr
	}
	f.settleCalls = append(f.settleCalls, stocksSettleCall{orderID, orderTxID, amount, idempKey})
	return &accountpb.PartialSettleReservationResponse{}, nil
}
func (f *stocksFakeAccountClient) CreditAccount(_ context.Context, acct string, amount decimal.Decimal, _, idempKey string) (*accountpb.AccountResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failCreditErr != nil {
		return nil, f.failCreditErr
	}
	f.creditCalls = append(f.creditCalls, stocksCreditCall{acct, amount, idempKey})
	return f.acct, nil
}
func (f *stocksFakeAccountClient) DebitAccount(_ context.Context, _ string, _ decimal.Decimal, _, _ string) (*accountpb.AccountResponse, error) {
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
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7), Quantity: 5, PricePerUnit: decimal.NewFromInt(155),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if updated.PublicQuantity != 5 {
		t.Errorf("first create: public_quantity=%d want 5", updated.PublicQuantity)
	}
	// Accumulate again.
	updated, err = env.svc.CreateSellOffer(context.Background(), CreateSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7), Quantity: 3, PricePerUnit: decimal.NewFromInt(160),
	})
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if updated.PublicQuantity != 8 {
		t.Errorf("accumulated: public_quantity=%d want 8", updated.PublicQuantity)
	}
}

// Phase 11 — price_per_unit is now required for sell direction.
func TestCreateSellOffer_RejectsMissingPrice(t *testing.T) {
	env := newOTCStockTestEnv(t)
	h := seedHolding(t, env, 7, 100, 0, 0)
	_, err := env.svc.CreateSellOffer(context.Background(), CreateSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7), Quantity: 5,
		// PricePerUnit deliberately omitted → zero → must reject.
	})
	if !errors.Is(err, ErrOTCStockSellPriceRequired) {
		t.Fatalf("want ErrOTCStockSellPriceRequired, got %v", err)
	}
	// Negative also rejected.
	_, err = env.svc.CreateSellOffer(context.Background(), CreateSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7), Quantity: 5,
		PricePerUnit: decimal.NewFromInt(-10),
	})
	if !errors.Is(err, ErrOTCStockSellPriceRequired) {
		t.Fatalf("negative price: want ErrOTCStockSellPriceRequired, got %v", err)
	}
}

// Phase 11 — accumulative call REPLACES the asking price with the
// latest call's value, allowing the seller to re-price.
func TestCreateSellOffer_ReplacesPriceOnRelist(t *testing.T) {
	env := newOTCStockTestEnv(t)
	h := seedHolding(t, env, 7, 100, 0, 0)
	_, err := env.svc.CreateSellOffer(context.Background(), CreateSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7), Quantity: 5,
		PricePerUnit: decimal.NewFromInt(155),
	})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Re-list with new price.
	updated, err := env.svc.CreateSellOffer(context.Background(), CreateSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7), Quantity: 3,
		PricePerUnit: decimal.NewFromInt(162),
	})
	if err != nil {
		t.Fatalf("relist: %v", err)
	}
	if updated.PublicQuantity != 8 {
		t.Errorf("accumulated qty=%d want 8", updated.PublicQuantity)
	}
	if !updated.PublicPrice.Equal(decimal.NewFromInt(162)) {
		t.Errorf("public_price=%s want 162 (latest call wins)", updated.PublicPrice)
	}
}

func TestCreateSellOffer_RejectsNonOwner(t *testing.T) {
	env := newOTCStockTestEnv(t)
	h := seedHolding(t, env, 7, 100, 0, 0)
	_, err := env.svc.CreateSellOffer(context.Background(), CreateSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(99), Quantity: 5, PricePerUnit: decimal.NewFromInt(150),
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
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7), Quantity: 5, PricePerUnit: decimal.NewFromInt(155),
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
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7), Quantity: 11, PricePerUnit: decimal.NewFromInt(150),
	}); !errors.Is(err, ErrOTCStockInsufficientShares) {
		t.Fatalf("want insufficient-shares, got %v", err)
	}
	// Right at the boundary — should succeed.
	if _, err := env.svc.CreateSellOffer(context.Background(), CreateSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7), Quantity: 10, PricePerUnit: decimal.NewFromInt(150),
	}); err != nil {
		t.Fatalf("boundary qty=10 should succeed, got %v", err)
	}
}

func TestCreateSellOffer_RejectsZeroQuantity(t *testing.T) {
	env := newOTCStockTestEnv(t)
	h := seedHolding(t, env, 7, 100, 0, 0)
	_, err := env.svc.CreateSellOffer(context.Background(), CreateSellOfferInput{
		HoldingID: h.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7), Quantity: 0, PricePerUnit: decimal.NewFromInt(150),
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

// ---------- FillBuyOffer (Phase 3B) ----------

// seedActiveBuyOffer creates an active buy offer in the buyer-owner=99
// shape, sized for the requested quantity at $100/share.
func seedActiveBuyOffer(t *testing.T, env *otcStockTestEnv, buyerOwnerID uint64, stockID uint64, qty int64) *model.OTCStockBuyOffer {
	t.Helper()
	pricePerUnit := decimal.NewFromInt(100)
	reserved := pricePerUnit.Mul(decimal.NewFromInt(qty))
	o := &model.OTCStockBuyOffer{
		BuyerOwnerType:            model.OwnerClient,
		BuyerOwnerID:              &buyerOwnerID,
		BuyerAccountID:            999,
		BuyerAccountNumber:        "111000000000000099",
		StockID:                   stockID,
		ListingID:                 1,
		Ticker:                    "AAPL",
		Name:                      "Apple Inc",
		OriginalQuantity:          qty,
		RemainingQuantity:         qty,
		PricePerUnit:              pricePerUnit,
		CurrencyCode:              "USD",
		ReservedAmount:            reserved,
		OriginalReservedAmount:    reserved,
		AccountReservationOrderID: 7_777_777,
		Status:                    model.OTCStockBuyOfferStatusActive,
	}
	if err := env.buyOfferRepo.Create(o); err != nil {
		t.Fatalf("seed buy offer: %v", err)
	}
	return o
}

func TestFillBuyOffer_HappyPath_DecrementsOfferAndSettles(t *testing.T) {
	env := newOTCStockTestEnv(t)
	// Seller has 10 shares of stock_id=100 (AAPL); buyer wants 6.
	seller := uint64(7)
	seedHolding(t, env, seller, 10, 0, 0)
	offer := seedActiveBuyOffer(t, env, 99 /*buyer*/, 100 /*stock_id*/, 6)

	res, err := env.svc.FillBuyOffer(context.Background(), FillBuyOfferInput{
		OfferID:         offer.ID,
		SellerOwnerType: model.OwnerClient,
		SellerOwnerID:   &seller,
		Quantity:        6,
		SellerAccountID: 42,
	})
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	if res.FilledQuantity != 6 {
		t.Errorf("filled=%d want 6", res.FilledQuantity)
	}
	wantTotal := decimal.NewFromInt(600)
	if !res.TotalAmount.Equal(wantTotal) {
		t.Errorf("total=%s want 600", res.TotalAmount)
	}

	// Buy offer: remaining → 0, status → filled, reserved → 0.
	updated, _ := env.buyOfferRepo.GetByID(offer.ID)
	if updated.RemainingQuantity != 0 {
		t.Errorf("remaining=%d want 0", updated.RemainingQuantity)
	}
	if updated.Status != model.OTCStockBuyOfferStatusFilled {
		t.Errorf("status=%s want filled", updated.Status)
	}

	// Seller holding decremented by 6.
	var h model.Holding
	env.db.Where("owner_id = ? AND stock_id_unused IS NULL", seller).Find(&h) // best-effort sqlite; just check sum below
	holdings, _, _ := env.holdingRepo.ListByOwner(model.OwnerClient, &seller, repository.HoldingFilter{Page: 1, PageSize: 10})
	if len(holdings) == 0 {
		t.Fatalf("seller holding gone (unexpected — we only decremented to 4)")
	}
	if holdings[0].Quantity != 4 {
		t.Errorf("seller holding qty=%d want 4", holdings[0].Quantity)
	}

	// Account-service calls: one settle + one credit, both with the
	// settle amount.
	if len(env.accountClient.settleCalls) != 1 {
		t.Fatalf("expected 1 settle call, got %d", len(env.accountClient.settleCalls))
	}
	if !env.accountClient.settleCalls[0].amount.Equal(wantTotal) {
		t.Errorf("settle amount=%s want 600", env.accountClient.settleCalls[0].amount)
	}
	if env.accountClient.settleCalls[0].orderID != 7_777_777 {
		t.Errorf("settle order_id=%d want 7777777", env.accountClient.settleCalls[0].orderID)
	}
	if len(env.accountClient.creditCalls) != 1 {
		t.Fatalf("expected 1 credit call, got %d", len(env.accountClient.creditCalls))
	}
	if !env.accountClient.creditCalls[0].amount.Equal(wantTotal) {
		t.Errorf("credit amount=%s want 600", env.accountClient.creditCalls[0].amount)
	}
}

func TestFillBuyOffer_PartialFill_LeavesOfferActive(t *testing.T) {
	env := newOTCStockTestEnv(t)
	seller := uint64(7)
	seedHolding(t, env, seller, 10, 0, 0)
	offer := seedActiveBuyOffer(t, env, 99, 100, 10) // buyer wants 10

	res, err := env.svc.FillBuyOffer(context.Background(), FillBuyOfferInput{
		OfferID:         offer.ID,
		SellerOwnerType: model.OwnerClient,
		SellerOwnerID:   &seller,
		Quantity:        4, // sell only 4 of the 10
		SellerAccountID: 42,
	})
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	if res.FilledQuantity != 4 {
		t.Errorf("filled=%d want 4", res.FilledQuantity)
	}
	updated, _ := env.buyOfferRepo.GetByID(offer.ID)
	if updated.RemainingQuantity != 6 {
		t.Errorf("remaining=%d want 6", updated.RemainingQuantity)
	}
	if updated.Status != model.OTCStockBuyOfferStatusActive {
		t.Errorf("status=%s want active", updated.Status)
	}
	if !updated.ReservedAmount.Equal(decimal.NewFromInt(600)) { // 1000 - 400
		t.Errorf("reserved_amount=%s want 600", updated.ReservedAmount)
	}
}

// CRITICAL safety test — seller cannot sell what they don't have.
func TestFillBuyOffer_RejectsIfSellerShort_NoMoneyMoves(t *testing.T) {
	env := newOTCStockTestEnv(t)
	seller := uint64(7)
	seedHolding(t, env, seller, 3, 0, 0) // seller only owns 3
	offer := seedActiveBuyOffer(t, env, 99, 100, 10)

	_, err := env.svc.FillBuyOffer(context.Background(), FillBuyOfferInput{
		OfferID:         offer.ID,
		SellerOwnerType: model.OwnerClient,
		SellerOwnerID:   &seller,
		Quantity:        5, // > 3 owned
		SellerAccountID: 42,
	})
	if !errors.Is(err, ErrOTCStockInsufficientShares) {
		t.Fatalf("want ErrOTCStockInsufficientShares, got %v", err)
	}
	// Zero account-service calls — money must NOT have moved.
	if len(env.accountClient.settleCalls) != 0 || len(env.accountClient.creditCalls) != 0 {
		t.Errorf("money moved despite insufficient shares: settle=%d credit=%d", len(env.accountClient.settleCalls), len(env.accountClient.creditCalls))
	}
	// Buy offer should be COMPENSATED back to active 10/1000 (the
	// step-1 decrement was reverted by compensateBuyOfferDecrement).
	o, _ := env.buyOfferRepo.GetByID(offer.ID)
	if o.RemainingQuantity != 10 {
		t.Errorf("post-compensation remaining=%d want 10", o.RemainingQuantity)
	}
	if o.Status != model.OTCStockBuyOfferStatusActive {
		t.Errorf("post-compensation status=%s want active", o.Status)
	}
}

func TestFillBuyOffer_RejectsSelfFill(t *testing.T) {
	env := newOTCStockTestEnv(t)
	// Seller and buyer are the same client → must reject.
	owner := uint64(7)
	seedHolding(t, env, owner, 10, 0, 0)
	offer := seedActiveBuyOffer(t, env, owner /*buyer*/, 100, 5)

	_, err := env.svc.FillBuyOffer(context.Background(), FillBuyOfferInput{
		OfferID:         offer.ID,
		SellerOwnerType: model.OwnerClient,
		SellerOwnerID:   &owner,
		Quantity:        5,
		SellerAccountID: 42,
	})
	if !errors.Is(err, ErrOTCBuyOwnOffer) {
		t.Fatalf("want ErrOTCBuyOwnOffer for self-fill, got %v", err)
	}
}

func TestFillBuyOffer_RejectsCancelledOffer(t *testing.T) {
	env := newOTCStockTestEnv(t)
	seller := uint64(7)
	seedHolding(t, env, seller, 10, 0, 0)
	offer := seedActiveBuyOffer(t, env, 99, 100, 5)
	// Manually flip to cancelled.
	offer.Status = model.OTCStockBuyOfferStatusCancelled
	_ = env.buyOfferRepo.Save(offer)

	_, err := env.svc.FillBuyOffer(context.Background(), FillBuyOfferInput{
		OfferID:         offer.ID,
		SellerOwnerType: model.OwnerClient,
		SellerOwnerID:   &seller,
		Quantity:        5,
		SellerAccountID: 42,
	})
	if !errors.Is(err, ErrOTCStockBuyOfferNotActive) {
		t.Fatalf("want ErrOTCStockBuyOfferNotActive, got %v", err)
	}
}

func TestFillBuyOffer_RejectsExceedsRemaining(t *testing.T) {
	env := newOTCStockTestEnv(t)
	seller := uint64(7)
	seedHolding(t, env, seller, 100, 0, 0)
	offer := seedActiveBuyOffer(t, env, 99, 100, 5) // buyer wants only 5

	_, err := env.svc.FillBuyOffer(context.Background(), FillBuyOfferInput{
		OfferID:         offer.ID,
		SellerOwnerType: model.OwnerClient,
		SellerOwnerID:   &seller,
		Quantity:        6, // > offer.RemainingQuantity
		SellerAccountID: 42,
	})
	if !errors.Is(err, ErrOTCStockInsufficientRemainingQty) {
		t.Fatalf("want ErrOTCStockInsufficientRemainingQty, got %v", err)
	}
}
