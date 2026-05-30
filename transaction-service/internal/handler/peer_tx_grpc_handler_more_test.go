package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/handler"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/exbanka/transaction-service/internal/sitx"
	"github.com/glebarez/sqlite"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// stubOptionRecorder satisfies handler.PeerOptionRecorder for COMMIT_TX
// option-leg materialisation tests.
type stubOptionRecorder struct {
	calls        []*stockpb.RecordOptionContractRequest
	err          error
	releaseCalls []string // crossbank_tx_ids passed to ReleaseSellerSharesForNewTx
	releaseErr   error
}

func (s *stubOptionRecorder) RecordOptionContract(ctx context.Context, in *stockpb.RecordOptionContractRequest, opts ...grpc.CallOption) (*stockpb.RecordOptionContractResponse, error) {
	s.calls = append(s.calls, in)
	if s.err != nil {
		return nil, s.err
	}
	return &stockpb.RecordOptionContractResponse{}, nil
}

func (s *stubOptionRecorder) ReleaseSellerSharesForNewTx(ctx context.Context, in *stockpb.ReleaseSellerSharesRequest, opts ...grpc.CallOption) (*stockpb.ReleaseSellerSharesResponse, error) {
	s.releaseCalls = append(s.releaseCalls, in.GetCrossbankTxId())
	if s.releaseErr != nil {
		return nil, s.releaseErr
	}
	return &stockpb.ReleaseSellerSharesResponse{}, nil
}

// handlerHoldingChecker satisfies sitx.SellerHoldingChecker for handler-level
// tests that need a DEBIT-option leg to vote YES (reserve) at NEW_TX. Always
// ok=true; reserve/release are recorded but the handler-level assertion uses
// the option recorder's release tracking.
type handlerHoldingChecker struct{}

func (handlerHoldingChecker) CheckSellerCanDeliver(ctx context.Context, in *stockpb.CheckSellerCanDeliverRequest, opts ...grpc.CallOption) (*stockpb.CheckSellerCanDeliverResponse, error) {
	return &stockpb.CheckSellerCanDeliverResponse{Ok: true}, nil
}
func (handlerHoldingChecker) ReserveSellerSharesForNewTx(ctx context.Context, in *stockpb.ReserveSellerSharesRequest, opts ...grpc.CallOption) (*stockpb.ReserveSellerSharesResponse, error) {
	return &stockpb.ReserveSellerSharesResponse{Ok: true}, nil
}
func (handlerHoldingChecker) ReleaseSellerSharesForNewTx(ctx context.Context, in *stockpb.ReleaseSellerSharesRequest, opts ...grpc.CallOption) (*stockpb.ReleaseSellerSharesResponse, error) {
	return &stockpb.ReleaseSellerSharesResponse{}, nil
}

// TestHandleNewTx_MissingIdempotenceKey_400 verifies the missing-key
// validation branch.
func TestHandleNewTx_MissingIdempotenceKey_400(t *testing.T) {
	h, _, _ := newPeerTxHandler(t)
	_, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: ""},
		PeerBankCode:   "222",
	})
	if err == nil || status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// TestHandleNewTx_MissingPeerBankCode_400 verifies the same on peer code.
func TestHandleNewTx_MissingPeerBankCode_400(t *testing.T) {
	h, _, _ := newPeerTxHandler(t)
	_, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k"},
		PeerBankCode:   "",
	})
	if err == nil || status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// TestHandleCommitTx_MissingKey_400 verifies CommitTx input validation.
func TestHandleCommitTx_MissingKey_400(t *testing.T) {
	h, _, _ := newPeerTxHandler(t)
	_, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: ""},
		PeerBankCode:   "",
	})
	if err == nil || status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// TestHandleCommitTx_NoNewTxRecord_404 verifies that committing without a
// prior NEW_TX returns NotFound.
func TestHandleCommitTx_NoNewTxRecord_404(t *testing.T) {
	h, _, _ := newPeerTxHandler(t)
	_, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "ghost"},
		PeerBankCode:   "222",
	})
	if err == nil || status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

