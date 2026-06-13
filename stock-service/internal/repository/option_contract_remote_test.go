// Package repository — REMOTE option-contract method tests (SP-2a).
//
// These port the retired peer-option-contract mirror repo tests onto the
// unified OptionContractRepository remote methods. A REMOTE contract is an
// OptionContract row with routing_number != OwnRouting() (the COUNTERPARTY
// routing) and native_id = "<crossbank_tx_id>:<posting_index>"; the cross-bank
// negotiation/direction/parties live in the Remote* columns + the shared
// columns. Every remote method scopes to routing_number != OwnRouting(), so a
// LOCAL row (routing == own) can never satisfy a remote query — verified by the
// *_ExcludesLocal tests.
//
// Setup: sqlite :memory:, OwnRouting = 111.
package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

func newRemoteContractRepo(t *testing.T) *OptionContractRepository {
	t.Helper()
	model.SetOwnRouting("111")
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.OptionContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewOptionContractRepository(db)
}

// remoteContract builds a REMOTE OptionContract row keyed on (routing, native)
// satisfying all NOT-NULL / CHECK / ValidateOwner constraints for a remote row
// (OwnerBank + nil owner ids). routing is the COUNTERPARTY (peer) routing.
func remoteContract(routing int64, crossID string, posting int32, direction string, buyerRouting int64, buyerID string, sellerRouting int64, sellerID, settle, status string) *model.OptionContract {
	native := crossID + ":" + itoa(posting)
	bbc := itoa64(buyerRouting)
	sbc := itoa64(sellerRouting)
	cbTx := crossID
	pIdx := posting
	negRouting := int64(222)
	negNative := "neg-1"
	dir := direction
	bID := buyerID
	sID := sellerID
	settleTime := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	if settle != "" {
		if tt, e := time.Parse("2006-01-02", settle); e == nil {
			settleTime = tt
		}
	}
	now := time.Now().UTC()
	return &model.OptionContract{
		RoutingNumber:             routing,
		NativeID:                  &native,
		BuyerOwnerType:            model.OwnerBank,
		BuyerBankCode:             &bbc,
		SellerOwnerType:           model.OwnerBank,
		SellerBankCode:            &sbc,
		Ticker:                    "AAPL",
		Quantity:                  decimal.NewFromInt(5),
		StrikePrice:               decimal.NewFromInt(150),
		PremiumPaid:               decimal.Zero,
		PremiumCurrency:           "USD",
		StrikeCurrency:            "USD",
		SettlementDate:            settleTime,
		Status:                    status,
		SagaID:                    crossID,
		PremiumPaidAt:             now,
		CrossbankTxID:             &cbTx,
		RemotePostingIndex:        &pIdx,
		RemoteNegotiationRouting:  &negRouting,
		RemoteNegotiationNativeID: &negNative,
		RemoteDirection:           &dir,
		RemoteBuyerID:             &bID,
		RemoteSellerID:            &sID,
		CreatedAt:                 now,
		UpdatedAt:                 now,
	}
}

func itoa(n int32) string { return itoa64(int64(n)) }
func itoa64(n int64) string {
	return decimal.NewFromInt(n).String()
}

// localContract builds a LOCAL OptionContract row (routing == own = 111) so the
// *_ExcludesLocal tests can assert remote queries never return it.
func localContract(t *testing.T) *model.OptionContract {
	t.Helper()
	offerID := uint64(42)
	bid := uint64(7)
	now := time.Now().UTC()
	return &model.OptionContract{
		// RoutingNumber left 0 → BeforeCreate stamps OwnRouting (111).
		OfferID:         &offerID,
		BuyerOwnerType:  model.OwnerClient,
		BuyerOwnerID:    &bid,
		SellerOwnerType: model.OwnerBank,
		Ticker:          "AAPL",
		Quantity:        decimal.NewFromInt(5),
		StrikePrice:     decimal.NewFromInt(150),
		PremiumPaid:     decimal.NewFromInt(10),
		PremiumCurrency: "USD",
		StrikeCurrency:  "USD",
		SettlementDate:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), // past
		Status:          model.OptionContractStatusActive,
		SagaID:          "local-saga",
		PremiumPaidAt:   now,
	}
}

