package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
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

type stubAccountForHandler struct {
	getFn        func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error)
	reserveFn    func(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error)
	commitFn     func(ctx context.Context, in *accountpb.CommitIncomingRequest, opts ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error)
	releaseFn    func(ctx context.Context, in *accountpb.ReleaseIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error)
	updateFn     func(ctx context.Context, in *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error)
	reserveOutFn func(ctx context.Context, in *accountpb.ReserveOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReserveOutgoingResponse, error)
	settleOutFn  func(ctx context.Context, in *accountpb.SettleOutgoingRequest, opts ...grpc.CallOption) (*accountpb.SettleOutgoingResponse, error)
	releaseOutFn func(ctx context.Context, in *accountpb.ReleaseOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseOutgoingResponse, error)

	// reserveGate, when non-nil, blocks ReserveIncoming until a value can be
	// received from it (close it to unblock all waiters). Used to simulate a
	// slow account-service reserve in the 202-async tests.
	reserveGate chan struct{}
	// reserveCalls counts ReserveIncoming invocations (atomic; the worker runs
	// on a background goroutine).
	reserveCalls int32
	// listFn, when non-nil, overrides ListAccountsByClient (used by exercise
	// seller-money resolution tests).
	listFn func(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error)
}

func (s *stubAccountForHandler) GetAccountByNumber(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.getFn != nil {
		return s.getFn(ctx, in, opts...)
	}
	return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
}
func (s *stubAccountForHandler) ReserveIncoming(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
	atomic.AddInt32(&s.reserveCalls, 1)
	if g := s.reserveGate; g != nil {
		// Block until the gate is closed/fed (nil gate = immediate), but honor
		// ctx cancellation like a real gRPC call would — so a bounded worker
		// context can abort a hung reserve.
		select {
		case <-g:
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}
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
func (s *stubAccountForHandler) ListAccountsByClient(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	if s.listFn != nil {
		return s.listFn(ctx, in, opts...)
	}
	return &accountpb.ListAccountsResponse{}, nil
}
func (s *stubAccountForHandler) ReserveOutgoing(ctx context.Context, in *accountpb.ReserveOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReserveOutgoingResponse, error) {
	if s.reserveOutFn != nil {
		return s.reserveOutFn(ctx, in, opts...)
	}
	return &accountpb.ReserveOutgoingResponse{ReservationKey: in.ReservationKey}, nil
}
func (s *stubAccountForHandler) SettleOutgoing(ctx context.Context, in *accountpb.SettleOutgoingRequest, opts ...grpc.CallOption) (*accountpb.SettleOutgoingResponse, error) {
	if s.settleOutFn != nil {
		return s.settleOutFn(ctx, in, opts...)
	}
	return &accountpb.SettleOutgoingResponse{}, nil
}
func (s *stubAccountForHandler) ReleaseOutgoing(ctx context.Context, in *accountpb.ReleaseOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseOutgoingResponse, error) {
	if s.releaseOutFn != nil {
		return s.releaseOutFn(ctx, in, opts...)
	}
	return &accountpb.ReleaseOutgoingResponse{Released: true}, nil
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
	return handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, nil, nil, nil, 111, 5*time.Second), db, stub
}

// TestHandleNewTx_PersistsMetadata_AndCommitPassesMemo is the Task 10 test:
// HandleNewTx persists the NEW_TX envelope's Message/PaymentCode/etc., and
// HandleCommitTx threads the stored Message into account-service as the ledger
// memo on the credit call.
func TestHandleNewTx_PersistsMetadata_AndCommitPassesMemo(t *testing.T) {
	h, db, stub := newPeerTxHandler(t)
	var capturedMemo string
	stub.commitFn = func(ctx context.Context, in *accountpb.CommitIncomingRequest, opts ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error) {
		capturedMemo = in.GetMemo()
		return &accountpb.CommitIncomingResponse{}, nil
	}
	if _, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "meta-L"},
		PeerBankCode:   "222",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "meta-L"},
		Message:        "rent for May",
		PaymentCode:    "289",
		PaymentPurpose: "housing",
		CallNumber:     "97-1234",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100.00", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100.00", Direction: "CREDIT"},
		},
	}); err != nil {
		t.Fatalf("NEW_TX: %v", err)
	}

	// Metadata persisted on the record.
	var rec model.PeerIdempotenceRecord
	if err := db.Where("peer_bank_code = ? AND locally_generated_key = ?", "222", "meta-L").First(&rec).Error; err != nil {
		t.Fatalf("lookup rec: %v", err)
	}
	if rec.Message != "rent for May" || rec.PaymentCode != "289" || rec.PaymentPurpose != "housing" || rec.CallNumber != "97-1234" {
		t.Errorf("metadata not persisted: %+v", rec)
	}
	if rec.TxForeignID != "meta-L" || rec.TxRoutingNumber != 222 {
		t.Errorf("transactionId not persisted: routing=%d id=%q", rec.TxRoutingNumber, rec.TxForeignID)
	}

	// COMMIT (own fresh idem, correlate by transactionId) passes Message as memo.
	if _, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "meta-M"},
		PeerBankCode:   "222",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "meta-L"},
	}); err != nil {
		t.Fatalf("COMMIT: %v", err)
	}
	if capturedMemo != "rent for May" {
		t.Errorf("commit memo = %q, want the stored NEW_TX message", capturedMemo)
	}
}

