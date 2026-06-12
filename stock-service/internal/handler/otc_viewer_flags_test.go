package handler

import (
	"testing"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
)

func vfU64(v uint64) *uint64 { return &v }

// Bidder receiving the OTHER side's latest offer (e.g. owner countered): it is
// the bidder's turn — Accept, Counter, AND Reject are all available, plus
// Withdraw (own chain). Matches "user can accept owner's offer or reject it".
func TestStampNegotiationViewerFlags_BidderAwaiting(t *testing.T) {
	item := &stockpb.OTCNegotiationResponse{Status: "countered"}
	stampNegotiationViewerFlags(item, "bidder", false)
	if item.GetViewerRole() != "bidder" {
		t.Errorf("viewer_role = %q, want bidder", item.GetViewerRole())
	}
	if !item.GetAwaitingViewer() || !item.GetCanAccept() || !item.GetCanCounter() || !item.GetCanReject() {
		t.Error("receiver: awaiting + can_accept + can_counter + can_reject must all be true")
	}
	if !item.GetCanWithdraw() {
		t.Error("bidder can withdraw while live")
	}
	if item.GetLastActionMine() {
		t.Error("bidder: last_action_mine must be false when the other side moved last")
	}
}

// Bidder who JUST bid (last_action_mine): not their turn to accept/reject, but
// they CAN still place a new counter (which supersedes the old bid) and can
// withdraw. Matches "during that time client can only place new counters".
func TestStampNegotiationViewerFlags_BidderJustBid(t *testing.T) {
	item := &stockpb.OTCNegotiationResponse{Status: "open"}
	stampNegotiationViewerFlags(item, "bidder", true)
	if item.GetAwaitingViewer() || item.GetCanAccept() || item.GetCanReject() {
		t.Error("after my own move I cannot accept/reject my own standing offer")
	}
	if !item.GetCanCounter() {
		t.Error("the maker can still place a NEW counter while live (counter is not turn-based)")
	}
	if !item.GetCanWithdraw() || !item.GetLastActionMine() {
		t.Error("can_withdraw + last_action_mine must be true")
	}
}

// Poster receiving a client's bid → Accept/Counter/Reject; never Withdraw.
func TestStampNegotiationViewerFlags_PosterReceivingBid(t *testing.T) {
	item := &stockpb.OTCNegotiationResponse{Status: "open"}
	stampNegotiationViewerFlags(item, "poster", false)
	if !item.GetCanAccept() || !item.GetCanCounter() || !item.GetCanReject() {
		t.Error("poster awaiting: accept/counter/reject must be true")
	}
	if item.GetCanWithdraw() {
		t.Error("poster cannot withdraw (they cancel the listing instead)")
	}
}

// Poster who JUST countered (last_action_mine): cannot accept their own offer,
// no reject of their own offer, but may counter again; never withdraws.
func TestStampNegotiationViewerFlags_PosterJustCountered(t *testing.T) {
	item := &stockpb.OTCNegotiationResponse{Status: "countered"}
	stampNegotiationViewerFlags(item, "poster", true)
	if item.GetCanAccept() || item.GetCanReject() || item.GetAwaitingViewer() {
		t.Error("the maker cannot accept/reject their own standing offer; not awaiting")
	}
	if !item.GetCanCounter() {
		t.Error("poster can still place a new counter while live")
	}
	if item.GetCanWithdraw() {
		t.Error("poster never withdraws a chain")
	}
}

// A terminal chain offers no actions regardless of role/turn.
func TestStampNegotiationViewerFlags_TerminalNoActions(t *testing.T) {
	for _, st := range []string{"accepted", "rejected", "cancelled", "expired"} {
		item := &stockpb.OTCNegotiationResponse{Status: st}
		stampNegotiationViewerFlags(item, "bidder", false)
		if item.GetAwaitingViewer() || item.GetCanAccept() || item.GetCanCounter() || item.GetCanWithdraw() {
			t.Errorf("status %q: no action flags should be set", st)
		}
	}
}

