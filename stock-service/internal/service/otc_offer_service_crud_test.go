package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// recordingOTCNotifier captures the in-app notifications emitted by
// OTCOfferService so tests can assert on them. Satisfies otcNotifier.
type recordingOTCNotifier struct {
	notifs []kafkamsg.GeneralNotificationMessage
}

func (r *recordingOTCNotifier) PublishGeneralNotification(_ context.Context, m kafkamsg.GeneralNotificationMessage) error {
	r.notifs = append(r.notifs, m)
	return nil
}

// otcCRUDFixture provides an isolated OTCOfferService backed by an in-memory
// sqlite DB so the CRUD-level methods (Create / Counter / Reject / List /
// Get) can be tested without the saga-layer dependencies.
type otcCRUDFixture struct {
	svc      *OTCOfferService
	offers   *repository.OTCOfferRepository
	holdings *repository.HoldingRepository
	notifier *recordingOTCNotifier
}

func newOTCCRUDFixture(t *testing.T) *otcCRUDFixture {
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
		&model.OTCNegotiation{},
		&model.OptionContract{},
		&model.OTCOfferReadReceipt{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	offerRepo := repository.NewOTCOfferRepository(db)
	revRepo := repository.NewOTCOfferRevisionRepository(db)
	contractRepo := repository.NewOptionContractRepository(db)
	receiptRepo := repository.NewOTCReadReceiptRepository(db)
	holdingRepo := repository.NewHoldingRepository(db)
	svc := NewOTCOfferService(offerRepo, revRepo, contractRepo, holdingRepo, receiptRepo, nil)
	notifier := &recordingOTCNotifier{}
	svc.notifier = notifier
	return &otcCRUDFixture{svc: svc, offers: offerRepo, holdings: holdingRepo, notifier: notifier}
}

func (f *otcCRUDFixture) seedHolding(t *testing.T, ownerID uint64, stockID uint64, qty int64) {
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

// ---------------- Create ----------------

func TestOTCOfferService_Create_SellInitiated_HappyPath(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)

	out, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID:     7,
		ActorSystemType: "client",
		Direction:       model.OTCDirectionSellInitiated,
		StockID:         42,
		Quantity:        decimal.NewFromInt(10),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.Status != model.OTCOfferStatusPending {
		t.Errorf("status=%s want pending", out.Status)
	}
	if out.InitiatorOwnerType != model.OwnerClient {
		t.Errorf("initiator owner type = %v", out.InitiatorOwnerType)
	}
}

func TestOTCOfferService_Create_StoresInitiatorAccount(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)

	out, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID:        7,
		ActorSystemType:    "client",
		Direction:          model.OTCDirectionSellInitiated,
		StockID:            42,
		Quantity:           decimal.NewFromInt(10),
		InitiatorAccountID: 9001,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.InitiatorAccountID != 9001 {
		t.Errorf("got %d, want 9001", out.InitiatorAccountID)
	}
}

func TestOTCOfferService_Create_BankOffer_CapturesActingEmployeeID(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	// Employee 17 creates a buy_initiated offer acting AS the bank: the
	// gateway resolves the bank owner (actor_system_type "bank", actor_user_id
	// 0) and threads the originating employee id separately.
	emp := uint64(17)
	out, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID:      0,
		ActorSystemType:  "bank",
		ActingEmployeeID: &emp,
		Direction:        model.OTCDirectionBuyInitiated,
		StockID:          42,
		Quantity:         decimal.NewFromInt(10),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.InitiatorOwnerType != model.OwnerBank {
		t.Errorf("initiator owner type = %v, want bank", out.InitiatorOwnerType)
	}
	if out.ActingEmployeeID == nil || *out.ActingEmployeeID != 17 {
		t.Fatalf("ActingEmployeeID = %v, want 17", out.ActingEmployeeID)
	}
	// Persisted row carries it.
	got, err := fx.offers.GetByID(out.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.ActingEmployeeID == nil || *got.ActingEmployeeID != 17 {
		t.Errorf("persisted ActingEmployeeID = %v, want 17", got.ActingEmployeeID)
	}
}

func TestOTCOfferService_Create_OnBehalfOfClient_NoActingEmployeeID(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 42, 7, 100)
	// Employee acting on behalf of client 42: the gateway resolves the client
	// owner (actor_system_type "client", actor_user_id 42). Even if an acting
	// employee id is threaded, a client-owned offer must NOT carry it.
	emp := uint64(17)
	out, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID:      42,
		ActorSystemType:  "client",
		ActingEmployeeID: &emp,
		Direction:        model.OTCDirectionSellInitiated,
		StockID:          7,
		Quantity:         decimal.NewFromInt(10),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.InitiatorOwnerType != model.OwnerClient {
		t.Errorf("initiator owner type = %v, want client", out.InitiatorOwnerType)
	}
	if out.ActingEmployeeID != nil {
		t.Errorf("ActingEmployeeID = %v, want nil for client-owned offer", *out.ActingEmployeeID)
	}
}

