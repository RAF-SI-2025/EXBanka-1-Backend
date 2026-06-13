package handler_test

import (
	"context"
	"testing"

	stockpb "github.com/exbanka/contract/stockpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeLocalSellerValidator answers SellerExists from a fixed allow-set of
// client participant ids. Any id not in the set is treated as non-existent.
// bank/employee-* are validated by the handler before the validator is asked,
// so this fake only ever sees client-<n> ids.
type fakeLocalSellerValidator struct {
	exists map[string]bool
	calls  []string
}

func (f *fakeLocalSellerValidator) SellerExists(_ context.Context, participantID string) bool {
	f.calls = append(f.calls, participantID)
	return f.exists[participantID]
}

// TestCreateNegotiation_PhantomSeller_Rejected guards the cross-bank phantom-row
// loophole found in the live two-stack adversarial sweep (2026-06-05): a raw peer
// (X-Api-Key) could POST /cross-bank-protocol/negotiations with a sellerId.id of
// the correct routing (111) but referencing a CLIENT that does not exist locally
// (e.g. "client-888888"). The handler persisted the row (HTTP 201) instead of
// returning a clean 4xx — an inert but unbounded junk row any peer could spam
// (resource-pollution / DoS), violating the documented contract that a
// non-resolvable seller id "surfaces as a clean 4xx ... no phantom row".
//
// With a seller validator wired, a client-<n> seller that does not exist must be
// rejected with NotFound and NO row persisted.
func TestCreateNegotiation_PhantomSeller_Rejected(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t)
	val := &fakeLocalSellerValidator{exists: map[string]bool{"client-9": true}}
	h = h.WithSellerValidator(val)

	_, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-3"},
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-888888"}, // no such local client
		Offer: &stockpb.PeerOtcOffer{
			Ticker: "AAPL", Amount: 2,
			PricePerStock: "175", Currency: "USD",
			Premium: "40", PremiumCurrency: "USD",
			SettlementDate: "2027-08-01T00:00:00Z",
		},
	})
	if err == nil {
		t.Fatal("expected error: a non-existent client-<n> seller must be rejected, got nil (phantom row created)")
	}
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
	// No phantom row may be persisted.
	var n int64
	db.Table("otc_negotiations").Count(&n)
	if n != 0 {
		t.Errorf("expected 0 persisted negotiations, got %d (phantom row leaked)", n)
	}
}

// TestCreateNegotiation_RealClientSeller_Accepted confirms the validator does
// not block a seller that DOES exist locally.
func TestCreateNegotiation_RealClientSeller_Accepted(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t)
	val := &fakeLocalSellerValidator{exists: map[string]bool{"client-9": true}}
	h = h.WithSellerValidator(val)

	_, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-3"},
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-9"},
		Offer: &stockpb.PeerOtcOffer{
			Ticker: "AAPL", Amount: 2,
			PricePerStock: "175", Currency: "USD",
			Premium: "40", PremiumCurrency: "USD",
			SettlementDate: "2027-08-01T00:00:00Z",
		},
	})
	if err != nil {
		t.Fatalf("create with real seller: %v", err)
	}
	var n int64
	db.Table("otc_negotiations").Count(&n)
	if n != 1 {
		t.Errorf("expected 1 persisted negotiation, got %d", n)
	}
}

// TestCreateNegotiation_BankSeller_ValidatorSkipped confirms a "bank"/"employee-"
// seller is accepted WITHOUT consulting the client validator (the bank always
// exists; only client-<n> needs an existence check).
func TestCreateNegotiation_BankSeller_ValidatorSkipped(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t)
	val := &fakeLocalSellerValidator{exists: map[string]bool{}} // would reject any client
	h = h.WithSellerValidator(val)

	for _, sellerID := range []string{"bank", "employee-1"} {
		_, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
			PeerBankCode: "222",
			BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-3"},
			SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: sellerID},
			Offer: &stockpb.PeerOtcOffer{
				Ticker: "AAPL", Amount: 2,
				PricePerStock: "175", Currency: "USD",
				Premium: "40", PremiumCurrency: "USD",
				SettlementDate: "2027-08-01T00:00:00Z",
			},
		})
		if err != nil {
			t.Fatalf("create with seller %q: %v", sellerID, err)
		}
	}
	if len(val.calls) != 0 {
		t.Errorf("validator must not be consulted for bank/employee sellers, calls=%v", val.calls)
	}
	var n int64
	db.Table("otc_negotiations").Count(&n)
	if n != 2 {
		t.Errorf("expected 2 persisted negotiations, got %d", n)
	}
}
