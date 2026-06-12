package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func seedConsumedParent(t *testing.T, f *otcCRUDFixture, ownerID uint64, qty int64) *model.OTCOffer {
	t.Helper()
	oid := ownerID
	parent := &model.OTCOffer{
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &oid,
		Direction:                   model.OTCDirectionSellInitiated,
		StockID:                     5,
		Ticker:                      "AAPL",
		Quantity:                    decimal.NewFromInt(qty),
		Status:                      model.OTCOfferStatusConsumed, // accept already consumed the listing
		Local:                       true,
		InitiatorAccountID:          9,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   ownerID,
	}
	if err := f.offers.Create(parent); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	return parent
}

// TestRelistAcceptRemainder_PartialAccept asserts that after a partial accept
// (bid 10 of a 35 listing) the unsold remainder (25) is re-advertised as a
// brand-new OPEN listing — a fresh id, NOT the consumed parent — so it keeps
// trading as a new negotiation surface.
func TestRelistAcceptRemainder_PartialAccept(t *testing.T) {
	f := newOTCCRUDFixture(t)
	ownerID := uint64(1)
	parent := seedConsumedParent(t, f, ownerID, 35)

	f.svc.relistAcceptRemainder(context.Background(), parent, decimal.NewFromInt(10))

	open, err := f.offers.ListOpenForCache(100)
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("want exactly 1 fresh open remainder listing, got %d", len(open))
	}
	r := open[0]
	if r.ID == parent.ID {
		t.Errorf("remainder must be a NEW listing, not the consumed parent (id %d)", parent.ID)
	}
	if !r.Quantity.Equal(decimal.NewFromInt(25)) {
		t.Errorf("remainder quantity = %s, want 25", r.Quantity)
	}
	if r.Ticker != "AAPL" || r.Direction != model.OTCDirectionSellInitiated || r.InitiatorAccountID != 9 {
		t.Errorf("remainder offer fields mismatch: %+v", r)
	}
	if r.InitiatorOwnerID == nil || *r.InitiatorOwnerID != ownerID {
		t.Errorf("remainder owner mismatch: %+v", r.InitiatorOwnerID)
	}
	// The consumed parent must stay consumed (not reopened).
	got, _ := f.offers.GetByID(parent.ID)
	if got.Status != model.OTCOfferStatusConsumed {
		t.Errorf("parent status = %q, want consumed", got.Status)
	}
}

// TestRelistAcceptRemainder_FullTake_NoRelist asserts that when the accept takes
// the WHOLE listing (bid == listing quantity) nothing is re-listed.
func TestRelistAcceptRemainder_FullTake_NoRelist(t *testing.T) {
	f := newOTCCRUDFixture(t)
	parent := seedConsumedParent(t, f, 1, 35)

	f.svc.relistAcceptRemainder(context.Background(), parent, decimal.NewFromInt(35))

	open, err := f.offers.ListOpenForCache(100)
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("full take must not re-list; got %d open offers", len(open))
	}
}

// TestConsumeLocalSellOfferForSeller_PartialReList covers the CROSS-BANK accept
// path's consume+re-list: accepting 30 of a 50-unit listing consumes the original
// and re-lists the unsold 20 as a fresh open offer (new id) — the seller's
// remainder keeps trading. A full take re-lists nothing.
func TestConsumeLocalSellOfferForSeller_PartialReList(t *testing.T) {
	env := newNegTestEnv(t)
	owner := uint64(1)
	listing := seedListing(t, env, owner, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	listing.Quantity = decimal.NewFromInt(50)
	if err := env.offerRepo.Save(listing); err != nil {
		t.Fatalf("set qty: %v", err)
	}

	if err := env.svc.ConsumeLocalSellOfferForSeller(model.OwnerClient, &owner, "AAPL", 30); err != nil {
		t.Fatalf("consume+relist: %v", err)
	}

	orig, _ := env.offerRepo.GetByID(listing.ID)
	if orig.Status != model.OTCOfferStatusConsumed {
		t.Errorf("original listing status = %q, want consumed", orig.Status)
	}
	open, err := env.offerRepo.ListOpenForCache(100)
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("want 1 fresh remainder listing, got %d", len(open))
	}
	if open[0].ID == listing.ID {
		t.Errorf("remainder must be a NEW listing, not the consumed original (id %d)", listing.ID)
	}
	if !open[0].Quantity.Equal(decimal.NewFromInt(20)) {
		t.Errorf("remainder quantity = %s, want 20", open[0].Quantity)
	}

	// A subsequent FULL take of the remainder re-lists nothing.
	if err := env.svc.ConsumeLocalSellOfferForSeller(model.OwnerClient, &owner, "AAPL", 20); err != nil {
		t.Fatalf("consume full: %v", err)
	}
	open2, _ := env.offerRepo.ListOpenForCache(100)
	if len(open2) != 0 {
		t.Fatalf("full take must re-list nothing, got %d", len(open2))
	}
}
