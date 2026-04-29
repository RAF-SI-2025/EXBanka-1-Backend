package repository_test

import (
	"testing"
	"time"

	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newOutboundTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.OutboundPeerTx{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestOutboundPeerTxRepo_CreateAndGet(t *testing.T) {
	db := newOutboundTestDB(t)
	repo := repository.NewOutboundPeerTxRepository(db)

	row := &model.OutboundPeerTx{
		IdempotenceKey: "tx-1",
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   `[{"routingNumber":111}]`,
		Status:         "pending",
	}
	if err := repo.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetByIdempotenceKey("tx-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PeerBankCode != "222" || got.Status != "pending" {
		t.Errorf("got %+v", got)
	}
}

func TestOutboundPeerTxRepo_GetByIdempotenceKey_NotFound(t *testing.T) {
	db := newOutboundTestDB(t)
	repo := repository.NewOutboundPeerTxRepository(db)
	_, err := repo.GetByIdempotenceKey("nope")
	if err == nil {
		t.Errorf("expected error on missing key")
	}
}

func TestOutboundPeerTxRepo_ListPendingOlderThan(t *testing.T) {
	db := newOutboundTestDB(t)
	repo := repository.NewOutboundPeerTxRepository(db)

	now := time.Now().UTC()
	long := now.Add(-2 * time.Minute)
	recent := now.Add(-10 * time.Second)

	rows := []*model.OutboundPeerTx{
		{IdempotenceKey: "old-pending", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "pending", LastAttemptAt: &long},
		{IdempotenceKey: "recent-pending", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "pending", LastAttemptAt: &recent},
		{IdempotenceKey: "never-pending", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "pending"},
		{IdempotenceKey: "committed", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "committed", LastAttemptAt: &long},
	}
	for _, r := range rows {
		if err := repo.Create(r); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	got, err := repo.ListPendingOlderThan(now.Add(-1 * time.Minute))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Expect: old-pending (2m ago) and never-pending (nil treated as old).
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d (%v)", len(got), got)
	}
	keys := map[string]bool{}
	for _, r := range got {
		keys[r.IdempotenceKey] = true
	}
	if !keys["old-pending"] || !keys["never-pending"] {
		t.Errorf("unexpected rows: %+v", got)
	}
}

func TestOutboundPeerTxRepo_MarkAttempt(t *testing.T) {
	db := newOutboundTestDB(t)
	repo := repository.NewOutboundPeerTxRepository(db)

	row := &model.OutboundPeerTx{IdempotenceKey: "tx-2", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "pending"}
	if err := repo.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.MarkAttempt("tx-2", "network error"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	got, _ := repo.GetByIdempotenceKey("tx-2")
	if got.AttemptCount != 1 || got.LastError != "network error" || got.LastAttemptAt == nil {
		t.Errorf("got %+v", got)
	}
}

func TestOutboundPeerTxRepo_MarkCommittedAndRolledBack(t *testing.T) {
	db := newOutboundTestDB(t)
	repo := repository.NewOutboundPeerTxRepository(db)

	a := &model.OutboundPeerTx{IdempotenceKey: "tx-a", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "pending"}
	b := &model.OutboundPeerTx{IdempotenceKey: "tx-b", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "pending"}
	_ = repo.Create(a)
	_ = repo.Create(b)

	if err := repo.MarkCommitted("tx-a"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := repo.MarkRolledBack("tx-b", "peer voted NO"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	gotA, _ := repo.GetByIdempotenceKey("tx-a")
	gotB, _ := repo.GetByIdempotenceKey("tx-b")
	if gotA.Status != "committed" {
		t.Errorf("a status: %s", gotA.Status)
	}
	if gotB.Status != "rolled_back" || gotB.LastError != "peer voted NO" {
		t.Errorf("b: %+v", gotB)
	}
}

func TestOutboundPeerTxRepo_MarkFailed(t *testing.T) {
	db := newOutboundTestDB(t)
	repo := repository.NewOutboundPeerTxRepository(db)

	row := &model.OutboundPeerTx{IdempotenceKey: "tx-fail", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "pending"}
	_ = repo.Create(row)

	if err := repo.MarkFailed("tx-fail", "max retries exceeded"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	got, _ := repo.GetByIdempotenceKey("tx-fail")
	if got.Status != "failed" || got.LastError != "max retries exceeded" {
		t.Errorf("got %+v", got)
	}
}
