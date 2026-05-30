package handler_test

import (
	"context"
	"testing"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/transaction-service/internal/model"
	"google.golang.org/grpc"
)

// TestReverseOutboundLocal_Payment_ReleasesSenderHold verifies that reversing
// a "payment" outbound row (the kind InitiateOutboundTx actually creates)
// releases the sender's single outgoing HOLD "peer-out:<idem>" with the same
// idempotency key the inline NO-vote path uses, so the two paths can never
// double-act. Under reserve-then-settle the money never left, so this is a
// hold release (no Balance movement), not a credit-back.
//
// Regression: rows are created with tx_kind="payment", but the recovery branch
// previously matched only ""/"transfer" — so payment rows fell into the
// per-posting executor branch and the real peer-out hold was never released.
func TestReverseOutboundLocal_Payment_ReleasesSenderHold(t *testing.T) {
	h, _, stub := newPeerTxHandler(t) // ownRouting 111

	var releases []*accountpb.ReleaseOutgoingRequest
	stub.releaseOutFn = func(_ context.Context, in *accountpb.ReleaseOutgoingRequest, _ ...grpc.CallOption) (*accountpb.ReleaseOutgoingResponse, error) {
		releases = append(releases, in)
		return &accountpb.ReleaseOutgoingResponse{Released: true}, nil
	}
	stub.updateFn = func(_ context.Context, in *accountpb.UpdateBalanceRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
		t.Errorf("UpdateBalance must NOT be called under reserve-then-settle; got %q", in.GetAmount())
		return &accountpb.AccountResponse{}, nil
	}

	row := &model.OutboundPeerTx{
		IdempotenceKey: "idem-1",
		TxKind:         "payment",
		PostingsJSON:   `[{"routingNumber":111,"accountId":"111-A","assetId":"RSD","amount":"100","direction":"DEBIT"},{"routingNumber":222,"accountId":"222-B","assetId":"RSD","amount":"100","direction":"CREDIT"}]`,
	}
	if err := h.ReverseOutboundLocal(context.Background(), row); err != nil {
		t.Fatalf("reverse: %v", err)
	}

	if len(releases) != 1 {
		t.Fatalf("expected exactly 1 hold release, got %d", len(releases))
	}
	if releases[0].GetReservationKey() != "peer-out:idem-1" {
		t.Errorf("release key = %q want peer-out:idem-1", releases[0].GetReservationKey())
	}
	if releases[0].GetIdempotencyKey() != "peer-out-release-idem-1" {
		t.Errorf("release idem key = %q want peer-out-release-idem-1", releases[0].GetIdempotencyKey())
	}
}

// TestCommitOutboundLocal_Payment_SettlesSenderHold verifies the commit-side
// recovery: a "payment" row settles the single outgoing hold (the money leaves)
// rather than falling into the per-posting executor branch. This is the exact
// path the OutboundReplayCron / PeerTxReconciler take when they resolve a
// crash-stranded payment row to committed.
func TestCommitOutboundLocal_Payment_SettlesSenderHold(t *testing.T) {
	h, _, stub := newPeerTxHandler(t) // ownRouting 111

	var settles []*accountpb.SettleOutgoingRequest
	stub.settleOutFn = func(_ context.Context, in *accountpb.SettleOutgoingRequest, _ ...grpc.CallOption) (*accountpb.SettleOutgoingResponse, error) {
		settles = append(settles, in)
		return &accountpb.SettleOutgoingResponse{}, nil
	}
	stub.updateFn = func(_ context.Context, in *accountpb.UpdateBalanceRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
		t.Errorf("UpdateBalance must NOT be called under reserve-then-settle; got %q", in.GetAmount())
		return &accountpb.AccountResponse{}, nil
	}

	row := &model.OutboundPeerTx{
		IdempotenceKey: "idem-pay-1",
		TxKind:         "payment",
		PostingsJSON:   `[{"routingNumber":111,"accountId":"111-A","assetId":"RSD","amount":"100","direction":"DEBIT"},{"routingNumber":222,"accountId":"222-B","assetId":"RSD","amount":"100","direction":"CREDIT"}]`,
	}
	if err := h.CommitOutboundLocal(context.Background(), row); err != nil {
		t.Fatalf("commit local: %v", err)
	}

	if len(settles) != 1 {
		t.Fatalf("expected exactly 1 hold settle, got %d", len(settles))
	}
	if settles[0].GetReservationKey() != "peer-out:idem-pay-1" {
		t.Errorf("settle key = %q want peer-out:idem-pay-1", settles[0].GetReservationKey())
	}
	if settles[0].GetIdempotencyKey() != "peer-out-settle-idem-pay-1" {
		t.Errorf("settle idem key = %q want peer-out-settle-idem-pay-1", settles[0].GetIdempotencyKey())
	}
}