// TestHandleCommitTx_AfterNoVote_FailedPrecondition verifies that committing
// after a NO vote returns FailedPrecondition.
func TestHandleCommitTx_AfterNoVote_FailedPrecondition(t *testing.T) {
	h, _, _ := newPeerTxHandler(t)
	// Unbalanced postings → BuildPrelimVote returns NO; cacheAndReturn writes
	// a record with TransactionID="".
	_, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "no-vote"},
		PeerBankCode:   "222",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountId: "A", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountId: "B", AssetId: "RSD", Amount: "50", Direction: "CREDIT"},
		},
	})
	if err != nil {
		t.Fatalf("setup NEW_TX: %v", err)
	}
	_, err = h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "no-vote"},
		PeerBankCode:   "222",
	})
	if err == nil || status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", err)
	}
}

// TestHandleCommitTx_NotFoundOnAccount_Benign verifies that account-service
// returning NotFound on CommitIncoming is treated as benign (no CREDIT legs
// landed on this bank).
func TestHandleCommitTx_NotFoundOnAccount_Benign(t *testing.T) {
	h, _, stub := newPeerTxHandler(t)
	stub.commitFn = func(ctx context.Context, in *accountpb.CommitIncomingRequest, opts ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error) {
		return nil, status.Error(codes.NotFound, "no reservation")
	}
	_, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k-nf"},
		PeerBankCode:   "222",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountId: "222000001", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountId: "111000001", AssetId: "RSD", Amount: "100", Direction: "CREDIT"},
		},
	})
	if err != nil {
		t.Fatalf("setup NEW_TX: %v", err)
	}
	if _, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k-nf"},
		PeerBankCode:   "222",
	}); err != nil {
		t.Errorf("expected nil err on NotFound from account, got %v", err)
	}
}

// TestHandleCommitTx_AccountInternalError_500 verifies that a non-NotFound
// gRPC error from account-service surfaces as Internal.
func TestHandleCommitTx_AccountInternalError_500(t *testing.T) {
	h, _, stub := newPeerTxHandler(t)
	stub.commitFn = func(ctx context.Context, in *accountpb.CommitIncomingRequest, opts ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error) {
		return nil, status.Error(codes.Internal, "boom")
	}
	_, _ = h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k-int"},
		PeerBankCode:   "222",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountId: "222000001", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountId: "111000001", AssetId: "RSD", Amount: "100", Direction: "CREDIT"},
		},
	})
	_, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k-int"},
		PeerBankCode:   "222",
	})
	if err == nil || status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", err)
	}
}

// TestHandleRollbackTx_MissingKey_400 verifies the validation branch.
func TestHandleRollbackTx_MissingKey_400(t *testing.T) {
	h, _, _ := newPeerTxHandler(t)
	_, err := h.HandleRollbackTx(context.Background(), &transactionpb.SiTxRollbackRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: ""},
		PeerBankCode:   "",
	})
	if err == nil || status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// TestHandleRollbackTx_NoRecord_Idempotent_NoError verifies that rolling back
// when there's no record is idempotent (no error).
func TestHandleRollbackTx_NoRecord_Idempotent_NoError(t *testing.T) {
	h, _, _ := newPeerTxHandler(t)
	_, err := h.HandleRollbackTx(context.Background(), &transactionpb.SiTxRollbackRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "ghost"},
		PeerBankCode:   "222",
	})
	if err != nil {
		t.Errorf("expected nil err on missing record, got %v", err)
	}
}