func TestRemoteContract_UpsertIdempotent(t *testing.T) {
	r := newRemoteContractRepo(t)
	c := remoteContract(222, "tx-1", 0, "DEBIT", 111, "client-7", 222, "client-99", "", "active")
	if err := r.UpsertRemoteContract(c); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if c.ID == 0 {
		t.Fatal("expected id")
	}
	c2 := remoteContract(222, "tx-1", 0, "DEBIT", 111, "client-7", 222, "client-99", "", "active")
	if err := r.UpsertRemoteContract(c2); err != nil {
		t.Fatalf("upsert idempotent: %v", err)
	}
	if c2.ID != c.ID {
		t.Errorf("expected same id (idempotent on natural key), got %d vs %d", c.ID, c2.ID)
	}
}

func TestRemoteContract_GetByNegotiationAndDirection(t *testing.T) {
	r := newRemoteContractRepo(t)
	c := remoteContract(222, "tx-2", 0, "DEBIT", 111, "client-7", 222, "client-99", "", "active")
	if err := r.UpsertRemoteContract(c); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := r.GetRemoteContractByNegotiationAndDirection(222, "neg-1", "DEBIT")
	if err != nil {
		t.Fatalf("by neg: %v", err)
	}
	if got.ID != c.ID {
		t.Errorf("mismatch id")
	}
	// Wrong direction → not found.
	if _, err := r.GetRemoteContractByNegotiationAndDirection(222, "neg-1", "CREDIT"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected NotFound for wrong direction, got %v", err)
	}
}

func TestRemoteContract_GetByID_NotFoundForLocal(t *testing.T) {
	r := newRemoteContractRepo(t)
	// Seed a LOCAL contract (routing == own).
	lc := localContract(t)
	if err := r.Create(lc); err != nil {
		t.Fatalf("create local: %v", err)
	}
	if lc.RoutingNumber != model.OwnRouting() {
		t.Fatalf("local contract should carry own routing, got %d", lc.RoutingNumber)
	}
	// GetRemoteContractByID must treat the local row as not-found.
	if _, err := r.GetRemoteContractByID(lc.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected NotFound for local row via remote getter, got %v", err)
	}
	// A remote row IS returned.
	rc := remoteContract(222, "tx-id", 0, "CREDIT", 222, "client-9", 111, "client-3", "", "active")
	if err := r.UpsertRemoteContract(rc); err != nil {
		t.Fatalf("upsert remote: %v", err)
	}
	got, err := r.GetRemoteContractByID(rc.ID)
	if err != nil {
		t.Fatalf("get remote by id: %v", err)
	}
	if got.ID != rc.ID {
		t.Errorf("mismatch")
	}
}

