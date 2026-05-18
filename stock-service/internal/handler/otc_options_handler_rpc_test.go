package handler

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// otcOptionsHandlerFixture wires an OTCOptionsHandler against a sqlite DB so
// the gRPC RPCs can be exercised end-to-end without docker / kafka. The
// service underneath is a real OTCOfferService with a real holdings repo.
type otcOptionsHandlerFixture struct {
	h         *OTCOptionsHandler
	db        *gorm.DB
	holdings  *repository.HoldingRepository
	offers    *repository.OTCOfferRepository
	contracts *repository.OptionContractRepository
}

func newOTCOptionsHandlerFixture(t *testing.T) *otcOptionsHandlerFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Holding{},
		&model.OTCOffer{},
		&model.OTCOfferRevision{},
		&model.OptionContract{},
		&model.OTCOfferReadReceipt{},
		&model.PeerOptionContract{},
		&model.Listing{},
		&model.Stock{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	offerRepo := repository.NewOTCOfferRepository(db)
	revRepo := repository.NewOTCOfferRevisionRepository(db)
	contractRepo := repository.NewOptionContractRepository(db)
	receiptRepo := repository.NewOTCReadReceiptRepository(db)
	holdingRepo := repository.NewHoldingRepository(db)

	svc := service.NewOTCOfferService(offerRepo, revRepo, contractRepo, holdingRepo, receiptRepo, nil)
	h := NewOTCOptionsHandler(svc, contractRepo)
	return &otcOptionsHandlerFixture{
		h: h, db: db,
		holdings: holdingRepo, offers: offerRepo, contracts: contractRepo,
	}
}

func (f *otcOptionsHandlerFixture) seedSellerHolding(t *testing.T, ownerID uint64, stockID uint64, qty int64) {
	t.Helper()
	uid := ownerID
	if err := f.holdings.Upsert(context.Background(), &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		SecurityType: "stock", SecurityID: stockID, Quantity: qty,
		AveragePrice: decimal.NewFromInt(100),
	}); err != nil {
		t.Fatalf("seed holding: %v", err)
	}
}

func (f *otcOptionsHandlerFixture) createOffer(t *testing.T, sellerID int64, stockID uint64) uint64 {
	t.Helper()
	resp, err := f.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		ActorUserId: sellerID, ActorSystemType: "client",
		Direction:      model.OTCDirectionSellInitiated,
		StockId:        stockID,
		Quantity:       "10",
		StrikePrice:    "150",
		Premium:        "20",
		SettlementDate: time.Now().AddDate(0, 0, 30).Format("2006-01-02"),
		AccountId:      9001,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return resp.GetId()
}

// ---------------- CreateOffer ----------------

func TestOTCOptionsHandler_CreateOffer_HappyPath(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	fx.seedSellerHolding(t, 7, 42, 100)
	resp, err := fx.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		ActorUserId: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockId: 42,
		Quantity: "10", StrikePrice: "150", Premium: "20",
		SettlementDate: time.Now().AddDate(0, 0, 30).Format("2006-01-02"),
		AccountId:      9001,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetId() == 0 || resp.GetStatus() != model.OTCOfferStatusPending {
		t.Errorf("got %+v", resp)
	}
}

