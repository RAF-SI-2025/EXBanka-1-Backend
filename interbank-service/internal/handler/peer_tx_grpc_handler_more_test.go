package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/interbank-service/internal/handler"
	"github.com/exbanka/interbank-service/internal/model"
	"github.com/exbanka/interbank-service/internal/repository"
	"github.com/exbanka/interbank-service/internal/sitx"
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
func (handlerHoldingChecker) ValidatePeerOptionMoneyLeg(ctx context.Context, in *stockpb.ValidatePeerOptionMoneyLegRequest, opts ...grpc.CallOption) (*stockpb.ValidatePeerOptionMoneyLegResponse, error) {
	return &stockpb.ValidatePeerOptionMoneyLegResponse{Ok: true}, nil
}
func (handlerHoldingChecker) LookupPeerOptionContract(ctx context.Context, in *stockpb.LookupPeerOptionContractRequest, opts ...grpc.CallOption) (*stockpb.LookupPeerOptionContractResponse, error) {
	return &stockpb.LookupPeerOptionContractResponse{Found: false}, nil
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
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: ""},
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
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "ghost"},
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
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "no-vote"},
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "A", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "B", AssetType: "MONAS", AssetId: "RSD", Amount: "50", Direction: "CREDIT"},
		},
	})
	if err != nil {
		t.Fatalf("setup NEW_TX: %v", err)
	}
	_, err = h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "no-vote"},
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "no-vote"},
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
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "k-nf"},
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "CREDIT"},
		},
	})
	if err != nil {
		t.Fatalf("setup NEW_TX: %v", err)
	}
	if _, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k-nf"},
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "k-nf"},
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
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "k-int"},
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "CREDIT"},
		},
	})
	_, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k-int"},
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "k-int"},
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
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: ""},
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
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "ghost"},
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
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "k-rb"},
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111-A", AssetType: "MONAS", AssetId: "RSD", Amount: "75", Direction: "DEBIT"},
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222-B", AssetType: "MONAS", AssetId: "RSD", Amount: "75", Direction: "CREDIT"},
		},
	})
	if err != nil {
		t.Fatalf("NEW_TX: %v", err)
	}
	if _, err := h.HandleRollbackTx(context.Background(), &transactionpb.SiTxRollbackRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k-rb"},
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "k-rb"},
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
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "k-rb-int"},
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "CREDIT"},
		},
	})
	_, err := h.HandleRollbackTx(context.Background(), &transactionpb.SiTxRollbackRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k-rb-int"},
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "k-rb-int"},
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
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, nil, nil, nil, 111, 5*time.Second)

	rec := &stubOptionRecorder{}
	h.SetOptionRecorder(rec)

	// NEW_TX with option-asset postings on our routing. Money postings
	// avoid participant-id resolution so the default stub works.
	optDesc := `{"ticker":"AAPL","amount":1,"intent":"accept"}`
	_, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "opt-1"},
		PeerBankCode:   "222",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "opt-1"},
		Postings: []*transactionpb.SiTxPosting{
			// Money legs — concrete account numbers so they bypass
			// participant-id resolution. Both balance per assetId.
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111-pay", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222-pay", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "CREDIT"},
			// Option legs — the executor on routing 111 is the buyer (CREDIT).
			{RoutingNumber: 222, AccountType: "PERSON", AccountId: "client-2", AssetType: "OPTION", AssetId: optDesc, Amount: "1", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "PERSON", AccountId: "client-1", AssetType: "OPTION", AssetId: optDesc, Amount: "1", Direction: "CREDIT"},
		},
	})
	if err != nil {
		t.Fatalf("NEW_TX: %v", err)
	}
	if _, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "opt-1"},
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "opt-1"},
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

// exerciseHoldingChecker is a holding checker that reports a found seller-side
// contract for LookupPeerOptionContract (the seller bank's exercise path).
type exerciseHoldingChecker struct {
	handlerHoldingChecker
	lookup *stockpb.LookupPeerOptionContractResponse
}

func (e exerciseHoldingChecker) LookupPeerOptionContract(ctx context.Context, in *stockpb.LookupPeerOptionContractRequest, opts ...grpc.CallOption) (*stockpb.LookupPeerOptionContractResponse, error) {
	if e.lookup != nil {
		return e.lookup, nil
	}
	return &stockpb.LookupPeerOptionContractResponse{Found: false}, nil
}

