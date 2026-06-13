// Package repository — REMOTE-negotiation method tests (SP-2a).
//
// These port the retired peer-OTC-negotiation mirror repo tests onto the unified
// OTCNegotiationRepository remote methods. A REMOTE chain is an OTCNegotiation
// row with routing_number != OwnRouting() and the cross-bank parties/offer in
// the Remote* columns.
//
// Setup: sqlite :memory:, OwnRouting = 111.
package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

func newRemoteNegTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	model.SetOwnRouting("111")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.OTCNegotiation{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// remoteNeg builds a REMOTE OTCNegotiation row keyed on (routing, native) with
// the given parties/status and an offer JSON in RemoteOfferJSON. All NOT-NULL /
// ValidateOwner constraints satisfied (OwnerBank + nil bidder).
func remoteNeg(routing int64, native string, buyerRouting int64, buyerID string, sellerRouting int64, sellerID, offerJSON, status string) *model.OTCNegotiation {
	nid := native
	oj := offerJSON
	bR := buyerRouting
	sR := sellerRouting
	bID := buyerID
	sID := sellerID
	now := time.Now().UTC()
	return &model.OTCNegotiation{
		RoutingNumber:             routing,
		NativeID:                  &nid,
		BidderOwnerType:           model.OwnerBank,
		Quantity:                  decimal.NewFromInt(1),
		StrikePrice:               decimal.NewFromInt(1),
		Premium:                   decimal.NewFromInt(1),
		SettlementDate:            now,
		Status:                    status,
		RemoteOfferJSON:           &oj,
		RemoteBuyerRouting:        &bR,
		RemoteBuyerID:             &bID,
		RemoteSellerRouting:       &sR,
		RemoteSellerID:            &sID,
		LastActionByPrincipalType: "system",
		LastActionByOwnerType:     string(model.OwnerBank),
		LastActionAt:              now,
		CreatedAt:                 now,
		UpdatedAt:                 now,
	}
}

func TestRemoteNeg_UpsertAndGet(t *testing.T) {
	db := newRemoteNegTestDB(t)
	r := NewOTCNegotiationRepository(db)

	neg := remoteNeg(222, "neg-1", 222, "client-7", 111, "client-3", `{"ticker":"AAPL","amount":100}`, "ongoing")
	if err := r.UpsertRemoteNeg(neg); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := r.GetRemoteNegByRoutingAndNative(222, "neg-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_, buyerID := remoteBuyerOf(got)
	_, sellerID := remoteSellerOf(got)
	if buyerID != "client-7" || sellerID != "client-3" {
		t.Errorf("got buyer=%q seller=%q", buyerID, sellerID)
	}
}

func TestRemoteNeg_GetNotFound(t *testing.T) {
	db := newRemoteNegTestDB(t)
	r := NewOTCNegotiationRepository(db)
	_, err := r.GetRemoteNegByRoutingAndNative(222, "nope")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected ErrRecordNotFound on missing, got %v", err)
	}
}

// TestRemoteNeg_UpsertIdempotent verifies a second upsert on the same natural
// key (routing, native) updates in place rather than inserting a duplicate.
func TestRemoteNeg_UpsertIdempotent(t *testing.T) {
	db := newRemoteNegTestDB(t)
	r := NewOTCNegotiationRepository(db)

	first := remoteNeg(222, "neg-dup", 222, "b", 111, "s", `{"premium":"100"}`, "ongoing")
	if err := r.UpsertRemoteNeg(first); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	second := remoteNeg(222, "neg-dup", 222, "b2", 111, "s2", `{"premium":"200"}`, "ongoing")
	if err := r.UpsertRemoteNeg(second); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}

	var count int64
	if err := db.Model(&model.OTCNegotiation{}).Where("routing_number = ? AND native_id = ?", 222, "neg-dup").Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row on natural key, got %d (upsert not idempotent)", count)
	}
	got, _ := r.GetRemoteNegByRoutingAndNative(222, "neg-dup")
	if remoteOfferJSON(got) != `{"premium":"200"}` {
		t.Errorf("offer not refreshed by second upsert: %s", remoteOfferJSON(got))
	}
	_, buyerID := remoteBuyerOf(got)
	if buyerID != "b2" {
		t.Errorf("buyer not refreshed: %q", buyerID)
	}
}