func TestOTCOfferService_Create_ClientOffer_NoActingEmployeeID(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)
	out, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID:     7,
		ActorSystemType: "client",
		Direction:       model.OTCDirectionSellInitiated,
		StockID:         42,
		Quantity:        decimal.NewFromInt(10),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.ActingEmployeeID != nil {
		t.Errorf("ActingEmployeeID = %v, want nil for client offer", *out.ActingEmployeeID)
	}
}

func TestOTCOfferService_Create_BankOffer_NilActingEmployeeForSystemPath(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	// Bank offer created by a non-employee/system path (no acting employee).
	out, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID:     0,
		ActorSystemType: "bank",
		Direction:       model.OTCDirectionBuyInitiated,
		StockID:         42,
		Quantity:        decimal.NewFromInt(10),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.InitiatorOwnerType != model.OwnerBank {
		t.Errorf("initiator owner type = %v, want bank", out.InitiatorOwnerType)
	}
	if out.ActingEmployeeID != nil {
		t.Errorf("ActingEmployeeID = %v, want nil (system path)", *out.ActingEmployeeID)
	}
}

func TestOTCOfferService_Create_RejectsZeroQuantity(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.Zero,
	})
	if err == nil {
		t.Fatal("expected error for zero quantity")
	}
}

func TestOTCOfferService_Create_RejectsUnknownDirection(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: "weird",
		StockID:   42,
		Quantity:  decimal.NewFromInt(10),
	})
	if err == nil {
		t.Fatal("expected error for unknown direction")
	}
}

// Phase 9 follow-up: open buy_initiated listings (counterparty=null)
// are now allowed — the parallel-chains marketplace lets any bidder
// open a negotiation chain against them.
func TestOTCOfferService_Create_BuyInitiated_OpenListingAllowed(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	o, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionBuyInitiated, StockID: 42,
		Quantity:           decimal.NewFromInt(10),
		InitiatorAccountID: 99,
	})
	if err != nil {
		t.Fatalf("open buy_initiated listing should succeed, got %v", err)
	}
	if o.Direction != model.OTCDirectionBuyInitiated {
		t.Errorf("direction=%s want buy_initiated", o.Direction)
	}
	if o.CounterpartyOwnerType != nil || o.CounterpartyOwnerID != nil {
		t.Errorf("counterparty fields should be nil for open listing, got %v / %v", o.CounterpartyOwnerType, o.CounterpartyOwnerID)
	}
}

func TestOTCOfferService_Create_CounterpartyHalfSet(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	cpID := int64(99)
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction:          model.OTCDirectionSellInitiated,
		StockID:            42,
		Quantity:           decimal.NewFromInt(10),
		CounterpartyUserID: &cpID,
		// CounterpartySystemType intentionally nil
	})
	if err == nil {
		t.Fatal("expected error when counterparty user_id is set without system_type")
	}
}

func TestOTCOfferService_Create_SellInitiated_NoSharesHeld(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	// no holdings seeded
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10),
	})
	if err == nil {
		t.Fatal("expected seller-no-holdings error")
	}
}