func TestRemoteContract_SetStatus(t *testing.T) {
	r := newRemoteContractRepo(t)
	c := remoteContract(222, "tx-3", 0, "DEBIT", 111, "client-7", 222, "client-99", "", "active")
	_ = r.UpsertRemoteContract(c)
	if err := r.SetRemoteContractStatus(c.ID, "exercised"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	got, _ := r.GetRemoteContractByID(c.ID)
	if got.Status != "exercised" {
		t.Errorf("got %s", got.Status)
	}
}

// TestRemoteContract_CompareAndSet_Atomicity verifies the exercise claim
// (active → exercising) is a guarded UPDATE: the WHERE status=from clause means
// only the FIRST attempt against a matching status wins; a second attempt (the
// status no longer matches) loses. This is the DB-serialised concurrency control
// that prevents a double strike charge. (Sequential to avoid the SQLite
// :memory: per-connection isolation; the guard is the WHERE clause, which the
// DB enforces atomically regardless of connection count.)
func TestRemoteContract_CompareAndSet_Atomicity(t *testing.T) {
	r := newRemoteContractRepo(t)
	c := remoteContract(222, "tx-cas", 0, "CREDIT", 222, "client-9", 111, "client-3", "", "active")
	_ = r.UpsertRemoteContract(c)

	// First claim wins (active → exercising).
	won, err := r.CompareAndSetRemoteContractStatus(c.ID, "active", "exercising")
	if err != nil {
		t.Fatalf("first cas: %v", err)
	}
	if !won {
		t.Fatal("first CAS must win")
	}
	// Second claim loses (status is no longer "active").
	won2, err := r.CompareAndSetRemoteContractStatus(c.ID, "active", "exercising")
	if err != nil {
		t.Fatalf("second cas: %v", err)
	}
	if won2 {
		t.Error("second CAS must lose (status already exercising)")
	}
	got, _ := r.GetRemoteContractByID(c.ID)
	if got.Status != "exercising" {
		t.Errorf("expected status exercising, got %s", got.Status)
	}
}

func TestRemoteContract_HasForNegotiation(t *testing.T) {
	r := newRemoteContractRepo(t)
	c := remoteContract(222, "tx-has", 0, "DEBIT", 111, "client-7", 222, "client-99", "", "active")
	_ = r.UpsertRemoteContract(c)
	has, err := r.HasRemoteContractForNegotiation(222, "neg-1")
	if err != nil || !has {
		t.Errorf("expected has=true, got %v/%v", has, err)
	}
	has, err = r.HasRemoteContractForNegotiation(222, "no-such")
	if err != nil || has {
		t.Errorf("expected has=false, got %v/%v", has, err)
	}
}

func TestRemoteContract_ListExpiring_ExcludesLocal(t *testing.T) {
	r := newRemoteContractRepo(t)
	// Seed a LOCAL past-settlement ACTIVE contract — it must NOT appear in the
	// remote expiry list (that's the local cron's job; routing guard separates).
	if err := r.Create(localContract(t)); err != nil {
		t.Fatalf("create local: %v", err)
	}
	old := remoteContract(222, "tx-old", 0, "DEBIT", 111, "client-7", 222, "client-99", "2024-01-01", "active")
	_ = r.UpsertRemoteContract(old)
	fresh := remoteContract(222, "tx-new", 0, "DEBIT", 111, "client-7", 222, "client-99", "2030-01-01", "active")
	_ = r.UpsertRemoteContract(fresh)

	rows, err := r.ListRemoteContractsExpiring("2026-01-01", 100)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 expiring remote (local excluded), got %d: %+v", len(rows), rows)
	}
	if rows[0].NativeID == nil || *rows[0].NativeID != "tx-old:0" {
		t.Errorf("got native %v", rows[0].NativeID)
	}
}

func TestRemoteContract_ListByLocalParticipant_ExcludesLocal(t *testing.T) {
	r := newRemoteContractRepo(t)
	// Seed a LOCAL contract owned by client 7 — must NOT leak into the remote
	// participant list even though the participant id maps to "client-7".
	if err := r.Create(localContract(t)); err != nil {
		t.Fatalf("create local: %v", err)
	}
	// CREDIT (this bank, 111, holds the buyer client-7).
	c1 := remoteContract(222, "tx-buyer", 0, "CREDIT", 111, "client-7", 222, "client-99", "", "active")
	_ = r.UpsertRemoteContract(c1)
	// DEBIT (this bank, 111, holds the seller client-7).
	c2 := remoteContract(222, "tx-seller", 1, "DEBIT", 222, "client-99", 111, "client-7", "", "active")
	_ = r.UpsertRemoteContract(c2)

	rows, total, err := r.ListRemoteContractsByLocalParticipant("client-7", 111, "buyer", 1, 10)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Errorf("buyer role: got %d/%d", total, len(rows))
	}
	rows, total, err = r.ListRemoteContractsByLocalParticipant("client-7", 111, "seller", 1, 10)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Errorf("seller role: got %d/%d", total, len(rows))
	}
	rows, total, err = r.ListRemoteContractsByLocalParticipant("client-7", 111, "either", 1, 10)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if total != 2 || len(rows) != 2 {
		t.Errorf("either role: got %d/%d (local must be excluded)", total, len(rows))
	}
}