func TestOTCOptionsHandler_CreateOffer_BadQuantity(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		Quantity: "abc", StrikePrice: "1", Premium: "1", SettlementDate: "2030-01-01",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestOTCOptionsHandler_CreateOffer_BadStrike(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		Quantity: "1", StrikePrice: "abc", Premium: "1", SettlementDate: "2030-01-01",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestOTCOptionsHandler_CreateOffer_BadPremium(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		Quantity: "1", StrikePrice: "1", Premium: "abc", SettlementDate: "2030-01-01",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestOTCOptionsHandler_CreateOffer_BadDate(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		Quantity: "1", StrikePrice: "1", Premium: "1", SettlementDate: "not-a-date",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestOTCOptionsHandler_CreateOffer_WithCounterparty(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	fx.seedSellerHolding(t, 7, 42, 100)
	resp, err := fx.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		ActorUserId: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockId: 42,
		Quantity: "10", StrikePrice: "150", Premium: "20",
		SettlementDate: time.Now().AddDate(0, 0, 30).Format("2006-01-02"),
		Counterparty:   &stockpb.PartyRef{UserId: 8, SystemType: "client"},
		AccountId:      9001,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetCounterparty() == nil || resp.GetCounterparty().UserId != 8 {
		t.Errorf("counterparty wiring lost: %+v", resp.GetCounterparty())
	}
}

func TestOTCOptionsHandler_CreateOffer_NoSharesError(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.CreateOffer(context.Background(), &stockpb.CreateOTCOfferRequest{
		ActorUserId: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockId: 42,
		Quantity: "10", StrikePrice: "150", Premium: "20",
		SettlementDate: time.Now().AddDate(0, 0, 30).Format("2006-01-02"),
		AccountId:      9001,
	})
	if err == nil {
		t.Fatal("expected error from service")
	}
}

// ---------------- ListMyOffers ----------------

func TestOTCOptionsHandler_ListMyOffers(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	fx.seedSellerHolding(t, 7, 42, 100)
	_ = fx.createOffer(t, 7, 42)
	resp, err := fx.h.ListMyOffers(context.Background(), &stockpb.ListMyOTCOffersRequest{
		ActorUserId: 7, ActorSystemType: "client", Role: "initiator",
		Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetTotal() != 1 || len(resp.GetOffers()) != 1 {
		t.Errorf("got total=%d len=%d", resp.GetTotal(), len(resp.GetOffers()))
	}
	// caller is last-modifier so unread should be false
	if resp.GetOffers()[0].GetUnread() {
		t.Errorf("expected unread=false (caller was last modifier)")
	}
}

// ---------------- GetOffer ----------------

func TestOTCOptionsHandler_GetOffer_HappyPath(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	fx.seedSellerHolding(t, 7, 42, 100)
	id := fx.createOffer(t, 7, 42)
	resp, err := fx.h.GetOffer(context.Background(), &stockpb.GetOTCOfferRequest{
		OfferId: id, ActorUserId: 7, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetOffer().GetId() != id {
		t.Errorf("id mismatch")
	}
	if len(resp.GetRevisions()) == 0 {
		t.Errorf("expected at least one revision")
	}
}

func TestOTCOptionsHandler_GetOffer_NotFound(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.GetOffer(context.Background(), &stockpb.GetOTCOfferRequest{
		OfferId: 9999, ActorUserId: 7, ActorSystemType: "client",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------------- CounterOffer ----------------

func TestOTCOptionsHandler_CounterOffer_HappyPath(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	fx.seedSellerHolding(t, 7, 42, 100)
	id := fx.createOffer(t, 7, 42)
	// Different actor (the buyer counters)
	resp, err := fx.h.CounterOffer(context.Background(), &stockpb.CounterOTCOfferRequest{
		OfferId: id, ActorUserId: 8, ActorSystemType: "client",
		Quantity: "5", StrikePrice: "160", Premium: "25",
		SettlementDate: time.Now().AddDate(0, 0, 31).Format("2006-01-02"),
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetStatus() != model.OTCOfferStatusCountered {
		t.Errorf("status=%s", resp.GetStatus())
	}
}

func TestOTCOptionsHandler_CounterOffer_BadQuantity(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.CounterOffer(context.Background(), &stockpb.CounterOTCOfferRequest{
		Quantity: "x", StrikePrice: "1", Premium: "1", SettlementDate: "2030-01-01",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestOTCOptionsHandler_CounterOffer_BadStrike(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.CounterOffer(context.Background(), &stockpb.CounterOTCOfferRequest{
		Quantity: "1", StrikePrice: "x", Premium: "1", SettlementDate: "2030-01-01",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestOTCOptionsHandler_CounterOffer_BadPremium(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.CounterOffer(context.Background(), &stockpb.CounterOTCOfferRequest{
		Quantity: "1", StrikePrice: "1", Premium: "x", SettlementDate: "2030-01-01",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestOTCOptionsHandler_CounterOffer_BadDate(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.CounterOffer(context.Background(), &stockpb.CounterOTCOfferRequest{
		Quantity: "1", StrikePrice: "1", Premium: "1", SettlementDate: "bad",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// ---------------- RejectOffer ----------------

func TestOTCOptionsHandler_RejectOffer_HappyPath(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	fx.seedSellerHolding(t, 7, 42, 100)
	id := fx.createOffer(t, 7, 42)
	resp, err := fx.h.RejectOffer(context.Background(), &stockpb.RejectOTCOfferRequest{
		OfferId: id, ActorUserId: 8, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetStatus() != model.OTCOfferStatusRejected {
		t.Errorf("status=%s", resp.GetStatus())
	}
}

func TestOTCOptionsHandler_RejectOffer_MissingOffer(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.RejectOffer(context.Background(), &stockpb.RejectOTCOfferRequest{
		OfferId: 9999, ActorUserId: 8, ActorSystemType: "client",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------------- AcceptOffer / ExerciseContract input validation ----------------

func TestOTCOptionsHandler_AcceptOffer_BadInput(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.AcceptOffer(context.Background(), &stockpb.AcceptOTCOfferRequest{
		OfferId: 1, ActorUserId: 7, ActorSystemType: "client",
		// missing account_id
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// ---------------- ListMyContracts ----------------

func TestOTCOptionsHandler_ListMyContracts_NoContractsRepoWired(t *testing.T) {
	// When constructed without a contracts repo, ListMyContracts returns empty.
	svc := &service.OTCOfferService{}
	h := &OTCOptionsHandler{svc: svc} // intentionally bare
	resp, err := h.ListMyContracts(context.Background(), &stockpb.ListMyContractsRequest{
		ActorUserId: 7, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetTotal() != 0 {
		t.Errorf("expected empty")
	}
}

func TestOTCOptionsHandler_ListMyContracts_HappyPath(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	resp, err := fx.h.ListMyContracts(context.Background(), &stockpb.ListMyContractsRequest{
		ActorUserId: 7, ActorSystemType: "client", Role: "buyer",
		Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetTotal() != 0 {
		t.Errorf("expected 0 contracts initially")
	}
}

func TestOTCOptionsHandler_ListMyContracts_WithPeerContracts(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	peerRepo := repository.NewPeerOptionContractRepository(fx.db)
	h := fx.h.WithPeerContracts(peerRepo, 111)
	resp, err := h.ListMyContracts(context.Background(), &stockpb.ListMyContractsRequest{
		ActorUserId: 7, ActorSystemType: "client",
		Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetPeerTotal() != 0 {
		t.Errorf("expected 0 peer contracts initially")
	}
}

// ---------------- GetContract ----------------

func TestOTCOptionsHandler_GetContract_NoRepoWired(t *testing.T) {
	h := &OTCOptionsHandler{}
	_, err := h.GetContract(context.Background(), &stockpb.GetContractRequest{
		ContractId: 1, ActorUserId: 7, ActorSystemType: "client",
	})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("expected Unimplemented, got %v", err)
	}
}

func TestOTCOptionsHandler_GetContract_NotFound(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	_, err := fx.h.GetContract(context.Background(), &stockpb.GetContractRequest{
		ContractId: 9999, ActorUserId: 7, ActorSystemType: "client",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOTCOptionsHandler_GetContract_HappyPath_BuyerView(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	uid := uint64(7)
	c := &model.OptionContract{
		StockID:         42,
		Quantity:        decimal.NewFromInt(10),
		StrikePrice:     decimal.NewFromInt(150),
		PremiumPaid:     decimal.NewFromInt(20),
		PremiumCurrency: "USD",
		StrikeCurrency:  "USD",
		SettlementDate:  time.Now().Add(30 * 24 * time.Hour),
		Status:          model.OptionContractStatusActive,
		BuyerOwnerType:  model.OwnerClient, BuyerOwnerID: &uid,
		SellerOwnerType: model.OwnerBank, SellerOwnerID: nil,
		PremiumPaidAt: time.Now(),
	}
	if err := fx.contracts.Create(c); err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	resp, err := fx.h.GetContract(context.Background(), &stockpb.GetContractRequest{
		ContractId: c.ID, ActorUserId: 7, ActorSystemType: "client",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetId() != c.ID {
		t.Errorf("id mismatch")
	}
}

func TestOTCOptionsHandler_GetContract_NonParticipant(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	uid := uint64(7)
	c := &model.OptionContract{
		StockID: 42, Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(20),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &uid,
		SellerOwnerType: model.OwnerBank, SellerOwnerID: nil,
		PremiumPaidAt: time.Now(),
	}
	_ = fx.contracts.Create(c)
	_, err := fx.h.GetContract(context.Background(), &stockpb.GetContractRequest{
		ContractId: c.ID, ActorUserId: 99, ActorSystemType: "client",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", err)
	}
}

// ---------------- marketRefPrice / WithListings ----------------

func TestOTCOptionsHandler_MarketRefPrice_NoListings(t *testing.T) {
	h := &OTCOptionsHandler{}
	if got := h.marketRefPrice(42); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestOTCOptionsHandler_WithListings_AddsListingsRepo(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	listingRepo := repository.NewListingRepository(fx.db)
	h2 := fx.h.WithListings(listingRepo)
	// Pure smoke — no listing rows present, returns "".
	if got := h2.marketRefPrice(42); got != "" {
		t.Errorf("expected empty for unknown stock, got %q", got)
	}
}