// TestHandleCommitTx_CorrelatesByTransactionId_AndIdempotentOnRetransmit is the
// Task 11 receiver test: a COMMIT whose OWN idempotence key differs from the
// NEW_TX's resolves the same record via transactionId and commits; a second
// COMMIT (any idem) is a no-op.
func TestHandleCommitTx_CorrelatesByTransactionId_AndIdempotentOnRetransmit(t *testing.T) {
	h, _, stub := newPeerTxHandler(t)
	commitCalls := 0
	stub.commitFn = func(ctx context.Context, in *accountpb.CommitIncomingRequest, opts ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error) {
		commitCalls++
		// Key is derived from the NEW_TX's L (222:L1), never from the COMMIT idem.
		if in.GetReservationKey() != "222:L1" {
			t.Errorf("reservation key = %q, want 222:L1 (derived from NEW_TX L)", in.GetReservationKey())
		}
		return &accountpb.CommitIncomingResponse{}, nil
	}
	// NEW_TX stored under L1 (peer 222, key L1); its own transactionId is {222,L1}.
	if _, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "L1"},
		PeerBankCode:   "222",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "L1"},
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "CREDIT"},
		},
	}); err != nil {
		t.Fatalf("NEW_TX: %v", err)
	}
	// COMMIT with a DIFFERENT idem (M1), correlating by transactionId {222,L1}.
	if _, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "M1"},
		PeerBankCode:   "222",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "L1"},
	}); err != nil {
		t.Fatalf("COMMIT: %v", err)
	}
	if commitCalls != 1 {
		t.Fatalf("expected exactly 1 CommitIncoming, got %d", commitCalls)
	}
	// Retransmitted COMMIT (idem M1 again) must be a no-op.
	if _, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "M1"},
		PeerBankCode:   "222",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "L1"},
	}); err != nil {
		t.Fatalf("COMMIT retransmit: %v", err)
	}
	if commitCalls != 1 {
		t.Errorf("retransmitted COMMIT must be a no-op; CommitIncoming called %d times", commitCalls)
	}
}

