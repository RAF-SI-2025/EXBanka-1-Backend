package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	accountpb "github.com/exbanka/contract/accountpb"
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

// TestInitiateOutboundTxWithPostings_PeerVotesNO_ReversesAndRollsBackPeer
// covers the peer-NO branch: the executor reserved our local DEBIT hold, the
// peer then voted NO, so the handler must release the local outgoing hold via
// the executor's per-posting key, mark the row rolled_back, and dispatch a
// ROLLBACK_TX so the peer drops any reservation.
func TestInitiateOutboundTxWithPostings_PeerVotesNO_ReversesAndRollsBackPeer(t *testing.T) {
	var sawRollback int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var probe map[string]any
		_ = json.NewDecoder(r.Body).Decode(&probe)
		switch probe["messageType"] {
		case "NEW_TX":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"vote":"NO","reasons":[{"reason":"INSUFFICIENT_ASSET"}]}`))
		case "ROLLBACK_TX":
			atomic.AddInt32(&sawRollback, 1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err := db.AutoMigrate(&model.PeerIdempotenceRecord{}, &model.OutboundPeerTx{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	stub := &stubAccountForHandler{}
	var releasedOut []string
	stub.releaseOutFn = func(_ context.Context, in *accountpb.ReleaseOutgoingRequest, _ ...grpc.CallOption) (*accountpb.ReleaseOutgoingResponse, error) {
		releasedOut = append(releasedOut, in.GetReservationKey())
		return &accountpb.ReleaseOutgoingResponse{Released: true}, nil
	}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	outRepo := repository.NewOutboundPeerTxRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(_ context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111, 5*time.Second)

	resp, err := h.InitiateOutboundTxWithPostings(context.Background(), &transactionpb.SiTxInitiateWithPostingsRequest{
		PeerBankCode: "222",
		TxKind:       "otc-accept",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111-A", AssetType: "MONAS", AssetId: "RSD", Amount: "700", Direction: "DEBIT"},
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222-A", AssetType: "MONAS", AssetId: "RSD", Amount: "700", Direction: "CREDIT"},
		},
	})
	if err != nil {
		t.Fatalf("InitiateOutboundTxWithPostings: %v", err)
	}
	if resp.GetStatus() != "rolled_back" {
		t.Errorf("status = %q, want rolled_back", resp.GetStatus())
	}
	row, _ := outRepo.GetByIdempotenceKey(resp.GetTransactionId())
	if row.Status != "rolled_back" {
		t.Errorf("row status = %q, want rolled_back", row.Status)
	}
	// The local DEBIT hold (posting idx 0) must be released by the executor key.
	if len(releasedOut) != 1 || releasedOut[0] != "111:"+resp.GetTransactionId()+":0" {
		t.Errorf("expected local outgoing hold release on 111:<idem>:0, got %v", releasedOut)
	}
	if atomic.LoadInt32(&sawRollback) == 0 {
		t.Errorf("expected a ROLLBACK_TX dispatched to the peer after NO vote")
	}
}

// TestInitiateOutboundTxWithPostings_OptionRecordFails_LeavesCommitting covers
// the post-YES local-finalisation branch where materialising the sender-side
// option contract fails: the row must stay forward-only `committing` (the cron
// retries the idempotent sequence) and the peer must NOT receive a COMMIT_TX yet.
func TestInitiateOutboundTxWithPostings_OptionRecordFails_LeavesCommitting(t *testing.T) {
	var sawCommit int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var probe map[string]any
		_ = json.NewDecoder(r.Body).Decode(&probe)
		switch probe["messageType"] {
		case "NEW_TX":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"vote":"YES"}`))
		case "COMMIT_TX":
			atomic.AddInt32(&sawCommit, 1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err := db.AutoMigrate(&model.PeerIdempotenceRecord{}, &model.OutboundPeerTx{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	stub := &stubAccountForHandler{}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	outRepo := repository.NewOutboundPeerTxRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(_ context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111, 5*time.Second)
	// Option recorder fails → local materialisation step errors after the YES pivot.
	h.SetOptionRecorder(&stubOptionRecorder{err: status.Error(codes.Unavailable, "stock-service down")})

	resp, err := h.InitiateOutboundTxWithPostings(context.Background(), &transactionpb.SiTxInitiateWithPostingsRequest{
		PeerBankCode: "222",
		TxKind:       "otc-accept",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111-A", AssetType: "MONAS", AssetId: "RSD", Amount: "700", Direction: "DEBIT"},
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222-A", AssetType: "MONAS", AssetId: "RSD", Amount: "700", Direction: "CREDIT"},
			{RoutingNumber: 111, AccountType: "PERSON", AccountId: "client-1", AssetType: "OPTION", AssetId: `{"ticker":"AAPL"}`, Amount: "1", Direction: "CREDIT"},
		},
	})
	if err != nil {
		t.Fatalf("InitiateOutboundTxWithPostings: %v", err)
	}
	if resp.GetStatus() != "committing" {
		t.Errorf("status = %q, want committing (forward-only after YES pivot)", resp.GetStatus())
	}
	row, _ := outRepo.GetByIdempotenceKey(resp.GetTransactionId())
	if row.Status != "committing" {
		t.Errorf("row status = %q, want committing", row.Status)
	}
	if atomic.LoadInt32(&sawCommit) != 0 {
		t.Errorf("COMMIT_TX must NOT be sent when local option-record fails")
	}
}

// TestInitiateOutboundTxWithPostings_LocalReserveFails_FailedPrecondition
// covers the early local-reserve-NO branch: when this bank cannot reserve its
// own leg (account lookup fails), the handler rolls the row back and returns
// FailedPrecondition WITHOUT ever contacting the peer.
func TestInitiateOutboundTxWithPostings_LocalReserveFails_FailedPrecondition(t *testing.T) {
	var peerCalled int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&peerCalled, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err := db.AutoMigrate(&model.PeerIdempotenceRecord{}, &model.OutboundPeerTx{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	stub := &stubAccountForHandler{}
	// Our own leg's account lookup fails → executor votes NO (NO_SUCH_ACCOUNT).
	stub.getFn = func(_ context.Context, _ *accountpb.GetAccountByNumberRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
		return nil, status.Error(codes.NotFound, "account not found")
	}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	outRepo := repository.NewOutboundPeerTxRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(_ context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111, 5*time.Second)

	_, err := h.InitiateOutboundTxWithPostings(context.Background(), &transactionpb.SiTxInitiateWithPostingsRequest{
		PeerBankCode: "222",
		TxKind:       "otc-accept",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111-A", AssetType: "MONAS", AssetId: "RSD", Amount: "700", Direction: "DEBIT"},
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222-A", AssetType: "MONAS", AssetId: "RSD", Amount: "700", Direction: "CREDIT"},
		},
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition on local reserve failure, got %v", err)
	}
	if atomic.LoadInt32(&peerCalled) != 0 {
		t.Errorf("peer must NOT be contacted when the local reserve fails")
	}
}