func TestRemoteNeg_UpdateOfferAndStatus(t *testing.T) {
	db := newRemoteNegTestDB(t)
	r := NewOTCNegotiationRepository(db)

	_ = r.UpsertRemoteNeg(remoteNeg(222, "neg-2", 222, "b", 111, "s", `{"premium":"100"}`, "ongoing"))

	if err := r.UpdateRemoteNegOffer(222, "neg-2", `{"premium":"200"}`); err != nil {
		t.Fatalf("update offer: %v", err)
	}
	got, _ := r.GetRemoteNegByRoutingAndNative(222, "neg-2")
	if remoteOfferJSON(got) != `{"premium":"200"}` {
		t.Errorf("offer not updated: %s", remoteOfferJSON(got))
	}

	if err := r.UpdateRemoteNegStatus(222, "neg-2", "accepted"); err != nil {
		t.Fatalf("update status: %v", err)
	}
	got, _ = r.GetRemoteNegByRoutingAndNative(222, "neg-2")
	if got.Status != "accepted" {
		t.Errorf("status: %s", got.Status)
	}
}

// TestRemoteNeg_CompareAndSetStatus_Atomic verifies the guarded UPDATE only
// matches when status == from, and that exactly one of two concurrent
// transitions wins (the loser observes RowsAffected == 0).
func TestRemoteNeg_CompareAndSetStatus_Atomic(t *testing.T) {
	db := newRemoteNegTestDB(t)
	r := NewOTCNegotiationRepository(db)
	_ = r.UpsertRemoteNeg(remoteNeg(222, "neg-cas", 222, "b", 111, "s", "{}", "ongoing"))

	// Wrong "from" never matches.
	ok, err := r.CompareAndSetRemoteNegStatus(222, "neg-cas", "accepted", "cancelled")
	if err != nil {
		t.Fatalf("cas wrong-from: %v", err)
	}
	if ok {
		t.Errorf("CAS matched on wrong from-status")
	}

	// First ongoing->accepted wins (matches exactly one row).
	won, err := r.CompareAndSetRemoteNegStatus(222, "neg-cas", "ongoing", "accepted")
	if err != nil {
		t.Fatalf("cas 1: %v", err)
	}
	if !won {
		t.Fatalf("first CAS ongoing->accepted did not win")
	}
	// A SECOND identical CAS must NOT win — the row is no longer "ongoing", so
	// the guarded WHERE matches zero rows (this is the serialisation that stops
	// a double-accept / double-premium-charge).
	won2, err := r.CompareAndSetRemoteNegStatus(222, "neg-cas", "ongoing", "accepted")
	if err != nil {
		t.Fatalf("cas 2: %v", err)
	}
	if won2 {
		t.Errorf("second CAS won; the from-status guard failed to serialise the transition")
	}
	got, _ := r.GetRemoteNegByRoutingAndNative(222, "neg-cas")
	if got.Status != "accepted" {
		t.Errorf("status after CAS = %q, want accepted", got.Status)
	}
}

// TestRemoteNeg_GetByNative_ScopedRemote verifies GetRemoteNegByNative finds a
// remote row by native id alone but never returns a local row with a colliding
// native id.
func TestRemoteNeg_GetByNative_ScopedRemote(t *testing.T) {
	db := newRemoteNegTestDB(t)
	r := NewOTCNegotiationRepository(db)
	_ = r.UpsertRemoteNeg(remoteNeg(222, "neg-bynat", 111, "client-1", 222, "client-9", `{"premium":"35"}`, "ongoing"))

	got, err := r.GetRemoteNegByNative("neg-bynat")
	if err != nil {
		t.Fatalf("get by native: %v", err)
	}
	if got.RoutingNumber == model.OwnRouting() {
		t.Errorf("GetRemoteNegByNative returned a LOCAL row (routing=%d)", got.RoutingNumber)
	}
}

