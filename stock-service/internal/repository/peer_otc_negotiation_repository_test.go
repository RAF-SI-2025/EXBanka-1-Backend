package repository_test

import (
	"testing"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newPeerOtcTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.PeerOtcNegotiation{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestPeerOtcNegRepo_CreateAndGet(t *testing.T) {
	db := newPeerOtcTestDB(t)
	repo := repository.NewPeerOtcNegotiationRepository(db)

	neg := &model.PeerOtcNegotiation{
		PeerBankCode:        "222",
		ForeignID:           "neg-1",
		BuyerRoutingNumber:  222,
		BuyerID:             "client-7",
		SellerRoutingNumber: 111,
		SellerID:            "client-3",
		OfferJSON:           `{"ticker":"AAPL","amount":100}`,
		Status:              "ongoing",
	}
	if err := repo.Create(neg); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetByPeerAndID("222", "neg-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.BuyerID != "client-7" || got.SellerID != "client-3" {
		t.Errorf("got %+v", got)
	}
}

func TestPeerOtcNegRepo_GetNotFound(t *testing.T) {
	db := newPeerOtcTestDB(t)
	repo := repository.NewPeerOtcNegotiationRepository(db)
	_, err := repo.GetByPeerAndID("222", "nope")
	if err == nil {
		t.Fatalf("expected error on missing")
	}
}

func TestPeerOtcNegRepo_UpdateOfferAndStatus(t *testing.T) {
	db := newPeerOtcTestDB(t)
	repo := repository.NewPeerOtcNegotiationRepository(db)

	neg := &model.PeerOtcNegotiation{
		PeerBankCode: "222", ForeignID: "neg-2",
		BuyerRoutingNumber: 222, BuyerID: "b", SellerRoutingNumber: 111, SellerID: "s",
		OfferJSON: `{"premium":"100"}`, Status: "ongoing",
	}
	_ = repo.Create(neg)

	if err := repo.UpdateOffer("222", "neg-2", `{"premium":"200"}`); err != nil {
		t.Fatalf("update offer: %v", err)
	}
	got, _ := repo.GetByPeerAndID("222", "neg-2")
	if got.OfferJSON != `{"premium":"200"}` {
		t.Errorf("offer not updated: %s", got.OfferJSON)
	}

	if err := repo.UpdateStatus("222", "neg-2", "accepted"); err != nil {
		t.Fatalf("update status: %v", err)
	}
	got, _ = repo.GetByPeerAndID("222", "neg-2")
	if got.Status != "accepted" {
		t.Errorf("status: %s", got.Status)
	}
}

func TestPeerOtcNegRepo_Delete(t *testing.T) {
	db := newPeerOtcTestDB(t)
	repo := repository.NewPeerOtcNegotiationRepository(db)
	neg := &model.PeerOtcNegotiation{
		PeerBankCode: "222", ForeignID: "neg-3",
		BuyerRoutingNumber: 222, BuyerID: "b", SellerRoutingNumber: 111, SellerID: "s",
		OfferJSON: "{}", Status: "ongoing",
	}
	_ = repo.Create(neg)
	if err := repo.Delete("222", "neg-3"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetByPeerAndID("222", "neg-3"); err == nil {
		t.Errorf("expected gone after delete")
	}
}
