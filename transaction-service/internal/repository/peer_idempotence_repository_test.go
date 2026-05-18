package repository_test

import (
	"testing"

	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newIdemTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.PeerIdempotenceRecord{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestPeerIdempotenceRepo_InsertAndLookup(t *testing.T) {
	db := newIdemTestDB(t)
	repo := repository.NewPeerIdempotenceRepository(db)

	rec := &model.PeerIdempotenceRecord{
		PeerBankCode:        "222",
		LocallyGeneratedKey: "abc-123",
		TransactionID:       "tx-1",
		ResponsePayloadJSON: `{"type":"YES"}`,
	}
	if err := repo.Insert(rec); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, found, err := repo.Lookup("222", "abc-123")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !found {
		t.Fatalf("expected found=true")
	}
	if got.TransactionID != "tx-1" || got.ResponsePayloadJSON != `{"type":"YES"}` {
		t.Errorf("got %+v", got)
	}
}

func TestPeerIdempotenceRepo_LookupMiss(t *testing.T) {
	db := newIdemTestDB(t)
	repo := repository.NewPeerIdempotenceRepository(db)

	_, found, err := repo.Lookup("222", "nope")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if found {
		t.Fatalf("expected found=false on miss")
	}
}

func TestPeerIdempotenceRepo_DuplicateKeyRejected(t *testing.T) {
	db := newIdemTestDB(t)
	repo := repository.NewPeerIdempotenceRepository(db)

	a := &model.PeerIdempotenceRecord{PeerBankCode: "222", LocallyGeneratedKey: "k", TransactionID: "1", ResponsePayloadJSON: "{}"}
	b := &model.PeerIdempotenceRecord{PeerBankCode: "222", LocallyGeneratedKey: "k", TransactionID: "2", ResponsePayloadJSON: "{}"}
	if err := repo.Insert(a); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := repo.Insert(b); err == nil {
		t.Fatalf("second insert: expected unique-constraint error")
	}
}
