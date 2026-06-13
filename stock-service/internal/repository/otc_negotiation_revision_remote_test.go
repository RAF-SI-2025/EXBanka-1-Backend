package repository

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

// newRevTestDB is a sqlite :memory: with BOTH the negotiation and revision tables
// migrated (newRemoteNegTestDB migrates only the negotiation table).
func newRevTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	model.SetOwnRouting("111")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.OTCNegotiation{}, &model.OTCNegotiationRevision{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

var revSettle = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

func revTemplate(action, role, wireID string, premium int64) *model.OTCNegotiationRevision {
	w := wireID
	return &model.OTCNegotiationRevision{
		Quantity:                decimal.NewFromInt(10),
		StrikePrice:             decimal.NewFromInt(150),
		Premium:                 decimal.NewFromInt(premium),
		SettlementDate:          revSettle,
		Action:                  action,
		ModifiedByPrincipalType: role,
		RemoteActorWireID:       &w,
	}
}

func revsFor(t *testing.T, db *gorm.DB, negID uint64) []model.OTCNegotiationRevision {
	t.Helper()
	var out []model.OTCNegotiationRevision
	if err := db.Where("negotiation_id = ?", negID).Order("revision_number ASC").Find(&out).Error; err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	return out
}

// TestRemoteRevision_BidOnceOnly: two upserts of the same chain ⇒ exactly one BID.
func TestRemoteRevision_BidOnceOnly(t *testing.T) {
	db := newRevTestDB(t)
	r := NewOTCNegotiationRepository(db)

	if err := r.UpsertRemoteNegWithRevision(
		remoteNeg(222, "neg-1", 222, "client-7", 111, "client-3", `{"premium":"5"}`, "ongoing"),
		revTemplate(model.OTCNegotiationActionBid, "buyer", "client-7", 5),
	); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	// Retried create (fresh struct, same natural key).
	if err := r.UpsertRemoteNegWithRevision(
		remoteNeg(222, "neg-1", 222, "client-7", 111, "client-3", `{"premium":"5"}`, "ongoing"),
		revTemplate(model.OTCNegotiationActionBid, "buyer", "client-7", 5),
	); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	row, _ := r.GetRemoteNegByRoutingAndNative(222, "neg-1")
	revs := revsFor(t, db, row.ID)
	if len(revs) != 1 || revs[0].Action != model.OTCNegotiationActionBid || revs[0].RevisionNumber != 1 {
		t.Fatalf("want exactly 1 BID rev (#1), got %+v", revs)
	}
}

// TestRemoteRevision_CounterDedup: a new counter records; a retry (same terms+wire)
// is a no-op; a same-terms counter by the OTHER party records.
func TestRemoteRevision_CounterDedup(t *testing.T) {
	db := newRevTestDB(t)
	r := NewOTCNegotiationRepository(db)
	if err := r.UpsertRemoteNegWithRevision(
		remoteNeg(222, "neg-2", 222, "client-7", 111, "client-3", `{"premium":"5"}`, "ongoing"),
		revTemplate(model.OTCNegotiationActionBid, "buyer", "client-7", 5),
	); err != nil {
		t.Fatalf("seed bid: %v", err)
	}

	// Seller counters to 7 → records (rev 2).
	if err := r.UpdateRemoteNegOfferWithRevision(222, "neg-2", `{"premium":"7"}`,
		revTemplate(model.OTCNegotiationActionCounter, "seller", "client-3", 7)); err != nil {
		t.Fatalf("counter: %v", err)
	}
	// Retry of the exact same counter → no-op.
	if err := r.UpdateRemoteNegOfferWithRevision(222, "neg-2", `{"premium":"7"}`,
		revTemplate(model.OTCNegotiationActionCounter, "seller", "client-3", 7)); err != nil {
		t.Fatalf("counter retry: %v", err)
	}
	// Buyer counters back to the SAME premium 7 (different mover) → records (rev 3).
	if err := r.UpdateRemoteNegOfferWithRevision(222, "neg-2", `{"premium":"7"}`,
		revTemplate(model.OTCNegotiationActionCounter, "buyer", "client-7", 7)); err != nil {
		t.Fatalf("counter back: %v", err)
	}

	row, _ := r.GetRemoteNegByRoutingAndNative(222, "neg-2")
	revs := revsFor(t, db, row.ID)
	if len(revs) != 3 {
		t.Fatalf("want 3 revs (BID, seller COUNTER, buyer COUNTER), got %d: %+v", len(revs), revs)
	}
	for i, want := range []int{1, 2, 3} {
		if revs[i].RevisionNumber != want {
			t.Fatalf("rev %d number = %d want %d (gap-free)", i, revs[i].RevisionNumber, want)
		}
	}
	if revs[1].ModifiedByPrincipalType != "seller" || revs[2].ModifiedByPrincipalType != "buyer" {
		t.Errorf("roles wrong: %q then %q", revs[1].ModifiedByPrincipalType, revs[2].ModifiedByPrincipalType)
	}
}

// TestRemoteRevision_AcceptOnTransition: ACCEPT recorded once; second CAS no-op.
func TestRemoteRevision_AcceptOnTransition(t *testing.T) {
	db := newRevTestDB(t)
	r := NewOTCNegotiationRepository(db)
	if err := r.UpsertRemoteNegWithRevision(
		remoteNeg(222, "neg-3", 222, "client-7", 111, "client-3", `{"premium":"5"}`, "ongoing"),
		revTemplate(model.OTCNegotiationActionBid, "buyer", "client-7", 5),
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ok, err := r.CompareAndSetRemoteNegStatusWithRevision(222, "neg-3", "ongoing", "accepted",
		revTemplate(model.OTCNegotiationActionAccept, "seller", "client-3", 5))
	if err != nil || !ok {
		t.Fatalf("accept CAS: ok=%v err=%v want true/nil", ok, err)
	}
	ok2, _ := r.CompareAndSetRemoteNegStatusWithRevision(222, "neg-3", "ongoing", "accepted",
		revTemplate(model.OTCNegotiationActionAccept, "seller", "client-3", 5))
	if ok2 {
		t.Fatalf("second accept CAS must not transition")
	}
	row, _ := r.GetRemoteNegByRoutingAndNative(222, "neg-3")
	revs := revsFor(t, db, row.ID)
	if len(revs) != 2 || revs[1].Action != model.OTCNegotiationActionAccept {
		t.Fatalf("want BID + ACCEPT (2 revs), got %+v", revs)
	}
}

// TestRemoteRevision_RejectOnTransition: REJECT recorded once; no-op when terminal.
func TestRemoteRevision_RejectOnTransition(t *testing.T) {
	db := newRevTestDB(t)
	r := NewOTCNegotiationRepository(db)
	if err := r.UpsertRemoteNegWithRevision(
		remoteNeg(222, "neg-4", 222, "client-7", 111, "client-3", `{"premium":"5"}`, "ongoing"),
		revTemplate(model.OTCNegotiationActionBid, "buyer", "client-7", 5),
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ok, err := r.SetRemoteNegStatusWithRevision(222, "neg-4", "cancelled",
		revTemplate(model.OTCNegotiationActionReject, "buyer", "client-7", 5))
	if err != nil || !ok {
		t.Fatalf("reject: ok=%v err=%v want true/nil", ok, err)
	}
	ok2, _ := r.SetRemoteNegStatusWithRevision(222, "neg-4", "cancelled",
		revTemplate(model.OTCNegotiationActionReject, "buyer", "client-7", 5))
	if ok2 {
		t.Fatalf("second reject on terminal chain must be a no-op")
	}
	row, _ := r.GetRemoteNegByRoutingAndNative(222, "neg-4")
	revs := revsFor(t, db, row.ID)
	if len(revs) != 2 || revs[1].Action != model.OTCNegotiationActionReject {
		t.Fatalf("want BID + REJECT (2 revs), got %+v", revs)
	}
}