// TestHandleRollbackTx_DebitHoldsReleased verifies the rollback path with
// captured DebitedItems releases the same per-posting outgoing holds (no
// Balance movement — reserve-then-settle).
func TestHandleRollbackTx_DebitHoldsReleased(t *testing.T) {
	h, _, stub := newPeerTxHandler(t)
	var releaseOutCalls []*accountpb.ReleaseOutgoingRequest
	stub.releaseOutFn = func(ctx context.Context, in *accountpb.ReleaseOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseOutgoingResponse, error) {
		releaseOutCalls = append(releaseOutCalls, in)
		return &accountpb.ReleaseOutgoingResponse{Released: true}, nil
	}
	stub.updateFn = func(ctx context.Context, in *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
		t.Errorf("UpdateBalance must NOT be called under reserve-then-settle; got %q", in.GetAmount())
		return &accountpb.AccountResponse{}, nil
	}
	// Set up a NEW_TX with one DEBIT on our routing — that creates a DebitedItem
	// (an outgoing hold keyed by its per-posting tag).
	_, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k-rb"},
		PeerBankCode:   "222",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 111, AccountId: "111-A", AssetId: "RSD", Amount: "75", Direction: "DEBIT"},
			{RoutingNumber: 222, AccountId: "222-B", AssetId: "RSD", Amount: "75", Direction: "CREDIT"},
		},
	})
	if err != nil {
		t.Fatalf("NEW_TX: %v", err)
	}
	if _, err := h.HandleRollbackTx(context.Background(), &transactionpb.SiTxRollbackRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k-rb"},
		PeerBankCode:   "222",
	}); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if len(releaseOutCalls) != 1 {
		t.Fatalf("expected 1 ReleaseOutgoing call, got %d", len(releaseOutCalls))
	}
	// The hold is keyed by the per-posting tag "<peer>:<idem>:<index>".
	if releaseOutCalls[0].GetReservationKey() != "222:k-rb:0" {
		t.Errorf("expected release key 222:k-rb:0, got %q", releaseOutCalls[0].GetReservationKey())
	}
}

// TestHandleRollbackTx_ReleaseInternalError_500 verifies non-NotFound errors
// from ReleaseIncoming surface as Internal.
func TestHandleRollbackTx_ReleaseInternalError_500(t *testing.T) {
	h, _, stub := newPeerTxHandler(t)
	stub.releaseFn = func(ctx context.Context, in *accountpb.ReleaseIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
		return nil, status.Error(codes.Internal, "rls boom")
	}
	_, _ = h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k-rb-int"},
		PeerBankCode:   "222",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountId: "222000001", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountId: "111000001", AssetId: "RSD", Amount: "100", Direction: "CREDIT"},
		},
	})
	_, err := h.HandleRollbackTx(context.Background(), &transactionpb.SiTxRollbackRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k-rb-int"},
		PeerBankCode:   "222",
	})
	if err == nil || status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", err)
	}
}

// TestHandleCommitTx_MaterialisesOptions verifies that after vote-YES with
// option postings, COMMIT_TX calls the option recorder once per option leg.
func TestHandleCommitTx_MaterialisesOptions(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err := db.AutoMigrate(&model.PeerIdempotenceRecord{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	stub := &stubAccountForHandler{}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, nil, nil, nil, 111)

	rec := &stubOptionRecorder{}
	h.SetOptionRecorder(rec)

	// NEW_TX with option-asset postings on our routing. Money postings
	// avoid participant-id resolution so the default stub works.
	optDesc := `{"ticker":"AAPL","amount":1,"intent":"accept"}`
	_, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "opt-1"},
		PeerBankCode:   "222",
		Postings: []*transactionpb.SiTxPosting{
			// Money legs — concrete account numbers so they bypass
			// participant-id resolution. Both balance per assetId.
			{RoutingNumber: 111, AccountId: "111-pay", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 222, AccountId: "222-pay", AssetId: "RSD", Amount: "100", Direction: "CREDIT"},
			// Option legs — the executor on routing 111 is the buyer (CREDIT).
			{RoutingNumber: 222, AccountId: "client-2", AssetId: optDesc, Amount: "1", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountId: "client-1", AssetId: optDesc, Amount: "1", Direction: "CREDIT"},
		},
	})
	if err != nil {
		t.Fatalf("NEW_TX: %v", err)
	}
	if _, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "opt-1"},
		PeerBankCode:   "222",
	}); err != nil {
		t.Fatalf("COMMIT_TX: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 RecordOptionContract call, got %d", len(rec.calls))
	}
	if rec.calls[0].CrossbankTxId != "222:opt-1" {
		t.Errorf("crossbank_tx_id: %q", rec.calls[0].CrossbankTxId)
	}
	if rec.calls[0].Intent != "accept" {
		t.Errorf("intent: %q", rec.calls[0].Intent)
	}
}

