package handler_test

import (
	"context"
	"testing"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/transaction-service/internal/model"
	"google.golang.org/grpc"
)

// TestReverseOutboundLocal_Transfer_CreditsBackSender verifies that reversing
// a simple-transfer outbound row credits the local DEBIT leg back to the
// sender with the same idempotency key the inline NO-vote path uses, so the
// two paths can never double-credit.
func TestReverseOutboundLocal_Transfer_CreditsBackSender(t *testing.T) {
	h, _, stub := newPeerTxHandler(t) // ownRouting 111

	var updates []*accountpb.UpdateBalanceRequest
	stub.updateFn = func(_ context.Context, in *accountpb.UpdateBalanceRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
		updates = append(updates, in)
		return &accountpb.AccountResponse{}, nil
	}

	row := &model.OutboundPeerTx{
		IdempotenceKey: "idem-1",
		TxKind:         "transfer",
		PostingsJSON:   `[{"routingNumber":111,"accountId":"111-A","assetId":"RSD","amount":"100","direction":"DEBIT"},{"routingNumber":222,"accountId":"222-B","assetId":"RSD","amount":"100","direction":"CREDIT"}]`,
	}
	if err := h.ReverseOutboundLocal(context.Background(), row); err != nil {
		t.Fatalf("reverse: %v", err)
	}

	if len(updates) != 1 {
		t.Fatalf("expected exactly 1 credit-back (local DEBIT leg), got %d", len(updates))
	}
	if updates[0].GetAccountNumber() != "111-A" {
		t.Errorf("credit-back account = %q want 111-A", updates[0].GetAccountNumber())
	}
	if updates[0].GetAmount() != "100" {
		t.Errorf("credit-back amount = %q want 100", updates[0].GetAmount())
	}
	if updates[0].GetIdempotencyKey() != "peer-out-creditback-idem-1" {
		t.Errorf("credit-back key = %q want peer-out-creditback-idem-1", updates[0].GetIdempotencyKey())
	}
}

// TestReverseOutboundLocal_OTC_ReleasesAndCreditsBack verifies that reversing
// an OTC multi-leg outbound row releases the local CREDIT reservation and
// credits back each local DEBIT money leg, using the executor's keys so the
// reversal matches the inline OTC rollback path. Option-asset legs carry no
// money and are skipped.
func TestReverseOutboundLocal_OTC_ReleasesAndCreditsBack(t *testing.T) {
	h, _, stub := newPeerTxHandler(t) // ownRouting 111

	var releases []*accountpb.ReleaseIncomingRequest
	var updates []*accountpb.UpdateBalanceRequest
	stub.releaseFn = func(_ context.Context, in *accountpb.ReleaseIncomingRequest, _ ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
		releases = append(releases, in)
		return &accountpb.ReleaseIncomingResponse{}, nil
	}
	stub.updateFn = func(_ context.Context, in *accountpb.UpdateBalanceRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
		updates = append(updates, in)
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
	if len(updates) != 1 {
		t.Fatalf("expected 1 credit-back (only the local DEBIT money leg), got %d", len(updates))
	}
	if updates[0].GetAccountNumber() != "111-PREM" || updates[0].GetAmount() != "50" {
		t.Errorf("credit-back = %s %s want 111-PREM 50", updates[0].GetAccountNumber(), updates[0].GetAmount())
	}
	if updates[0].GetIdempotencyKey() != "sitx-localcreditback-111:idem-otc:0" {
		t.Errorf("credit-back key = %q want sitx-localcreditback-111:idem-otc:0", updates[0].GetIdempotencyKey())
	}
}