func TestHandleNewTx_HappyPath_YES(t *testing.T) {
	h, _, _ := newPeerTxHandler(t)
	resp, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k1"},
		PeerBankCode:   "222",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100.00", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100.00", Direction: "CREDIT"},
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
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100.00", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111000001", AssetType: "MONAS", AssetId: "RSD", Amount: "90.00", Direction: "CREDIT"},
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
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100.00", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100.00", Direction: "CREDIT"},
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
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "k4"},
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100.00", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100.00", Direction: "CREDIT"},
		},
	})
	if _, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		// COMMIT carries its OWN fresh idem (m4) and correlates to the NEW_TX
		// (L = k4) by transactionId.
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "m4"},
		PeerBankCode:   "222",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "k4"},
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
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "k5"},
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100.00", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100.00", Direction: "CREDIT"},
		},
	})
	if _, err := h.HandleRollbackTx(context.Background(), &transactionpb.SiTxRollbackRequest{
		// ROLLBACK carries its OWN fresh idem (m5) and correlates to the NEW_TX
		// (L = k5) by transactionId.
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "m5"},
		PeerBankCode:   "222",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "k5"},
	}); err != nil {
		t.Fatalf("HandleRollbackTx: %v", err)
	}
	if !called {
		t.Errorf("expected ReleaseIncoming call")
	}
}

// TestHandleCommitTx_DivergentTransactionId_ResolvesByForeignID is the
// interop regression test: a spec-conformant FOREIGN peer (SI-TX §2.8.2) may
// pick a transactionId.id DIFFERENT from its NEW_TX idempotence key. Here the
// NEW_TX is stored under locally_generated_key "L1" but carries
// transactionId.id="TXDIFF". The COMMIT then arrives with its OWN fresh idem
// "M1" and transactionId.id="TXDIFF". The handler MUST resolve the L1 record
// via tx_foreign_id and settle the holds placed under L1 — keys derived from
// "L1", never "TXDIFF" and never "M1".
func TestHandleCommitTx_DivergentTransactionId_ResolvesByForeignID(t *testing.T) {
	h, _, stub := newPeerTxHandler(t)
	var commitKeys, settleKeys []string
	stub.commitFn = func(ctx context.Context, in *accountpb.CommitIncomingRequest, opts ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error) {
		commitKeys = append(commitKeys, in.GetReservationKey())
		return &accountpb.CommitIncomingResponse{}, nil
	}
	stub.settleOutFn = func(ctx context.Context, in *accountpb.SettleOutgoingRequest, opts ...grpc.CallOption) (*accountpb.SettleOutgoingResponse, error) {
		settleKeys = append(settleKeys, in.GetReservationKey())
		return &accountpb.SettleOutgoingResponse{}, nil
	}
	// NEW_TX: idem key L1, but transactionId.id is the DIVERGENT "TXDIFF".
	// One DEBIT leg on our routing (111) → an outgoing hold keyed under L1; one
	// CREDIT leg on our routing → a reservation keyed under L1.
	if _, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "L1"},
		PeerBankCode:   "222",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "TXDIFF"},
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111-A", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111-B", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "CREDIT"},
		},
	}); err != nil {
		t.Fatalf("NEW_TX: %v", err)
	}
	// COMMIT: its OWN fresh idem M1, correlating by the divergent transactionId
	// "TXDIFF". This must still resolve the record stored under L1.
	if _, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "M1"},
		PeerBankCode:   "222",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "TXDIFF"},
	}); err != nil {
		t.Fatalf("COMMIT: %v", err)
	}
	// The CREDIT-leg reservation key is the NEW_TX L: "222:L1" — NOT TXDIFF/M1.
	if len(commitKeys) != 1 || commitKeys[0] != "222:L1" {
		t.Errorf("CommitIncoming keys = %v, want exactly [222:L1] (derived from NEW_TX L)", commitKeys)
	}
	// The DEBIT-leg settle key is the per-posting tag under L: "222:L1:0".
	if len(settleKeys) != 1 || settleKeys[0] != "222:L1:0" {
		t.Errorf("SettleOutgoing keys = %v, want exactly [222:L1:0] (derived from NEW_TX L)", settleKeys)
	}
}

