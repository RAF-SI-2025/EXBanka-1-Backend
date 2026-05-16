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

// TestCreateNegotiation_NoLocalParty_NoNotif — neither side is local
// (own_routing=111 but both routings are 222) → nothing published.
func TestCreateNegotiation_NoLocalParty_NoNotif(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	notif := &peerNotifCapture{}
	h = h.WithNotifier(notif)

	_, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-3"},
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-7"},
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
		t.Errorf("no local party should mean no notif, got %+v", got)
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
func TestDeleteNegotiation_DiscoveredChain_CascadeCancelled(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	notif := &peerNotifCapture{}
	h = h.WithNotifier(notif)

	createResp, err := h.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: "222",
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-3"}, // we are the BUYER side here
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-9"},
		Offer: &stockpb.PeerOtcOffer{
			Ticker: "AAPL", Amount: 2,
			PricePerStock: "175", Currency: "USD",
			Premium: "40", PremiumCurrency: "USD",
			SettlementDate: "2027-08-01T00:00:00Z",
			ParentOfferId:  &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "42"},
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
	if got.UserID != 3 {
		t.Errorf("recipient = %d, want 3 (local buyer whose chain lost)", got.UserID)
	}
	if got.Data["accepted_premium"] == "" {
		t.Errorf("expected accepted_premium in data, got %+v", got.Data)
	}
}