// TestRemoteNeg_ListBySellerAndParent_Match ports the Phase-10 cascade match:
// only ongoing remote chains under the same seller AND the same precise parent
// lot come back; distinct parents, free-form (no parent), different sellers,
// and already-cancelled chains are excluded.
func TestRemoteNeg_ListBySellerAndParent_Match(t *testing.T) {
	db := newRemoteNegTestDB(t)
	r := NewOTCNegotiationRepository(db)
	prout := func(i int64) *int64 { return &i }
	pid := func(s string) *string { return &s }

	mk := func(routing int64, native string, sellerRouting int64, sellerID, status string, pr *int64, pi *string) *model.OTCNegotiation {
		n := remoteNeg(routing, native, routing, "client-1", sellerRouting, sellerID, "{}", status)
		n.RemoteParentRouting = pr
		n.RemoteParentNativeID = pi
		return n
	}

	// Listing #100 on seller (111, client-1): two parallel remote bidders.
	_ = r.UpsertRemoteNeg(mk(222, "neg-a", 111, "client-1", "ongoing", prout(111), pid("100")))
	_ = r.UpsertRemoteNeg(mk(333, "neg-b", 111, "client-1", "ongoing", prout(111), pid("100")))
	// Listing #200 on same seller — different parent, must NOT match.
	_ = r.UpsertRemoteNeg(mk(222, "neg-c", 111, "client-1", "ongoing", prout(111), pid("200")))
	// Free-form (no parent) — must NOT match.
	_ = r.UpsertRemoteNeg(mk(333, "neg-d", 111, "client-1", "ongoing", nil, nil))
	// Different seller — must NOT match.
	_ = r.UpsertRemoteNeg(mk(222, "neg-e", 999, "client-7", "ongoing", prout(111), pid("100")))
	// Cancelled on the right group — must NOT match (status filter).
	_ = r.UpsertRemoteNeg(mk(222, "neg-f", 111, "client-1", "cancelled", prout(111), pid("100")))

	got, err := r.ListRemoteNegBySellerAndParent(111, "client-1", 111, "100")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	gotIDs := map[string]bool{}
	for i := range got {
		gotIDs[*got[i].NativeID] = true
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

// TestRemoteNeg_ListByClient_ExcludesLocal seeds a LOCAL routing=own neg and a
// REMOTE one and asserts the remote lister never returns the local row.
func TestRemoteNeg_ListByClient_ExcludesLocal(t *testing.T) {
	db := newRemoteNegTestDB(t)
	r := NewOTCNegotiationRepository(db)

	// LOCAL row (routing stamped to own=111 by BeforeCreate), bidder client-7.
	bidder := uint64(7)
	local := &model.OTCNegotiation{
		ParentOfferID:             1,
		BidderOwnerType:           model.OwnerClient,
		BidderOwnerID:             &bidder,
		Quantity:                  decimal.NewFromInt(1),
		StrikePrice:               decimal.NewFromInt(1),
		Premium:                   decimal.NewFromInt(1),
		SettlementDate:            time.Now().UTC(),
		Status:                    model.OTCNegotiationStatusOpen,
		LastActionByPrincipalType: "client",
		LastActionByPrincipalID:   7,
		LastActionByOwnerType:     "client",
		LastActionByOwnerID:       &bidder,
		LastActionAt:              time.Now().UTC(),
	}
	if err := db.Create(local).Error; err != nil {
		t.Fatalf("seed local: %v", err)
	}
	// REMOTE row where our bank hosts the buyer "client-7".
	_ = r.UpsertRemoteNeg(remoteNeg(222, "neg-remote", 111, "client-7", 222, "client-3", `{"premium":"5"}`, "ongoing"))

	rows, err := r.ListRemoteNegByClient(111, "client-7", "")
	if err != nil {
		t.Fatalf("list by client: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 remote row, got %d (local row leaked?)", len(rows))
	}
	if rows[0].RoutingNumber == model.OwnRouting() {
		t.Errorf("remote lister returned a LOCAL row (routing=%d)", rows[0].RoutingNumber)
	}
	if rows[0].NativeID == nil || *rows[0].NativeID != "neg-remote" {
		t.Errorf("unexpected row returned: %+v", rows[0])
	}

	// Role filter: seller role for client-7 should NOT match (client-7 is the buyer).
	sellerRows, _ := r.ListRemoteNegByClient(111, "client-7", "seller")
	if len(sellerRows) != 0 {
		t.Errorf("seller-role filter returned %d rows for a buyer principal", len(sellerRows))
	}
}

// TestRemoteNeg_ListOngoing_ExcludesLocal verifies ListRemoteNegOngoing returns
// only remote ongoing rows, never a local ongoing chain.
func TestRemoteNeg_ListOngoing_ExcludesLocal(t *testing.T) {
	db := newRemoteNegTestDB(t)
	r := NewOTCNegotiationRepository(db)

	bidder := uint64(1)
	local := &model.OTCNegotiation{
		ParentOfferID:             1,
		BidderOwnerType:           model.OwnerClient,
		BidderOwnerID:             &bidder,
		Quantity:                  decimal.NewFromInt(1),
		StrikePrice:               decimal.NewFromInt(1),
		Premium:                   decimal.NewFromInt(1),
		SettlementDate:            time.Now().UTC(),
		Status:                    model.OTCNegotiationStatusOpen, // "open" — a LOCAL status, never "ongoing"
		LastActionByPrincipalType: "client",
		LastActionByPrincipalID:   1,
		LastActionByOwnerType:     "client",
		LastActionByOwnerID:       &bidder,
		LastActionAt:              time.Now().UTC(),
	}
	if err := db.Create(local).Error; err != nil {
		t.Fatalf("seed local: %v", err)
	}
	_ = r.UpsertRemoteNeg(remoteNeg(222, "neg-on", 222, "b", 111, "s", "{}", "ongoing"))

	rows, err := r.ListRemoteNegOngoing()
	if err != nil {
		t.Fatalf("list ongoing: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 remote ongoing row, got %d", len(rows))
	}
	if rows[0].RoutingNumber == model.OwnRouting() {
		t.Errorf("ListRemoteNegOngoing returned a LOCAL row")
	}
}

// TestRemoteNeg_ListByBankParty matches the cross-bank chains WE host where the
// hosted side is the BANK (party id "employee-<N>"), by prefix — the bank has no
// single wire principal. It must:
//   - "buyer"  → only remote rows with remote_buyer_routing==own AND
//     remote_buyer_id LIKE "employee-%" (our cross-bank bids).
//   - "seller" → only remote rows with remote_seller_routing==own AND
//     remote_seller_id LIKE "employee-%" (peer bids on our bank-owned offer).
//   - ""       → either side.
//
// It must EXCLUDE client-party rows (no "employee-" prefix), peer-hosted rows
// (the bank side hosted by a PEER, routing != own on that side), and never
// return a LOCAL row.
func TestRemoteNeg_ListByBankParty(t *testing.T) {
	db := newRemoteNegTestDB(t)
	r := NewOTCNegotiationRepository(db)
	const own int64 = 111
	const peer int64 = 222

	// (A) WE host the bank as BUYER (our cross-bank bid): buyer routing=own,
	//     buyer id employee-5; counterparty seller on peer.
	_ = r.UpsertRemoteNeg(remoteNeg(peer, "neg-bankbuyer", own, "employee-5", peer, "client-3", `{"premium":"1"}`, "ongoing"))
	// (B) WE host the bank as SELLER (peer bid on our bank-owned offer):
	//     seller routing=own, seller id employee-9; buyer on peer.
	_ = r.UpsertRemoteNeg(remoteNeg(peer, "neg-bankseller", peer, "client-7", own, "employee-9", `{"premium":"2"}`, "ongoing"))
	// (C) WE host a CLIENT buyer (no employee- prefix) — must be excluded from
	//     the bank lister entirely.
	_ = r.UpsertRemoteNeg(remoteNeg(peer, "neg-clientbuyer", own, "client-1", peer, "client-3", `{"premium":"3"}`, "ongoing"))
	// (D) The BANK side is hosted by a PEER (routing != own on the buyer side):
	//     buyer routing=peer, buyer id employee-2. We do NOT host it → exclude.
	_ = r.UpsertRemoteNeg(remoteNeg(333, "neg-peerbank", peer, "employee-2", own, "client-4", `{"premium":"4"}`, "ongoing"))

	// LOCAL row (routing stamped to own by BeforeCreate) — must never leak.
	bidder := uint64(8)
	local := &model.OTCNegotiation{
		ParentOfferID:             1,
		BidderOwnerType:           model.OwnerClient,
		BidderOwnerID:             &bidder,
		Quantity:                  decimal.NewFromInt(1),
		StrikePrice:               decimal.NewFromInt(1),
		Premium:                   decimal.NewFromInt(1),
		SettlementDate:            time.Now().UTC(),
		Status:                    model.OTCNegotiationStatusOpen,
		LastActionByPrincipalType: "client",
		LastActionByPrincipalID:   8,
		LastActionByOwnerType:     "client",
		LastActionByOwnerID:       &bidder,
		LastActionAt:              time.Now().UTC(),
	}
	if err := db.Create(local).Error; err != nil {
		t.Fatalf("seed local: %v", err)
	}

	natives := func(rows []model.OTCNegotiation) map[string]bool {
		m := map[string]bool{}
		for i := range rows {
			if rows[i].RoutingNumber == model.OwnRouting() {
				t.Errorf("bank lister returned a LOCAL row (routing=%d, native=%v)", rows[i].RoutingNumber, rows[i].NativeID)
			}
			if rows[i].NativeID != nil {
				m[*rows[i].NativeID] = true
			}
		}
		return m
	}

	// role="buyer" → only (A).
	buyerRows, err := r.ListRemoteNegByBankParty(own, "buyer")
	if err != nil {
		t.Fatalf("buyer: %v", err)
	}
	bn := natives(buyerRows)
	if len(bn) != 1 || !bn["neg-bankbuyer"] {
		t.Errorf("buyer role = %v, want only {neg-bankbuyer}", bn)
	}

	// role="seller" → only (B).
	sellerRows, err := r.ListRemoteNegByBankParty(own, "seller")
	if err != nil {
		t.Fatalf("seller: %v", err)
	}
	sn := natives(sellerRows)
	if len(sn) != 1 || !sn["neg-bankseller"] {
		t.Errorf("seller role = %v, want only {neg-bankseller}", sn)
	}

	// role="" → both (A) and (B), and NOTHING else.
	bothRows, err := r.ListRemoteNegByBankParty(own, "")
	if err != nil {
		t.Fatalf("both: %v", err)
	}
	en := natives(bothRows)
	if len(en) != 2 || !en["neg-bankbuyer"] || !en["neg-bankseller"] {
		t.Errorf("both role = %v, want {neg-bankbuyer, neg-bankseller}", en)
	}
	// Explicit exclusions: client party + peer-hosted bank party never appear.
	for _, bad := range []string{"neg-clientbuyer", "neg-peerbank"} {
		if en[bad] {
			t.Errorf("bank lister leaked %q (client party or peer-hosted bank side)", bad)
		}
	}
}

// --- small accessors mirroring the handler-package helpers (test-local) ---

func remoteBuyerOf(n *model.OTCNegotiation) (int64, string) {
	var r int64
	var id string
	if n.RemoteBuyerRouting != nil {
		r = *n.RemoteBuyerRouting
	}
	if n.RemoteBuyerID != nil {
		id = *n.RemoteBuyerID
	}
	return r, id
}

func remoteSellerOf(n *model.OTCNegotiation) (int64, string) {
	var r int64
	var id string
	if n.RemoteSellerRouting != nil {
		r = *n.RemoteSellerRouting
	}
	if n.RemoteSellerID != nil {
		id = *n.RemoteSellerID
	}
	return r, id
}

func remoteOfferJSON(n *model.OTCNegotiation) string {
	if n.RemoteOfferJSON != nil {
		return *n.RemoteOfferJSON
	}
	return ""
}