// TestHandleRollbackTx_AfterCommit_NoOp verifies the defense-in-depth guard: a
// ROLLBACK that arrives after the TX already COMMITTED (committed_at set) must
// be a safe no-op — it MUST NOT release any settled funds. A committed TX
// cannot be rolled back.
func TestHandleRollbackTx_AfterCommit_NoOp(t *testing.T) {
	h, _, stub := newPeerTxHandler(t)
	releaseCalled := false
	releaseOutCalled := false
	stub.releaseFn = func(ctx context.Context, in *accountpb.ReleaseIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
		releaseCalled = true
		return &accountpb.ReleaseIncomingResponse{}, nil
	}
	stub.releaseOutFn = func(ctx context.Context, in *accountpb.ReleaseOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseOutgoingResponse, error) {
		releaseOutCalled = true
		return &accountpb.ReleaseOutgoingResponse{Released: true}, nil
	}
	// NEW_TX (idem k6, transactionId k6) with a DEBIT leg on our routing.
	if _, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: "k6"},
		PeerBankCode:   "222",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "k6"},
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111-A", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111-B", AssetType: "MONAS", AssetId: "RSD", Amount: "100", Direction: "CREDIT"},
		},
	}); err != nil {
		t.Fatalf("NEW_TX: %v", err)
	}
	// COMMIT it — money settles, committed_at gets stamped.
	if _, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "m6"},
		PeerBankCode:   "222",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "k6"},
	}); err != nil {
		t.Fatalf("COMMIT: %v", err)
	}
	// Now a late/erroneous ROLLBACK for the same transactionId must no-op.
	if _, err := h.HandleRollbackTx(context.Background(), &transactionpb.SiTxRollbackRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "r6"},
		PeerBankCode:   "222",
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: "k6"},
	}); err != nil {
		t.Fatalf("ROLLBACK after COMMIT should be a no-op, got: %v", err)
	}
	if releaseCalled {
		t.Error("ReleaseIncoming must NOT be called for a ROLLBACK after COMMIT")
	}
	if releaseOutCalled {
		t.Error("ReleaseOutgoing must NOT be called for a ROLLBACK after COMMIT (would release settled funds)")
	}
}

func TestInitiateOutboundTxWithPostings_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var probe map[string]any
		_ = json.NewDecoder(r.Body).Decode(&probe)
		if probe["messageType"] == "NEW_TX" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"vote":"YES"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111, 5*time.Second)

	// Note: stubAccountForHandler returns CurrencyCode="RSD" by default,
	// so the money postings use RSD here. The option postings use a
	// JSON-shaped assetId starting with `{` which the executor skips
	// (option-asset handling is out of scope; matches production where
	// optAssetID is a marshalled OptionDescription).
	resp, err := h.InitiateOutboundTxWithPostings(context.Background(), &transactionpb.SiTxInitiateWithPostingsRequest{
		PeerBankCode: "222",
		TxKind:       "otc-accept",
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111-A", AssetType: "MONAS", AssetId: "RSD", Amount: "700", Direction: "DEBIT"},
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222-A", AssetType: "MONAS", AssetId: "RSD", Amount: "700", Direction: "CREDIT"},
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222-B", AssetType: "OPTION", AssetId: `{"ticker":"AAPL"}`, Amount: "1", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111-B", AssetType: "OPTION", AssetId: `{"ticker":"AAPL"}`, Amount: "1", Direction: "CREDIT"},
		},
	})
	if err != nil {
		t.Fatalf("InitiateOutboundTxWithPostings: %v", err)
	}
	if resp.GetTransactionId() == "" {
		t.Errorf("expected transaction_id, got %+v", resp)
	}
	// The peer votes YES and the local commit succeeds, so the response now reports
	// the row's real terminal status ("committed") instead of a blanket "pending"
	// (which used to mask a rolled-back settlement from the OTC accept caller).
	if resp.GetStatus() != "committed" {
		t.Errorf("status: %s", resp.GetStatus())
	}
}