// TestHandleRollbackTx_ReleasesSellerShareHold verifies that when this bank
// held the seller (a DEBIT option leg on our routing), ROLLBACK_TX releases the
// vote-time share hold via ReleaseSellerSharesForNewTx, keyed on the SI-TX
// identity. This is the asset-side counterpart to the money creditback.
func TestHandleRollbackTx_ReleasesSellerShareHold(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err := db.AutoMigrate(&model.PeerIdempotenceRecord{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	stub := &stubAccountForHandler{}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	// Executor needs a holding checker so the DEBIT-option leg on our routing
	// votes YES (reserves) at NEW_TX. ok=true via the stub.
	exec.SetHoldingChecker(handlerHoldingChecker{})
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, nil, nil, nil, 111)
	rec := &stubOptionRecorder{}
	h.SetOptionRecorder(rec)

	// NEW_TX: option DEBIT on our routing 111 = WE hold the seller.
	optDesc := `{"ticker":"AAPL","amount":1,"intent":"accept"}`
	if _, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "rb-shares"},
		PeerBankCode:   "222",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountId: "222-pay", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountId: "111-pay", AssetId: "RSD", Amount: "100", Direction: "CREDIT"},
			{RoutingNumber: 111, AccountId: "client-7", AssetId: optDesc, Amount: "1", Direction: "DEBIT"},
			{RoutingNumber: 222, AccountId: "client-8", AssetId: optDesc, Amount: "1", Direction: "CREDIT"},
		},
	}); err != nil {
		t.Fatalf("NEW_TX: %v", err)
	}
	if _, err := h.HandleRollbackTx(context.Background(), &transactionpb.SiTxRollbackRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "rb-shares"},
		PeerBankCode:   "222",
	}); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if len(rec.releaseCalls) != 1 {
		t.Fatalf("expected 1 share-release call on rollback, got %d", len(rec.releaseCalls))
	}
	if rec.releaseCalls[0] != "222:rb-shares" {
		t.Errorf("release keyed on %q, want 222:rb-shares", rec.releaseCalls[0])
	}
}

// TestHandleCommitTx_OptionRecorderError_Internal verifies that a recorder
// failure surfaces as Internal.
func TestHandleCommitTx_OptionRecorderError_Internal(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	_ = db.AutoMigrate(&model.PeerIdempotenceRecord{})
	stub := &stubAccountForHandler{}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, nil, nil, nil, 111)
	h.SetOptionRecorder(&stubOptionRecorder{err: errors.New("recorder boom")})

	optDesc := `{"ticker":"AAPL","amount":1}`
	_, _ = h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "opt-err"},
		PeerBankCode:   "222",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 111, AccountId: "111-pay", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 222, AccountId: "222-pay", AssetId: "RSD", Amount: "100", Direction: "CREDIT"},
			{RoutingNumber: 222, AccountId: "client-2", AssetId: optDesc, Amount: "1", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountId: "client-1", AssetId: optDesc, Amount: "1", Direction: "CREDIT"},
		},
	})
	_, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "opt-err"},
		PeerBankCode:   "222",
	})
	if err == nil || status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", err)
	}
}

// TestInitiateOutboundTx_NoDeps_Unimplemented verifies the unconfigured-deps
// guard.
func TestInitiateOutboundTx_NoDeps_Unimplemented(t *testing.T) {
	h, _, _ := newPeerTxHandler(t)
	_, err := h.InitiateOutboundTx(context.Background(), &transactionpb.SiTxInitiateRequest{
		FromAccountNumber: "111-A",
		ToAccountNumber:   "222-A",
		Amount:            "10",
		Currency:          "RSD",
	})
	if err == nil || status.Code(err) != codes.Unimplemented {
		t.Errorf("expected Unimplemented, got %v", err)
	}
}

