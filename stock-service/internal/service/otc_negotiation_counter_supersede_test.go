package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// P4 — counter supersession (LOCAL path). When a chain is countered multiple
// times, the chain row holds only the LATEST-terms snapshot; prior counters live
// in the append-only revision history (visible) but are never independently
// acceptable (there is no accept-by-revision API). Accept always uses the current
// snapshot and forbids accepting the terms YOU last proposed. So only the newest
// counter is acceptable, and only by the OTHER party.
//
// The remote path's equivalent guarantee (snapshot overwrite + turn rule +
// anti-self-accept) is covered by peer_otc_counter_turn_test.go and
// otc_negotiation_remote_selfaccept_test.go.

func counterInput(negID, callerID uint64, premium float64) CounterNegotiationInput {
	return CounterNegotiationInput{
		NegotiationID:       negID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(callerID),
		Quantity:            decimal.NewFromInt(10),
		StrikePrice:         decimal.NewFromFloat(150.0),
		Premium:             decimal.NewFromFloat(premium),
		SettlementDate:      time.Now().UTC().AddDate(0, 1, 0),
		ActingPrincipalType: "client",
		ActingPrincipalID:   callerID,
	}
}

// TestCounterSupersede_OnlyNewestAcceptable: bidder 7 vs poster 1 exchange a run
// of counters; only the newest terms are acceptable, only by the opposite party,
// and the prior counters survive in history.
func TestCounterSupersede_OnlyNewestAcceptable(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1 /*poster*/, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	neg, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7 /*bidder*/))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// A run of counters: poster→6, bidder→7, poster→8, bidder→9 (the newest).
	for _, c := range []struct {
		caller  uint64
		premium float64
	}{{1, 6}, {7, 7}, {1, 8}, {7, 9}} {
		if _, err := env.svc.CounterNegotiation(context.Background(), counterInput(neg.ID, c.caller, c.premium)); err != nil {
			t.Fatalf("counter by %d: %v", c.caller, err)
		}
	}

	// Snapshot must reflect ONLY the newest terms (premium 9), not any earlier one.
	cur, err := env.negRepo.GetByID(neg.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !cur.Premium.Equal(decimal.NewFromFloat(9.0)) {
		t.Fatalf("snapshot premium=%s want 9.0 (newest counter only)", cur.Premium)
	}

	// The party who proposed the current terms (bidder 7, last mover) CANNOT
	// accept them — that would be accepting your own counter. This is what makes
	// a superseding counter "yours to honor, not yours to accept".
	if _, err := env.svc.AcceptNegotiation(context.Background(), AcceptNegotiationInput{
		NegotiationID: neg.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(7),
		ActingPrincipalType: "client", ActingPrincipalID: 7,
	}); err == nil {
		t.Fatalf("bidder accepting its OWN last counter must fail (only the counterparty may accept)")
	}

	// The opposite party (poster 1) accepts — the minted/accepted terms are the
	// NEWEST (premium 9), never an earlier superseded counter.
	res, err := env.svc.AcceptNegotiation(context.Background(), AcceptNegotiationInput{
		NegotiationID: neg.ID, CallerOwnerType: model.OwnerClient, CallerOwnerID: u64p(1),
		ActingPrincipalType: "client", ActingPrincipalID: 1,
	})
	if err != nil {
		t.Fatalf("poster accept: %v", err)
	}
	if !res.WinningNegotiation.Premium.Equal(decimal.NewFromFloat(9.0)) {
		t.Fatalf("accepted premium=%s want 9.0 (newest); a stale counter was accepted",
			res.WinningNegotiation.Premium)
	}
	if res.WinningNegotiation.Status != model.OTCNegotiationStatusAccepted {
		t.Fatalf("status=%s want accepted", res.WinningNegotiation.Status)
	}

	// All prior counters remain VISIBLE in history: BID + 4 COUNTER + ACCEPT = 6.
	revs, _ := env.negRepo.ListRevisions(neg.ID)
	if len(revs) != 6 {
		t.Fatalf("want 6 revisions (BID + 4 COUNTER + ACCEPT) preserved, got %d", len(revs))
	}
	if revs[0].Action != model.OTCNegotiationActionBid || revs[5].Action != model.OTCNegotiationActionAccept {
		t.Fatalf("history order wrong: first=%s last=%s", revs[0].Action, revs[5].Action)
	}
}