// TestReverseOutboundLocal_OTC_ReleasesReservationAndHolds verifies that
// reversing an OTC multi-leg outbound row releases the local CREDIT reservation
// (ReleaseIncoming) and releases each local DEBIT money HOLD (ReleaseOutgoing),
// using the executor's keys so the reversal matches the inline OTC rollback
// path. Option-asset legs carry no money and are skipped.
func TestReverseOutboundLocal_OTC_ReleasesReservationAndHolds(t *testing.T) {
	h, _, stub := newPeerTxHandler(t) // ownRouting 111

	var releases []*accountpb.ReleaseIncomingRequest
	var releaseOuts []*accountpb.ReleaseOutgoingRequest
	stub.releaseFn = func(_ context.Context, in *accountpb.ReleaseIncomingRequest, _ ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
		releases = append(releases, in)
		return &accountpb.ReleaseIncomingResponse{}, nil
	}
	stub.releaseOutFn = func(_ context.Context, in *accountpb.ReleaseOutgoingRequest, _ ...grpc.CallOption) (*accountpb.ReleaseOutgoingResponse, error) {
		releaseOuts = append(releaseOuts, in)
		return &accountpb.ReleaseOutgoingResponse{Released: true}, nil
	}
	stub.updateFn = func(_ context.Context, in *accountpb.UpdateBalanceRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
		t.Errorf("UpdateBalance must NOT be called under reserve-then-settle; got %q", in.GetAmount())
		return &accountpb.AccountResponse{}, nil
	}

	row := &model.OutboundPeerTx{
		IdempotenceKey: "idem-otc",
		TxKind:         "otc-accept",
		// Local premium DEBIT (idx 0), local CREDIT money leg (idx 1), and a
		// peer-side leg (idx 2) that must be ignored locally.
		PostingsJSON: `[{"routingNumber":111,"accountId":"111-PREM","assetId":"USD","amount":"50","direction":"DEBIT"},{"routingNumber":111,"accountId":"111-RECV","assetId":"USD","amount":"50","direction":"CREDIT"},{"routingNumber":222,"accountId":"222-X","assetId":"USD","amount":"50","direction":"DEBIT"}]`,
	}
	if err := h.ReverseOutboundLocal(context.Background(), row); err != nil {
		t.Fatalf("reverse: %v", err)
	}

	if len(releases) != 1 {
		t.Fatalf("expected 1 reservation release, got %d", len(releases))
	}
	if releases[0].GetReservationKey() != "111:idem-otc" {
		t.Errorf("release key = %q want 111:idem-otc", releases[0].GetReservationKey())
	}
	if len(releaseOuts) != 1 {
		t.Fatalf("expected 1 outgoing hold release (only the local DEBIT money leg), got %d", len(releaseOuts))
	}
	if releaseOuts[0].GetReservationKey() != "111:idem-otc:0" {
		t.Errorf("hold release key = %q want 111:idem-otc:0", releaseOuts[0].GetReservationKey())
	}
	if releaseOuts[0].GetIdempotencyKey() != "sitx-localrelease-out-111:idem-otc:0" {
		t.Errorf("hold release idem key = %q want sitx-localrelease-out-111:idem-otc:0", releaseOuts[0].GetIdempotencyKey())
	}
}
