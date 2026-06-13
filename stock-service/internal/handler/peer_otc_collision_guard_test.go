package handler_test

// Collision guard tests for SP-2a Task 7 Part B.
//
// Part B: ingestion collision guards — a peer must never write a row that
// claims OUR routing (routing_number == OwnRouting()), which would make the
// row look LOCAL and corrupt the local-vs-remote invariant.
//
// These tests cover:
//   - RecordOptionContract guard (own-counterparty-routing rejection)
//   - CreateNegotiation guard (peer_bank_code == own routing rejected)
//
// The otccache guard is tested in internal/otccache/option_cache_test.go.

import (
	"context"
	"encoding/json"
	"testing"

	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestPeerOTC_RecordOptionContract_OwnCounterpartyRouting_Rejected verifies
// that RecordOptionContract rejects a payload where the derived counterparty
// routing equals this bank's own routing. Such a payload would stamp the row
// with routing_number=OwnRouting(), making it indistinguishable from a local
// contract row and leaking into local money paths.
//
// Scenario: direction=DEBIT, buyer routing=111 (this bank), seller routing=111
// (also this bank) → both sides are local → counterparty routing = buyer routing
// = 111 = OwnRouting() → must be rejected.
func TestPeerOTC_RecordOptionContract_OwnCounterpartyRouting_Rejected(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t) // ownRouting = 111

	optDesc := contractsitx.OptionDescription{
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 111, ID: "neg-own"},
		Stock:          contractsitx.StockDescription{Ticker: "AAPL"},
		PricePerUnit:   contractsitx.MonetaryValue{Amount: contractsitx.DecimalNumber{Decimal: decimal.NewFromInt(100)}, Currency: "USD"},
		SettlementDate: "2026-12-31",
		Amount:         5,
	}
	optJSON, _ := json.Marshal(optDesc)

	// DEBIT direction: counterparty = buyer routing = 111 = OwnRouting().
	_, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-own-rout",
		PostingIndex:          0,
		Direction:             contractsitx.DirectionDebit,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-10"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		OptionDescriptionJson: string(optJSON),
	})
	if err == nil {
		t.Fatal("expected error: counterparty routing == OwnRouting() must be rejected")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// TestPeerOTC_RecordOptionContract_OwnCounterpartyRouting_CreditBothOwn_Rejected
// verifies the CREDIT direction variant where both parties are on this bank.
// For CREDIT direction, remoteContractCounterpartyRouting() returns the SELLER
// routing (the bank that sold the option). With buyer=111 AND seller=111 the
// seller routing == OwnRouting() (111) → the row would look local → REJECTED.
func TestPeerOTC_RecordOptionContract_OwnCounterpartyRouting_CreditBothOwn_Rejected(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t) // ownRouting = 111

	optDesc := contractsitx.OptionDescription{
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 111, ID: "neg-own-credit"},
		Stock:          contractsitx.StockDescription{Ticker: "MSFT"},
		PricePerUnit:   contractsitx.MonetaryValue{Amount: contractsitx.DecimalNumber{Decimal: decimal.NewFromInt(50)}, Currency: "USD"},
		SettlementDate: "2026-12-31",
		Amount:         1,
	}
	optJSON, _ := json.Marshal(optDesc)

	// CREDIT direction: counterparty = seller routing = 111 = OwnRouting().
	_, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-own-credit",
		PostingIndex:          0,
		Direction:             contractsitx.DirectionCredit,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-5"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-9"},
		OptionDescriptionJson: string(optJSON),
	})
	if err == nil {
		t.Fatal("expected error: counterparty routing == OwnRouting() must be rejected (CREDIT direction, both parties on own bank)")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// CreateNegotiation collision guard
// ---------------------------------------------------------------------------

// TestPeerOTC_CreateNegotiation_OwnRouting_Rejected verifies that
// CreateNegotiation rejects a payload whose peer_bank_code resolves to this
// bank's own routing number. The unified table keys remote negotiation rows on
// routing_number=<peer>; if the peer routing equals OwnRouting() the row would
// alias a local chain and corrupt the local-vs-remote invariant.
//
// Guard location: peer_otc_grpc_handler.go CreateNegotiation(), after
// peer_bank_code is parsed and before the UpsertRemoteNeg call.
func TestPeerOTC_CreateNegotiation_OwnRouting_Rejected(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t) // ownRouting = 111

	// Provide a minimal valid offer, buyer_id, and seller_id so the handler
	// reaches the collision guard (nil-arg check comes first). The guard keys
	// on peer_bank_code, so "111" (== OwnRouting()) must trigger rejection
	// regardless of the buyer/seller values.
	_, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: "111", // == OwnRouting() — must be rejected
		Offer:        &stockpb.PeerOtcOffer{Ticker: "AAPL", Amount: 1},
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-20"},
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
	})
	if err == nil {
		t.Fatal("expected error: peer_bank_code colliding with own routing must be rejected")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// RecordOptionContract happy-path sanity check
// ---------------------------------------------------------------------------

// TestPeerOTC_RecordOptionContract_LegitCrossBank_Allowed verifies that a
// genuine cross-bank contract (buyer on one bank, seller on the other) is
// NOT blocked by the collision guard. This is the happy-path sanity check.
func TestPeerOTC_RecordOptionContract_LegitCrossBank_Allowed(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t) // ownRouting = 111
	reserver := &fakeReserver{}
	h.SetHoldingReserver(reserver)

	optDesc := contractsitx.OptionDescription{
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-legit"},
		Stock:          contractsitx.StockDescription{Ticker: "AAPL"},
		PricePerUnit:   contractsitx.MonetaryValue{Amount: contractsitx.DecimalNumber{Decimal: decimal.NewFromInt(100)}, Currency: "USD"},
		SettlementDate: "2026-12-31",
		Amount:         5,
	}
	optJSON, _ := json.Marshal(optDesc)

	// DEBIT direction: seller=111(us), buyer=222(peer) → counterparty=222 ≠ OwnRouting() → OK.
	resp, err := h.RecordOptionContract(context.Background(), &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "tx-legit",
		PostingIndex:          0,
		Direction:             contractsitx.DirectionDebit,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-99"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-7"},
		OptionDescriptionJson: string(optJSON),
	})
	if err != nil {
		t.Fatalf("legitimate cross-bank contract must not be rejected: %v", err)
	}
	if resp.GetContractId() == 0 {
		t.Errorf("expected a non-zero contract id")
	}
}
