package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/handler"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// fakeReserver satisfies handler.HoldingReserver. Used by RecordOptionContract
// + recordOptionExercise tests to assert the seller-side share lock and the
// buyer-side credit calls.
type fakeReserver struct {
	reserveCalls       int
	reserveErr         error
	consumeCalls       int
	consumeErr         error
	creditBuyerCalls   int
	creditBuyerErr     error
	lastConsumeQty     int64
	lastReserveTicker  string
	lastConsumeContrID uint64
	// Cross-bank vote-time hold (Celina-5 share reservation).
	newTxReserveCalls int
	newTxReserveErr   error
	attachCalls       int
	attachErr         error
	lastAttachTxID    string
	lastAttachContrID uint64
	releaseTxCalls    int
	releaseTxErr      error
	lastReleaseTxID   string
}

func (f *fakeReserver) ReserveForPeerOptionContract(_ context.Context, _ model.OwnerType, _ *uint64, _, ticker string, _ uint64, qty int64) (*service.ReserveHoldingResult, error) {
	f.reserveCalls++
	f.lastReserveTicker = ticker
	if f.reserveErr != nil {
		return nil, f.reserveErr
	}
	return &service.ReserveHoldingResult{}, nil
}
func (f *fakeReserver) ReserveForCrossBankNewTx(_ context.Context, _ model.OwnerType, _ *uint64, _, ticker, _ string, qty int64) (*service.ReserveHoldingResult, error) {
	f.newTxReserveCalls++
	f.lastReserveTicker = ticker
	if f.newTxReserveErr != nil {
		return nil, f.newTxReserveErr
	}
	return &service.ReserveHoldingResult{ReservedQuantity: qty, AvailableQuantity: 0}, nil
}
func (f *fakeReserver) AttachCrossBankReservationToContract(_ context.Context, crossbankTxID string, peerOptionContractID uint64) error {
	f.attachCalls++
	f.lastAttachTxID = crossbankTxID
	f.lastAttachContrID = peerOptionContractID
	return f.attachErr
}
func (f *fakeReserver) ReleaseForCrossBankNewTx(_ context.Context, crossbankTxID string) (*service.ReleaseHoldingResult, error) {
	f.releaseTxCalls++
	f.lastReleaseTxID = crossbankTxID
	if f.releaseTxErr != nil {
		return nil, f.releaseTxErr
	}
	return &service.ReleaseHoldingResult{}, nil
}
func (f *fakeReserver) ConsumeForPeerOptionContract(_ context.Context, contractID uint64, qty int64) (*service.PartialSettleHoldingResult, error) {
	f.consumeCalls++
	f.lastConsumeContrID = contractID
	f.lastConsumeQty = qty
	if f.consumeErr != nil {
		return nil, f.consumeErr
	}
	return &service.PartialSettleHoldingResult{}, nil
}
func (f *fakeReserver) ExerciseBuyerCreditForPeerOption(_ context.Context, _ uint64, _ model.OwnerType, _ *uint64, _ string, _ int64, _ decimal.Decimal) error {
	f.creditBuyerCalls++
	return f.creditBuyerErr
}

// ---------------------------------------------------------------------------
// GetPublicStocks
// ---------------------------------------------------------------------------

// seedPeerOffer inserts an OTCOffer into the handler test DB, filling the
// required NOT-NULL columns with sane defaults so a caller need only set the
// discriminating fields (owner, direction, status, public/private, routing).
// db.Create runs BeforeCreate (stamps routing→Local) and BeforeSave (validates
// the owner pair), exactly as production inserts do.
func seedPeerOffer(t *testing.T, db *gorm.DB, o *model.OTCOffer) {
	t.Helper()
	if o.StockID == 0 {
		o.StockID = 1
	}
	if o.Quantity.IsZero() {
		o.Quantity = decimal.NewFromInt(1)
	}
	if o.LastModifiedByPrincipalType == "" {
		o.LastModifiedByPrincipalType = "client"
	}
	require.NoError(t, db.Create(o).Error)
}