// TestHandleCommitTx_ExerciseSeller_RoutesToExerciseSettlement verifies that the
// seller bank, given the spec exercise pseudo-account NEW_TX, votes YES (crediting
// the seller's money account + emitting an exercise_seller item) and that COMMIT
// calls RecordOptionContract with Intent=exercise, Direction=DEBIT — driving the
// existing recordOptionExercise DEBIT branch (consume reserved shares + mark used).
func TestHandleCommitTx_ExerciseSeller_RoutesToExerciseSettlement(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err := db.AutoMigrate(&model.PeerIdempotenceRecord{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Seller's money account resolves via ListAccountsByClient("client-3").
	stub := &stubAccountForHandler{
		listFn: func(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
			return &accountpb.ListAccountsResponse{Accounts: []*accountpb.AccountResponse{
				{AccountNumber: "222000999", CurrencyCode: "RSD", Status: "active"},
			}}, nil
		},
	}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	exec := sitx.NewPostingExecutor(stub, 222) // seller bank
	exec.SetHoldingChecker(exerciseHoldingChecker{
		lookup: &stockpb.LookupPeerOptionContractResponse{
			Found: true, SellerId: "client-3", Ticker: "WMT", StrikePrice: "50",
			Quantity: 10, Currency: "RSD", SettlementDate: "2999-12-31T00:00:00+02:00", Status: "active",
		},
	})
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, nil, nil, nil, 222, 5*time.Second)
	rec := &stubOptionRecorder{}
	h.SetOptionRecorder(rec)

	// The spec exercise pseudo-account NEW_TX (buyer at 111, seller bank = us 222,
	// negotiationId {111,"neg-1"}). Strike 50 x 10 = 500.
	if _, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "ex-1"},
		PeerBankCode:   "111",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 111, Id: "ex-1"},
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111000117810858011", AssetType: "MONAS", AssetId: "RSD", Amount: "500", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "OPTION", AccountId: "neg-1", AssetType: "MONAS", AssetId: "RSD", Amount: "500", Direction: "CREDIT"},
			{RoutingNumber: 111, AccountType: "OPTION", AccountId: "neg-1", AssetType: "STOCK", AssetId: "WMT", Amount: "10", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "PERSON", AccountId: "client-1", AssetType: "STOCK", AssetId: "WMT", Amount: "10", Direction: "CREDIT"},
		},
	}); err != nil {
		t.Fatalf("NEW_TX: %v", err)
	}
	if _, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "ex-1"},
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 111, Id: "ex-1"},
		PeerBankCode:   "111",
	}); err != nil {
		t.Fatalf("COMMIT_TX: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 RecordOptionContract call, got %d", len(rec.calls))
	}
	if rec.calls[0].GetIntent() != contractsitx.OptionIntentExercise {
		t.Errorf("intent = %q, want %q", rec.calls[0].GetIntent(), contractsitx.OptionIntentExercise)
	}
	if rec.calls[0].GetDirection() != contractsitx.DirectionDebit {
		t.Errorf("direction = %q, want DEBIT", rec.calls[0].GetDirection())
	}
	// The reconstructed option JSON must carry the negotiationId so
	// recordOptionExercise can look up the seller-side contract.
	if !strings.Contains(rec.calls[0].GetOptionDescriptionJson(), "neg-1") {
		t.Errorf("option JSON missing negotiationId: %q", rec.calls[0].GetOptionDescriptionJson())
	}
}

