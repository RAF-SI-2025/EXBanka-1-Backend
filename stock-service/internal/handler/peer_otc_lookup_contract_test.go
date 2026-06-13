package handler_test

import (
	"context"
	"testing"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/shopspring/decimal"
)

func strPtr(s string) *string { return &s }

// TestLookupPeerOptionContract_FoundSellerSide verifies the RPC returns the
// stored SELLER-side (DEBIT) terms for a negotiationId this bank holds.
func TestLookupPeerOptionContract_FoundSellerSide(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t)
	ctx := context.Background()

	// SP-2a: REMOTE seller-side (DEBIT) contract. We host the seller (111); the
	// buyer's bank (222) is the counterparty, so routing_number=222.
	row := seedRemoteContractRow(
		222, "222:k-1", 2, "DEBIT", 111, "neg-1",
		222, "client-7", 111, "client-3",
		"WMT", 10, decimal.RequireFromString("50"), "RSD", "2026-12-31T00:00:00+02:00", "active",
	)
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := h.LookupPeerOptionContract(ctx, &stockpb.LookupPeerOptionContractRequest{
		NegotiationRoutingNumber: 111,
		NegotiationId:            "neg-1",
	})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !resp.GetFound() {
		t.Fatalf("expected found=true")
	}
	if resp.GetSellerId() != "client-3" {
		t.Errorf("seller_id: %q", resp.GetSellerId())
	}
	if resp.GetTicker() != "WMT" {
		t.Errorf("ticker: %q", resp.GetTicker())
	}
	if resp.GetStrikePrice() != "50" {
		t.Errorf("strike_price: %q", resp.GetStrikePrice())
	}
	if resp.GetQuantity() != 10 {
		t.Errorf("quantity: %d", resp.GetQuantity())
	}
	if resp.GetCurrency() != "RSD" {
		t.Errorf("currency: %q", resp.GetCurrency())
	}
	// SP-2a: the unified table stores settlement as a time.Time; the lookup
	// formats it back to RFC3339 in UTC. The INSTANT is preserved (the only
	// thing optionExpired cares about): +02:00 midnight == 22:00Z the day before.
	if resp.GetSettlementDate() != "2026-12-30T22:00:00Z" {
		t.Errorf("settlement_date: %q", resp.GetSettlementDate())
	}
	if resp.GetStatus() != "active" {
		t.Errorf("status: %q", resp.GetStatus())
	}
}

// TestLookupPeerOptionContract_ReturnsNominatedSellerAccount verifies the RPC
// surfaces the seller's stored NOMINATED account number so the exercise strike
// credit can target the seller's bound account (sub-case 2).
func TestLookupPeerOptionContract_ReturnsNominatedSellerAccount(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t)
	ctx := context.Background()

	const nominated = "111000000000000777"
	row := seedRemoteContractRow(
		222, "222:k-3", 2, "DEBIT", 111, "neg-3",
		222, "client-7", 111, "client-3",
		"WMT", 10, decimal.RequireFromString("50"), "RSD", "2026-12-31T00:00:00+02:00", "active",
	)
	row.RemoteSellerAccountNumber = strPtr(nominated)
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := h.LookupPeerOptionContract(ctx, &stockpb.LookupPeerOptionContractRequest{
		NegotiationRoutingNumber: 111,
		NegotiationId:            "neg-3",
	})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if resp.GetSellerAccountNumber() != nominated {
		t.Errorf("seller_account_number = %q, want %q", resp.GetSellerAccountNumber(), nominated)
	}
}

// TestLookupPeerOptionContract_NotFound verifies found=false when this bank does
// not hold the seller-side row for the negotiationId (e.g. it holds the buyer
// side only, or knows nothing of the negotiation).
func TestLookupPeerOptionContract_NotFound(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t)
	ctx := context.Background()

	// Only a CREDIT (buyer-side) REMOTE row exists — the seller-side lookup must
	// miss. We host the buyer (111); the seller's bank (222) is the counterparty.
	row := seedRemoteContractRow(
		222, "222:k-2", 3, "CREDIT", 111, "neg-2",
		111, "client-1", 222, "client-9",
		"WMT", 5, decimal.RequireFromString("50"), "RSD", "2026-12-31T00:00:00+02:00", "active",
	)
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := h.LookupPeerOptionContract(ctx, &stockpb.LookupPeerOptionContractRequest{
		NegotiationRoutingNumber: 111,
		NegotiationId:            "neg-2",
	})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if resp.GetFound() {
		t.Fatalf("expected found=false for a buyer-side-only negotiation")
	}
}
