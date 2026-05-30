package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
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

func TestPeerOTC_GetPublicStocks_HappyPath(t *testing.T) {
	h, _, _, holdings := newPeerOtcHandler(t)
	uid := uint64(7)
	holdings.rows = []model.Holding{
		{OwnerType: model.OwnerClient, OwnerID: &uid, Ticker: "AAPL", PublicQuantity: 5},
		{OwnerType: model.OwnerBank, OwnerID: nil, Ticker: "MSFT", PublicQuantity: 3},
	}
	resp, err := h.GetPublicStocks(context.Background(), &stockpb.GetPublicStocksRequest{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetStocks()) != 2 {
		t.Fatalf("expected 2 stocks, got %d", len(resp.GetStocks()))
	}
	first := resp.GetStocks()[0]
	if first.GetOwnerId().GetId() != "7" {
		t.Errorf("first owner id = %q want 7", first.GetOwnerId().GetId())
	}
	second := resp.GetStocks()[1]
	if second.GetOwnerId().GetId() != "0" {
		t.Errorf("bank-owned should map to 0, got %q", second.GetOwnerId().GetId())
	}
}

func TestPeerOTC_GetPublicStocks_ListErr(t *testing.T) {
	h, _, _, holdings := newPeerOtcHandler(t)
	holdings.err = errors.New("db down")
	_, err := h.GetPublicStocks(context.Background(), &stockpb.GetPublicStocksRequest{})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", err)
	}
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
		Ticker:         "AAPL",
		Amount:         5,
		StrikePrice:    decimal.NewFromInt(100),
		Currency:       "USD",
		SettlementDate: "2026-12-31",
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-1"},
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
		Ticker: "MSFT", Amount: 1, StrikePrice: decimal.NewFromInt(50), Currency: "USD",
		SettlementDate: "2026-12-31",
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-2"},
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
		Ticker: "AAPL", Amount: 5, StrikePrice: decimal.NewFromInt(100), Currency: "USD",
		SettlementDate: "2026-12-31",
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-rf"},
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
		Ticker: "AAPL", Amount: 5, StrikePrice: decimal.NewFromInt(100), Currency: "USD",
		SettlementDate: "2026-12-31",
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-bad"},
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
		Ticker: "AAPL", Amount: 10, StrikePrice: decimal.NewFromInt(100), Currency: "USD",
		SettlementDate: "2026-12-31",
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-x"},
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
		Ticker: "AAPL", Amount: 7, StrikePrice: decimal.NewFromInt(100), Currency: "USD",
		SettlementDate: "2026-12-31",
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-c"},
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
		Ticker: "AAPL", Amount: 10, StrikePrice: decimal.NewFromInt(150), Currency: "USD",
		SettlementDate: "2026-12-31",
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-credit-fail"},
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
		Ticker: "AAPL", Amount: 10, StrikePrice: decimal.NewFromInt(150), Currency: "USD",
		SettlementDate: "2026-12-31",
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-init"},
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
		Ticker: "AAPL", Amount: 10, StrikePrice: decimal.NewFromInt(150), Currency: "USD",
		SettlementDate: "2026-12-31",
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-d"},
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
	h, _, peerTx, _ := newPeerOtcHandler(t)
	createResp, _ := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		Offer: &stockpb.PeerOtcOffer{
			Ticker: "AAPL", Amount: 1, PricePerStock: "1", Premium: "1",
			Currency: "USD", PremiumCurrency: "USD",
		},
		BuyerId:  &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "b"},
		SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "s"},
	})
	peerTx.err = errors.New("peer down")
	_, err := h.AcceptNegotiation(context.Background(), &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: createResp.GetNegotiationId(),
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