func TestOTCOfferService_Create_SellInitiated_InsufficientShares(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 5) // only 5 shares
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10), // requesting 10 > 5
	})
	if err == nil {
		t.Fatal("expected insufficient-shares error")
	}
	// Must surface as a FailedPrecondition (business-rule violation → HTTP 409),
	// not an opaque Unknown/Internal that the gateway maps to 500. A 500 here
	// leaks an internal error class for what is a normal covered-call rejection.
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Fatalf("insufficient-shares code = %s, want FailedPrecondition", got)
	}
}

// TestOTCOfferService_Create_ValidationErrorsAreInvalidArgument guards that the
// create-time field validations carry codes.InvalidArgument (→ HTTP 400) rather
// than the default Unknown/Internal that the gateway maps to 500.
func TestOTCOfferService_Create_ValidationErrorsAreInvalidArgument(t *testing.T) {
	base := func() CreateOfferInput {
		return CreateOfferInput{
			ActorUserID: 7, ActorSystemType: "client",
			Direction: model.OTCDirectionSellInitiated, StockID: 42,
			Quantity: decimal.NewFromInt(10),
		}
	}
	cases := map[string]func(CreateOfferInput) CreateOfferInput{
		"zero quantity":     func(in CreateOfferInput) CreateOfferInput { in.Quantity = decimal.Zero; return in },
		"unknown direction": func(in CreateOfferInput) CreateOfferInput { in.Direction = "weird"; return in },
		"counterparty half": func(in CreateOfferInput) CreateOfferInput { id := int64(9); in.CounterpartyUserID = &id; return in },
	}
	for name, mut := range cases {
		fx2 := newOTCCRUDFixture(t)
		fx2.seedHolding(t, 7, 42, 100)
		_, err := fx2.svc.Create(context.Background(), mut(base()))
		if err == nil {
			t.Fatalf("%s: expected error", name)
		}
		if got := status.Code(err); got != codes.InvalidArgument {
			t.Fatalf("%s: code = %s, want InvalidArgument", name, got)
		}
	}
}

// TestCreate_RejectsDuplicateOpenOfferSameTickerDirection asserts that a second
// OPEN offer for the same (owner, ticker, direction) is rejected with the
// sentinel ErrOTCOfferDuplicateOpen (mapped to gRPC AlreadyExists).
func TestCreate_RejectsDuplicateOpenOfferSameTickerDirection(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 1, 100) // seller 7 holds plenty of OPK (stock 1)
	in := CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 1, Ticker: "OPK",
		Quantity: decimal.NewFromInt(5), InitiatorAccountID: 1,
	}
	_, err := fx.svc.Create(context.Background(), in)
	require.NoError(t, err)
	_, err = fx.svc.Create(context.Background(), in) // duplicate (owner,ticker,direction)
	require.ErrorIs(t, err, ErrOTCOfferDuplicateOpen)
}

// TestCreate_DBIndexBackstopsDuplicateOpen mounts the SAME partial unique index
// that stock-service/cmd/main.go builds, then drives Create twice for the same
// (owner,ticker,direction). The second Create must still surface
// ErrOTCOfferDuplicateOpen — proving Create translates the DB's
// unique-constraint violation (the TOCTOU backstop) to the typed sentinel, even
// though the pre-check would also fire. With the index present the invariant
// holds at the DB even when two racing inserts both pass the count pre-check.
func TestCreate_DBIndexBackstopsDuplicateOpen(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	require.NoError(t, fx.offers.DB().Exec(`CREATE UNIQUE INDEX IF NOT EXISTS ux_otc_offer_open_owner_ticker_dir
		ON otc_offers (initiator_owner_id, ticker, direction)
		WHERE status IN ('open','PENDING','COUNTERED') AND local = true AND initiator_owner_id IS NOT NULL`).Error)
	fx.seedHolding(t, 7, 1, 100)
	in := CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 1, Ticker: "OPK",
		Quantity: decimal.NewFromInt(5), InitiatorAccountID: 1,
	}
	_, err := fx.svc.Create(context.Background(), in)
	require.NoError(t, err)
	_, err = fx.svc.Create(context.Background(), in)
	require.ErrorIs(t, err, ErrOTCOfferDuplicateOpen)
}