// TestInitiateOutboundTxWithPostings_LocalCommitFailure_LeavesCommitting is the
// saga-pivot regression: after a YES vote, a failing local commit step (here the
// strike/premium settle) must leave the row in the forward-only `committing`
// state — NEVER `pending` (which the cron could max-attempts-COMPENSATE,
// stranding settled money) and never `rolled_back`.
func TestInitiateOutboundTxWithPostings_LocalCommitFailure_LeavesCommitting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var probe map[string]any
		_ = json.NewDecoder(r.Body).Decode(&probe)
		if probe["messageType"] == "NEW_TX" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"vote":"YES"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err := db.AutoMigrate(&model.PeerIdempotenceRecord{}, &model.OutboundPeerTx{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	stub := &stubAccountForHandler{}
	// Force the local DEBIT-leg settle to fail — simulates account-service
	// briefly unavailable during the commit phase.
	stub.settleOutFn = func(ctx context.Context, in *accountpb.SettleOutgoingRequest, opts ...grpc.CallOption) (*accountpb.SettleOutgoingResponse, error) {
		return nil, status.Error(codes.Unavailable, "account-service down")
	}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	outRepo := repository.NewOutboundPeerTxRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
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
	row, gerr := outRepo.GetByIdempotenceKey(resp.GetTransactionId())
	if gerr != nil {
		t.Fatalf("get row: %v", gerr)
	}
	if row.Status != "committing" {
		t.Errorf("after YES + local settle failure, row must be `committing` (forward-only), got %q", row.Status)
	}
}

func TestInitiateOutboundTxWithPostings_NoPostings_400(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	_ = db.AutoMigrate(&model.PeerIdempotenceRecord{}, &model.OutboundPeerTx{})
	stub := &stubAccountForHandler{}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	outRepo := repository.NewOutboundPeerTxRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: "http://x", APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111, 5*time.Second)

	_, err := h.InitiateOutboundTxWithPostings(context.Background(), &transactionpb.SiTxInitiateWithPostingsRequest{
		PeerBankCode: "222",
		TxKind:       "otc-accept",
	})
	if err == nil {
		t.Fatalf("expected error for empty postings")
	}
}

// TestInitiateOutboundTx_NewTxTransactionId_CommitFreshIdem is the Task 11
// initiator test: the NEW_TX's Transaction.TransactionID.ID equals its own
// idempotence key (L), and the COMMIT envelope carries a DIFFERENT idem (M)
// while correlating to L via its Message.TransactionID.
func TestInitiateOutboundTx_NewTxTransactionId_CommitFreshIdem(t *testing.T) {
	type captured struct {
		messageType string
		idemKey     string
		txID        string
	}
	var seen []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		_ = json.NewDecoder(r.Body).Decode(&raw)
		mt, _ := raw["messageType"].(string)
		idem, _ := raw["idempotenceKey"].(map[string]any)
		idemKey, _ := idem["locallyGeneratedKey"].(string)
		msg, _ := raw["message"].(map[string]any)
		txid, _ := msg["transactionId"].(map[string]any)
		txID, _ := txid["id"].(string)
		seen = append(seen, captured{messageType: mt, idemKey: idemKey, txID: txID})
		if mt == "NEW_TX" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"vote":"YES"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111, 5*time.Second)

	resp, err := h.InitiateOutboundTx(context.Background(), &transactionpb.SiTxInitiateRequest{
		FromAccountNumber: "111000000000000001",
		ToAccountNumber:   "222000000000000002",
		Amount:            "100",
		Currency:          "RSD",
	})
	if err != nil {
		t.Fatalf("InitiateOutboundTx: %v", err)
	}
	L := resp.GetTransactionId()
	var newtx, commit *captured
	for i := range seen {
		switch seen[i].messageType {
		case "NEW_TX":
			newtx = &seen[i]
		case "COMMIT_TX":
			commit = &seen[i]
		}
	}
	if newtx == nil || commit == nil {
		t.Fatalf("expected both NEW_TX and COMMIT_TX, got %+v", seen)
	}
	// NEW_TX idem == L; NEW_TX transactionId.id == L.
	if newtx.idemKey != L {
		t.Errorf("NEW_TX idem = %q, want L = %q", newtx.idemKey, L)
	}
	if newtx.txID != L {
		t.Errorf("NEW_TX transactionId.id = %q, want L = %q", newtx.txID, L)
	}
	// COMMIT idem != L (its own unique key); COMMIT transactionId.id == L.
	if commit.idemKey == L || commit.idemKey == "" {
		t.Errorf("COMMIT idem = %q, must be a fresh key distinct from L = %q", commit.idemKey, L)
	}
	if commit.txID != L {
		t.Errorf("COMMIT transactionId.id = %q, want L = %q", commit.txID, L)
	}
}