// TestGetPublicStocks_ServesOptionOffers asserts the peer /public-stock catalog
// now publishes our OPEN, sell-initiated, public, non-private, LOCAL option
// offers (the optionable inventory peers negotiate options off) instead of the
// holdings table — one seller entry per (owner, ticker). The seller id uses the
// conformant SI-TX form (composePeerSellerID): a client offer → "client-<n>".
func TestGetPublicStocks_ServesOptionOffers(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t)
	uid := uint64(7)

	// The one offer that MUST be published.
	seedPeerOffer(t, db, &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &uid,
		Direction: model.OTCDirectionSellInitiated, Ticker: "OPK",
		Quantity: decimal.NewFromInt(75), Status: model.OTCOfferStatusOpen,
		Public: true,
	})

	// Exclusions — none of these may surface on /public-stock:
	//   buy_initiated  — poster is a BUYER (seller-centric discovery only)
	seedPeerOffer(t, db, &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &uid,
		Direction: model.OTCDirectionBuyInitiated, Ticker: "BUYT",
		Status: model.OTCOfferStatusOpen, Public: true,
	})
	//   private        — only visible to a named bank, never on /public-stock
	seedPeerOffer(t, db, &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &uid,
		Direction: model.OTCDirectionSellInitiated, Ticker: "PRIV",
		Status: model.OTCOfferStatusOpen, Public: true, Private: true,
	})
	//   remote (local=false) — a peer's row folded in; never re-published as ours
	remoteNative := "ext-remote-1"
	seedPeerOffer(t, db, &model.OTCOffer{
		RoutingNumber:      222, // != OwnRouting(111) ⇒ BeforeCreate stamps Local=false
		NativeID:           &remoteNative,
		InitiatorOwnerType: model.OwnerBank,
		Direction:          model.OTCDirectionSellInitiated, Ticker: "REMT",
		Status: model.OTCOfferStatusOpen, Public: true,
	})
	//   consumed       — terminal status, no longer accepting negotiations
	seedPeerOffer(t, db, &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &uid,
		Direction: model.OTCDirectionSellInitiated, Ticker: "CONS",
		Status: model.OTCOfferStatusConsumed, Public: true,
	})

	resp, err := h.GetPublicStocks(context.Background(), &stockpb.GetPublicStocksRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Stocks, 1)
	require.Equal(t, "OPK", resp.Stocks[0].Ticker)
	require.Equal(t, int64(75), resp.Stocks[0].Amount)
	require.Equal(t, "client-7", resp.Stocks[0].OwnerId.Id)
	require.Equal(t, int64(111), resp.Stocks[0].OwnerId.RoutingNumber)
}

// TestGetPublicStocks_SkipsNonConformantSeller asserts a bank offer with no
// acting employee (composePeerSellerID == "") is dropped rather than published
// with an empty / un-addressable seller id.
func TestGetPublicStocks_SkipsNonConformantSeller(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t)
	seedPeerOffer(t, db, &model.OTCOffer{
		InitiatorOwnerType: model.OwnerBank, // bank owner, no ActingEmployeeID ⇒ ""
		Direction:          model.OTCDirectionSellInitiated, Ticker: "NOID",
		Status: model.OTCOfferStatusOpen, Public: true,
	})
	resp, err := h.GetPublicStocks(context.Background(), &stockpb.GetPublicStocksRequest{})
	require.NoError(t, err)
	require.Empty(t, resp.Stocks)
}

// TestGetPublicStocks_ListErr asserts a list error from the offer reader surfaces
// as codes.Internal. Dropping the otc_offers table forces the underlying query
// to fail with the real repository.
func TestGetPublicStocks_ListErr(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t)
	require.NoError(t, db.Migrator().DropTable(&model.OTCOffer{}))
	_, err := h.GetPublicStocks(context.Background(), &stockpb.GetPublicStocksRequest{})
	require.Equal(t, codes.Internal, status.Code(err))
}

// ---------------------------------------------------------------------------
// CheckSellerCanDeliver
// ---------------------------------------------------------------------------

