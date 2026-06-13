// remaining_coverage_test.go — covers 0% functions not reached by prior test files:
//
//	HoldingReservationRepository: GetByCrossbankTxID, GetByCrossbankTxIDForUpdate,
//	                               GetByPeerOptionContractIDForUpdate
//	OTCNegotiationRepository: DB, CreateTx, Save, SaveTx, FindChainByBidderTx,
//	                           AppendRevision, ListRemoteNegByParent, AppendRemoteRevision
//	OrderRepository:         GetBySagaID
//	OptionContractRepository: GetBySagaID
package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

// ---------------------------------------------------------------------------
// HoldingReservation — CrossbankTxID and PeerOptionContractID paths
// ---------------------------------------------------------------------------

// seedCrossbankReservation creates a HoldingReservation with only CrossbankTxID
// set (no OrderID/OTCContractID/PeerOptionContractID).
func seedCrossbankReservation(t *testing.T, db *gorm.DB, crossbankTxID string) *model.HoldingReservation {
	t.Helper()
	h := seedHolding(t, db)
	res := &model.HoldingReservation{
		HoldingID:     h.ID,
		CrossbankTxID: strPtr(crossbankTxID),
		Quantity:      100,
		Status:        model.HoldingReservationStatusActive,
	}
	if err := db.Create(res).Error; err != nil {
		t.Fatalf("seedCrossbankReservation: %v", err)
	}
	return res
}

// seedPeerOTCReservation creates a HoldingReservation with only PeerOptionContractID set.
func seedPeerOTCReservation(t *testing.T, db *gorm.DB, peerContractID uint64) *model.HoldingReservation {
	t.Helper()
	h := seedHolding(t, db)
	res := &model.HoldingReservation{
		HoldingID:            h.ID,
		PeerOptionContractID: uint64Ptr(peerContractID),
		Quantity:             50,
		Status:               model.HoldingReservationStatusActive,
	}
	if err := db.Create(res).Error; err != nil {
		t.Fatalf("seedPeerOTCReservation: %v", err)
	}
	return res
}

func strPtr(s string) *string { return &s }

func TestHoldingReservation_GetByCrossbankTxID(t *testing.T) {
	db := newHoldingReservationTestDB(t)
	repo := NewHoldingReservationRepository(db)

	res := seedCrossbankReservation(t, db, "bank-222:uuid-abc")

	got, err := repo.GetByCrossbankTxID("bank-222:uuid-abc")
	if err != nil {
		t.Fatalf("GetByCrossbankTxID: %v", err)
	}
	if got.ID != res.ID {
		t.Errorf("id mismatch: got %d want %d", got.ID, res.ID)
	}

	// Not found
	_, err = repo.GetByCrossbankTxID("nonexistent-tx")
	if err == nil {
		t.Error("expected error for missing crossbank_tx_id")
	}
}

func TestHoldingReservation_GetByCrossbankTxIDForUpdate(t *testing.T) {
	db := newHoldingReservationTestDB(t)
	repo := NewHoldingReservationRepository(db)

	res := seedCrossbankReservation(t, db, "bank-333:uuid-def")

	// SQLite ignores SELECT FOR UPDATE but the query must succeed.
	got, err := repo.GetByCrossbankTxIDForUpdate("bank-333:uuid-def")
	if err != nil {
		t.Fatalf("GetByCrossbankTxIDForUpdate: %v", err)
	}
	if got.ID != res.ID {
		t.Errorf("id mismatch: got %d want %d", got.ID, res.ID)
	}

	// Not found
	_, err = repo.GetByCrossbankTxIDForUpdate("bank-333:not-there")
	if err == nil {
		t.Error("expected error for missing crossbank_tx_id")
	}
}

func TestHoldingReservation_GetByPeerOptionContractIDForUpdate(t *testing.T) {
	db := newHoldingReservationTestDB(t)
	repo := NewHoldingReservationRepository(db)

	res := seedPeerOTCReservation(t, db, 9001)

	got, err := repo.GetByPeerOptionContractIDForUpdate(9001)
	if err != nil {
		t.Fatalf("GetByPeerOptionContractIDForUpdate: %v", err)
	}
	if got.ID != res.ID {
		t.Errorf("id mismatch: got %d want %d", got.ID, res.ID)
	}

	// Not found
	_, err = repo.GetByPeerOptionContractIDForUpdate(99999)
	if err == nil {
		t.Error("expected error for missing peer_option_contract_id")
	}
}

// ---------------------------------------------------------------------------
// OTCNegotiationRepository — DB, CreateTx, Save, SaveTx, FindChainByBidderTx,
//                             AppendRevision, ListRemoteNegByParent, AppendRemoteRevision
// ---------------------------------------------------------------------------

func TestOTCNegotiationRepository_DB(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)
	if r.DB() != db {
		t.Error("DB() should return the underlying *gorm.DB")
	}
}