// --- Task 3: receiver-side 202-async ---

// dbNonce makes each in-memory cache=shared DSN unique per construction, so
// shared-cache DBs don't survive across `go test -count=N` reruns.
var dbNonce int64

// newPeerTxHandlerWithDeadline builds a handler with a custom receive-sync
// deadline and an optional blockable reserve gate on the fake account client.
func newPeerTxHandlerWithDeadline(t *testing.T, deadline time.Duration, gate chan struct{}) (*handler.PeerTxGRPCHandler, *gorm.DB, *stubAccountForHandler) {
	t.Helper()
	// Shared-cache in-memory DB so the background worker's pooled connection
	// sees the same table the request goroutine migrated. A unique name per
	// CONSTRUCTION (test name + atomic nonce) keeps them isolated AND prevents a
	// cache=shared DB surviving across `-count` reruns (which would leak rows
	// between runs and fail at -count>1). Pool capped at 1 conn to avoid a
	// closed-conn dropping the shared in-memory DB.
	dsn := "file:" + t.Name() + "-" + strconv.FormatInt(atomic.AddInt64(&dbNonce, 1), 10) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if sqlDB, derr := db.DB(); derr == nil {
		sqlDB.SetMaxOpenConns(1)
		// Close the underlying pool when the test ends so the shared in-memory
		// DB is released and not reused on the next `-count` iteration.
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if err := db.AutoMigrate(&model.PeerIdempotenceRecord{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	stub := &stubAccountForHandler{reserveGate: gate}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, nil, nil, nil, 111, deadline)
	return h, db, stub
}

// balancedPostings builds a 2-leg MONAS NEW_TX request (DEBIT on the peer's
// routing, CREDIT on ours) the executor votes YES on against the fake account
// client. The CREDIT leg on routing 111 drives one ReserveIncoming call.
func balancedPostings(peerCode, idem string) *transactionpb.SiTxNewTxRequest {
	return &transactionpb.SiTxNewTxRequest{
		IdempotenceKey: &transactionpb.SiTxIdempotenceKey{RoutingNumber: 222, LocallyGeneratedKey: idem},
		PeerBankCode:   peerCode,
		TransactionId:  &transactionpb.SiTxForeignBankId{RoutingNumber: 222, Id: idem},
		Postings: []*transactionpb.SiTxPosting{
			{RoutingNumber: 222, AccountType: "ACCOUNT", AccountId: "222000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100.00", Direction: "DEBIT"},
			{RoutingNumber: 111, AccountType: "ACCOUNT", AccountId: "111000001", AssetType: "MONAS", AssetId: "RSD", Amount: "100.00", Direction: "CREDIT"},
		},
	}
}

// waitFor polls cond every 5ms up to ~2s, failing the test if it never holds.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within 2s")
}

func statusOf(t *testing.T, db *gorm.DB, peerCode, idem string) string {
	t.Helper()
	var rec model.PeerIdempotenceRecord
	if err := db.Where("peer_bank_code = ? AND locally_generated_key = ?", peerCode, idem).First(&rec).Error; err != nil {
		return ""
	}
	return rec.Status
}

// TestHandleNewTx_FastPath_ReturnsVoteNotPending: with the deadline far larger
// than the (immediate) reserve, HandleNewTx returns the YES vote synchronously
// and the record lands as status="done" — identical to pre-async behaviour.
func TestHandleNewTx_FastPath_ReturnsVoteNotPending(t *testing.T) {
	h, db, _ := newPeerTxHandlerWithDeadline(t, 5*time.Second, nil)
	resp, err := h.HandleNewTx(context.Background(), balancedPostings("222", "fast-1"))
	if err != nil {
		t.Fatalf("HandleNewTx: %v", err)
	}
	if resp.GetPending() {
		t.Fatalf("fast path must not be pending: %+v", resp)
	}
	if resp.GetType() != contractsitx.VoteYes {
		t.Errorf("expected YES, got %+v", resp)
	}
	if resp.GetTransactionId() == "" {
		t.Errorf("expected transaction_id on the synchronous YES vote")
	}
	if s := statusOf(t, db, "222", "fast-1"); s != "done" {
		t.Errorf("record status = %q, want done", s)
	}
}

// TestHandleNewTx_SlowReserve_Returns202ThenCaches: a gated reserve forces the
// worker past a short deadline. The first delivery returns 202 (pending) and a
// pending row; a retransmit while still blocked returns 202 again and the
// reserve runs exactly ONCE (shared worker). After the gate opens the row goes
// done and a retransmit returns the cached YES vote.
func TestHandleNewTx_SlowReserve_Returns202ThenCaches(t *testing.T) {
	gate := make(chan struct{})
	h, db, stub := newPeerTxHandlerWithDeadline(t, 30*time.Millisecond, gate)

	// 1st delivery — reserve is blocked on the gate, deadline fires → 202.
	resp1, err := h.HandleNewTx(context.Background(), balancedPostings("222", "slow-1"))
	if err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if !resp1.GetPending() {
		t.Fatalf("first delivery must be pending (reserve blocked), got %+v", resp1)
	}
	// A pending row must exist (written by the timeout branch).
	waitFor(t, func() bool { return statusOf(t, db, "222", "slow-1") == "pending" })

	// Retransmit while still blocked — must also be pending, and must NOT spawn
	// a second worker / second reserve.
	resp2, err := h.HandleNewTx(context.Background(), balancedPostings("222", "slow-1"))
	if err != nil {
		t.Fatalf("retransmit while blocked: %v", err)
	}
	if !resp2.GetPending() {
		t.Errorf("retransmit while blocked must be pending, got %+v", resp2)
	}
	if got := atomic.LoadInt32(&stub.reserveCalls); got != 1 {
		t.Errorf("ReserveIncoming called %d times while blocked, want exactly 1 (single shared worker)", got)
	}

	// Unblock the reserve → worker finishes and overwrites the row to done.
	close(gate)
	waitFor(t, func() bool { return statusOf(t, db, "222", "slow-1") == "done" })

	// Retransmit after done — returns the cached YES vote, not pending.
	resp3, err := h.HandleNewTx(context.Background(), balancedPostings("222", "slow-1"))
	if err != nil {
		t.Fatalf("retransmit after done: %v", err)
	}
	if resp3.GetPending() {
		t.Errorf("retransmit after done must not be pending, got %+v", resp3)
	}
	if resp3.GetType() != contractsitx.VoteYes {
		t.Errorf("retransmit after done must be YES, got %+v", resp3)
	}
	if got := atomic.LoadInt32(&stub.reserveCalls); got != 1 {
		t.Errorf("ReserveIncoming total = %d, want 1 (cached after done, no re-reserve)", got)
	}
}

// TestHandleNewTx_RestartRecovery_RekicksWorker: a pending row pre-exists (as if
// written before a process restart) and the in-flight map is empty (fresh
// handler). A retransmit must return 202 AND re-kick a worker that drives the
// row to done.
func TestHandleNewTx_RestartRecovery_RekicksWorker(t *testing.T) {
	h, db, stub := newPeerTxHandlerWithDeadline(t, 5*time.Second, nil)
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	// Pre-insert a pending row (no in-flight worker exists for it).
	if err := idemRepo.UpsertPending(&model.PeerIdempotenceRecord{
		PeerBankCode: "222", LocallyGeneratedKey: "restart-1",
		TxRoutingNumber: 222, TxForeignID: "restart-1",
	}); err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	if s := statusOf(t, db, "222", "restart-1"); s != "pending" {
		t.Fatalf("seed status = %q, want pending", s)
	}

	resp, err := h.HandleNewTx(context.Background(), balancedPostings("222", "restart-1"))
	if err != nil {
		t.Fatalf("retransmit: %v", err)
	}
	if !resp.GetPending() {
		t.Errorf("retransmit on a pending row must return pending, got %+v", resp)
	}
	// The re-kicked worker drives the row to done.
	waitFor(t, func() bool { return statusOf(t, db, "222", "restart-1") == "done" })
	if got := atomic.LoadInt32(&stub.reserveCalls); got < 1 {
		t.Errorf("expected the re-kicked worker to run a reserve, got %d", got)
	}
}

// TestHandleNewTx_WorkerTimeout_DoesNotCacheAndRekicks: when the background
// reserve hangs past the worker's bounded context, the worker must abort WITHOUT
// caching a (false) NO vote. The pending row stays `pending`, and a retransmit
// re-kicks a fresh worker — proving a transient infra timeout can't permanently
// reject a legitimate TX.
func TestHandleNewTx_WorkerTimeout_DoesNotCacheAndRekicks(t *testing.T) {
	// Gate stays blocked for the whole test — the reserve never completes, so the
	// only way the worker exits is the bounded workerTimeout firing.
	gate := make(chan struct{})
	h, db, stub := newPeerTxHandlerWithDeadline(t, 20*time.Millisecond, gate)
	h.SetWorkerTimeout(40 * time.Millisecond)

	// 1st delivery: reserve blocked → receiveSyncDeadline fires → 202 pending,
	// and the timeout branch writes a pending row.
	resp1, err := h.HandleNewTx(context.Background(), balancedPostings("222", "wt-1"))
	if err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if !resp1.GetPending() {
		t.Fatalf("first delivery must be pending (reserve blocked), got %+v", resp1)
	}
	waitFor(t, func() bool { return statusOf(t, db, "222", "wt-1") == "pending" })

	// Wait until the first worker has exited (workerTimeout fired, runReserve
	// returned nil, inflight entry cleared). With the gate still blocked, the
	// only exit is the timeout; the inflight set going empty proves it exited.
	waitFor(t, func() bool { return h.InflightLen() == 0 })

	// The infra timeout must NOT have cached a NO vote — the row is still pending.
	if s := statusOf(t, db, "222", "wt-1"); s != "pending" {
		t.Fatalf("after worker timeout the row must STILL be pending (no cached NO vote), got %q", s)
	}
	if got := atomic.LoadInt32(&stub.reserveCalls); got != 1 {
		t.Fatalf("expected exactly 1 reserve attempt before the retransmit, got %d", got)
	}

	// Retransmit on the still-pending row: returns 202 AND re-kicks a FRESH
	// worker (reserveCalls increments to 2). The gate stays blocked; the new
	// worker exits ~40ms later on its own bounded timeout, so the test process
	// is not hung.
	resp2, err := h.HandleNewTx(context.Background(), balancedPostings("222", "wt-1"))
	if err != nil {
		t.Fatalf("retransmit: %v", err)
	}
	if !resp2.GetPending() {
		t.Errorf("retransmit on a pending row must return pending, got %+v", resp2)
	}
	waitFor(t, func() bool { return atomic.LoadInt32(&stub.reserveCalls) == 2 })
}
