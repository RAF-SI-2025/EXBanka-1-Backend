package repository_test

import (
	"errors"
	"testing"
	"time"

	"github.com/exbanka/interbank-service/internal/model"
	"github.com/exbanka/interbank-service/internal/repository"
)

// TestOutboundPeerTxRepo_ListResumableOlderThan verifies the resumable query
// returns BOTH pending and committing rows whose last attempt is older than the
// cutoff (or never attempted), and excludes terminal rows + recently-attempted
// rows.
func TestOutboundPeerTxRepo_ListResumableOlderThan(t *testing.T) {
	db := newOutboundTestDB(t)
	repo := repository.NewOutboundPeerTxRepository(db)

	now := time.Now().UTC()
	old := now.Add(-5 * time.Minute)
	recent := now.Add(-1 * time.Second)

	rows := []*model.OutboundPeerTx{
		{IdempotenceKey: "old-pending", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "pending", LastAttemptAt: &old},
		{IdempotenceKey: "old-committing", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "committing", LastAttemptAt: &old},
		{IdempotenceKey: "never-pending", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "pending"},
		{IdempotenceKey: "recent-pending", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "pending", LastAttemptAt: &recent},
		{IdempotenceKey: "committed", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "committed", LastAttemptAt: &old},
		{IdempotenceKey: "rolled-back", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "rolled_back", LastAttemptAt: &old},
	}
	for _, r := range rows {
		if err := repo.Create(r); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	got, err := repo.ListResumableOlderThan(now.Add(-1 * time.Minute))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	keys := map[string]bool{}
	for _, r := range got {
		keys[r.IdempotenceKey] = true
	}
	want := []string{"old-pending", "old-committing", "never-pending"}
	if len(got) != len(want) {
		t.Fatalf("expected %d resumable rows, got %d (%v)", len(want), len(got), keys)
	}
	for _, k := range want {
		if !keys[k] {
			t.Errorf("expected resumable row %q in result, got %v", k, keys)
		}
	}
	if keys["recent-pending"] || keys["committed"] || keys["rolled-back"] {
		t.Errorf("terminal/recent rows must be excluded, got %v", keys)
	}
}

// TestOutboundPeerTxRepo_MarkCommitting verifies the saga pivot: pending →
// committing succeeds, a second call (already committing) is an idempotent
// no-op (nil), and a terminal row returns ErrPeerTxAlreadyResolved.
func TestOutboundPeerTxRepo_MarkCommitting(t *testing.T) {
	db := newOutboundTestDB(t)
	repo := repository.NewOutboundPeerTxRepository(db)

	pending := &model.OutboundPeerTx{IdempotenceKey: "pivot-1", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "pending"}
	if err := repo.Create(pending); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := repo.MarkCommitting("pivot-1"); err != nil {
		t.Fatalf("first MarkCommitting: %v", err)
	}
	if row, _ := repo.GetByIdempotenceKey("pivot-1"); row.Status != "committing" {
		t.Fatalf("expected committing, got %s", row.Status)
	}

	// Idempotent: already committing → nil (no error).
	if err := repo.MarkCommitting("pivot-1"); err != nil {
		t.Errorf("idempotent MarkCommitting should be nil, got %v", err)
	}

	// Terminal row → ErrPeerTxAlreadyResolved.
	terminal := &model.OutboundPeerTx{IdempotenceKey: "pivot-2", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "committed"}
	if err := repo.Create(terminal); err != nil {
		t.Fatalf("create terminal: %v", err)
	}
	if err := repo.MarkCommitting("pivot-2"); !errors.Is(err, repository.ErrPeerTxAlreadyResolved) {
		t.Errorf("expected ErrPeerTxAlreadyResolved on terminal row, got %v", err)
	}
}

// TestOutboundPeerTxRepo_MarkCommitted_FromCommitting verifies the committing →
// committed forward step is allowed (the resumable saga only ever drives
// committing forward).
func TestOutboundPeerTxRepo_MarkCommitted_FromCommitting(t *testing.T) {
	db := newOutboundTestDB(t)
	repo := repository.NewOutboundPeerTxRepository(db)

	row := &model.OutboundPeerTx{IdempotenceKey: "fwd-1", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "committing"}
	if err := repo.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.MarkCommitted("fwd-1"); err != nil {
		t.Fatalf("MarkCommitted from committing: %v", err)
	}
	if got, _ := repo.GetByIdempotenceKey("fwd-1"); got.Status != "committed" {
		t.Errorf("expected committed, got %s", got.Status)
	}
}

// TestOutboundPeerTxRepo_MarkCommitted_AlreadyResolved verifies a terminal row
// is not resurrected: MarkCommitted on a rolled_back row returns
// ErrPeerTxAlreadyResolved.
func TestOutboundPeerTxRepo_MarkCommitted_AlreadyResolved(t *testing.T) {
	db := newOutboundTestDB(t)
	repo := repository.NewOutboundPeerTxRepository(db)

	row := &model.OutboundPeerTx{IdempotenceKey: "term-1", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "rolled_back"}
	if err := repo.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.MarkCommitted("term-1"); !errors.Is(err, repository.ErrPeerTxAlreadyResolved) {
		t.Errorf("expected ErrPeerTxAlreadyResolved, got %v", err)
	}
}

// TestOutboundPeerTxRepo_MarkRolledBack_AlreadyResolved verifies MarkRolledBack
// only fires on a pending row; a committing row is not pending, so the guard
// returns ErrPeerTxAlreadyResolved and the caller skips the local reversal.
func TestOutboundPeerTxRepo_MarkRolledBack_AlreadyResolved(t *testing.T) {
	db := newOutboundTestDB(t)
	repo := repository.NewOutboundPeerTxRepository(db)

	row := &model.OutboundPeerTx{IdempotenceKey: "rb-guard", PeerBankCode: "222", TxKind: "transfer", PostingsJSON: "[]", Status: "committing"}
	if err := repo.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.MarkRolledBack("rb-guard", "peer voted NO"); !errors.Is(err, repository.ErrPeerTxAlreadyResolved) {
		t.Errorf("expected ErrPeerTxAlreadyResolved on non-pending row, got %v", err)
	}
	// Status must be unchanged (still committing).
	if got, _ := repo.GetByIdempotenceKey("rb-guard"); got.Status != "committing" {
		t.Errorf("committing row must not be rolled back, got %s", got.Status)
	}
}
