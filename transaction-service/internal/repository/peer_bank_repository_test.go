package repository_test

import (
	"testing"

	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newPeerBankTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.PeerBank{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestPeerBankRepository_CreateGetByCode(t *testing.T) {
	db := newPeerBankTestDB(t)
	repo := repository.NewPeerBankRepository(db)

	pb := &model.PeerBank{
		BankCode:          "222",
		RoutingNumber:     222,
		BaseURL:           "http://peer-222/api/v3",
		APITokenBcrypt:    "$2a$10$dummybcrypt",
		APITokenPlaintext: "test-token-222",
		Active:            true,
	}
	if err := repo.Create(pb); err != nil {
		t.Fatalf("create: %v", err)
	}
	if pb.ID == 0 {
		t.Fatalf("create did not set ID")
	}

	got, err := repo.GetByBankCode("222")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.BankCode != "222" || got.RoutingNumber != 222 {
		t.Errorf("got %+v", got)
	}
}

func TestPeerBankRepository_GetByCode_NotFound(t *testing.T) {
	db := newPeerBankTestDB(t)
	repo := repository.NewPeerBankRepository(db)

	_, err := repo.GetByBankCode("999")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestPeerBankRepository_List(t *testing.T) {
	db := newPeerBankTestDB(t)
	repo := repository.NewPeerBankRepository(db)

	for _, c := range []struct {
		code   string
		rn     int64
		active bool
	}{{"222", 222, true}, {"333", 333, true}, {"444", 444, false}} {
		pb := &model.PeerBank{
			BankCode:          c.code,
			RoutingNumber:     c.rn,
			BaseURL:           "http://peer/" + c.code,
			APITokenBcrypt:    "$2a$10$x",
			APITokenPlaintext: "tok-" + c.code,
			Active:            c.active,
		}
		if err := repo.Create(pb); err != nil {
			t.Fatalf("create %s: %v", c.code, err)
		}
	}

	all, err := repo.List(false)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("list all: got %d", len(all))
	}

	active, err := repo.List(true)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("list active: got %d", len(active))
	}
}

func TestPeerBankRepository_UpdateAndDelete(t *testing.T) {
	db := newPeerBankTestDB(t)
	repo := repository.NewPeerBankRepository(db)

	pb := &model.PeerBank{
		BankCode:          "222",
		RoutingNumber:     222,
		BaseURL:           "http://old",
		APITokenBcrypt:    "$2a$10$x",
		APITokenPlaintext: "old",
		Active:            true,
	}
	if err := repo.Create(pb); err != nil {
		t.Fatalf("create: %v", err)
	}

	pb.BaseURL = "http://new"
	pb.Active = false
	if err := repo.Update(pb); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, _ := repo.GetByID(pb.ID)
	if got.BaseURL != "http://new" || got.Active {
		t.Errorf("update not persisted: %+v", got)
	}

	if err := repo.Delete(pb.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetByID(pb.ID); err == nil {
		t.Errorf("expected not-found after delete")
	}
}
