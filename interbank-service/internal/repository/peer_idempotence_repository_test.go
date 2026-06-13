package repository_test

import (
	"testing"

	"github.com/exbanka/interbank-service/internal/model"
	"github.com/exbanka/interbank-service/internal/repository"
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

func TestUpsertPending_ThenUpsertDone(t *testing.T) {
	db := newIdemTestDB(t)
	repo := repository.NewPeerIdempotenceRepository(db)
	peer, idem := "222", "k-async-1"

	// 1. UpsertPending creates a pending row.
	if err := repo.UpsertPending(&model.PeerIdempotenceRecord{PeerBankCode: peer, LocallyGeneratedKey: idem, TxForeignID: "tx-1"}); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	rec, found, _ := repo.Lookup(peer, idem)
	if !found || rec.Status != "pending" {
		t.Fatalf("want pending row, got found=%v status=%q", found, rec.Status)
	}

	// 2. UpsertDone overwrites pending -> done with the vote.
	if err := repo.UpsertDone(&model.PeerIdempotenceRecord{PeerBankCode: peer, LocallyGeneratedKey: idem, TransactionID: "rx-uuid", ResponsePayloadJSON: `{"type":"YES"}`, TxForeignID: "tx-1"}); err != nil {
		t.Fatalf("UpsertDone: %v", err)
	}
	rec, _, _ = repo.Lookup(peer, idem)
	if rec.Status != "done" || rec.ResponsePayloadJSON != `{"type":"YES"}` {
		t.Fatalf("want done+vote, got status=%q payload=%q", rec.Status, rec.ResponsePayloadJSON)
	}

	// 3. UpsertPending against an existing done row is a no-op (does NOT clobber).
	if err := repo.UpsertPending(&model.PeerIdempotenceRecord{PeerBankCode: peer, LocallyGeneratedKey: idem, TxForeignID: "tx-1"}); err != nil {
		t.Fatalf("UpsertPending(2): %v", err)
	}
	rec, _, _ = repo.Lookup(peer, idem)
	if rec.Status != "done" || rec.ResponsePayloadJSON != `{"type":"YES"}` {
		t.Fatalf("UpsertPending clobbered a done row: status=%q payload=%q", rec.Status, rec.ResponsePayloadJSON)
	}
}
