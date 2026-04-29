package handler_test

import (
	"context"
	"testing"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/handler"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/exbanka/transaction-service/internal/sitx"
	"github.com/glebarez/sqlite"
	"google.golang.org/grpc"
	"gorm.io/gorm"
)

type stubAccountForHandler struct {
	getFn     func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error)
	reserveFn func(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error)
	commitFn  func(ctx context.Context, in *accountpb.CommitIncomingRequest, opts ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error)
	releaseFn func(ctx context.Context, in *accountpb.ReleaseIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error)
	updateFn  func(ctx context.Context, in *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error)
}

func (s *stubAccountForHandler) GetAccountByNumber(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.getFn != nil {
		return s.getFn(ctx, in, opts...)
	}
	return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
}
func (s *stubAccountForHandler) ReserveIncoming(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
	if s.reserveFn != nil {
		return s.reserveFn(ctx, in, opts...)
	}
	return &accountpb.ReserveIncomingResponse{ReservationKey: in.ReservationKey}, nil
}
func (s *stubAccountForHandler) CommitIncoming(ctx context.Context, in *accountpb.CommitIncomingRequest, opts ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error) {
	if s.commitFn != nil {
		return s.commitFn(ctx, in, opts...)
	}
	return &accountpb.CommitIncomingResponse{}, nil
}
func (s *stubAccountForHandler) ReleaseIncoming(ctx context.Context, in *accountpb.ReleaseIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
	if s.releaseFn != nil {
		return s.releaseFn(ctx, in, opts...)
	}
	return &accountpb.ReleaseIncomingResponse{}, nil
}
func (s *stubAccountForHandler) UpdateBalance(ctx context.Context, in *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.updateFn != nil {
		return s.updateFn(ctx, in, opts...)
	}
	return &accountpb.AccountResponse{AccountNumber: in.AccountNumber}, nil
}

func newPeerTxHandler(t *testing.T) (*handler.PeerTxGRPCHandler, *gorm.DB, *stubAccountForHandler) {
	t.Helper()
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err := db.AutoMigrate(&model.PeerIdempotenceRecord{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	stub := &stubAccountForHandler{}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	return handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, nil, nil, nil, 111), db, stub
}

func TestHandleNewTx_HappyPath_YES(t *testing.T) {
	h, _, _ := newPeerTxHandler(t)
	resp, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k1"},
		PeerBankCode:   "222",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountId: "222000001", AssetId: "RSD", Amount: "100.00", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountId: "111000001", AssetId: "RSD", Amount: "100.00", Direction: "CREDIT"},
		},
	})
	if err != nil {
		t.Fatalf("HandleNewTx: %v", err)
	}
	if resp.GetType() != contractsitx.VoteYes {
		t.Errorf("expected YES, got %+v", resp)
	}
	if resp.GetTransactionId() == "" {
		t.Errorf("expected transaction_id")
	}
}

func TestHandleNewTx_Unbalanced_NO(t *testing.T) {
	h, _, _ := newPeerTxHandler(t)
	resp, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k2"},
		PeerBankCode:   "222",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountId: "222000001", AssetId: "RSD", Amount: "100.00", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountId: "111000001", AssetId: "RSD", Amount: "90.00", Direction: "CREDIT"},
		},
	})
	if err != nil {
		t.Fatalf("HandleNewTx: %v", err)
	}
	if resp.GetType() != contractsitx.VoteNo {
		t.Errorf("expected NO, got %+v", resp)
	}
}

func TestHandleNewTx_Replay_ReturnsCachedResponse(t *testing.T) {
	h, _, _ := newPeerTxHandler(t)
	in := &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k3"},
		PeerBankCode:   "222",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountId: "222000001", AssetId: "RSD", Amount: "100.00", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountId: "111000001", AssetId: "RSD", Amount: "100.00", Direction: "CREDIT"},
		},
	}
	r1, err := h.HandleNewTx(context.Background(), in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	r2, err := h.HandleNewTx(context.Background(), in)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if r1.GetTransactionId() != r2.GetTransactionId() {
		t.Errorf("replay should return same transaction_id: %s vs %s", r1.GetTransactionId(), r2.GetTransactionId())
	}
}

func TestHandleCommitTx_AfterYes(t *testing.T) {
	h, _, stub := newPeerTxHandler(t)
	called := false
	stub.commitFn = func(ctx context.Context, in *accountpb.CommitIncomingRequest, opts ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error) {
		called = true
		if in.ReservationKey != "222:k4" {
			t.Errorf("reservation key: %q", in.ReservationKey)
		}
		return &accountpb.CommitIncomingResponse{}, nil
	}
	_, _ = h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k4"},
		PeerBankCode:   "222",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountId: "222000001", AssetId: "RSD", Amount: "100.00", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountId: "111000001", AssetId: "RSD", Amount: "100.00", Direction: "CREDIT"},
		},
	})
	if _, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k4"},
		PeerBankCode:   "222",
	}); err != nil {
		t.Fatalf("HandleCommitTx: %v", err)
	}
	if !called {
		t.Errorf("expected CommitIncoming call")
	}
}

func TestHandleRollbackTx_AfterYes(t *testing.T) {
	h, _, stub := newPeerTxHandler(t)
	called := false
	stub.releaseFn = func(ctx context.Context, in *accountpb.ReleaseIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
		called = true
		return &accountpb.ReleaseIncomingResponse{}, nil
	}
	_, _ = h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k5"},
		PeerBankCode:   "222",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountId: "222000001", AssetId: "RSD", Amount: "100.00", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountId: "111000001", AssetId: "RSD", Amount: "100.00", Direction: "CREDIT"},
		},
	})
	if _, err := h.HandleRollbackTx(context.Background(), &transactionpb.SiTxRollbackRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k5"},
		PeerBankCode:   "222",
	}); err != nil {
		t.Fatalf("HandleRollbackTx: %v", err)
	}
	if !called {
		t.Errorf("expected ReleaseIncoming call")
	}
}
