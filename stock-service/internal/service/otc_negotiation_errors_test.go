package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func TestCounterNegotiation_ErrorBranches(t *testing.T) {
	env := newNegTestEnv(t)
	mkInput := func(negID uint64, callerID uint64) CounterNegotiationInput {
		return CounterNegotiationInput{
			NegotiationID: negID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(callerID),
			Quantity: decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(150),
			Premium: decimal.NewFromInt(5), SettlementDate: time.Now().UTC().AddDate(0, 1, 0),
			ActingPrincipalType: "client", ActingPrincipalID: callerID,
		}
	}
	// Missing negotiation.
	if _, err := env.svc.CounterNegotiation(context.Background(), mkInput(4242, 1)); !errors.Is(err, ErrOTCNegotiationNotFound) {
		t.Fatalf("want not found, got %v", err)
	}

	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	neg, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Third party cannot counter.
	if _, err := env.svc.CounterNegotiation(context.Background(), mkInput(neg.ID, 99)); !errors.Is(err, ErrOTCCounterUnauthorized) {
		t.Fatalf("want unauthorized, got %v", err)
	}
	// Poster counters (happy), then the chain is countered (still non-terminal).
	if _, err := env.svc.CounterNegotiation(context.Background(), mkInput(neg.ID, 1)); err != nil {
		t.Fatalf("poster counter: %v", err)
	}
	// Reject it → terminal, then counter → terminal error.
	if _, err := env.svc.RejectNegotiation(context.Background(), RejectNegotiationInput{
		NegotiationID: neg.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7),
		ActingPrincipalType: "client", ActingPrincipalID: 7,
	}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if _, err := env.svc.CounterNegotiation(context.Background(), mkInput(neg.ID, 1)); !errors.Is(err, ErrOTCNegotiationTerminal) {
		t.Fatalf("want terminal, got %v", err)
	}
}

func TestRejectNegotiation_ErrorBranches(t *testing.T) {
	env := newNegTestEnv(t)
	// Missing negotiation.
	if _, err := env.svc.RejectNegotiation(context.Background(), RejectNegotiationInput{
		NegotiationID: 4242, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(1),
	}); !errors.Is(err, ErrOTCNegotiationNotFound) {
		t.Fatalf("want not found, got %v", err)
	}

	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	neg, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Unauthorized: a third party (not bidder, not poster) cannot reject.
	if _, err := env.svc.RejectNegotiation(context.Background(), RejectNegotiationInput{
		NegotiationID: neg.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(99),
		ActingPrincipalType: "client", ActingPrincipalID: 99,
	}); !errors.Is(err, ErrOTCCounterUnauthorized) {
		t.Fatalf("want unauthorized, got %v", err)
	}

	// Poster rejects successfully.
	if _, err := env.svc.RejectNegotiation(context.Background(), RejectNegotiationInput{
		NegotiationID: neg.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(1),
		ActingPrincipalType: "client", ActingPrincipalID: 1,
	}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	// Rejecting again → terminal.
	if _, err := env.svc.RejectNegotiation(context.Background(), RejectNegotiationInput{
		NegotiationID: neg.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(1),
		ActingPrincipalType: "client", ActingPrincipalID: 1,
	}); !errors.Is(err, ErrOTCNegotiationTerminal) {
		t.Fatalf("want terminal, got %v", err)
	}
}

func TestCancelNegotiation_ErrorBranches(t *testing.T) {
	env := newNegTestEnv(t)
	if _, err := env.svc.CancelNegotiation(context.Background(), CancelNegotiationInput{
		NegotiationID: 4242, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7),
	}); !errors.Is(err, ErrOTCNegotiationNotFound) {
		t.Fatalf("want not found, got %v", err)
	}

	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	neg, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// The poster (1) cannot cancel the bidder's chain — only the bidder can.
	if _, err := env.svc.CancelNegotiation(context.Background(), CancelNegotiationInput{
		NegotiationID: neg.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(1),
		ActingPrincipalType: "client", ActingPrincipalID: 1,
	}); !errors.Is(err, ErrOTCCounterUnauthorized) {
		t.Fatalf("want unauthorized, got %v", err)
	}

	// The bidder (7) cancels successfully.
	if _, err := env.svc.CancelNegotiation(context.Background(), CancelNegotiationInput{
		NegotiationID: neg.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7),
		ActingPrincipalType: "client", ActingPrincipalID: 7,
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	// Cancelling again → terminal.
	if _, err := env.svc.CancelNegotiation(context.Background(), CancelNegotiationInput{
		NegotiationID: neg.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7),
		ActingPrincipalType: "client", ActingPrincipalID: 7,
	}); !errors.Is(err, ErrOTCNegotiationTerminal) {
		t.Fatalf("want terminal, got %v", err)
	}
}

func TestCancelListing_ErrorBranches(t *testing.T) {
	env := newNegTestEnv(t)
	// Missing offer.
	if _, err := env.svc.CancelListing(context.Background(), CancelListingInput{
		OfferID: 4242, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(1),
	}); !errors.Is(err, ErrOTCOfferNotFound) {
		t.Fatalf("want offer not found, got %v", err)
	}

	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	// Non-initiator cannot cancel the listing.
	if _, err := env.svc.CancelListing(context.Background(), CancelListingInput{
		OfferID: listing.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(99),
		ActingPrincipalType: "client", ActingPrincipalID: 99,
	}); err == nil {
		t.Fatalf("expected unauthorized for non-initiator listing cancel")
	}

	// The initiator cancels with a child chain → cascade-cancel.
	if _, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7)); err != nil {
		t.Fatalf("open child: %v", err)
	}
	res, err := env.svc.CancelListing(context.Background(), CancelListingInput{
		OfferID: listing.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(1),
		ActingPrincipalType: "client", ActingPrincipalID: 1,
	})
	if err != nil {
		t.Fatalf("cancel listing: %v", err)
	}
	if res.Offer.Status != model.OTCOfferStatusCancelled {
		t.Errorf("listing status = %s, want cancelled", res.Offer.Status)
	}
	if len(res.CancelledChains) != 1 {
		t.Errorf("expected 1 cascade-cancelled chain, got %d", len(res.CancelledChains))
	}
}