// TestHandleCommitTx_ExerciseBuyer_RoutesToExerciseSettlement verifies the buyer
// bank (sender path) emits an exercise_buyer item that COMMIT routes to
// RecordOptionContract Intent=exercise Direction=CREDIT.
func TestHandleCommitTx_ExerciseBuyer_RoutesToExerciseSettlement(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err := db.AutoMigrate(&model.PeerIdempotenceRecord{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	stub := &stubAccountForHandler{}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111) // buyer bank
	// Buyer bank: lookup found=false; peerBankCode == own routing (sender) so the
	// pseudo legs are SKIPPED, not voted NO.
	exec.SetHoldingChecker(exerciseHoldingChecker{})
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, nil, nil, nil, 111, 5*time.Second)
	rec := &stubOptionRecorder{}
	h.SetOptionRecorder(rec)

	// Sender's local reserve uses peerBankCode == own routing ("111").
	if _, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "exb-1"},
		PeerBankCode:   "111",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 111, Id: "exb-1"},
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111000117810858011", AssetType: "MONAS", AssetId: "RSD", Amount: "500", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "OPTION", AccountId: "neg-1", AssetType: "MONAS", AssetId: "RSD", Amount: "500", Direction: "CREDIT"},
			{RoutingNumber: 111, AccountType: "OPTION", AccountId: "neg-1", AssetType: "STOCK", AssetId: "WMT", Amount: "10", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "PERSON", AccountId: "client-1", AssetType: "STOCK", AssetId: "WMT", Amount: "10", Direction: "CREDIT"},
		},
	}); err != nil {
		t.Fatalf("NEW_TX: %v", err)
	}
	if _, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "exb-1"},
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 111, Id: "exb-1"},
		PeerBankCode:   "111",
	}); err != nil {
		t.Fatalf("COMMIT_TX: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 RecordOptionContract call, got %d", len(rec.calls))
	}
	if rec.calls[0].GetIntent() != contractsitx.OptionIntentExercise {
		t.Errorf("intent = %q, want %q", rec.calls[0].GetIntent(), contractsitx.OptionIntentExercise)
	}
	if rec.calls[0].GetDirection() != contractsitx.DirectionCredit {
		t.Errorf("direction = %q, want CREDIT", rec.calls[0].GetDirection())
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
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, nil, nil, nil, 111, 5*time.Second)
	rec := &stubOptionRecorder{}
	h.SetOptionRecorder(rec)

	// NEW_TX: option DEBIT on our routing 111 = WE hold the seller.
	optDesc := `{"ticker":"AAPL","amount":1,"intent":"accept"}`
	if _, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "rb-shares"},
		PeerBankCode:   "222",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "rb-shares"},
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222-pay", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111-pay", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "CREDIT"},
			{RoutingNumber: 111, AccountType: "PERSON", AccountId: "client-7", AssetType: "OPTION", AssetId: optDesc, Amount: "1", Direction: "DEBIT"},
			{RoutingNumber: 222, AccountType: "PERSON", AccountId: "client-8", AssetType: "OPTION", AssetId: optDesc, Amount: "1", Direction: "CREDIT"},
		},
	}); err != nil {
		t.Fatalf("NEW_TX: %v", err)
	}
	if _, err := h.HandleRollbackTx(context.Background(), &transactionpb.SiTxRollbackRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "rb-shares"},
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "rb-shares"},
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
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, nil, nil, nil, 111, 5*time.Second)
	h.SetOptionRecorder(&stubOptionRecorder{err: errors.New("recorder boom")})

	optDesc := `{"ticker":"AAPL","amount":1}`
	_, _ = h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "opt-err"},
		PeerBankCode:   "222",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "opt-err"},
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111-pay", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222-pay", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "CREDIT"},
			{RoutingNumber: 222, AccountType: "PERSON", AccountId: "client-2", AssetType: "OPTION", AssetId: optDesc, Amount: "1", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "PERSON", AccountId: "client-1", AssetType: "OPTION", AssetId: optDesc, Amount: "1", Direction: "CREDIT"},
		},
	})
	_, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "opt-err"},
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "opt-err"},
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
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111, 5*time.Second)
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
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111, 5*time.Second)
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
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111, 5*time.Second)
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
			_, _ = w.Write([]byte(`{"vote":"YES"}`))
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
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111, 5*time.Second)

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
		_, _ = w.Write([]byte(`{"vote":"NO","reasons":[{"reason":"INSUFFICIENT_ASSET"}]}`))
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
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111, 5*time.Second)

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
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111-A", AssetType: "MONAS", AssetId: "RSD", Amount: "10", Direction: "DEBIT"},
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222-A", AssetType: "MONAS", AssetId: "RSD", Amount: "10", Direction: "CREDIT"},
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
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111, 5*time.Second)
	_, err := h.InitiateOutboundTxWithPostings(context.Background(), &transactionpb.SiTxInitiateWithPostingsRequest{
		PeerBankCode: "222",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111-A", AssetType: "MONAS", AssetId: "RSD", Amount: "10", Direction: "DEBIT"},
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222-A", AssetType: "MONAS", AssetId: "RSD", Amount: "10", Direction: "CREDIT"},
		},
	})
	if err == nil || status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}