// ---------------- UpdateQuantity ----------------

// Per Task B3: editing the total quantity of an open sell offer SETS the total
// (up or down), rejects above the owner's holding, rejects non-positive, and is
// owner-only.
func TestUpdateQuantity_SetsTotal_RejectsBelowCommitted_AndAboveHolding(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	ctx := context.Background()
	fx.seedHolding(t, 7, 1, 100) // seller 7 holds 100 OPK (stock 1)

	off, err := fx.svc.Create(ctx, CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 1, Ticker: "OPK",
		Quantity: decimal.NewFromInt(10), InitiatorAccountID: 1,
	})
	require.NoError(t, err)

	owner7Type, owner7ID := model.OwnerFromLegacy(7, "client")

	got, err := fx.svc.UpdateQuantity(ctx, off.ID, owner7Type, owner7ID, decimal.NewFromInt(80))
	require.NoError(t, err)
	require.True(t, got.Quantity.Equal(decimal.NewFromInt(80)))

	// > holding 100
	_, err = fx.svc.UpdateQuantity(ctx, off.ID, owner7Type, owner7ID, decimal.NewFromInt(200))
	require.ErrorIs(t, err, ErrOTCInsufficientShares)

	// non-positive
	_, err = fx.svc.UpdateQuantity(ctx, off.ID, owner7Type, owner7ID, decimal.Zero)
	require.ErrorIs(t, err, ErrOTCOfferFieldInvalid)

	// a DIFFERENT owner cannot edit
	owner8Type, owner8ID := model.OwnerFromLegacy(8, "client")
	_, err = fx.svc.UpdateQuantity(ctx, off.ID, owner8Type, owner8ID, decimal.NewFromInt(5))
	require.ErrorIs(t, err, ErrOTCNotOwner)
}

// The committed lower bound: a reduction below the shares already committed to an
// accepted (contract-forming) negotiation chain on this offer is rejected, while
// a value at/above that floor (and within the holding) is accepted. The accepted
// chain is inserted directly to exercise OutstandingCommittedQuantityTx in
// isolation (the normal accept flow would consume the parent, blocking the edit).
func TestUpdateQuantity_RejectsBelowCommittedChain(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	ctx := context.Background()
	fx.seedHolding(t, 7, 1, 100)

	off, err := fx.svc.Create(ctx, CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 1, Ticker: "OPK",
		Quantity: decimal.NewFromInt(60), InitiatorAccountID: 1,
	})
	require.NoError(t, err)

	bidder := uint64(8)
	now := time.Now().UTC()
	require.NoError(t, fx.offers.DB().Create(&model.OTCNegotiation{
		ParentOfferID:   off.ID,
		BidderOwnerType: model.OwnerClient, BidderOwnerID: &bidder,
		Quantity: decimal.NewFromInt(50), StrikePrice: decimal.NewFromInt(10),
		Premium: decimal.NewFromInt(1), SettlementDate: now.AddDate(0, 0, 30),
		Status:                    model.OTCNegotiationStatusAccepted,
		LastActionByPrincipalType: "client", LastActionByPrincipalID: 8,
		LastActionByOwnerType: "client", LastActionAt: now,
	}).Error)

	owner7Type, owner7ID := model.OwnerFromLegacy(7, "client")
	_, err = fx.svc.UpdateQuantity(ctx, off.ID, owner7Type, owner7ID, decimal.NewFromInt(40)) // below committed 50
	require.ErrorIs(t, err, ErrOTCOfferFieldInvalid)

	got, err := fx.svc.UpdateQuantity(ctx, off.ID, owner7Type, owner7ID, decimal.NewFromInt(70)) // >= 50, <= holding 100
	require.NoError(t, err)
	require.True(t, got.Quantity.Equal(decimal.NewFromInt(70)))
}