func TestOTCNegotiationRepository_CreateTx(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)

	bidder := uint64(201)
	n := newSampleNegotiation(601, &bidder, model.OTCNegotiationStatusOpen)

	if err := db.Transaction(func(tx *gorm.DB) error {
		return r.CreateTx(tx, n)
	}); err != nil {
		t.Fatalf("CreateTx: %v", err)
	}
	if n.ID == 0 {
		t.Error("expected non-zero ID after CreateTx")
	}

	got, err := r.GetByID(n.ID)
	if err != nil {
		t.Fatalf("GetByID after CreateTx: %v", err)
	}
	if got.ParentOfferID != 601 {
		t.Errorf("parent offer id mismatch")
	}
}

func TestOTCNegotiationRepository_Save(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)

	bidder := uint64(301)
	n := newSampleNegotiation(701, &bidder, model.OTCNegotiationStatusOpen)
	if err := r.Create(n); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Load fresh and stale copies before any mutation.
	fresh, err := r.GetByID(n.ID)
	if err != nil {
		t.Fatalf("get fresh: %v", err)
	}
	stale, err := r.GetByID(n.ID)
	if err != nil {
		t.Fatalf("get stale: %v", err)
	}

	// Save the fresh copy → must succeed.
	fresh.Status = model.OTCNegotiationStatusCountered
	if err := r.Save(fresh); err != nil {
		t.Fatalf("Save (fresh): %v", err)
	}

	// Save the stale copy → must return ErrOptimisticLock (DB version is now 1,
	// stale still carries version = 0).
	stale.Status = model.OTCNegotiationStatusCancelled
	if err := r.Save(stale); !errors.Is(err, ErrOptimisticLock) {
		t.Errorf("Save (stale): expected ErrOptimisticLock, got %v", err)
	}

	// Verify the countered status from the winning save.
	reloaded, _ := r.GetByID(n.ID)
	if reloaded.Status != model.OTCNegotiationStatusCountered {
		t.Errorf("status after save: got %s want %s", reloaded.Status, model.OTCNegotiationStatusCountered)
	}
}

func TestOTCNegotiationRepository_SaveTx(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)

	bidder := uint64(401)
	n := newSampleNegotiation(801, &bidder, model.OTCNegotiationStatusOpen)
	if err := r.Create(n); err != nil {
		t.Fatalf("Create: %v", err)
	}

	n.Status = model.OTCNegotiationStatusCountered
	if err := db.Transaction(func(tx *gorm.DB) error {
		return r.SaveTx(tx, n)
	}); err != nil {
		t.Fatalf("SaveTx: %v", err)
	}

	reloaded, _ := r.GetByID(n.ID)
	if reloaded.Status != model.OTCNegotiationStatusCountered {
		t.Errorf("status after SaveTx: got %s", reloaded.Status)
	}

	// Optimistic lock via SaveTx — save with stale version.
	stale, _ := r.GetByID(n.ID)
	stale.Version = 0 // rewind to a stale version
	stale.Status = model.OTCNegotiationStatusCancelled
	var saveTxErr error
	_ = db.Transaction(func(tx *gorm.DB) error {
		saveTxErr = r.SaveTx(tx, stale)
		return saveTxErr
	})
	if !errors.Is(saveTxErr, ErrOptimisticLock) {
		t.Errorf("SaveTx stale: expected ErrOptimisticLock, got %v", saveTxErr)
	}
}

func TestOTCNegotiationRepository_FindChainByBidderTx(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)

	bidder := uint64(501)
	n := newSampleNegotiation(901, &bidder, model.OTCNegotiationStatusOpen)
	if err := r.Create(n); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Found case inside a transaction.
	var found *model.OTCNegotiation
	if err := db.Transaction(func(tx *gorm.DB) error {
		var e error
		found, e = r.FindChainByBidderTx(tx, 901, model.OwnerClient, &bidder)
		return e
	}); err != nil {
		t.Fatalf("FindChainByBidderTx: %v", err)
	}
	if found == nil || found.ID != n.ID {
		t.Errorf("expected chain id=%d, got %v", n.ID, found)
	}

	// Not-found case.
	other := uint64(9999)
	notFoundErr := db.Transaction(func(tx *gorm.DB) error {
		_, e := r.FindChainByBidderTx(tx, 901, model.OwnerClient, &other)
		return e
	})
	if !errors.Is(notFoundErr, gorm.ErrRecordNotFound) {
		t.Errorf("FindChainByBidderTx not-found: expected ErrRecordNotFound, got %v", notFoundErr)
	}
}

