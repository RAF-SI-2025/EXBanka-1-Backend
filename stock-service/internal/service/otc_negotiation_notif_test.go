package service

import (
	"context"
	"sync"
	"testing"

	"github.com/shopspring/decimal"

	contractkafka "github.com/exbanka/contract/kafka"
	"github.com/exbanka/stock-service/internal/model"
)

// notifCapture is a no-op notifier that records every published intent
// so tests can assert recipient + type + Data shape without spinning up
// real Kafka.
type notifCapture struct {
	mu   sync.Mutex
	msgs []contractkafka.GeneralNotificationMessage
}

func (c *notifCapture) PublishGeneralNotification(_ context.Context, m contractkafka.GeneralNotificationMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, m)
	return nil
}

func (c *notifCapture) all() []contractkafka.GeneralNotificationMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]contractkafka.GeneralNotificationMessage, len(c.msgs))
	copy(out, c.msgs)
	return out
}

func (c *notifCapture) byType(t string) *contractkafka.GeneralNotificationMessage {
	for i := range c.msgs {
		if c.msgs[i].Type == t {
			return &c.msgs[i]
		}
	}
	return nil
}

func newNegTestEnvWithNotifier(t *testing.T) (*negTestEnv, *notifCapture) {
	env := newNegTestEnv(t)
	notif := &notifCapture{}
	env.svc = env.svc.WithNotifier(notif)
	return env, notif
}

func TestOpenNegotiation_PublishesOTCOfferReceivedToPoster(t *testing.T) {
	env, notif := newNegTestEnvWithNotifier(t)
	listing := seedListing(t, env, 1 /*poster*/, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)

	if _, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7 /*bidder*/)); err != nil {
		t.Fatalf("open: %v", err)
	}
	got := notif.byType("OTC_OFFER_RECEIVED")
	if got == nil {
		t.Fatalf("expected OTC_OFFER_RECEIVED intent, got %+v", notif.all())
	}
	if got.UserID != 1 {
		t.Errorf("recipient = %d, want 1 (poster)", got.UserID)
	}
	if got.Data["ticker"] != "AAPL" {
		t.Errorf("ticker = %q, want AAPL", got.Data["ticker"])
	}
	if got.RefType != "otc_negotiation" {
		t.Errorf("ref_type = %q, want otc_negotiation", got.RefType)
	}
}

func TestCounterNegotiation_PublishesOTCOfferCounteredToOtherParty(t *testing.T) {
	env, notif := newNegTestEnvWithNotifier(t)
	listing := seedListing(t, env, 1 /*poster*/, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	neg, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7 /*bidder*/))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	notif.msgs = nil // discard the OTC_OFFER_RECEIVED from open

	// Bidder (7) counters → poster (1) should be notified.
	_, err = env.svc.CounterNegotiation(context.Background(), CounterNegotiationInput{
		NegotiationID:       neg.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(7),
		Quantity:            decimal.NewFromInt(10),
		StrikePrice:         decimal.NewFromFloat(150),
		Premium:             decimal.NewFromFloat(7),
		SettlementDate:      listing.SettlementDate,
		ActingPrincipalType: "client",
		ActingPrincipalID:   7,
	})
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	got := notif.byType("OTC_OFFER_COUNTERED")
	if got == nil {
		t.Fatalf("expected OTC_OFFER_COUNTERED intent, got %+v", notif.all())
	}
	if got.UserID != 1 {
		t.Errorf("counter recipient = %d, want 1 (poster — the other party)", got.UserID)
	}
	if got.Data["premium"] != "7" {
		t.Errorf("premium = %q, want 7", got.Data["premium"])
	}
}

func TestRejectNegotiation_PublishesOTCOfferRejectedToOtherParty(t *testing.T) {
	env, notif := newNegTestEnvWithNotifier(t)
	listing := seedListing(t, env, 1 /*poster*/, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	neg, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7 /*bidder*/))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	notif.msgs = nil

	// Poster (1) rejects → bidder (7) should be notified.
	_, err = env.svc.RejectNegotiation(context.Background(), RejectNegotiationInput{
		NegotiationID:       neg.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
	})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	got := notif.byType("OTC_OFFER_REJECTED")
	if got == nil {
		t.Fatalf("expected OTC_OFFER_REJECTED, got %+v", notif.all())
	}
	if got.UserID != 7 {
		t.Errorf("reject recipient = %d, want 7 (bidder — the other party)", got.UserID)
	}
}

func TestCancelNegotiation_PublishesOTCOfferCancelledToPoster(t *testing.T) {
	env, notif := newNegTestEnvWithNotifier(t)
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	neg, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	notif.msgs = nil

	// Bidder (7) cancels own chain → poster (1) notified.
	_, err = env.svc.CancelNegotiation(context.Background(), CancelNegotiationInput{
		NegotiationID:       neg.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(7),
		ActingPrincipalType: "client",
		ActingPrincipalID:   7,
	})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	got := notif.byType("OTC_OFFER_CANCELLED")
	if got == nil {
		t.Fatalf("expected OTC_OFFER_CANCELLED, got %+v", notif.all())
	}
	if got.UserID != 1 {
		t.Errorf("cancel recipient = %d, want 1 (poster)", got.UserID)
	}
}

func TestPublishNotif_SkipsBankRecipient(t *testing.T) {
	env, notif := newNegTestEnvWithNotifier(t)
	// Bank-owned listing — poster has nil OwnerID.
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	listing.InitiatorOwnerType = model.OwnerBank
	listing.InitiatorOwnerID = nil
	if err := env.offerRepo.Save(listing); err != nil {
		t.Fatalf("flip to bank: %v", err)
	}
	if _, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7)); err != nil {
		t.Fatalf("open: %v", err)
	}
	if len(notif.all()) != 0 {
		t.Errorf("bank-owned poster should not be notified, got %+v", notif.all())
	}
}
