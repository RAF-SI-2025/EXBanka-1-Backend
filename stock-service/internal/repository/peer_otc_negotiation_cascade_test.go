package repository

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

// TestPeerOtcNegotiation_CascadeMatchByParentOfferID verifies the
// Phase-10 ListBySellerAndParentOffer query: only ongoing chains
// under the same seller AND the same atomic parent_offer_id come
// back. Two LEGITIMATELY DISTINCT listings on the same ticker +
// same settlement_date but different parent ids must NOT match each
// other — that's the core safety guarantee of the precise-key
// approach over the heuristic ticker+date one.
func TestPeerOtcNegotiation_CascadeMatchByParentOfferID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.PeerOtcNegotiation{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewPeerOtcNegotiationRepository(db)
	now := time.Now().UTC()
	pid := func(s string) *string { return &s }
	prout := func(i int64) *int64 { return &i }

	rows := []model.PeerOtcNegotiation{
		// Listing #100 on seller (111, "client-1"): two parallel bidders.
		{PeerBankCode: "222", ForeignID: "neg-a", SellerRoutingNumber: 111, SellerID: "client-1",
			BuyerRoutingNumber: 222, BuyerID: "client-1", OfferJSON: "{}", Status: "ongoing",
			ParentOfferRouting: prout(111), ParentOfferID: pid("100"),
			CreatedAt: now, UpdatedAt: now},
		{PeerBankCode: "333", ForeignID: "neg-b", SellerRoutingNumber: 111, SellerID: "client-1",
			BuyerRoutingNumber: 333, BuyerID: "client-1", OfferJSON: "{}", Status: "ongoing",
			ParentOfferRouting: prout(111), ParentOfferID: pid("100"),
			CreatedAt: now, UpdatedAt: now},
		// Listing #200 on SAME seller — must NOT match listing #100's group.
		{PeerBankCode: "222", ForeignID: "neg-c", SellerRoutingNumber: 111, SellerID: "client-1",
			BuyerRoutingNumber: 222, BuyerID: "client-2", OfferJSON: "{}", Status: "ongoing",
			ParentOfferRouting: prout(111), ParentOfferID: pid("200"),
			CreatedAt: now, UpdatedAt: now},
		// Free-form chain (no parent) — must NOT be matched regardless.
		{PeerBankCode: "333", ForeignID: "neg-d", SellerRoutingNumber: 111, SellerID: "client-1",
			BuyerRoutingNumber: 333, BuyerID: "client-2", OfferJSON: "{}", Status: "ongoing",
			ParentOfferRouting: nil, ParentOfferID: nil,
			CreatedAt: now, UpdatedAt: now},
		// Different seller — must NOT match.
		{PeerBankCode: "222", ForeignID: "neg-e", SellerRoutingNumber: 999, SellerID: "client-7",
			BuyerRoutingNumber: 222, BuyerID: "client-1", OfferJSON: "{}", Status: "ongoing",
			ParentOfferRouting: prout(111), ParentOfferID: pid("100"),
			CreatedAt: now, UpdatedAt: now},
		// Already-cancelled chain on the right group — must NOT match (status filter).
		{PeerBankCode: "222", ForeignID: "neg-f", SellerRoutingNumber: 111, SellerID: "client-1",
			BuyerRoutingNumber: 222, BuyerID: "client-3", OfferJSON: "{}", Status: "cancelled",
			ParentOfferRouting: prout(111), ParentOfferID: pid("100"),
			CreatedAt: now, UpdatedAt: now},
	}
	for i := range rows {
		if err := r.Create(&rows[i]); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	got, err := r.ListBySellerAndParentOffer(111, "client-1", 111, "100")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	gotIDs := map[string]bool{}
	for _, n := range got {
		gotIDs[n.ForeignID] = true
	}
	want := map[string]bool{"neg-a": true, "neg-b": true}
	for fid := range want {
		if !gotIDs[fid] {
			t.Errorf("expected %s in result, missing", fid)
		}
	}
	for fid := range gotIDs {
		if !want[fid] {
			t.Errorf("unexpected %s in result (would cause wrong-cancel)", fid)
		}
	}
}

// TestPeerOtcNegotiation_NoParentMeansNoCascade verifies the
// free-form-bidder safety: a chain with NULL parent_offer_id is
// never returned by the cascade query even when queried with the
// same seller_id, so the seller's free-form listings stay safe.
func TestPeerOtcNegotiation_NoParentMeansNoCascade(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.PeerOtcNegotiation{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewPeerOtcNegotiationRepository(db)
	now := time.Now().UTC()
	if err := r.Create(&model.PeerOtcNegotiation{
		PeerBankCode: "222", ForeignID: "neg-x", SellerRoutingNumber: 111, SellerID: "client-1",
		BuyerRoutingNumber: 222, BuyerID: "client-1", OfferJSON: "{}", Status: "ongoing",
		ParentOfferRouting: nil, ParentOfferID: nil,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Query with ANY parent_offer_id — the free-form row must not match.
	got, _ := r.ListBySellerAndParentOffer(111, "client-1", 111, "anything")
	if len(got) != 0 {
		t.Errorf("free-form chain incorrectly matched cascade query: %d rows", len(got))
	}
}
