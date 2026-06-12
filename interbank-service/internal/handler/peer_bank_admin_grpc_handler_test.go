package handler_test

import (
	"context"
	"testing"

	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/interbank-service/internal/handler"
	"github.com/exbanka/interbank-service/internal/model"
	"github.com/exbanka/interbank-service/internal/repository"
	"github.com/glebarez/sqlite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func newAdminTestHandler(t *testing.T) *handler.PeerBankAdminGRPCHandler {
	t.Helper()
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err := db.AutoMigrate(&model.PeerBank{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return handler.NewPeerBankAdminGRPCHandler(repository.NewPeerBankRepository(db), "111")
}

func TestPeerBankAdmin_CreateAndGet(t *testing.T) {
	h := newAdminTestHandler(t)
	ctx := context.Background()

	created, err := h.CreatePeerBank(ctx, &transactionpb.CreatePeerBankRequest{
		BankCode: "222", RoutingNumber: 222, BaseUrl: "http://peer-222/api/v3",
		ApiToken: "secret-222", Active: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Id == 0 || created.BankCode != "222" {
		t.Errorf("created: %+v", created)
	}
	if created.ApiTokenPreview == "secret-222" {
		t.Errorf("token leaked in preview: %q", created.ApiTokenPreview)
	}
	if !endsWith(created.ApiTokenPreview, "-222") {
		t.Errorf("preview should end with last 4 of token: %q", created.ApiTokenPreview)
	}

	got, err := h.GetPeerBank(ctx, &transactionpb.GetPeerBankRequest{Id: created.Id})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.BankCode != "222" {
		t.Errorf("get: %+v", got)
	}
}

func TestPeerBankAdmin_ListActiveOnly(t *testing.T) {
	h := newAdminTestHandler(t)
	ctx := context.Background()
	for _, c := range []struct {
		code   string
		active bool
	}{{"222", true}, {"333", false}, {"444", true}} {
		_, _ = h.CreatePeerBank(ctx, &transactionpb.CreatePeerBankRequest{
			BankCode: c.code, RoutingNumber: parseInt(c.code), BaseUrl: "http://x", ApiToken: "tok-" + c.code, Active: c.active,
		})
	}

	all, _ := h.ListPeerBanks(ctx, &transactionpb.ListPeerBanksRequest{ActiveOnly: false})
	if len(all.PeerBanks) != 3 {
		t.Errorf("list all: %d", len(all.PeerBanks))
	}
	active, _ := h.ListPeerBanks(ctx, &transactionpb.ListPeerBanksRequest{ActiveOnly: true})
	if len(active.PeerBanks) != 2 {
		t.Errorf("list active: %d", len(active.PeerBanks))
	}
}

func TestPeerBankAdmin_UpdateAndDelete(t *testing.T) {
	h := newAdminTestHandler(t)
	ctx := context.Background()

	created, _ := h.CreatePeerBank(ctx, &transactionpb.CreatePeerBankRequest{
		BankCode: "222", RoutingNumber: 222, BaseUrl: "http://old", ApiToken: "tok", Active: true,
	})

	updated, err := h.UpdatePeerBank(ctx, &transactionpb.UpdatePeerBankRequest{
		Id: created.Id, BaseUrl: "http://new", BaseUrlSet: true, ActiveSet: true, Active: false,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.BaseUrl != "http://new" || updated.Active {
		t.Errorf("update: %+v", updated)
	}

	if _, err := h.DeletePeerBank(ctx, &transactionpb.DeletePeerBankRequest{Id: created.Id}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := h.GetPeerBank(ctx, &transactionpb.GetPeerBankRequest{Id: created.Id}); err == nil {
		t.Errorf("expected NotFound after delete")
	}
}

func TestPeerBankAdmin_ResolveByAPIToken(t *testing.T) {
	h := newAdminTestHandler(t)
	ctx := context.Background()
	_, _ = h.CreatePeerBank(ctx, &transactionpb.CreatePeerBankRequest{
		BankCode: "222", RoutingNumber: 222, BaseUrl: "http://x", ApiToken: "secret-222",
		HmacInboundKey: "in-222", HmacOutboundKey: "out-222", Active: true,
	})

	resp, err := h.ResolvePeerByAPIToken(ctx, &transactionpb.ResolvePeerByAPITokenRequest{ApiToken: "secret-222"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !resp.GetFound() {
		t.Fatalf("expected found=true")
	}
	pb := resp.GetPeerBank()
	if pb.GetBankCode() != "222" || pb.GetApiTokenPlaintext() != "secret-222" {
		t.Errorf("got %+v", pb)
	}
	if pb.GetHmacInboundKey() != "in-222" || pb.GetHmacOutboundKey() != "out-222" {
		t.Errorf("hmac keys missing: %+v", pb)
	}
}

func TestPeerBankAdmin_ResolveByAPIToken_NotFound(t *testing.T) {
	h := newAdminTestHandler(t)
	resp, err := h.ResolvePeerByAPIToken(context.Background(), &transactionpb.ResolvePeerByAPITokenRequest{ApiToken: "nope"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resp.GetFound() {
		t.Errorf("expected found=false on miss")
	}
}

func TestPeerBankAdmin_ResolveByAPIToken_InactiveExcluded(t *testing.T) {
	h := newAdminTestHandler(t)
	ctx := context.Background()
	_, _ = h.CreatePeerBank(ctx, &transactionpb.CreatePeerBankRequest{
		BankCode: "222", RoutingNumber: 222, BaseUrl: "http://x", ApiToken: "tok-222", Active: false,
	})
	resp, _ := h.ResolvePeerByAPIToken(ctx, &transactionpb.ResolvePeerByAPITokenRequest{ApiToken: "tok-222"})
	if resp.GetFound() {
		t.Errorf("inactive peer should not match")
	}
}

func TestPeerBankAdmin_ResolveByBankCode(t *testing.T) {
	h := newAdminTestHandler(t)
	ctx := context.Background()
	_, _ = h.CreatePeerBank(ctx, &transactionpb.CreatePeerBankRequest{
		BankCode: "222", RoutingNumber: 222, BaseUrl: "http://x", ApiToken: "tok-222",
		HmacInboundKey: "in-222", Active: true,
	})

	resp, err := h.ResolvePeerByBankCode(ctx, &transactionpb.ResolvePeerByBankCodeRequest{BankCode: "222"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !resp.GetFound() || resp.GetPeerBank().GetHmacInboundKey() != "in-222" {
		t.Errorf("got %+v", resp.GetPeerBank())
	}
}

func TestPeerBankAdmin_ResolveByBankCode_NotFound(t *testing.T) {
	h := newAdminTestHandler(t)
	resp, _ := h.ResolvePeerByBankCode(context.Background(), &transactionpb.ResolvePeerByBankCodeRequest{BankCode: "999"})
	if resp.GetFound() {
		t.Errorf("expected found=false")
	}
}

func TestPeerBankAdmin_RejectsOwnCode(t *testing.T) {
	h := newAdminTestHandler(t)
	ctx := context.Background()
	if _, err := h.CreatePeerBank(ctx, &transactionpb.CreatePeerBankRequest{
		BankCode: "111", RoutingNumber: 222, BaseUrl: "http://x/api/v3", ApiToken: "t", Active: true,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("same bank_code: want InvalidArgument, got %v", err)
	}
	if _, err := h.CreatePeerBank(ctx, &transactionpb.CreatePeerBankRequest{
		BankCode: "222", RoutingNumber: 111, BaseUrl: "http://x/api/v3", ApiToken: "t", Active: true,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("same routing: want InvalidArgument, got %v", err)
	}
	if _, err := h.CreatePeerBank(ctx, &transactionpb.CreatePeerBankRequest{
		BankCode: "222", RoutingNumber: 222, BaseUrl: "http://x/api/v3", ApiToken: "t", Active: true,
	}); err != nil {
		t.Fatalf("distinct peer should succeed: %v", err)
	}
}

// TestPeerBankAdmin_RejectsBankCodeRoutingMismatch pins the SI-TX invariant that a
// peer's bank_code IS its numeric routing number. A registration where they differ
// — or a non-numeric code — is rejected, so the inbound peerRoutingForCode(bank_code)
// derivation used by the cross-bank OTC lookups is provably exact for every
// registered peer (no "authenticates but never resolves" peer can exist).
func TestPeerBankAdmin_RejectsBankCodeRoutingMismatch(t *testing.T) {
	h := newAdminTestHandler(t)
	ctx := context.Background()
	// bank_code != routing (neither is our own 111).
	if _, err := h.CreatePeerBank(ctx, &transactionpb.CreatePeerBankRequest{
		BankCode: "333", RoutingNumber: 444, BaseUrl: "http://x/api/v3", ApiToken: "t", Active: true,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bank_code 333 / routing 444: want InvalidArgument, got %v", err)
	}
	// non-numeric bank_code can never resolve via peerRoutingForCode → rejected.
	if _, err := h.CreatePeerBank(ctx, &transactionpb.CreatePeerBankRequest{
		BankCode: "bank4", RoutingNumber: 444, BaseUrl: "http://x/api/v3", ApiToken: "t", Active: true,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("non-numeric bank_code: want InvalidArgument, got %v", err)
	}
	// bank_code == routing succeeds.
	if _, err := h.CreatePeerBank(ctx, &transactionpb.CreatePeerBankRequest{
		BankCode: "444", RoutingNumber: 444, BaseUrl: "http://x/api/v3", ApiToken: "t", Active: true,
	}); err != nil {
		t.Fatalf("matching bank_code/routing should succeed: %v", err)
	}
}

func endsWith(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func parseInt(s string) int64 {
	var n int64
	for _, c := range s {
		n = n*10 + int64(c-'0')
	}
	return n
}