// A non-party viewer (e.g. an employee browsing a client's listing) gets no
// action flags and an empty role.
func TestStampNegotiationViewerFlags_NonParty(t *testing.T) {
	item := &stockpb.OTCNegotiationResponse{Status: "open"}
	stampNegotiationViewerFlags(item, "", true)
	if item.GetViewerRole() != "" {
		t.Errorf("viewer_role = %q, want empty", item.GetViewerRole())
	}
	if item.GetAwaitingViewer() || item.GetCanAccept() || item.GetCanCounter() ||
		item.GetCanReject() || item.GetCanWithdraw() || item.GetLastActionMine() {
		t.Error("non-party viewer: every action flag must be false")
	}
}

func TestOwnerFromPrincipal(t *testing.T) {
	if ot, oid := ownerFromPrincipal("client", 7); ot != model.OwnerClient || oid == nil || *oid != 7 {
		t.Errorf("client → (%v,%v), want (client,7)", ot, oid)
	}
	if ot, oid := ownerFromPrincipal("employee", 5); ot != model.OwnerBank || oid != nil {
		t.Errorf("employee → (%v,%v), want (bank,nil)", ot, oid)
	}
}

func TestStampRevisionViewerFlags_MineAndLatest(t *testing.T) {
	src := []model.OTCNegotiationRevision{{RevisionNumber: 1}, {RevisionNumber: 2}, {RevisionNumber: 3}}
	out := []*stockpb.OTCNegotiationRevisionResponse{{RevisionNumber: 1}, {RevisionNumber: 2}, {RevisionNumber: 3}}
	stampRevisionViewerFlags(out, src, func(r model.OTCNegotiationRevision) bool { return r.RevisionNumber%2 == 0 })
	if out[0].GetMine() || !out[1].GetMine() || out[2].GetMine() {
		t.Error("mine predicate must be applied per (index-aligned) revision")
	}
	if out[0].GetIsLatest() || out[1].GetIsLatest() || !out[2].GetIsLatest() {
		t.Error("is_latest must be ONLY the highest revision_number")
	}
}

// Poster's cross-chain timeline: local entry by the caller, a peer bidder
// ("buyer"), and our own ("seller") entry; is_latest is per chain.
func TestStampTimelineViewerFlags_PosterLocalRemoteMix(t *testing.T) {
	tl := []*stockpb.OTCTimelineEntry{
		{NegotiationId: 1, RevisionNumber: 1, ActionByPrincipalType: "client", ActionByPrincipalId: 7},
		{NegotiationId: 2, RevisionNumber: 1, ActionByPrincipalType: "buyer"},
		{NegotiationId: 2, RevisionNumber: 2, ActionByPrincipalType: "seller"},
	}
	stampTimelineViewerFlags(tl, model.OwnerClient, vfU64(7), "seller")
	if !tl[0].GetMine() {
		t.Error("local entry authored by the caller (client-7) should be mine")
	}
	if tl[1].GetMine() {
		t.Error("remote buyer entry is the peer bidder, not the poster")
	}
	if !tl[2].GetMine() {
		t.Error("remote seller entry is the poster (us) → mine")
	}
	if !tl[0].GetIsLatest() || tl[1].GetIsLatest() || !tl[2].GetIsLatest() {
		t.Error("is_latest must mark the highest revision_number per chain")
	}
}

// On a remote listing the caller is the BIDDER (viewerRemoteRole "buyer"): the
// "buyer" entries are theirs, "seller" (the peer poster) are not.
func TestStampTimelineViewerFlags_BidderOnRemoteListing(t *testing.T) {
	tl := []*stockpb.OTCTimelineEntry{
		{NegotiationId: 9, RevisionNumber: 1, ActionByPrincipalType: "buyer"},
		{NegotiationId: 9, RevisionNumber: 2, ActionByPrincipalType: "seller"},
	}
	stampTimelineViewerFlags(tl, model.OwnerClient, vfU64(3), "buyer")
	if !tl[0].GetMine() || tl[1].GetMine() {
		t.Error("bidder viewer: buyer entries mine, seller entries not")
	}
}
