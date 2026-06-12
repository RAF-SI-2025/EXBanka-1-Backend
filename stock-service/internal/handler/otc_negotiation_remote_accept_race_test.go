// Package handler — concurrency regression for the cross-bank (outbound/seller)
// accept path. acceptRemoteNegotiation previously dispatched the peer GET /accept
// (which forms the contract + moves the premium) BEFORE atomically claiming the
// negotiation, and swallowed the late CAS no-match. Two concurrent accepts of the
// SAME chain therefore both passed a stale "ongoing" read, both dispatched, and
// each formed a contract + credited the premium TWICE (verified live 2026-06-12).
// The fix claims the chain (CAS ongoing→accepted) BEFORE any dispatch and reverts
// the claim if the dispatch/peer/settlement fails. These tests pin that ordering.
package handler

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/repository"
)

// TestAcceptRemoteNegotiation_ClaimsBeforeDispatch proves the claim happens BEFORE
// the money-moving /accept dispatch: at the moment GET /accept fires, the local
// mirror must ALREADY be "accepted". Before the fix it was still "ongoing" at
// dispatch time (claim came after), which is exactly what let a concurrent second
// accept slip through and double-settle.
func TestAcceptRemoteNegotiation_ClaimsBeforeDispatch(t *testing.T) {
	var statusAtDispatch string
	dispatcher := &fakePeerDispatcher{
		proxyByKey: map[string]proxyResult{
			"GET /accept": {resp: []byte(`{"transactionId":"tx-race","status":"accepted"}`), status: 200},
		},
	}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	nid := seedRemoteNeg(t, db, "neg-race", "client-9", "client-77", "")

	// Observe the mirror's persisted status at the instant /accept is dispatched.
	dispatcher.onProxy = func(pc proxyCall) {
		if pc.method == "GET" && pc.subpath == "/accept" {
			row, err := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, "neg-race")
			if err == nil {
				statusAtDispatch = row.Status
			}
		}
	}

	if _, err := h.AcceptNegotiationChain(context.Background(), &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId:       nid,
		CallerOwnerType:     "client",
		CallerOwnerId:       9,
		ActingPrincipalType: "client",
		ActingPrincipalId:   9,
		AcceptorAccountId:   5001,
	}); err != nil {
		t.Fatalf("AcceptNegotiationChain: %v", err)
	}

	if statusAtDispatch != "accepted" {
		t.Fatalf("negotiation status at /accept dispatch = %q, want \"accepted\" — the claim MUST precede the money-moving dispatch so concurrent accepts can't both settle", statusAtDispatch)
	}
}

// TestAcceptRemoteNegotiation_SecondAcceptDoesNotDispatch proves the authoritative
// claim rejects a second accept of an already-accepted chain WITHOUT dispatching a
// second /accept (no second premium movement). A no-match on the CAS → 409.
func TestAcceptRemoteNegotiation_SecondAcceptDoesNotDispatch(t *testing.T) {
	dispatcher := &fakePeerDispatcher{
		proxyByKey: map[string]proxyResult{
			"GET /accept": {resp: []byte(`{"transactionId":"tx-1","status":"accepted"}`), status: 200},
		},
	}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	nid := seedRemoteNeg(t, db, "neg-twice", "client-9", "client-77", "")

	req := &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId:       nid,
		CallerOwnerType:     "client",
		CallerOwnerId:       9,
		ActingPrincipalType: "client",
		ActingPrincipalId:   9,
		AcceptorAccountId:   5001,
	}
	if _, err := h.AcceptNegotiationChain(context.Background(), req); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	dispatchesAfterFirst := len(dispatcher.proxyCalls)

	// The second accept must be rejected and must NOT fire another /accept.
	_, err := h.AcceptNegotiationChain(context.Background(), req)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("second accept code: got %v, want FailedPrecondition", status.Code(err))
	}
	for _, pc := range dispatcher.proxyCalls[dispatchesAfterFirst:] {
		if pc.method == "GET" && pc.subpath == "/accept" {
			t.Errorf("a second GET /accept was dispatched on an already-accepted chain — double-settle risk")
		}
	}
}

// TestAcceptRemoteNegotiation_RevertsClaimOnPeerReject proves the claim is REVERTED
// (accepted→ongoing) when the peer rejects the /accept, so the chain stays
// re-acceptable instead of being stranded "accepted" with no contract.
func TestAcceptRemoteNegotiation_RevertsClaimOnPeerReject(t *testing.T) {
	dispatcher := &fakePeerDispatcher{
		proxyByKey: map[string]proxyResult{
			"GET /accept": {resp: []byte(`{"message":"negotiation is closed"}`), status: 409},
		},
	}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)
	nid := seedRemoteNeg(t, db, "neg-revert", "client-9", "client-77", "")

	_, err := h.AcceptNegotiationChain(context.Background(), &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId:       nid,
		CallerOwnerType:     "client",
		CallerOwnerId:       9,
		ActingPrincipalType: "client",
		ActingPrincipalId:   9,
		AcceptorAccountId:   5001,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("accept against a rejecting peer: got %v, want FailedPrecondition", status.Code(err))
	}

	row, gerr := repository.NewOTCNegotiationRepository(db).GetRemoteNegByRoutingAndNative(222, "neg-revert")
	if gerr != nil {
		t.Fatalf("reload mirror: %v", gerr)
	}
	if row.Status != "ongoing" {
		t.Errorf("mirror status after a rejected peer accept = %q, want \"ongoing\" (claim must be reverted so the chain can be re-accepted)", row.Status)
	}
}