// A terminal/non-open offer cannot be edited.
func TestUpdateQuantity_RejectsNonOpenOffer(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	ctx := context.Background()
	fx.seedHolding(t, 7, 1, 100)
	off, err := fx.svc.Create(ctx, CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 1, Ticker: "OPK",
		Quantity: decimal.NewFromInt(10), InitiatorAccountID: 1,
	})
	require.NoError(t, err)
	off.Status = model.OTCOfferStatusAccepted
	require.NoError(t, fx.offers.Save(off))

	owner7Type, owner7ID := model.OwnerFromLegacy(7, "client")
	_, err = fx.svc.UpdateQuantity(ctx, off.ID, owner7Type, owner7ID, decimal.NewFromInt(5))
	require.ErrorIs(t, err, ErrOTCOfferFieldInvalid)
}

// ---------------- In-app notifications ----------------

func TestOTCOfferService_Create_NotifiesNamedClientCounterparty(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)
	cpID := int64(8)
	cpType := "client"
	out, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42, Ticker: "ACME",
		Quantity:           decimal.NewFromInt(10),
		CounterpartyUserID: &cpID, CounterpartySystemType: &cpType,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(fx.notifier.notifs) != 1 {
		t.Fatalf("got %d notifications, want 1", len(fx.notifier.notifs))
	}
	n := fx.notifier.notifs[0]
	if n.Type != "OTC_OFFER_RECEIVED" {
		t.Errorf("type=%s want OTC_OFFER_RECEIVED", n.Type)
	}
	if n.UserID != 8 {
		t.Errorf("user_id=%d want 8", n.UserID)
	}
	if n.RefType != "otc_offer" || n.RefID != out.ID {
		t.Errorf("ref=%s/%d want otc_offer/%d", n.RefType, n.RefID, out.ID)
	}
	if n.Data["ticker"] != "ACME" {
		t.Errorf("expected ticker ACME in data, got %+v", n.Data)
	}
}

func TestOTCOfferService_Create_BroadcastOffer_NoNotification(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)
	// sell_initiated with no counterparty = broadcast.
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(fx.notifier.notifs) != 0 {
		t.Fatalf("got %d notifications, want 0", len(fx.notifier.notifs))
	}
}

func TestOTCOfferService_Create_BankCounterparty_NoNotification(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)
	cpID := int64(0)
	cpType := "bank"
	_, err := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity:           decimal.NewFromInt(10),
		CounterpartyUserID: &cpID, CounterpartySystemType: &cpType,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(fx.notifier.notifs) != 0 {
		t.Fatalf("got %d notifications, want 0 (bank counterparty)", len(fx.notifier.notifs))
	}
}

// ---------------- ListMyOffers / GetOffer ----------------

func TestOTCOfferService_ListMyOffers_FindsByOwner(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)
	_, _ = fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10),
	})
	rows, total, err := fx.svc.ListMyOffers(7, "client", "initiator", nil, 0, 1, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Errorf("got %d rows total %d", len(rows), total)
	}
}

func TestOTCOfferService_LastReadReceipt_NoOpWhenReceiptsNil(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	// Bypass receipts wiring: copy svc with receipts=nil.
	bare := *fx.svc
	bare.receipts = nil
	r, err := bare.LastReadReceipt(7, "client", 1)
	if err != nil {
		t.Errorf("err=%v", err)
	}
	if r != nil {
		t.Errorf("expected nil")
	}
}

func TestOTCOfferService_GetOffer_HappyPath(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)
	out, _ := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10),
	})
	got, revs, err := fx.svc.GetOffer(out.ID, 7, "client")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != out.ID {
		t.Errorf("id mismatch")
	}
	if len(revs) == 0 {
		t.Errorf("expected at least one revision")
	}
}

