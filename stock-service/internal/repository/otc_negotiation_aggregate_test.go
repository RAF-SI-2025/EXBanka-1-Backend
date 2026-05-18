package repository

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestAggregateActiveBidsByOffer_BasicMixedDirections — three chains
// on parent 100 (40, 45, 50 premiums; mix of open + countered) and
// two chains on parent 200 (one cancelled + one terminal accepted).
// Cancelled/accepted chains must NOT contribute; parent 200's only
// active row is the open one.
func TestAggregateActiveBidsByOffer_BasicMixedDirections(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)

	mkChain := func(parent uint64, bidderID uint64, status string, premium float64) {
		bidder := bidderID
		n := newSampleNegotiation(parent, &bidder, status)
		n.Premium = decimal.NewFromFloat(premium)
		if err := r.Create(n); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	mkChain(100, 11, model.OTCNegotiationStatusOpen, 40)
	mkChain(100, 12, model.OTCNegotiationStatusCountered, 45)
	mkChain(100, 13, model.OTCNegotiationStatusOpen, 50)
	mkChain(200, 21, model.OTCNegotiationStatusCancelled, 30)
	mkChain(200, 22, model.OTCNegotiationStatusOpen, 25)

	got, err := r.AggregateActiveBidsByOffer([]uint64{100, 200, 999})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}

	p100, ok := got[100]
	if !ok {
		t.Fatalf("parent 100 missing from result")
	}
	if p100.ActiveCount != 3 {
		t.Errorf("p100 active = %d, want 3", p100.ActiveCount)
	}
	if !p100.BestBid.Equal(decimal.NewFromFloat(50)) {
		t.Errorf("p100 best_bid = %s, want 50", p100.BestBid)
	}
	if !p100.BestAsk.Equal(decimal.NewFromFloat(40)) {
		t.Errorf("p100 best_ask = %s, want 40", p100.BestAsk)
	}

	p200, ok := got[200]
	if !ok {
		t.Fatalf("parent 200 missing from result")
	}
	if p200.ActiveCount != 1 {
		t.Errorf("p200 active = %d, want 1 (cancelled chain excluded)", p200.ActiveCount)
	}
	if !p200.BestBid.Equal(decimal.NewFromFloat(25)) {
		t.Errorf("p200 best_bid = %s, want 25", p200.BestBid)
	}

	if _, ok := got[999]; ok {
		t.Errorf("parent 999 has no chains — must be absent, not zero-keyed")
	}
}

// TestAggregateActiveBidsByOffer_EmptyInput — empty offerIDs returns
// empty map, no SQL roundtrip needed (defensive).
func TestAggregateActiveBidsByOffer_EmptyInput(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)
	got, err := r.AggregateActiveBidsByOffer(nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("nil input must yield empty map, got %+v", got)
	}
}
