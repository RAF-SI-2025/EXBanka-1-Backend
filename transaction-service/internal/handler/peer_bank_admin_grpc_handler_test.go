package handler_test

import (
	"context"
	"testing"

	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/handler"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newAdminTestHandler(t *testing.T) *handler.PeerBankAdminGRPCHandler {
	t.Helper()
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err := db.AutoMigrate(&model.PeerBank{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return handler.NewPeerBankAdminGRPCHandler(repository.NewPeerBankRepository(db))
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
