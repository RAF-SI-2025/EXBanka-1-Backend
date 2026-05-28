package handler_test

import (
	"context"
	"testing"

	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/handler"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/exbanka/transaction-service/internal/sitx"
	"github.com/glebarez/sqlite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// newPeerTxHandlerWithOutRepo creates a handler with both idem and outbound
// repos wired for GetTxStatus tests.
func newPeerTxHandlerWithOutRepo(t *testing.T) (*handler.PeerTxGRPCHandler, *repository.PeerIdempotenceRepository, *repository.OutboundPeerTxRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.PeerIdempotenceRecord{}, &model.OutboundPeerTx{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	stub := &stubAccountForHandler{}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	outRepo := repository.NewOutboundPeerTxRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, nil, nil, 111)
	return h, idemRepo, outRepo
}

// TestGetTxStatus_SenderFound verifies we return "sender" role when the
// outbound_peer_tx row exists.
func TestGetTxStatus_SenderFound(t *testing.T) {
	h, _, outRepo := newPeerTxHandlerWithOutRepo(t)

	idem := "test-tx-sender-001"
	if err := outRepo.Create(&model.OutboundPeerTx{
		IdempotenceKey: idem,
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   "[]",
		Status:         "pending",
	}); err != nil {
		t.Fatalf("create outbound: %v", err)
	}

	resp, err := h.GetTxStatus(context.Background(), &transactionpb.GetTxStatusRequest{
		TransactionId:      idem,
		CallerPeerBankCode: "222",
	})
	if err != nil {
		t.Fatalf("GetTxStatus: %v", err)
	}
	if resp.GetOurRole() != "sender" {
		t.Errorf("expected our_role=sender, got %q", resp.GetOurRole())
	}
	if resp.GetState() != "prepared" {
		t.Errorf("expected state=prepared (pending→prepared), got %q", resp.GetState())
	}
}

// TestGetTxStatus_SenderCommitted verifies committed outbound rows map to
// "committed" state.
func TestGetTxStatus_SenderCommitted(t *testing.T) {
	h, _, outRepo := newPeerTxHandlerWithOutRepo(t)

	idem := "test-tx-sender-002"
	if err := outRepo.Create(&model.OutboundPeerTx{
		IdempotenceKey: idem,
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   "[]",
		Status:         "committed",
	}); err != nil {
		t.Fatalf("create outbound: %v", err)
	}

	resp, err := h.GetTxStatus(context.Background(), &transactionpb.GetTxStatusRequest{
		TransactionId:      idem,
		CallerPeerBankCode: "222",
	})
	if err != nil {
		t.Fatalf("GetTxStatus: %v", err)
	}
	if resp.GetOurRole() != "sender" {
		t.Errorf("expected our_role=sender, got %q", resp.GetOurRole())
	}
	if resp.GetState() != "committed" {
		t.Errorf("expected state=committed, got %q", resp.GetState())
	}
}

// TestGetTxStatus_SenderFailed verifies failed outbound rows map to
// "dead_letter" state.
func TestGetTxStatus_SenderFailed(t *testing.T) {
	h, _, outRepo := newPeerTxHandlerWithOutRepo(t)

	idem := "test-tx-sender-003"
	if err := outRepo.Create(&model.OutboundPeerTx{
		IdempotenceKey: idem,
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   "[]",
		Status:         "failed",
		LastError:      "max retries exceeded",
	}); err != nil {
		t.Fatalf("create outbound: %v", err)
	}

	resp, err := h.GetTxStatus(context.Background(), &transactionpb.GetTxStatusRequest{
		TransactionId:      idem,
		CallerPeerBankCode: "222",
	})
	if err != nil {
		t.Fatalf("GetTxStatus: %v", err)
	}
	if resp.GetState() != "dead_letter" {
		t.Errorf("expected state=dead_letter, got %q", resp.GetState())
	}
	if resp.GetLastError() == "" {
		t.Errorf("expected last_error populated")
	}
}

// TestGetTxStatus_ReceiverFound verifies we return "receiver" role when an
// idem record exists for the (callerCode, transactionID) pair.
func TestGetTxStatus_ReceiverFound(t *testing.T) {
	h, idemRepo, _ := newPeerTxHandlerWithOutRepo(t)

	txID := "peer-tx-uuid-receiver-001"
	if err := idemRepo.Insert(&model.PeerIdempotenceRecord{
		PeerBankCode:        "222",
		LocallyGeneratedKey: "some-idem-key",
		TransactionID:       txID,
		ResponsePayloadJSON: `{"type":"YES","transaction_id":"` + txID + `"}`,
		DebitsJSON:          "[]",
		OptionsJSON:         "[]",
	}); err != nil {
		t.Fatalf("insert idem: %v", err)
	}

	resp, err := h.GetTxStatus(context.Background(), &transactionpb.GetTxStatusRequest{
		TransactionId:      txID,
		CallerPeerBankCode: "222",
	})
	if err != nil {
		t.Fatalf("GetTxStatus: %v", err)
	}
	if resp.GetOurRole() != "receiver" {
		t.Errorf("expected our_role=receiver, got %q", resp.GetOurRole())
	}
	if resp.GetState() != "committed" {
		t.Errorf("expected state=committed, got %q", resp.GetState())
	}
}

// TestGetTxStatus_Unknown verifies that a completely unknown transactionID
// returns state="unknown" and empty our_role.
func TestGetTxStatus_Unknown(t *testing.T) {
	h, _, _ := newPeerTxHandlerWithOutRepo(t)

	resp, err := h.GetTxStatus(context.Background(), &transactionpb.GetTxStatusRequest{
		TransactionId:      "completely-unknown-uuid",
		CallerPeerBankCode: "333",
	})
	if err != nil {
		t.Fatalf("GetTxStatus: %v", err)
	}
	if resp.GetState() != "unknown" {
		t.Errorf("expected state=unknown, got %q", resp.GetState())
	}
	if resp.GetOurRole() != "" {
		t.Errorf("expected our_role empty, got %q", resp.GetOurRole())
	}
}

// TestGetTxStatus_MissingTransactionID verifies InvalidArgument is returned
// when transaction_id is empty.
func TestGetTxStatus_MissingTransactionID(t *testing.T) {
	h, _, _ := newPeerTxHandlerWithOutRepo(t)

	_, err := h.GetTxStatus(context.Background(), &transactionpb.GetTxStatusRequest{
		TransactionId:      "",
		CallerPeerBankCode: "222",
	})
	if err == nil || status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}