func TestPeerOTC_CheckSellerCanDeliver_OK(t *testing.T) {
	h, _, _, holdings := newPeerOtcHandler(t)
	uid := uint64(7)
	holdings.rows = []model.Holding{
		{OwnerType: model.OwnerClient, OwnerID: &uid, SecurityType: "stock", Ticker: "AAPL", Quantity: 100, ReservedQuantity: 10},
	}
	resp, err := h.CheckSellerCanDeliver(context.Background(), &stockpb.CheckSellerCanDeliverRequest{
		SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		Ticker:   "AAPL",
		Quantity: 50,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !resp.GetOk() {
		t.Errorf("expected ok=true")
	}
	if resp.GetAvailableQuantity() != 90 {
		t.Errorf("available=%d want 90", resp.GetAvailableQuantity())
	}
}

func TestPeerOTC_CheckSellerCanDeliver_Insufficient(t *testing.T) {
	h, _, _, holdings := newPeerOtcHandler(t)
	uid := uint64(7)
	holdings.rows = []model.Holding{
		{OwnerType: model.OwnerClient, OwnerID: &uid, SecurityType: "stock", Ticker: "AAPL", Quantity: 5, ReservedQuantity: 4},
	}
	resp, err := h.CheckSellerCanDeliver(context.Background(), &stockpb.CheckSellerCanDeliverRequest{
		SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		Ticker:   "AAPL",
		Quantity: 50,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetOk() {
		t.Errorf("expected ok=false")
	}
}

func TestPeerOTC_CheckSellerCanDeliver_HoldingMissing(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	resp, err := h.CheckSellerCanDeliver(context.Background(), &stockpb.CheckSellerCanDeliverRequest{
		SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		Ticker:   "AAPL",
		Quantity: 1,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetOk() {
		t.Errorf("expected ok=false for missing holding")
	}
	if resp.GetAvailableQuantity() != 0 {
		t.Errorf("available=%d", resp.GetAvailableQuantity())
	}
}

func TestPeerOTC_CheckSellerCanDeliver_BadInput(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	_, err := h.CheckSellerCanDeliver(context.Background(), &stockpb.CheckSellerCanDeliverRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestPeerOTC_CheckSellerCanDeliver_UnparseableSeller(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	resp, err := h.CheckSellerCanDeliver(context.Background(), &stockpb.CheckSellerCanDeliverRequest{
		SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "weird-7"},
		Ticker:   "AAPL",
		Quantity: 1,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetOk() {
		t.Errorf("expected ok=false for unparseable seller")
	}
}

func TestPeerOTC_CheckSellerCanDeliver_DBError(t *testing.T) {
	h, _, _, holdings := newPeerOtcHandler(t)
	holdings.err = errors.New("db blew up")
	_, err := h.CheckSellerCanDeliver(context.Background(), &stockpb.CheckSellerCanDeliverRequest{
		SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		Ticker:   "AAPL",
		Quantity: 1,
	})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// RecordOptionContract: accept (default) intent
// ---------------------------------------------------------------------------

func TestPeerOTC_RecordOptionContract_AcceptIntent(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	reserver := &fakeReserver{}
	h.SetHoldingReserver(reserver)

	optDesc := contractsitx.OptionDescription{
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-1"},
		Stock:          contractsitx.StockDescription{Ticker: "AAPL"},
		PricePerUnit:   contractsitx.MonetaryValue{Amount: contractsitx.DecimalNumber{Decimal: decimal.NewFromInt(100)}, Currency: "USD"},
		SettlementDate: "2026-12-31",
		Amount:         5,
	}
	optJSON, _ := json.Marshal(optDesc)

	resp, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-1",
		PostingIndex:          2,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-99"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		Direction:             contractsitx.DirectionDebit,
		OptionDescriptionJson: string(optJSON),
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.GetContractId() == 0 {
		t.Errorf("expected contract id")
	}
	// Spec-aligned COMMIT path: the shares were reserved at NEW_TX (keyed on
	// crossbank_tx_id), so COMMIT ATTACHES that hold to the minted contract
	// rather than reserving afresh.
	if reserver.attachCalls != 1 {
		t.Errorf("attach calls = %d want 1", reserver.attachCalls)
	}
	if reserver.lastAttachTxID != "tx-1" || reserver.lastAttachContrID != resp.GetContractId() {
		t.Errorf("attach got (tx=%s, contract=%d) want (tx-1, %d)", reserver.lastAttachTxID, reserver.lastAttachContrID, resp.GetContractId())
	}
	if reserver.reserveCalls != 0 {
		t.Errorf("legacy reserve must not be called when attach succeeds, got %d", reserver.reserveCalls)
	}
}

func TestPeerOTC_RecordOptionContract_AcceptIntent_CreditDirection_NoReserveCall(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	reserver := &fakeReserver{}
	h.SetHoldingReserver(reserver)

	optDesc := contractsitx.OptionDescription{
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-2"},
		Stock:          contractsitx.StockDescription{Ticker: "MSFT"},
		PricePerUnit:   contractsitx.MonetaryValue{Amount: contractsitx.DecimalNumber{Decimal: decimal.NewFromInt(50)}, Currency: "USD"},
		SettlementDate: "2026-12-31",
		Amount:         1,
	}
	optJSON, _ := json.Marshal(optDesc)
	_, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-2",
		PostingIndex:          0,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-99"},
		Direction:             contractsitx.DirectionCredit,
		OptionDescriptionJson: string(optJSON),
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if reserver.reserveCalls != 0 {
		t.Errorf("expected no reserve call on CREDIT direction, got %d", reserver.reserveCalls)
	}
}

// TestPeerOTC_RecordOptionContract_ReserveFails_ReturnsError verifies the
// LEGACY fallback path: when there is NO vote-time hold to attach (attach
// returns NotFound — e.g. a NEW_TX from before this change) COMMIT falls back
// to ReserveForPeerOptionContract, and if THAT fails the RPC returns an error
// instead of silently leaving an "active" contract with no holding reservation.
func TestPeerOTC_RecordOptionContract_ReserveFails_ReturnsError(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	reserver := &fakeReserver{
		attachErr:  status.Error(codes.NotFound, "no vote-time hold"), // force fallback
		reserveErr: status.Error(codes.FailedPrecondition, "shares traded away"),
	}
	h.SetHoldingReserver(reserver)

	optDesc := contractsitx.OptionDescription{
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-rf"},
		Stock:          contractsitx.StockDescription{Ticker: "AAPL"},
		PricePerUnit:   contractsitx.MonetaryValue{Amount: contractsitx.DecimalNumber{Decimal: decimal.NewFromInt(100)}, Currency: "USD"},
		SettlementDate: "2026-12-31",
		Amount:         5,
	}
	optJSON, _ := json.Marshal(optDesc)
	_, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-reserve-fail",
		PostingIndex:          2,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-99"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		Direction:             contractsitx.DirectionDebit,
		OptionDescriptionJson: string(optJSON),
	})
	if err == nil {
		t.Fatal("expected error when seller-side reservation fails; silent success leaves an unlocked active contract")
	}
	if reserver.reserveCalls != 1 {
		t.Errorf("reserve calls = %d want 1", reserver.reserveCalls)
	}
}

// TestPeerOTC_RecordOptionContract_UnparseableSeller_ReturnsError verifies
// that a DEBIT-side contract whose seller_id cannot be parsed (so no share
// lock can be applied) is reported as an error rather than silently leaving
// an active, unlockable contract (Bug 2, parse branch).
func TestPeerOTC_RecordOptionContract_UnparseableSeller_ReturnsError(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	reserver := &fakeReserver{}
	h.SetHoldingReserver(reserver)

	optDesc := contractsitx.OptionDescription{
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-bad"},
		Stock:          contractsitx.StockDescription{Ticker: "AAPL"},
		PricePerUnit:   contractsitx.MonetaryValue{Amount: contractsitx.DecimalNumber{Decimal: decimal.NewFromInt(100)}, Currency: "USD"},
		SettlementDate: "2026-12-31",
		Amount:         5,
	}
	optJSON, _ := json.Marshal(optDesc)
	_, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-bad-seller",
		PostingIndex:          2,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-99"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "weird-7"},
		Direction:             contractsitx.DirectionDebit,
		OptionDescriptionJson: string(optJSON),
	})
	if err == nil {
		t.Fatal("expected error when seller_id is unparseable; cannot lock shares")
	}
	if reserver.reserveCalls != 0 {
		t.Errorf("reserve should not be called for unparseable seller, got %d", reserver.reserveCalls)
	}
}

func TestPeerOTC_RecordOptionContract_BadDirection(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	_, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-1",
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "x"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		Direction:             "WEIRD",
		OptionDescriptionJson: `{"ticker":"AAPL"}`,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestPeerOTC_RecordOptionContract_MissingFields(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	_, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestPeerOTC_RecordOptionContract_MissingBuyerSeller(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	_, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx",
		OptionDescriptionJson: `{}`,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestPeerOTC_RecordOptionContract_BadJSON(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	_, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx",
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-1"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		Direction:             contractsitx.DirectionDebit,
		OptionDescriptionJson: "not-json",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// RecordOptionContract: exercise intent
// ---------------------------------------------------------------------------

func TestPeerOTC_RecordOptionContract_ExerciseIntent_DebitConsumesReservation(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	reserver := &fakeReserver{}
	h.SetHoldingReserver(reserver)

	// Step 1: record an active contract on DEBIT direction.
	optDesc := contractsitx.OptionDescription{
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-x"},
		Stock:          contractsitx.StockDescription{Ticker: "AAPL"},
		PricePerUnit:   contractsitx.MonetaryValue{Amount: contractsitx.DecimalNumber{Decimal: decimal.NewFromInt(100)}, Currency: "USD"},
		SettlementDate: "2026-12-31",
		Amount:         10,
	}
	optJSON, _ := json.Marshal(optDesc)
	_, _ = h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-active",
		PostingIndex:          0,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-99"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		Direction:             contractsitx.DirectionDebit,
		OptionDescriptionJson: string(optJSON),
	})

	// Step 2: exercise with intent="exercise", DEBIT direction.
	_, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-exercise",
		Intent:                "exercise",
		Direction:             contractsitx.DirectionDebit,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-99"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		OptionDescriptionJson: string(optJSON),
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if reserver.consumeCalls != 1 {
		t.Errorf("consume calls = %d want 1", reserver.consumeCalls)
	}
}

func TestPeerOTC_RecordOptionContract_ExerciseIntent_CreditCreditsBuyer(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	reserver := &fakeReserver{}
	h.SetHoldingReserver(reserver)

	// Step 1: record active contract CREDIT direction.
	optDesc := contractsitx.OptionDescription{
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-c"},
		Stock:          contractsitx.StockDescription{Ticker: "AAPL"},
		PricePerUnit:   contractsitx.MonetaryValue{Amount: contractsitx.DecimalNumber{Decimal: decimal.NewFromInt(100)}, Currency: "USD"},
		SettlementDate: "2026-12-31",
		Amount:         7,
	}
	optJSON, _ := json.Marshal(optDesc)
	_, _ = h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-c-active",
		PostingIndex:          0,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-99"},
		Direction:             contractsitx.DirectionCredit,
		OptionDescriptionJson: string(optJSON),
	})

	// Step 2: exercise.
	_, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-c-ex",
		Intent:                "exercise",
		Direction:             contractsitx.DirectionCredit,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-99"},
		OptionDescriptionJson: string(optJSON),
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if reserver.creditBuyerCalls != 1 {
		t.Errorf("expected credit buyer call, got %d", reserver.creditBuyerCalls)
	}
}

func TestPeerOTC_RecordOptionContract_ExerciseIntent_NoActiveContract(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	reserver := &fakeReserver{}
	h.SetHoldingReserver(reserver)
	optDesc := contractsitx.OptionDescription{
		NegotiationID: contractsitx.ForeignBankId{RoutingNumber: 222, ID: "missing"},
	}
	optJSON, _ := json.Marshal(optDesc)
	_, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-1",
		Intent:                "exercise",
		Direction:             contractsitx.DirectionDebit,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "x"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		OptionDescriptionJson: string(optJSON),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", err)
	}
}

// TestPeerOTC_RecordOptionContract_ExerciseIntent_BuyerCreditFails_ReturnsError
// verifies that when the buyer-side share credit fails during exercise, the
// RPC returns an error and does NOT mark the contract exercised. The buyer has
// paid the strike (money moved cross-bank); silently swallowing the credit
// failure and marking exercised would leave the buyer paid-but-not-delivered
// with no retry (exercise-time analog of Bug 2).
func TestPeerOTC_RecordOptionContract_ExerciseIntent_BuyerCreditFails_ReturnsError(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	reserver := &fakeReserver{creditBuyerErr: errors.New("holding write failed")}
	h.SetHoldingReserver(reserver)

	optDesc := contractsitx.OptionDescription{
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-credit-fail"},
		Stock:          contractsitx.StockDescription{Ticker: "AAPL"},
		PricePerUnit:   contractsitx.MonetaryValue{Amount: contractsitx.DecimalNumber{Decimal: decimal.NewFromInt(150)}, Currency: "USD"},
		SettlementDate: "2026-12-31",
		Amount:         10,
	}
	optJSON, _ := json.Marshal(optDesc)
	// Seed an active CREDIT-direction contract (this bank holds the buyer).
	if _, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-cf-seed",
		PostingIndex:          0,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-99"},
		Direction:             contractsitx.DirectionCredit,
		OptionDescriptionJson: string(optJSON),
	}); err != nil {
		t.Fatalf("seed accept: %v", err)
	}

	_, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-cf-exercise",
		PostingIndex:          0,
		Intent:                "exercise",
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-99"},
		Direction:             contractsitx.DirectionCredit,
		OptionDescriptionJson: string(optJSON),
	})
	if err == nil {
		t.Fatal("expected error when buyer holding credit fails; buyer would be paid but undelivered")
	}
}

func TestPeerOTC_RecordOptionContract_ExerciseIntent_NoReserverWired(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	// holdingReserver intentionally not wired.
	optDesc := contractsitx.OptionDescription{
		NegotiationID: contractsitx.ForeignBankId{RoutingNumber: 222, ID: "any"},
	}
	optJSON, _ := json.Marshal(optDesc)
	_, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx",
		Intent:                "exercise",
		Direction:             contractsitx.DirectionDebit,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "x"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		OptionDescriptionJson: string(optJSON),
	})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("expected Unimplemented, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// InitiateOptionExercise
// ---------------------------------------------------------------------------

func TestPeerOTC_InitiateOptionExercise_HappyPath(t *testing.T) {
	h, _, peerTx, _ := newPeerOtcHandler(t)
	reserver := &fakeReserver{}
	h.SetHoldingReserver(reserver)

	// Seed an active CREDIT-direction contract (this bank holds the buyer).
	optDesc := contractsitx.OptionDescription{
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-init"},
		Stock:          contractsitx.StockDescription{Ticker: "AAPL"},
		PricePerUnit:   contractsitx.MonetaryValue{Amount: contractsitx.DecimalNumber{Decimal: decimal.NewFromInt(150)}, Currency: "USD"},
		SettlementDate: "2026-12-31",
		Amount:         10,
	}
	optJSON, _ := json.Marshal(optDesc)
	resp, _ := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-init",
		PostingIndex:          0,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-99"},
		Direction:             contractsitx.DirectionCredit,
		OptionDescriptionJson: string(optJSON),
	})

	out, err := h.InitiateOptionExercise(context.Background(), &stockpb.InitiateOptionExerciseRequest{
		PeerOptionContractId: resp.GetContractId(),
		BuyerAccountNumber:   "BUYER-ACCT-1",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if peerTx.gotReq == nil {
		t.Fatal("InitiateOutboundTxWithPostings not called")
	}
	if peerTx.gotReq.GetTxKind() != "otc-exercise" {
		t.Errorf("tx_kind=%s", peerTx.gotReq.GetTxKind())
	}
	if len(peerTx.gotReq.GetPostings()) != 4 {
		t.Errorf("expected 4 postings, got %d", len(peerTx.gotReq.GetPostings()))
	}
	if out.GetTransactionId() == "" {
		t.Errorf("expected tx id")
	}
}

// seedActiveBuyerContract records an active CREDIT-direction contract and
// returns its id (helper for the exercise concurrency tests).
func seedActiveBuyerContract(t *testing.T, h *handler.PeerOTCGRPCHandler, neg string) uint64 {
	t.Helper()
	optDesc := contractsitx.OptionDescription{
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: neg},
		Stock:          contractsitx.StockDescription{Ticker: "AAPL"},
		PricePerUnit:   contractsitx.MonetaryValue{Amount: contractsitx.DecimalNumber{Decimal: decimal.NewFromInt(150)}, Currency: "USD"},
		SettlementDate: "2026-12-31",
		Amount:         10,
	}
	optJSON, _ := json.Marshal(optDesc)
	resp, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-" + neg,
		PostingIndex:          0,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-99"},
		Direction:             contractsitx.DirectionCredit,
		OptionDescriptionJson: string(optJSON),
	})
	if err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	return resp.GetContractId()
}

