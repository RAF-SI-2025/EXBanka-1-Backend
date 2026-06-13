package repository_test

import (
	"testing"

	"github.com/exbanka/interbank-service/internal/model"
	"github.com/exbanka/interbank-service/internal/repository"
)

// TestPeerIdempotenceRepo_MarkCommitted_Idempotent verifies the id-based
// MarkCommitted stamps committed_at exactly once: the first call performs the
// transition (returns true) and a retransmit is a no-op (returns false).
func TestPeerIdempotenceRepo_MarkCommitted_Idempotent(t *testing.T) {
	db := newIdemTestDB(t)
	repo := repository.NewPeerIdempotenceRepository(db)

	rec := &model.PeerIdempotenceRecord{
		PeerBankCode: "222", LocallyGeneratedKey: "commit-1",
		TransactionID: "tx-1", ResponsePayloadJSON: "{}",
	}
	if err := repo.Insert(rec); err != nil {
		t.Fatalf("insert: %v", err)
	}

	first, err := repo.MarkCommitted(rec.ID)
	if err != nil {
		t.Fatalf("first MarkCommitted: %v", err)
	}
	if !first {
		t.Fatalf("first MarkCommitted should report the transition")
	}

	// Re-delivered COMMIT_TX: already committed → no-op (false).
	second, err := repo.MarkCommitted(rec.ID)
	if err != nil {
		t.Fatalf("second MarkCommitted: %v", err)
	}
	if second {
		t.Errorf("second MarkCommitted should be a no-op (false)")
	}

	got, _, _ := repo.Lookup("222", "commit-1")
	if got.CommittedAt == nil {
		t.Errorf("committed_at not stamped")
	}
}

// TestPeerIdempotenceRepo_MarkRolledBack_Idempotent mirrors the committed test
// for the rollback transition.
func TestPeerIdempotenceRepo_MarkRolledBack_Idempotent(t *testing.T) {
	db := newIdemTestDB(t)
	repo := repository.NewPeerIdempotenceRepository(db)

	rec := &model.PeerIdempotenceRecord{
		PeerBankCode: "222", LocallyGeneratedKey: "rb-1",
		TransactionID: "tx-2", ResponsePayloadJSON: "{}",
	}
	if err := repo.Insert(rec); err != nil {
		t.Fatalf("insert: %v", err)
	}

	first, err := repo.MarkRolledBack(rec.ID)
	if err != nil {
		t.Fatalf("first MarkRolledBack: %v", err)
	}
	if !first {
		t.Fatalf("first MarkRolledBack should report the transition")
	}

	second, err := repo.MarkRolledBack(rec.ID)
	if err != nil {
		t.Fatalf("second MarkRolledBack: %v", err)
	}
	if second {
		t.Errorf("second MarkRolledBack should be a no-op (false)")
	}

	got, _, _ := repo.Lookup("222", "rb-1")
	if got.RolledBackAt == nil {
		t.Errorf("rolled_back_at not stamped")
	}
}

// TestPeerIdempotenceRepo_LookupByTransactionID verifies the COMMIT_TX/ROLLBACK_TX
// correlation lookup resolves a record by (peer_bank_code, tx_foreign_id), and
// reports found=false on a miss.
func TestPeerIdempotenceRepo_LookupByTransactionID(t *testing.T) {
	db := newIdemTestDB(t)
	repo := repository.NewPeerIdempotenceRepository(db)

	rec := &model.PeerIdempotenceRecord{
		PeerBankCode: "222", LocallyGeneratedKey: "newtx-key",
		TransactionID: "rx-uuid", ResponsePayloadJSON: "{}",
		TxForeignID: "initiator-tx-id",
	}
	if err := repo.Insert(rec); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, found, err := repo.LookupByTransactionID("222", "initiator-tx-id")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !found || got.LocallyGeneratedKey != "newtx-key" {
		t.Fatalf("expected hit on tx_foreign_id, got found=%v rec=%+v", found, got)
	}

	// Wrong bank code → miss.
	if _, found, _ := repo.LookupByTransactionID("999", "initiator-tx-id"); found {
		t.Errorf("expected miss for wrong peer bank code")
	}
	// Unknown transaction id → miss.
	if _, found, _ := repo.LookupByTransactionID("222", "nope"); found {
		t.Errorf("expected miss for unknown transaction id")
	}
}