// A non-participant can READ the offer (public discovery mirrors the unified
// list) but never sees its revision history and triggers no read-receipt.
func TestOTCOfferService_GetOffer_NonParticipantSeesOfferNotRevisions(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	fx.seedHolding(t, 7, 42, 100)
	out, _ := fx.svc.Create(context.Background(), CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10),
	})
	got, revs, err := fx.svc.GetOffer(out.ID, 999, "client")
	if err != nil {
		t.Fatalf("non-participant read should succeed, got: %v", err)
	}
	if got == nil || got.ID != out.ID {
		t.Fatalf("non-participant should receive the offer; got %+v", got)
	}
	if len(revs) != 0 {
		t.Errorf("non-participant must not receive revision history; got %d revs", len(revs))
	}
}

func TestOTCOfferService_GetOffer_MissingOffer(t *testing.T) {
	fx := newOTCCRUDFixture(t)
	_, _, err := fx.svc.GetOffer(9999, 7, "client")
	if err == nil {
		t.Fatal("expected error for missing offer")
	}
}

// ---------------- Helpers: ptrCounterparty / otcOtherParty / actorToOwnerParty ----------------

func TestPtrCounterparty_Nil(t *testing.T) {
	o := &model.OTCOffer{}
	if got := ptrCounterparty(o); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestPtrCounterparty_NonNil(t *testing.T) {
	tp := model.OwnerClient
	uid := uint64(99)
	o := &model.OTCOffer{CounterpartyOwnerType: &tp, CounterpartyOwnerID: &uid}
	got := ptrCounterparty(o)
	if got == nil || got.OwnerType != "client" || got.OwnerID == nil || *got.OwnerID != 99 {
		t.Errorf("got %+v", got)
	}
}

func TestOTCOtherParty_ActorIsInitiator(t *testing.T) {
	uid := uint64(7)
	cpType := model.OwnerClient
	cpID := uint64(8)
	o := &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &uid,
		CounterpartyOwnerType: &cpType, CounterpartyOwnerID: &cpID,
	}
	got := otcOtherParty(o, 7, "client")
	if got.OwnerType != "client" || got.OwnerID == nil || *got.OwnerID != 8 {
		t.Errorf("got %+v", got)
	}
}

func TestOTCOtherParty_ActorIsCounterparty(t *testing.T) {
	uid := uint64(7)
	cpType := model.OwnerClient
	cpID := uint64(8)
	o := &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &uid,
		CounterpartyOwnerType: &cpType, CounterpartyOwnerID: &cpID,
	}
	got := otcOtherParty(o, 8, "client")
	if got.OwnerType != "client" || got.OwnerID == nil || *got.OwnerID != 7 {
		t.Errorf("got %+v", got)
	}
}

func TestOTCOtherParty_NoCounterparty(t *testing.T) {
	uid := uint64(7)
	o := &model.OTCOffer{InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &uid}
	got := otcOtherParty(o, 7, "client")
	if (got != kafkamsg.OTCParty{}) {
		t.Errorf("expected zero OTCParty, got %+v", got)
	}
}

func TestActorToOwnerParty_Employee(t *testing.T) {
	tp, id := actorToOwnerParty(123, "employee")
	if tp != "bank" || id != nil {
		t.Errorf("got %s/%v want bank/nil", tp, id)
	}
}

func TestActorToOwnerParty_Bank(t *testing.T) {
	tp, id := actorToOwnerParty(0, "bank")
	if tp != "bank" || id != nil {
		t.Errorf("got %s/%v want bank/nil", tp, id)
	}
}

func TestActorToOwnerParty_Client(t *testing.T) {
	tp, id := actorToOwnerParty(99, "client")
	if tp != "client" || id == nil || *id != 99 {
		t.Errorf("got %s/%v want client/99", tp, id)
	}
}

// ---------------- assertSellerHasShares: nil holdings ----------------

func TestOTCOfferService_AssertSellerHasShares_NilLookup(t *testing.T) {
	svc := &OTCOfferService{}
	svc.holdings = nil
	uid := uint64(7)
	err := svc.assertSellerHasShares(model.OwnerClient, &uid, 42, decimal.NewFromInt(1))
	if err == nil || !errors.Is(err, err) { // sanity
		t.Fatalf("expected error: %v", err)
	}
}
