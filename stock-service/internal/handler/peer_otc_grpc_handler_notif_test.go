package handler_test

import (
	"context"
	"sync"
	"testing"

	contractkafka "github.com/exbanka/contract/kafka"
	stockpb "github.com/exbanka/contract/stockpb"
)

// peerNotifCapture: same shape as the service-package one but lives
// in handler_test to keep package boundaries clean.
type peerNotifCapture struct {
	mu   sync.Mutex
	msgs []contractkafka.GeneralNotificationMessage
}

func (c *peerNotifCapture) PublishGeneralNotification(_ context.Context, m contractkafka.GeneralNotificationMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, m)
	return nil
}

func (c *peerNotifCapture) snapshot() []contractkafka.GeneralNotificationMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]contractkafka.GeneralNotificationMessage, len(c.msgs))
	copy(out, c.msgs)
	return out
}

func (c *peerNotifCapture) byType(t string) *contractkafka.GeneralNotificationMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.msgs {
		if c.msgs[i].Type == t {
			return &c.msgs[i]
		}
	}
	return nil
}

// TestCreateNegotiation_NotifiesLocalSeller — inbound bid from peer
// 222; the seller (own_routing=111) is our local user, so we publish
// OTC_OFFER_RECEIVED for client-9.
func TestCreateNegotiation_NotifiesLocalSeller(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	notif := &peerNotifCapture{}
	h = h.WithNotifier(notif)

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
		t.Fatalf("create: %v", err)
	}

	got := notif.byType("OTC_OFFER_RECEIVED")
	if got == nil {
		t.Fatalf("expected OTC_OFFER_RECEIVED, got %+v", notif.snapshot())
	}
	if got.UserID != 9 {
		t.Errorf("recipient = %d, want 9 (local seller)", got.UserID)
	}
	if got.Data["ticker"] != "AAPL" {
		t.Errorf("ticker = %q, want AAPL", got.Data["ticker"])
	}
}

// TestCreateNegotiation_BankSeller_NoNotif — seller is the local bank
// (not a client) → no notification fires. Post-Fix #7/#9 the seller's
// routing MUST be local, so this is the only "no notif on Create" path
// left (pre-fix this was paired with "neither party local", which is
// now rejected up front by the routing assertion).
func TestCreateNegotiation_BankSeller_NoNotif(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	notif := &peerNotifCapture{}
	h = h.WithNotifier(notif)

	_, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-3"},
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "bank"},
		Offer: &stockpb.PeerOtcOffer{
			Ticker: "AAPL", Amount: 2,
			PricePerStock: "175", Currency: "USD",
			Premium: "40", PremiumCurrency: "USD",
			SettlementDate: "2027-08-01T00:00:00Z",
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := notif.snapshot(); len(got) != 0 {
		t.Errorf("bank seller should mean no client notif, got %+v", got)
	}
}

// TestCreateNegotiation_RejectsBuyerRoutingSpoofing — Fix #7 security
// check: a peer authenticated as 222 cannot claim bank 333 as the
// buyer's routing. Pre-fix this would have created a row that, on
// accept, sent a debit posting to bank 333 (silently moving a third
// bank's user's money).
func TestCreateNegotiation_RejectsBuyerRoutingSpoofing(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)

	_, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 333, Id: "client-3"}, // CLAIMS bank 333
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-9"},
		Offer: &stockpb.PeerOtcOffer{
			Ticker: "AAPL", Amount: 2,
			PricePerStock: "175", Currency: "USD",
			Premium: "40", PremiumCurrency: "USD",
			SettlementDate: "2027-08-01T00:00:00Z",
		},
	})
	if err == nil {
		t.Fatal("expected PermissionDenied for buyer-routing spoof, got nil")
	}
}

// TestCreateNegotiation_RejectsForeignSeller — Fix #9 consistency
// check: the seller in an inbound bid must be on this bank.
func TestCreateNegotiation_RejectsForeignSeller(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)

	_, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-3"},
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 333, Id: "client-9"}, // bank 333 ≠ own (111)
		Offer: &stockpb.PeerOtcOffer{
			Ticker: "AAPL", Amount: 2,
			PricePerStock: "175", Currency: "USD",
			Premium: "40", PremiumCurrency: "USD",
			SettlementDate: "2027-08-01T00:00:00Z",
		},
	})
	if err == nil {
		t.Fatal("expected InvalidArgument for foreign seller routing, got nil")
	}
}

// TestDeleteNegotiation_FreeFormChain_PlainCancelled — caller-driven
// cancel of a chain that has no parent_offer_id → OTC_OFFER_CANCELLED.
func TestDeleteNegotiation_FreeFormChain_PlainCancelled(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	notif := &peerNotifCapture{}
	h = h.WithNotifier(notif)

	createResp, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-3"},
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-9"},
		Offer: &stockpb.PeerOtcOffer{
			Ticker: "AAPL", Amount: 2,
			PricePerStock: "175", Currency: "USD",
			Premium: "40", PremiumCurrency: "USD",
			SettlementDate: "2027-08-01T00:00:00Z",
			// No ParentOfferId — free-form chain.
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	notif.mu.Lock()
	notif.msgs = nil
	notif.mu.Unlock()

	if _, err := h.DeleteNegotiation(context.Background(), &stockpb.DeleteNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: createResp.GetNegotiationId(),
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if got := notif.byType("OTC_OFFER_CANCELLED"); got == nil {
		t.Fatalf("expected OTC_OFFER_CANCELLED for free-form chain, got %+v", notif.snapshot())
	}
	if got := notif.byType("OTC_OFFER_CASCADE_CANCELLED"); got != nil {
		t.Errorf("free-form chain must not emit CASCADE_CANCELLED, got %+v", got)
	}
}

// TestDeleteNegotiation_DiscoveredChain_CascadeCancelled — chain
// has parent_offer_id ⇒ cascade heuristic ⇒ CASCADE_CANCELLED type.
// Post-Fix #7/#9 the test inverts the original routings: the inbound
// /negotiations is always received by the SELLER's bank, so the seller
// must be local (own_routing=111) and the buyer must be on the
// authenticated peer (222). The cancel notification therefore fires
// to the local SELLER (client-9), not the foreign buyer.
func TestDeleteNegotiation_DiscoveredChain_CascadeCancelled(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	notif := &peerNotifCapture{}
	h = h.WithNotifier(notif)

	createResp, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-3"}, // foreign bidder
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-9"}, // local seller (us)
		Offer: &stockpb.PeerOtcOffer{
			Ticker: "AAPL", Amount: 2,
			PricePerStock: "175", Currency: "USD",
			Premium: "40", PremiumCurrency: "USD",
			SettlementDate: "2027-08-01T00:00:00Z",
			ParentOfferId:  &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "42"},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	notif.mu.Lock()
	notif.msgs = nil
	notif.mu.Unlock()

	if _, err := h.DeleteNegotiation(context.Background(), &stockpb.DeleteNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: createResp.GetNegotiationId(),
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got := notif.byType("OTC_OFFER_CASCADE_CANCELLED")
	if got == nil {
		t.Fatalf("expected OTC_OFFER_CASCADE_CANCELLED, got %+v", notif.snapshot())
	}
	if got.UserID != 9 {
		t.Errorf("recipient = %d, want 3 (local buyer whose chain lost)", got.UserID)
	}
	if got.Data["accepted_premium"] == "" {
		t.Errorf("expected accepted_premium in data, got %+v", got.Data)
	}
}
