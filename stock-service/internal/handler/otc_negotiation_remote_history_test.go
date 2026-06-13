package handler

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// seedRemoteRev inserts one recorded revision for a remote chain (negID) so the
// read paths can expand the chain into its full history.
func seedRemoteRev(t *testing.T, db *gorm.DB, negID uint64, revNum int, action, role, wireID string, premium int64, at time.Time) {
	t.Helper()
	w := wireID
	rev := &model.OTCNegotiationRevision{
		NegotiationID:           negID,
		RevisionNumber:          revNum,
		Quantity:                decimal.NewFromInt(10),
		StrikePrice:             decimal.NewFromInt(150),
		Premium:                 decimal.NewFromInt(premium),
		SettlementDate:          time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		Action:                  action,
		ModifiedByPrincipalType: role,
		RemoteActorWireID:       &w,
		CreatedAt:               at,
	}
	if err := db.Create(rev).Error; err != nil {
		t.Fatalf("seed remote rev: %v", err)
	}
}

// TestParity_RemoteTimeline_FullHistory: a remote chain with recorded BID + 2
// COUNTER revisions expands into 3 ordered timeline entries (not one snapshot),
// each carrying its action, role, and exact wire id.
func TestParity_RemoteTimeline_FullHistory(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	model.SetOwnRouting("111")

	remote := &fakeRemoteOfferGetter{byID: map[uint64]*model.OTCOffer{
		900: remoteMirrorRow(900, peerSellerRouting, "foreign-7", "222", "client-3", "ACME", "open"),
	}}
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRowWithParent(55, ownRouting, "client-7", peerSellerRouting, "client-3", "ongoing", peerSellerRouting, "foreign-7"),
	}}
	h, db := newListingViewsFixture(t, ownRouting, remote, peer)
	seedRemoteOfferRow(t, db, 900, peerSellerRouting, "foreign-7", "client-3")

	base := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	seedRemoteRev(t, db, 55, 1, model.OTCNegotiationActionBid, "buyer", "client-7", 5, base)
	seedRemoteRev(t, db, 55, 2, model.OTCNegotiationActionCounter, "seller", "client-3", 7, base.Add(time.Minute))
	seedRemoteRev(t, db, 55, 3, model.OTCNegotiationActionCounter, "buyer", "client-7", 6, base.Add(2*time.Minute))

	resp, err := h.GetOfferTimeline(context.Background(), &stockpb.GetOfferTimelineRequest{
		ParentOfferId: 900, CallerOwnerType: "client", CallerOwnerId: 7,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetTimeline()) != 3 {
		t.Fatalf("want 3 timeline entries (full history), got %d: %+v", len(resp.GetTimeline()), resp.GetTimeline())
	}
	wantAction := []string{"BID", "COUNTER", "COUNTER"}
	wantWire := []string{"client-7", "client-3", "client-7"}
	for i, e := range resp.GetTimeline() {
		if e.GetAction() != wantAction[i] {
			t.Errorf("entry %d action=%q want %q", i, e.GetAction(), wantAction[i])
		}
		if e.GetActionByWireId() != wantWire[i] {
			t.Errorf("entry %d wire_id=%q want %q", i, e.GetActionByWireId(), wantWire[i])
		}
	}
}

// TestParity_RemoteTimeline_LegacyFallback: a remote chain with NO recorded
// revisions still yields one current-terms entry (graceful degradation).
func TestParity_RemoteTimeline_LegacyFallback(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	model.SetOwnRouting("111")

	remote := &fakeRemoteOfferGetter{byID: map[uint64]*model.OTCOffer{
		900: remoteMirrorRow(900, peerSellerRouting, "foreign-7", "222", "client-3", "ACME", "open"),
	}}
	peer := &fakePeerNegLister{rows: []model.OTCNegotiation{
		peerRowWithParent(56, ownRouting, "client-7", peerSellerRouting, "client-3", "ongoing", peerSellerRouting, "foreign-7"),
	}}
	h, db := newListingViewsFixture(t, ownRouting, remote, peer)
	seedRemoteOfferRow(t, db, 900, peerSellerRouting, "foreign-7", "client-3")
	// No revisions seeded for chain 56.

	resp, err := h.GetOfferTimeline(context.Background(), &stockpb.GetOfferTimelineRequest{
		ParentOfferId: 900, CallerOwnerType: "client", CallerOwnerId: 7,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.GetTimeline()) != 1 {
		t.Fatalf("want 1 fallback entry for a chain with no revisions, got %d", len(resp.GetTimeline()))
	}
}

// newRevisionsFixture builds a handler wired for the /revisions remote path:
// the negotiation service (LOCAL lookup), the remote-neg ops (GetRemoteNegByID),
// and a peer dispatcher (so resolveRemoteNegAction is reachable).
func newRevisionsFixture(t *testing.T) (*OTCOptionsHandler, *gorm.DB) {
	t.Helper()
	model.SetOwnRouting("111")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.OTCOffer{}, &model.OTCNegotiation{}, &model.OTCNegotiationRevision{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	offerRepo := repository.NewOTCOfferRepository(db)
	negRepo := repository.NewOTCNegotiationRepository(db)
	negSvc := service.NewOTCNegotiationService(db, offerRepo, negRepo)
	h := NewOTCOptionsHandler(nil, nil).
		WithNegotiations(negSvc).
		WithPeerContracts(nil, 111).
		WithRemoteNegOps(negRepo).
		WithPeerOTCDispatch(&fakePeerDispatcher{routing: 222, foreignID: "foreign-7"}, negRepo, nil)
	return h, db
}

// TestParity_RemoteRevisionsEndpoint: the hosted party can read a remote chain's
// full /revisions history (with wire ids); a non-party gets NotFound.
func TestParity_RemoteRevisionsEndpoint(t *testing.T) {
	h, db := newRevisionsFixture(t)

	// A remote chain WE host as the buyer (client-7); peer 222 hosts the seller.
	row := peerRowWithParent(77, 111, "client-7", 222, "client-3", "ongoing", 222, "foreign-7")
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed remote chain: %v", err)
	}
	base := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	seedRemoteRev(t, db, row.ID, 1, model.OTCNegotiationActionBid, "buyer", "client-7", 5, base)
	seedRemoteRev(t, db, row.ID, 2, model.OTCNegotiationActionCounter, "seller", "client-3", 7, base.Add(time.Minute))

	// The hosted buyer (client-7) sees the full history.
	resp, err := h.ListNegotiationRevisions(context.Background(), &stockpb.ListNegotiationRevisionsRequest{
		NegotiationId: row.ID, CallerOwnerType: "client", CallerOwnerId: 7,
	})
	if err != nil {
		t.Fatalf("hosted party must read remote revisions, got err: %v", err)
	}
	if len(resp.GetRevisions()) != 2 {
		t.Fatalf("want 2 revisions, got %d", len(resp.GetRevisions()))
	}
	if resp.GetRevisions()[1].GetActionByWireId() != "client-3" {
		t.Errorf("rev 2 wire_id=%q want client-3", resp.GetRevisions()[1].GetActionByWireId())
	}

	// A non-party client gets NotFound (existence must not leak).
	_, err = h.ListNegotiationRevisions(context.Background(), &stockpb.ListNegotiationRevisionsRequest{
		NegotiationId: row.ID, CallerOwnerType: "client", CallerOwnerId: 99,
	})
	if st, _ := status.FromError(err); st == nil || st.Code() != codes.NotFound {
		t.Fatalf("non-party must get NotFound, got %v", err)
	}
}
