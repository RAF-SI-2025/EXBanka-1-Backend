package service

import (
	"context"
	"errors"
	"testing"

	"github.com/exbanka/stock-service/internal/model"
)

func TestOfferTimeline_And_ListByParentOffer(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1 /*poster*/, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	if _, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7)); err != nil {
		t.Fatalf("open: %v", err)
	}

	// Poster sees the full timeline.
	parent, items, err := env.svc.OfferTimeline(context.Background(), listing.ID, model.OwnerClient, u64p(1))
	if err != nil {
		t.Fatalf("poster timeline: %v", err)
	}
	if parent.ID != listing.ID || len(items) == 0 {
		t.Errorf("expected timeline items for the poster, got %d", len(items))
	}

	// A permission-gated employee (bank) is allowed.
	if _, _, err := env.svc.OfferTimeline(context.Background(), listing.ID, model.OwnerBank, nil); err != nil {
		t.Errorf("bank timeline should be allowed: %v", err)
	}

	// A competing third party is forbidden.
	if _, _, err := env.svc.OfferTimeline(context.Background(), listing.ID, model.OwnerClient, u64p(99)); !errors.Is(err, ErrOTCListingAudienceForbidden) {
		t.Errorf("third-party timeline should be forbidden, got %v", err)
	}

	// Missing offer → not found.
	if _, _, err := env.svc.OfferTimeline(context.Background(), 4242, model.OwnerClient, u64p(1)); !errors.Is(err, ErrOTCOfferNotFound) {
		t.Errorf("missing offer → not found, got %v", err)
	}

	// ListByParentOffer mirrors the same authorization.
	_, chains, err := env.svc.ListByParentOffer(context.Background(), listing.ID, model.OwnerClient, u64p(1))
	if err != nil || len(chains) != 1 {
		t.Fatalf("poster list-by-parent: %v len=%d", err, len(chains))
	}
	if _, _, err := env.svc.ListByParentOffer(context.Background(), listing.ID, model.OwnerClient, u64p(99)); !errors.Is(err, ErrOTCListingAudienceForbidden) {
		t.Errorf("third-party list-by-parent should be forbidden, got %v", err)
	}
}