// TestPeerOTC_InitiateOptionExercise_SecondExerciseRejected verifies the
// concurrency guard: once a contract is claimed for exercise (active →
// exercising), a second exercise attempt is rejected instead of dispatching a
// second strike-money payment (the double-charge bug).
func TestPeerOTC_InitiateOptionExercise_SecondExerciseRejected(t *testing.T) {
	h, _, peerTx, _ := newPeerOtcHandler(t)
	h.SetHoldingReserver(&fakeReserver{})
	cid := seedActiveBuyerContract(t, h, "neg-concurrent")

	if _, err := h.InitiateOptionExercise(context.Background(), &stockpb.InitiateOptionExerciseRequest{
		PeerOptionContractId: cid, BuyerAccountNumber: "BUYER-ACCT-1",
	}); err != nil {
		t.Fatalf("first exercise: %v", err)
	}
	dispatches := 0
	if peerTx.gotReq != nil {
		dispatches = 1
	}
	// Second attempt must be rejected (contract now "exercising"), NOT dispatched.
	peerTx.gotReq = nil
	_, err := h.InitiateOptionExercise(context.Background(), &stockpb.InitiateOptionExerciseRequest{
		PeerOptionContractId: cid, BuyerAccountNumber: "BUYER-ACCT-1",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition on second exercise, got %v", err)
	}
	if peerTx.gotReq != nil {
		t.Errorf("second exercise must NOT dispatch a strike-money TX (double charge)")
	}
	if dispatches != 1 {
		t.Errorf("expected exactly 1 dispatch from the first exercise")
	}
}

// TestPeerOTC_InitiateOptionExercise_DispatchFailureRevertsClaim verifies that a
// synchronous dispatch failure (e.g. buyer can't afford the strike) releases the
// exercise claim (exercising → active), preserves the gRPC code (FailedPrecondition,
// not Internal), and leaves the contract retryable.
func TestPeerOTC_InitiateOptionExercise_DispatchFailureRevertsClaim(t *testing.T) {
	h, _, peerTx, _ := newPeerOtcHandler(t)
	h.SetHoldingReserver(&fakeReserver{})
	cid := seedActiveBuyerContract(t, h, "neg-revert")

	peerTx.err = status.Error(codes.FailedPrecondition, "local reserve failed: INSUFFICIENT_ASSET")
	_, err := h.InitiateOptionExercise(context.Background(), &stockpb.InitiateOptionExerciseRequest{
		PeerOptionContractId: cid, BuyerAccountNumber: "BUYER-ACCT-1",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition (code preserved), got %v", err)
	}
	// Claim reverted → a retry (now funded) must succeed.
	peerTx.err = nil
	peerTx.resp = &transactionpb.SiTxInitiateResponse{TransactionId: "tx-retry", Status: "pending"}
	if _, rerr := h.InitiateOptionExercise(context.Background(), &stockpb.InitiateOptionExerciseRequest{
		PeerOptionContractId: cid, BuyerAccountNumber: "BUYER-ACCT-1",
	}); rerr != nil {
		t.Errorf("retry after revert should succeed, got %v", rerr)
	}
}

func TestPeerOTC_InitiateOptionExercise_NotFound(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	_, err := h.InitiateOptionExercise(context.Background(), &stockpb.InitiateOptionExerciseRequest{
		PeerOptionContractId: 9999,
		BuyerAccountNumber:   "BUYER-ACCT-1",
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestPeerOTC_InitiateOptionExercise_BadInput(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	_, err := h.InitiateOptionExercise(context.Background(), &stockpb.InitiateOptionExerciseRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestPeerOTC_InitiateOptionExercise_WrongDirection(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	reserver := &fakeReserver{}
	h.SetHoldingReserver(reserver)

	// Seed an active DEBIT-direction contract (this bank does NOT hold the buyer).
	optDesc := contractsitx.OptionDescription{
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-d"},
		Stock:          contractsitx.StockDescription{Ticker: "AAPL"},
		PricePerUnit:   contractsitx.MonetaryValue{Amount: contractsitx.DecimalNumber{Decimal: decimal.NewFromInt(150)}, Currency: "USD"},
		SettlementDate: "2026-12-31",
		Amount:         10,
	}
	optJSON, _ := json.Marshal(optDesc)
	resp, _ := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-debit",
		PostingIndex:          0,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-99"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		Direction:             contractsitx.DirectionDebit,
		OptionDescriptionJson: string(optJSON),
	})
	_, err := h.InitiateOptionExercise(context.Background(), &stockpb.InitiateOptionExerciseRequest{
		PeerOptionContractId: resp.GetContractId(),
		BuyerAccountNumber:   "x",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// AcceptNegotiation negative paths to extend coverage
// ---------------------------------------------------------------------------

func TestPeerOTC_AcceptNegotiation_NotFound(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	_, err := h.AcceptNegotiation(context.Background(), &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "missing"},
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestPeerOTC_AcceptNegotiation_DispatchError(t *testing.T) {
	h, db, peerTx, _ := newPeerOtcHandler(t)
	// Seed a legit "WE last proposed" mirror so the authoritative accept guard
	// passes and we exercise the dispatch-error path. (The inbound create/counter
	// paths can only stamp lastModifiedBy = the peer, so a local-last-proposer
	// state is produced by our own outbound write, simulated here as a seed.)
	offer := contractsitx.OtcOffer{
		Ticker: "AAPL", Amount: 1,
		PricePerStock:   decimal.RequireFromString("1"),
		Currency:        "USD",
		Premium:         decimal.RequireFromString("1"),
		PremiumCurrency: "USD",
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: 111, ID: "client-9"},
	}
	offerJSON, _ := json.Marshal(offer)
	if err := repository.NewOTCNegotiationRepository(db).UpsertRemoteNeg(buildRemoteNegForTest(
		222, "neg-dispatch-err", offer, string(offerJSON),
		222, "client-7", 111, "client-9",
	)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	peerTx.err = errors.New("peer down")
	_, err := h.AcceptNegotiation(context.Background(), &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-dispatch-err"},
	})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal on dispatch fail, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// CreateNegotiation negative paths
// ---------------------------------------------------------------------------

func TestPeerOTC_CreateNegotiation_BadInput(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	_, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		// missing offer/buyer/seller
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestPeerOTC_UpdateNegotiation_BadInput(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	_, err := h.UpdateNegotiation(context.Background(), &stockpb.UpdateNegotiationRequest{
		PeerBankCode: "222",
		// missing offer/negotiation_id
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestPeerOTC_DeleteNegotiation_BadInput(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	_, err := h.DeleteNegotiation(context.Background(), &stockpb.DeleteNegotiationRequest{
		PeerBankCode: "222",
		// missing negotiation_id
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestPeerOTC_AcceptNegotiation_BadInput(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	_, err := h.AcceptNegotiation(context.Background(), &stockpb.AcceptNegotiationRequest{
		PeerBankCode: "222",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestPeerOTC_GetNegotiation_BadInput(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	_, err := h.GetNegotiation(context.Background(), &stockpb.GetNegotiationRequest{
		PeerBankCode: "222",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// SetHoldingReserver / wiring smoke test (exercises the setter line).
func TestPeerOTC_SetHoldingReserver(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	r := &fakeReserver{}
	h.SetHoldingReserver(r)
	// no panic, that's the whole assertion. Following call would error
	// because of missing fields, but tests the setter line.
	_, _ = h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{})
}

// ---------------------------------------------------------------------------
// TestInitiateOptionExercise_SpecPseudoAccountForm
// ---------------------------------------------------------------------------

// TestInitiateOptionExercise_SpecPseudoAccountForm verifies that the exercise
// builder emits the spec pseudo-account form: MONAS (strike) from the buyer
// account to the OPTION pseudo-account, then STOCK from the OPTION
// pseudo-account to the buyer's PERSON record.
//
//	leg0: buyer ACCOUNT  --MONAS RSD DEBIT--> (pays strike)
//	leg1: OPTION neg-1   --MONAS RSD CREDIT-> (seller bank credits seller)
//	leg2: OPTION neg-1   --STOCK WMT DEBIT--> (seller bank releases shares)
//	leg3: buyer PERSON   --STOCK WMT CREDIT-> (buyer bank credits holding)
//
// 500 = StrikePrice(50) × Quantity(10).
func TestInitiateOptionExercise_SpecPseudoAccountForm(t *testing.T) {
	h, db, peerTx, _ := newPeerOtcHandler(t) // ownRouting = 111

	// Seed an active CREDIT-direction contract directly so we can control
	// every field value (Ticker, StrikePrice, Quantity, NegotiationID, …)
	// without going through the RecordOptionContract / OptionDescription
	// JSON path.
	// SP-2a: REMOTE buyer-side (CREDIT) contract. We host the buyer (111); the
	// seller's bank (222) is the counterparty, so routing_number=222.
	if err := db.Create(seedRemoteContractRow(
		222, "seed:spec-1", 0, contractsitx.DirectionCredit, 111, "neg-1",
		111, "client-1", 222, "seller-1",
		"WMT", 10, decimal.NewFromInt(50), "RSD", "2028-01-01", "active",
	)).Error; err != nil {
		t.Fatalf("seed contract: %v", err)
	}

	// Retrieve the auto-assigned ID.
	var contract model.OptionContract
	if err := db.Where("remote_negotiation_native_id = ?", "neg-1").First(&contract).Error; err != nil {
		t.Fatalf("load seeded contract: %v", err)
	}

	peerTx.resp = &transactionpb.SiTxInitiateResponse{TransactionId: "tx-spec-1", Status: "initiated"}

	_, err := h.InitiateOptionExercise(context.Background(), &stockpb.InitiateOptionExerciseRequest{
		PeerOptionContractId: contract.ID,
		BuyerAccountNumber:   "111000117810858011",
	})
	if err != nil {
		t.Fatalf("InitiateOptionExercise: %v", err)
	}

	if peerTx.gotReq == nil {
		t.Fatal("InitiateOutboundTxWithPostings was not called")
	}

	if peerTx.gotReq.GetTxKind() != "otc-exercise" {
		t.Errorf("tx_kind: want otc-exercise, got %q", peerTx.gotReq.GetTxKind())
	}
	if peerTx.gotReq.GetPeerBankCode() != "222" {
		t.Errorf("peer_bank_code: want 222 (seller routing), got %q", peerTx.gotReq.GetPeerBankCode())
	}

	postings := peerTx.gotReq.GetPostings()
	if got := len(postings); got != 4 {
		t.Fatalf("expected 4 postings, got %d", got)
	}

	// leg0: buyer ACCOUNT pays strike MONAS
	p0 := postings[0]
	if p0.GetRoutingNumber() != 111 {
		t.Errorf("leg0 routing: want 111, got %d", p0.GetRoutingNumber())
	}
	if p0.GetAccountType() != "ACCOUNT" {
		t.Errorf("leg0 account_type: want ACCOUNT, got %q", p0.GetAccountType())
	}
	if p0.GetAccountId() != "111000117810858011" {
		t.Errorf("leg0 account_id: want 111000117810858011, got %q", p0.GetAccountId())
	}
	if p0.GetAssetType() != "MONAS" {
		t.Errorf("leg0 asset_type: want MONAS, got %q", p0.GetAssetType())
	}
	if p0.GetAssetId() != "RSD" {
		t.Errorf("leg0 asset_id: want RSD, got %q", p0.GetAssetId())
	}
	if p0.GetAmount() != "500" {
		t.Errorf("leg0 amount: want 500, got %q", p0.GetAmount())
	}
	if p0.GetDirection() != "DEBIT" {
		t.Errorf("leg0 direction: want DEBIT, got %q", p0.GetDirection())
	}

	// leg1: OPTION pseudo-account receives strike MONAS
	p1 := postings[1]
	if p1.GetRoutingNumber() != 111 {
		t.Errorf("leg1 routing: want 111, got %d", p1.GetRoutingNumber())
	}
	if p1.GetAccountType() != "OPTION" {
		t.Errorf("leg1 account_type: want OPTION, got %q", p1.GetAccountType())
	}
	if p1.GetAccountId() != "neg-1" {
		t.Errorf("leg1 account_id: want neg-1, got %q", p1.GetAccountId())
	}
	if p1.GetAssetType() != "MONAS" {
		t.Errorf("leg1 asset_type: want MONAS, got %q", p1.GetAssetType())
	}
	if p1.GetAssetId() != "RSD" {
		t.Errorf("leg1 asset_id: want RSD, got %q", p1.GetAssetId())
	}
	if p1.GetAmount() != "500" {
		t.Errorf("leg1 amount: want 500, got %q", p1.GetAmount())
	}
	if p1.GetDirection() != "CREDIT" {
		t.Errorf("leg1 direction: want CREDIT, got %q", p1.GetDirection())
	}

	// leg2: OPTION pseudo-account releases STOCK (shares leave)
	p2 := postings[2]
	if p2.GetRoutingNumber() != 111 {
		t.Errorf("leg2 routing: want 111, got %d", p2.GetRoutingNumber())
	}
	if p2.GetAccountType() != "OPTION" {
		t.Errorf("leg2 account_type: want OPTION, got %q", p2.GetAccountType())
	}
	if p2.GetAccountId() != "neg-1" {
		t.Errorf("leg2 account_id: want neg-1, got %q", p2.GetAccountId())
	}
	if p2.GetAssetType() != "STOCK" {
		t.Errorf("leg2 asset_type: want STOCK, got %q", p2.GetAssetType())
	}
	if p2.GetAssetId() != "WMT" {
		t.Errorf("leg2 asset_id: want WMT, got %q", p2.GetAssetId())
	}
	if p2.GetAmount() != "10" {
		t.Errorf("leg2 amount: want 10, got %q", p2.GetAmount())
	}
	if p2.GetDirection() != "DEBIT" {
		t.Errorf("leg2 direction: want DEBIT, got %q", p2.GetDirection())
	}

	// leg3: buyer PERSON receives STOCK
	p3 := postings[3]
	if p3.GetRoutingNumber() != 111 {
		t.Errorf("leg3 routing: want 111, got %d", p3.GetRoutingNumber())
	}
	if p3.GetAccountType() != "PERSON" {
		t.Errorf("leg3 account_type: want PERSON, got %q", p3.GetAccountType())
	}
	if p3.GetAccountId() != "client-1" {
		t.Errorf("leg3 account_id: want client-1, got %q", p3.GetAccountId())
	}
	if p3.GetAssetType() != "STOCK" {
		t.Errorf("leg3 asset_type: want STOCK, got %q", p3.GetAssetType())
	}
	if p3.GetAssetId() != "WMT" {
		t.Errorf("leg3 asset_id: want WMT, got %q", p3.GetAssetId())
	}
	if p3.GetAmount() != "10" {
		t.Errorf("leg3 amount: want 10, got %q", p3.GetAmount())
	}
	if p3.GetDirection() != "CREDIT" {
		t.Errorf("leg3 direction: want CREDIT, got %q", p3.GetDirection())
	}

	// Negative: no posting may carry AssetType OPTION.
	for i, p := range postings {
		if p.GetAssetType() == "OPTION" {
			t.Errorf("posting %d carries AssetType OPTION — spec pseudo-account form must not use OPTION asset markers", i)
		}
	}
}
