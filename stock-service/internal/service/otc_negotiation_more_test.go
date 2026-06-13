package service

import (
	"context"
	"errors"
	"testing"

	"github.com/exbanka/stock-service/internal/model"
)

func TestLocalParentIsOpen(t *testing.T) {
	env := newNegTestEnv(t)
	if env.svc.LocalParentIsOpen(0) {
		t.Error("offerID 0 should be false")
	}
	if env.svc.LocalParentIsOpen(999) {
		t.Error("missing offer should be false")
	}
	open := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	if !env.svc.LocalParentIsOpen(open.ID) {
		t.Error("open listing should be true")
	}
	consumed := seedListing(t, env, 2, model.OTCDirectionSellInitiated, model.OTCOfferStatusConsumed)
	if env.svc.LocalParentIsOpen(consumed.ID) {
		t.Error("consumed listing should be false")
	}
}

func TestLocalSellOfferOpenForSeller(t *testing.T) {
	env := newNegTestEnv(t)
	seller := uint64(1)
	// No listing yet.
	if env.svc.LocalSellOfferOpenForSeller(model.OwnerClient, &seller, "AAPL") {
		t.Error("no open listing → false")
	}
	seedListing(t, env, seller, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	if !env.svc.LocalSellOfferOpenForSeller(model.OwnerClient, &seller, "AAPL") {
		t.Error("open sell listing → true")
	}
}

func TestConsumeLocalSellOfferForSeller_RelistRemainder(t *testing.T) {
	env := newNegTestEnv(t)
	seller := uint64(1)
	listing := seedListing(t, env, seller, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	// listing qty is 10; accept 4 → remainder 6 re-listed.
	if err := env.svc.ConsumeLocalSellOfferForSeller(model.OwnerClient, &seller, "AAPL", 4); err != nil {
		t.Fatalf("consume: %v", err)
	}
	// Original consumed.
	got, err := env.offerRepo.GetByID(listing.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.OTCOfferStatusConsumed {
		t.Errorf("original status = %s, want consumed", got.Status)
	}
	// A fresh open listing for remainder 6 must now exist.
	if !env.svc.LocalSellOfferOpenForSeller(model.OwnerClient, &seller, "AAPL") {
		t.Error("remainder should be re-listed as open")
	}

	// Idempotent: a second consume of the now-only-open remainder with full qty
	// consumes it without relisting.
	if err := env.svc.ConsumeLocalSellOfferForSeller(model.OwnerClient, &seller, "AAPL", 6); err != nil {
		t.Fatalf("second consume: %v", err)
	}
	if env.svc.LocalSellOfferOpenForSeller(model.OwnerClient, &seller, "AAPL") {
		t.Error("after full consume, no open listing should remain")
	}

	// Third consume → nothing open → no-op (idempotent).
	if err := env.svc.ConsumeLocalSellOfferForSeller(model.OwnerClient, &seller, "AAPL", 1); err != nil {
		t.Fatalf("idempotent consume: %v", err)
	}
}

func TestListRevisions_Authorization(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1 /*poster*/, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	neg, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7 /*bidder*/))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Bidder authorized.
	revs, err := env.svc.ListRevisions(context.Background(), neg.ID, model.OwnerClient, u64p(7))
	if err != nil || len(revs) != 1 {
		t.Fatalf("bidder list: %v len=%d", err, len(revs))
	}
	// Poster authorized.
	revs, err = env.svc.ListRevisions(context.Background(), neg.ID, model.OwnerClient, u64p(1))
	if err != nil || len(revs) != 1 {
		t.Fatalf("poster list: %v len=%d", err, len(revs))
	}
	// Third party → unauthorized.
	_, err = env.svc.ListRevisions(context.Background(), neg.ID, model.OwnerClient, u64p(99))
	if !errors.Is(err, ErrOTCRevisionsUnauthorized) {
		t.Fatalf("want unauthorized, got %v", err)
	}
	// Missing negotiation → not found.
	_, err = env.svc.ListRevisions(context.Background(), 4242, model.OwnerClient, u64p(7))
	if !errors.Is(err, ErrOTCNegotiationNotFound) {
		t.Fatalf("want not found, got %v", err)
	}

	// Unchecked variant returns the same revisions with no auth gate.
	revs, err = env.svc.ListRevisionsUnchecked(neg.ID)
	if err != nil || len(revs) != 1 {
		t.Fatalf("unchecked list: %v len=%d", err, len(revs))
	}
}

func TestNotifyOTCParticipant(t *testing.T) {
	env, notif := newNegTestEnvWithNotifier(t)
	uid := uint64(7)
	data := map[string]string{"k": "v"}

	// Client recipient → published to client inbox.
	env.svc.NotifyOTCParticipant(context.Background(), model.OwnerClient, &uid, "OTC_TEST", data, "otc", 1)
	if m := notif.byType("OTC_TEST"); m == nil || m.SystemType != "client" || m.UserID != 7 {
		t.Fatalf("client notify wrong: %+v", m)
	}

	// Bank recipient → employee inbox.
	env.svc.NotifyOTCParticipant(context.Background(), model.OwnerBank, nil, "OTC_BANK", data, "otc", 2)
	if m := notif.byType("OTC_BANK"); m == nil || m.SystemType != "employee" {
		t.Fatalf("bank notify wrong: %+v", m)
	}

	// Client recipient with nil id → no-op (no new message).
	before := len(notif.all())
	env.svc.NotifyOTCParticipant(context.Background(), model.OwnerClient, nil, "OTC_NIL", data, "otc", 3)
	if len(notif.all()) != before {
		t.Errorf("nil client id should be a no-op")
	}
}

func TestMarkNegotiationFailed(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	neg, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := env.svc.markNegotiationFailed(context.Background(), neg.ID); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	got, err := env.negRepo.GetByID(neg.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "failed" {
		t.Errorf("status = %s, want failed", got.Status)
	}
}