// TestInitiateOutboundTx_ShortAccount_400 verifies the routing-prefix
// validation.
func TestInitiateOutboundTx_ShortAccount_400(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	_ = db.AutoMigrate(&model.PeerIdempotenceRecord{}, &model.OutboundPeerTx{})
	stub := &stubAccountForHandler{}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	outRepo := repository.NewOutboundPeerTxRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: "http://x", APIToken: "t", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111)
	_, err := h.InitiateOutboundTx(context.Background(), &transactionpb.SiTxInitiateRequest{
		FromAccountNumber: "111-A",
		ToAccountNumber:   "ab",
		Amount:            "10",
		Currency:          "RSD",
	})
	if err == nil || status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// TestInitiateOutboundTx_PeerNotFound_404 verifies the lookup-failure path.
func TestInitiateOutboundTx_PeerNotFound_404(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	_ = db.AutoMigrate(&model.PeerIdempotenceRecord{}, &model.OutboundPeerTx{})
	stub := &stubAccountForHandler{}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	outRepo := repository.NewOutboundPeerTxRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return nil, errors.New("not registered")
	}
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111)
	_, err := h.InitiateOutboundTx(context.Background(), &transactionpb.SiTxInitiateRequest{
		FromAccountNumber: "111-A",
		ToAccountNumber:   "222-B-account",
		Amount:            "10",
		Currency:          "RSD",
	})
	if err == nil || status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

// TestInitiateOutboundTx_BadAmount_400 verifies the amount-parse path.
func TestInitiateOutboundTx_BadAmount_400(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	_ = db.AutoMigrate(&model.PeerIdempotenceRecord{}, &model.OutboundPeerTx{})
	stub := &stubAccountForHandler{}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	outRepo := repository.NewOutboundPeerTxRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: "http://x", APIToken: "t", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111)
	_, err := h.InitiateOutboundTx(context.Background(), &transactionpb.SiTxInitiateRequest{
		FromAccountNumber: "111-A",
		ToAccountNumber:   "222-B-account",
		Amount:            "not-a-number",
		Currency:          "RSD",
	})
	if err == nil || status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// TestInitiateOutboundTx_HappyPath_Yes verifies the simple-transfer outbound
// path on a peer YES vote — debits sender, posts NEW_TX, posts COMMIT_TX,
// marks committed.
func TestInitiateOutboundTx_HappyPath_Yes(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var probe map[string]any
		_ = json.NewDecoder(r.Body).Decode(&probe)
		if probe["messageType"] == contractsitx.MessageTypeNewTx {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"type":"YES"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	_ = db.AutoMigrate(&model.PeerIdempotenceRecord{}, &model.OutboundPeerTx{})
	stub := &stubAccountForHandler{}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	outRepo := repository.NewOutboundPeerTxRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: srv.URL, APIToken: "t", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111)

	resp, err := h.InitiateOutboundTx(context.Background(), &transactionpb.SiTxInitiateRequest{
		FromAccountNumber: "111-A",
		ToAccountNumber:   "222-recipient",
		Amount:            "100",
		Currency:          "RSD",
	})
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	if resp.GetStatus() != "pending" {
		t.Errorf("status: %s", resp.GetStatus())
	}
	if calls < 2 {
		t.Errorf("expected at least 2 HTTP calls (NEW_TX + COMMIT_TX), got %d", calls)
	}
}