func TestOTCNegotiationRepository_AppendRevision(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)

	bidder := uint64(601)
	n := newSampleNegotiation(1001, &bidder, model.OTCNegotiationStatusOpen)
	if err := r.Create(n); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rev := &model.OTCNegotiationRevision{
		NegotiationID:           n.ID,
		RevisionNumber:          1,
		Quantity:                decimal.NewFromInt(5),
		StrikePrice:             decimal.NewFromFloat(100),
		Premium:                 decimal.NewFromFloat(3),
		SettlementDate:          time.Now().UTC().AddDate(0, 1, 0),
		ModifiedByPrincipalType: "client",
		ModifiedByPrincipalID:   601,
		Action:                  model.OTCNegotiationActionBid,
	}
	if err := r.AppendRevision(rev); err != nil {
		t.Fatalf("AppendRevision: %v", err)
	}
	if rev.ID == 0 {
		t.Error("expected non-zero revision ID")
	}

	revs, err := r.ListRevisions(n.ID)
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if len(revs) != 1 || revs[0].Action != model.OTCNegotiationActionBid {
		t.Errorf("expected 1 BID revision, got %d revisions", len(revs))
	}
}

func TestOTCNegotiationRepository_ListRemoteNegByParent(t *testing.T) {
	db := newRevTestDB(t) // migrates OTCNegotiation + OTCNegotiationRevision; own=111
	r := NewOTCNegotiationRepository(db)

	parentRouting := int64(111)
	parentNative := "offer-parent-1"

	// Two ongoing rows under the target parent.
	row1 := remoteNeg(222, "rnbp-1", 222, "client-1", 111, "client-3", "{}", "ongoing")
	row1.RemoteParentRouting = &parentRouting
	row1.RemoteParentNativeID = &parentNative
	if err := r.UpsertRemoteNeg(row1); err != nil {
		t.Fatalf("upsert row1: %v", err)
	}

	row2 := remoteNeg(333, "rnbp-2", 333, "client-2", 111, "client-3", "{}", "ongoing")
	row2.RemoteParentRouting = &parentRouting
	row2.RemoteParentNativeID = &parentNative
	if err := r.UpsertRemoteNeg(row2); err != nil {
		t.Fatalf("upsert row2: %v", err)
	}

	// Cancelled under the same parent — must be excluded.
	row3 := remoteNeg(444, "rnbp-3", 444, "client-5", 111, "client-3", "{}", "cancelled")
	row3.RemoteParentRouting = &parentRouting
	row3.RemoteParentNativeID = &parentNative
	if err := r.UpsertRemoteNeg(row3); err != nil {
		t.Fatalf("upsert row3: %v", err)
	}

	// Different parent — must be excluded.
	otherNative := "offer-parent-2"
	row4 := remoteNeg(222, "rnbp-4", 222, "client-1", 111, "client-3", "{}", "ongoing")
	row4.RemoteParentRouting = &parentRouting
	row4.RemoteParentNativeID = &otherNative
	if err := r.UpsertRemoteNeg(row4); err != nil {
		t.Fatalf("upsert row4: %v", err)
	}

	// No parent (free-form) — must be excluded.
	row5 := remoteNeg(222, "rnbp-5", 222, "client-1", 111, "client-3", "{}", "ongoing")
	if err := r.UpsertRemoteNeg(row5); err != nil {
		t.Fatalf("upsert row5: %v", err)
	}

	got, err := r.ListRemoteNegByParent(111, "offer-parent-1")
	if err != nil {
		t.Fatalf("ListRemoteNegByParent: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 ongoing rows under parent, got %d", len(got))
	}
	for i := range got {
		if got[i].Status != "ongoing" {
			t.Errorf("row %d status=%q, want ongoing", i, got[i].Status)
		}
	}
}

func TestOTCNegotiationRepository_AppendRemoteRevision(t *testing.T) {
	db := newRevTestDB(t) // migrates OTCNegotiation + OTCNegotiationRevision; own=111
	r := NewOTCNegotiationRepository(db)

	settle := time.Date(2030, 6, 1, 0, 0, 0, 0, time.UTC)
	wireID := "client-7"

	// Seed a remote chain.
	if err := r.UpsertRemoteNeg(remoteNeg(222, "arr-neg-1", 222, "client-7", 111, "client-3", `{"premium":"5"}`, "ongoing")); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rev := &model.OTCNegotiationRevision{
		Quantity:                decimal.NewFromInt(10),
		StrikePrice:             decimal.NewFromInt(150),
		Premium:                 decimal.NewFromInt(5),
		SettlementDate:          settle,
		Action:                  model.OTCNegotiationActionBid,
		ModifiedByPrincipalType: "buyer",
		RemoteActorWireID:       &wireID,
	}

	// First append — should record revision 1.
	if err := r.AppendRemoteRevision(222, "arr-neg-1", rev); err != nil {
		t.Fatalf("AppendRemoteRevision: %v", err)
	}

	row, _ := r.GetRemoteNegByRoutingAndNative(222, "arr-neg-1")
	revs := revsFor(t, db, row.ID)
	if len(revs) != 1 || revs[0].Action != model.OTCNegotiationActionBid {
		t.Fatalf("expected 1 BID revision, got %d", len(revs))
	}

	// Second append with identical move — idempotent no-op.
	rev2 := &model.OTCNegotiationRevision{
		Quantity:                decimal.NewFromInt(10),
		StrikePrice:             decimal.NewFromInt(150),
		Premium:                 decimal.NewFromInt(5),
		SettlementDate:          settle,
		Action:                  model.OTCNegotiationActionBid,
		ModifiedByPrincipalType: "buyer",
		RemoteActorWireID:       &wireID,
	}
	if err := r.AppendRemoteRevision(222, "arr-neg-1", rev2); err != nil {
		t.Fatalf("AppendRemoteRevision retry: %v", err)
	}
	revs2 := revsFor(t, db, row.ID)
	if len(revs2) != 1 {
		t.Errorf("idempotent retry should not add a revision: got %d", len(revs2))
	}

	// Append on a non-existent chain — should error.
	if err := r.AppendRemoteRevision(999, "no-such-neg", rev); err == nil {
		t.Error("expected error when chain does not exist")
	}
}

