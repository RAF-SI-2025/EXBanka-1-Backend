package handler_test

import (
	"context"
	"testing"

	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/interbank-service/internal/handler"
	"github.com/exbanka/interbank-service/internal/repository"
	"github.com/glebarez/sqlite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// newAdminHandlerNoTable builds an admin handler whose repo points at a DB
// WITHOUT the peer_banks table, so every repository call returns a real DB
// error ("no such table") — exercising the Internal error branches.
func newAdminHandlerNoTable(t *testing.T) *handler.PeerBankAdminGRPCHandler {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// NOTE: deliberately NOT migrating model.PeerBank.
	return handler.NewPeerBankAdminGRPCHandler(repository.NewPeerBankRepository(db), "111")
}

// TestPeerBankAdmin_DBErrors_SurfaceInternal verifies that a DB-layer failure on
// each admin RPC is surfaced as codes.Internal (never masked or panicked).
func TestPeerBankAdmin_DBErrors_SurfaceInternal(t *testing.T) {
	h := newAdminHandlerNoTable(t)
	ctx := context.Background()

	if _, err := h.ListPeerBanks(ctx, &transactionpb.ListPeerBanksRequest{}); status.Code(err) != codes.Internal {
		t.Errorf("ListPeerBanks: want Internal, got %v", err)
	}
	if _, err := h.GetPeerBank(ctx, &transactionpb.GetPeerBankRequest{Id: 1}); status.Code(err) != codes.Internal {
		t.Errorf("GetPeerBank: want Internal, got %v", err)
	}
	// Create with fully valid input so it passes validation and fails at the
	// repo.Create call (covers the bcrypt + create error branch).
	if _, err := h.CreatePeerBank(ctx, &transactionpb.CreatePeerBankRequest{
		BankCode: "222", RoutingNumber: 222, BaseUrl: "http://peer-222/api/v3", ApiToken: "secret-222", Active: true,
	}); status.Code(err) != codes.Internal {
		t.Errorf("CreatePeerBank: want Internal, got %v", err)
	}
	// Update fails at the initial GetByID (no table).
	if _, err := h.UpdatePeerBank(ctx, &transactionpb.UpdatePeerBankRequest{Id: 1}); status.Code(err) != codes.Internal {
		t.Errorf("UpdatePeerBank: want Internal, got %v", err)
	}
	if _, err := h.DeletePeerBank(ctx, &transactionpb.DeletePeerBankRequest{Id: 1}); status.Code(err) != codes.Internal {
		t.Errorf("DeletePeerBank: want Internal, got %v", err)
	}
	if _, err := h.ResolvePeerByAPIToken(ctx, &transactionpb.ResolvePeerByAPITokenRequest{ApiToken: "tok"}); status.Code(err) != codes.Internal {
		t.Errorf("ResolvePeerByAPIToken: want Internal, got %v", err)
	}
	if _, err := h.ResolvePeerByBankCode(ctx, &transactionpb.ResolvePeerByBankCodeRequest{BankCode: "222"}); status.Code(err) != codes.Internal {
		t.Errorf("ResolvePeerByBankCode: want Internal, got %v", err)
	}
}