// TestInitiateOutboundTx_PeerVotesNO_HoldReleased verifies the NO-vote path
// reserves the sender's funds at NEW_TX and then releases the hold (no money
// ever left) under reserve-then-settle.
func TestInitiateOutboundTx_PeerVotesNO_HoldReleased(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"NO","noVotes":[{"reason":"INSUFFICIENT_ASSET"}]}`))
	}))
	defer srv.Close()

	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	_ = db.AutoMigrate(&model.PeerIdempotenceRecord{}, &model.OutboundPeerTx{})
	stub := &stubAccountForHandler{}
	var reserveKeys, releaseKeys []string
	stub.reserveOutFn = func(ctx context.Context, in *accountpb.ReserveOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReserveOutgoingResponse, error) {
		reserveKeys = append(reserveKeys, in.GetIdempotencyKey())
		return &accountpb.ReserveOutgoingResponse{ReservationKey: in.GetReservationKey()}, nil
	}
	stub.releaseOutFn = func(ctx context.Context, in *accountpb.ReleaseOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseOutgoingResponse, error) {
		releaseKeys = append(releaseKeys, in.GetIdempotencyKey())
		return &accountpb.ReleaseOutgoingResponse{Released: true}, nil
	}
	stub.updateFn = func(ctx context.Context, in *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
		t.Errorf("UpdateBalance must NOT be called under reserve-then-settle; got %q", in.GetAmount())
		return &accountpb.AccountResponse{}, nil
	}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	outRepo := repository.NewOutboundPeerTxRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: srv.URL, APIToken: "t", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111)

	if _, err := h.InitiateOutboundTx(context.Background(), &transactionpb.SiTxInitiateRequest{
		FromAccountNumber: "111-A",
		ToAccountNumber:   "222-recipient",
		Amount:            "50",
		Currency:          "RSD",
	}); err != nil {
		t.Fatalf("initiate: %v", err)
	}
	// One reserve at NEW_TX, one release on the NO vote.
	var foundReserve, foundRelease bool
	for _, k := range reserveKeys {
		if strings.HasPrefix(k, "peer-out-reserve") {
			foundReserve = true
		}
	}
	for _, k := range releaseKeys {
		if strings.HasPrefix(k, "peer-out-release") {
			foundRelease = true
		}
	}
	if !foundReserve || !foundRelease {
		t.Errorf("expected reserve + release keys; reserve=%v release=%v reserveKeys=%v releaseKeys=%v", foundReserve, foundRelease, reserveKeys, releaseKeys)
	}
}

// TestInitiateOutboundTxWithPostings_NoDeps_Unimplemented verifies the
// unconfigured-deps guard for the multi-leg variant.
func TestInitiateOutboundTxWithPostings_NoDeps_Unimplemented(t *testing.T) {
	h, _, _ := newPeerTxHandler(t)
	_, err := h.InitiateOutboundTxWithPostings(context.Background(), &transactionpb.SiTxInitiateWithPostingsRequest{
		PeerBankCode: "222",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 111, AccountId: "111-A", AssetId: "RSD", Amount: "10", Direction: "DEBIT"},
			{RoutingNumber: 222, AccountId: "222-A", AssetId: "RSD", Amount: "10", Direction: "CREDIT"},
		},
	})
	if err == nil || status.Code(err) != codes.Unimplemented {
		t.Errorf("expected Unimplemented, got %v", err)
	}
}

// TestInitiateOutboundTxWithPostings_PeerNotFound_404 verifies lookup failure.
func TestInitiateOutboundTxWithPostings_PeerNotFound_404(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	_ = db.AutoMigrate(&model.PeerIdempotenceRecord{}, &model.OutboundPeerTx{})
	stub := &stubAccountForHandler{}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	outRepo := repository.NewOutboundPeerTxRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return nil, errors.New("not registered")
	}
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111)
	_, err := h.InitiateOutboundTxWithPostings(context.Background(), &transactionpb.SiTxInitiateWithPostingsRequest{
		PeerBankCode: "222",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 111, AccountId: "111-A", AssetId: "RSD", Amount: "10", Direction: "DEBIT"},
			{RoutingNumber: 222, AccountId: "222-A", AssetId: "RSD", Amount: "10", Direction: "CREDIT"},
		},
	})
	if err == nil || status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}