// ---------------------------------------------------------------------------
// OrderRepository — GetBySagaID
// ---------------------------------------------------------------------------

func TestOrderRepository_GetBySagaID(t *testing.T) {
	r, _, _ := newOrderTestDB(t)

	uid := uint64(50)
	o := &model.Order{
		OwnerType:        model.OwnerClient,
		OwnerID:          &uid,
		ListingID:        1,
		SecurityType:     "stock",
		Ticker:           "GOOG",
		Direction:        "buy",
		OrderType:        "market",
		Quantity:         5,
		PricePerUnit:     decimal.NewFromInt(200),
		ApproximatePrice: decimal.NewFromInt(1000),
		Status:           "pending",
		SagaID:           "test-saga-id-001",
	}
	if err := r.Create(o); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Found — empty sagaID guard.
	_, err := r.GetBySagaID("")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("empty sagaID: expected ErrRecordNotFound, got %v", err)
	}

	// Found with a real saga ID.
	got, err := r.GetBySagaID("test-saga-id-001")
	if err != nil {
		t.Fatalf("GetBySagaID: %v", err)
	}
	if got.Ticker != "GOOG" {
		t.Errorf("ticker: got %s want GOOG", got.Ticker)
	}

	// Not found.
	_, err = r.GetBySagaID("no-such-saga")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("missing saga: expected ErrRecordNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// OptionContractRepository — GetBySagaID
// ---------------------------------------------------------------------------

func newOptionContractTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	model.SetOwnRouting("111")
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.OptionContract{}); err != nil {
		t.Fatalf("migrate option_contracts: %v", err)
	}
	return db
}

func seedLocalOptionContract(t *testing.T, db *gorm.DB, sagaID string) *model.OptionContract {
	t.Helper()
	offerID := uint64(1000)
	bid := uint64(7)
	now := time.Now().UTC()
	c := &model.OptionContract{
		OfferID:         &offerID,
		BuyerOwnerType:  model.OwnerClient,
		BuyerOwnerID:    &bid,
		SellerOwnerType: model.OwnerBank,
		Ticker:          "AAPL",
		Quantity:        decimal.NewFromInt(5),
		StrikePrice:     decimal.NewFromInt(150),
		PremiumPaid:     decimal.NewFromFloat(5),
		PremiumCurrency: "USD",
		StrikeCurrency:  "USD",
		SettlementDate:  now.AddDate(0, 3, 0),
		Status:          model.OptionContractStatusActive,
		SagaID:          sagaID,
		PremiumPaidAt:   now,
	}
	if err := db.Create(c).Error; err != nil {
		t.Fatalf("seedLocalOptionContract: %v", err)
	}
	return c
}

func TestOptionContractRepository_GetBySagaID(t *testing.T) {
	db := newOptionContractTestDB(t)
	r := NewOptionContractRepository(db)

	c := seedLocalOptionContract(t, db, "oc-saga-test-001")

	// Empty sagaID guard.
	_, err := r.GetBySagaID("")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("empty sagaID: expected ErrRecordNotFound, got %v", err)
	}

	// Found.
	got, err := r.GetBySagaID("oc-saga-test-001")
	if err != nil {
		t.Fatalf("GetBySagaID: %v", err)
	}
	if got.ID != c.ID {
		t.Errorf("id mismatch: got %d want %d", got.ID, c.ID)
	}
	if got.Ticker != "AAPL" {
		t.Errorf("ticker: got %s", got.Ticker)
	}

	// Not found.
	_, err = r.GetBySagaID("no-such-oc-saga")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("missing saga: expected ErrRecordNotFound, got %v", err)
	}
}