// ListRemoteContractsByBankParty matches the BANK party by the "employee-%"
// prefix (the bank has no single wire principal), scoped to the side WE host:
// a CREDIT row where this bank holds the buyer (remote_buyer_id LIKE employee-%)
// or a DEBIT row where this bank holds the seller (remote_seller_id LIKE
// employee-%). Client rows, peer-hosted bank rows, and LOCAL rows are excluded.
func TestRemoteContract_ListByBankParty_PrefixMatch(t *testing.T) {
	r := newRemoteContractRepo(t)
	// Seed a LOCAL contract — must NEVER leak into the remote bank-party list.
	if err := r.Create(localContract(t)); err != nil {
		t.Fatalf("create local: %v", err)
	}
	// CREDIT (this bank, 111, holds the BUYER = our bank → employee-1).
	credit := remoteContract(222, "tx-bankbuy", 0, "CREDIT", 111, "employee-1", 222, "client-99", "", "active")
	_ = r.UpsertRemoteContract(credit)
	// DEBIT (this bank, 111, holds the SELLER = our bank → employee-2).
	debit := remoteContract(222, "tx-banksell", 1, "DEBIT", 222, "client-99", 111, "employee-2", "", "active")
	_ = r.UpsertRemoteContract(debit)
	// A CLIENT-side remote row WE host (CREDIT, buyer = client-7) — must be
	// EXCLUDED from the bank-party list (client identity, not employee).
	clientRow := remoteContract(222, "tx-clientbuy", 0, "CREDIT", 111, "client-7", 222, "client-99", "", "active")
	_ = r.UpsertRemoteContract(clientRow)
	// A PEER-HOSTED bank row (the bank side lives at routing 222, NOT us) — must
	// be EXCLUDED: CREDIT held by the peer (buyer routing 222 = employee-9).
	peerHosted := remoteContract(222, "tx-peerbank", 0, "CREDIT", 222, "employee-9", 111, "client-7", "", "active")
	_ = r.UpsertRemoteContract(peerHosted)

	// buyer role → only the CREDIT bank-buy row.
	rows, total, err := r.ListRemoteContractsByBankParty(111, "buyer", 1, 10)
	if err != nil {
		t.Fatalf("buyer err: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("buyer role: got %d/%d, want 1/1", total, len(rows))
	}
	if rows[0].RemoteBuyerID == nil || *rows[0].RemoteBuyerID != "employee-1" {
		t.Errorf("buyer row remote_buyer_id = %v, want employee-1", rows[0].RemoteBuyerID)
	}

	// seller role → only the DEBIT bank-sell row.
	rows, total, err = r.ListRemoteContractsByBankParty(111, "seller", 1, 10)
	if err != nil {
		t.Fatalf("seller err: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("seller role: got %d/%d, want 1/1", total, len(rows))
	}
	if rows[0].RemoteSellerID == nil || *rows[0].RemoteSellerID != "employee-2" {
		t.Errorf("seller row remote_seller_id = %v, want employee-2", rows[0].RemoteSellerID)
	}

	// either role → both bank rows; client + peer-hosted + local all excluded.
	rows, total, err = r.ListRemoteContractsByBankParty(111, "either", 1, 10)
	if err != nil {
		t.Fatalf("either err: %v", err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("either role: got %d/%d, want 2/2 (client/peer-hosted/local excluded)", total, len(rows))
	}
}
