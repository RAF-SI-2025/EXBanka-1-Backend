// Package repository — folded-in remote OTCOffer row tests (SP-2a).
//
// Remote offers are stored as OTCOffer rows with routing_number=<peer> and
// native_id=<peer foreign id>. These tests cover the three remote-scoped
// methods that replaced the retired remote_otc_offer mirror:
//   - UpsertRemote          (idempotent on the natural key, stable surrogate
//     id, reopen on re-upsert)
//   - ReconcileRemoteNotSeen (flips not-seen remote rows to cancelled; never
//     touches local or other-peer rows)
//   - GetRemoteByID         (returns a remote row; NotFound for a local id)
package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

// newRemoteOfferTestDB opens a sqlite :memory: DB, migrates OTCOffer, and sets
// OwnRouting=111 so local rows stamp routing 111 and remote rows (222) stay
// distinct.
func newRemoteOfferTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	model.SetOwnRouting("111")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.OTCOffer{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// sampleRemoteOffer builds a remote OTCOffer row for (peerRouting, nativeID).
func sampleRemoteOffer(peerRouting int64, nativeID string) *model.OTCOffer {
	nid := nativeID
	bankCode := "222"
	sellerID := "employee-1"
	return &model.OTCOffer{
		RoutingNumber:               peerRouting,
		NativeID:                    &nid,
		InitiatorBankCode:           &bankCode,
		RemoteSellerID:              &sellerID,
		InitiatorOwnerType:          model.OwnerBank,
		Direction:                   model.OTCDirectionSellInitiated,
		Ticker:                      "BAC",
		Quantity:                    decimal.NewFromInt(7),
		Status:                      model.OTCOfferStatusOpen,
		LastModifiedByPrincipalType: "system",
		LastModifiedByPrincipalID:   0,
	}
}

func TestUpsertRemote_IdempotentAndStableID(t *testing.T) {
	db := newRemoteOfferTestDB(t)
	r := NewOTCOfferRepository(db)
	now := time.Now().UTC()

	id1, err := r.UpsertRemote(sampleRemoteOffer(222, "1"), now)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if id1 == 0 {
		t.Fatal("first upsert returned id 0")
	}

	o := sampleRemoteOffer(222, "1")
	// Mutate a still-upserted column (quantity is in UpsertRemote's DoUpdates
	// set) to prove the re-upsert refreshes the row under the same surrogate id.
	o.Quantity = decimal.NewFromInt(12)
	id2, err := r.UpsertRemote(o, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("surrogate id changed across upserts: %d != %d", id1, id2)
	}

	got, err := r.GetRemoteByID(id1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Quantity.Equal(decimal.NewFromInt(12)) {
		t.Fatalf("quantity not updated: %s", got.Quantity)
	}
	if got.Status != model.OTCOfferStatusOpen {
		t.Fatalf("status = %q, want open", got.Status)
	}
	// Exactly one row exists for the natural key.
	var count int64
	db.Model(&model.OTCOffer{}).Where("routing_number = ? AND native_id = ?", 222, "1").Count(&count)
	if count != 1 {
		t.Fatalf("row count = %d, want 1 (upsert must not insert a duplicate)", count)
	}
}

func TestUpsertRemote_ReopensCancelledRow(t *testing.T) {
	db := newRemoteOfferTestDB(t)
	r := NewOTCOfferRepository(db)
	now := time.Now().UTC()

	id, err := r.UpsertRemote(sampleRemoteOffer(222, "A"), now)
	if err != nil {
		t.Fatalf("setup upsert: %v", err)
	}
	if _, err := r.ReconcileRemoteNotSeen(222, nil); err != nil {
		t.Fatalf("setup reconcile: %v", err)
	}
	// Re-upsert (peer re-lists) must reopen the row.
	if _, err := r.UpsertRemote(sampleRemoteOffer(222, "A"), now.Add(time.Hour)); err != nil {
		t.Fatalf("reopen upsert: %v", err)
	}
	got, err := r.GetRemoteByID(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.OTCOfferStatusOpen {
		t.Fatalf("reappeared offer = %q, want open", got.Status)
	}
}

func TestReconcileRemoteNotSeen_FlipsOnlyNotSeenAndScopedToPeer(t *testing.T) {
	db := newRemoteOfferTestDB(t)
	r := NewOTCOfferRepository(db)
	now := time.Now().UTC()

	idA, err := r.UpsertRemote(sampleRemoteOffer(222, "A"), now)
	if err != nil {
		t.Fatalf("setup upsert A: %v", err)
	}
	if _, err := r.UpsertRemote(sampleRemoteOffer(222, "B"), now); err != nil {
		t.Fatalf("setup upsert B: %v", err)
	}
	// Another peer's offer with the same native id — must be untouched.
	if _, err := r.UpsertRemote(sampleRemoteOffer(333, "A"), now); err != nil {
		t.Fatalf("setup upsert 333/A: %v", err)
	}
	// A LOCAL open offer (routing 111 via BeforeCreate) — must NEVER be cancelled.
	localNative := "local-1"
	bidder := uint64(5)
	local := &model.OTCOffer{
		NativeID:                    &localNative,
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &bidder,
		Direction:                   model.OTCDirectionSellInitiated,
		StockID:                     1,
		Ticker:                      "AAA",
		Quantity:                    decimal.NewFromInt(1),
		Status:                      model.OTCOfferStatusOpen,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   5,
	}
	if err := db.Create(local).Error; err != nil {
		t.Fatalf("seed local: %v", err)
	}
	if local.RoutingNumber != model.OwnRouting() {
		t.Fatalf("local offer routing = %d, want %d (own)", local.RoutingNumber, model.OwnRouting())
	}

	// Reconcile peer 222 having seen only "A": B should flip to cancelled.
	n, err := r.ReconcileRemoteNotSeen(222, []string{"A"})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 1 {
		t.Fatalf("cancelled %d rows, want 1", n)
	}

	a, _ := r.GetRemoteByID(idA)
	if a.Status != model.OTCOfferStatusOpen {
		t.Fatalf("seen offer A flipped to %q", a.Status)
	}
	var b model.OTCOffer
	db.Where("routing_number = ? AND native_id = ?", 222, "B").First(&b)
	if b.Status != model.OTCOfferStatusCancelled {
		t.Fatalf("unseen offer B = %q, want cancelled", b.Status)
	}
	var other model.OTCOffer
	db.Where("routing_number = ? AND native_id = ?", 333, "A").First(&other)
	if other.Status != model.OTCOfferStatusOpen {
		t.Fatalf("other peer's offer flipped to %q", other.Status)
	}
	var gotLocal model.OTCOffer
	db.First(&gotLocal, local.ID)
	if gotLocal.Status != model.OTCOfferStatusOpen {
		t.Fatalf("LOCAL offer flipped to %q — reconcile must never touch local rows", gotLocal.Status)
	}
}

func TestReconcileRemoteNotSeen_EmptySeenCancelsAllForPeer(t *testing.T) {
	db := newRemoteOfferTestDB(t)
	r := NewOTCOfferRepository(db)
	now := time.Now().UTC()

	idA, err := r.UpsertRemote(sampleRemoteOffer(222, "A"), now)
	if err != nil {
		t.Fatalf("setup upsert A: %v", err)
	}
	idB, err := r.UpsertRemote(sampleRemoteOffer(222, "B"), now)
	if err != nil {
		t.Fatalf("setup upsert B: %v", err)
	}
	n, err := r.ReconcileRemoteNotSeen(222, nil)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 2 {
		t.Fatalf("cancelled %d, want 2", n)
	}
	a, _ := r.GetRemoteByID(idA)
	b, _ := r.GetRemoteByID(idB)
	if a.Status != model.OTCOfferStatusCancelled || b.Status != model.OTCOfferStatusCancelled {
		t.Fatalf("A=%q B=%q, want both cancelled", a.Status, b.Status)
	}
}

func TestReconcile_NamespaceScoped(t *testing.T) {
	db := newRemoteOfferTestDB(t)
	r := NewOTCOfferRepository(db)
	mk := func(native string) uint64 {
		n := native
		id, err := r.UpsertRemote(&model.OTCOffer{
			RoutingNumber:               222,
			NativeID:                    &n,
			InitiatorOwnerType:          model.OwnerBank,
			Direction:                   model.OTCDirectionSellInitiated,
			Ticker:                      "AAPL",
			Quantity:                    decimal.NewFromInt(1),
			LastModifiedByPrincipalType: "system",
		}, time.Now().UTC())
		if err != nil {
			t.Fatalf("upsert %s: %v", native, err)
		}
		return id
	}
	optID := mk("peer-offer-1")
	shellID := mk("ps:222:client-5:AAPL")

	// Reconcile option-offers namespace (non-shell): should cancel optID but spare shellID.
	if _, err := r.ReconcileRemoteNotSeen(222, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.GetRemoteByID(shellID); got.Status != model.OTCOfferStatusOpen {
		t.Fatalf("shell cancelled by option reconcile: %s", got.Status)
	}
	if got, _ := r.GetRemoteByID(optID); got.Status != model.OTCOfferStatusCancelled {
		t.Fatalf("option-offer not cancelled: %s", got.Status)
	}
	// Reconcile shells namespace: should now cancel shellID.
	if _, err := r.ReconcileRemoteShellsNotSeen(222, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.GetRemoteByID(shellID); got.Status != model.OTCOfferStatusCancelled {
		t.Fatalf("shell not cancelled by shell reconcile: %s", got.Status)
	}
}

// TestUpsertRemoteShell_RoundTrips verifies a /public-stock shell row upserts
// and resolves under a stable surrogate id. Shells are termless like every
// other remote row (UpsertRemoteShell is a named alias over UpsertRemote).
func TestUpsertRemoteShell_RoundTrips(t *testing.T) {
	db := newRemoteOfferTestDB(t)
	r := NewOTCOfferRepository(db)
	native := "ps:222:client-5:AAPL"
	bc := "222"
	sid := "client-5"
	row := &model.OTCOffer{
		RoutingNumber:               222,
		NativeID:                    &native,
		InitiatorBankCode:           &bc,
		RemoteSellerID:              &sid,
		InitiatorOwnerType:          model.OwnerBank,
		Direction:                   model.OTCDirectionSellInitiated,
		Ticker:                      "AAPL",
		Quantity:                    decimal.NewFromInt(1),
		LastModifiedByPrincipalType: "system",
	}
	id, err := r.UpsertRemoteShell(row, time.Now().UTC())
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := r.GetRemoteByID(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.NativeID == nil || *got.NativeID != native {
		t.Fatalf("native_id = %v, want %q", got.NativeID, native)
	}
	if got.Status != model.OTCOfferStatusOpen {
		t.Fatalf("status = %q, want open", got.Status)
	}
}

func TestGetRemoteByID_RemoteRowAndLocalIsNotFound(t *testing.T) {
	db := newRemoteOfferTestDB(t)
	r := NewOTCOfferRepository(db)
	now := time.Now().UTC()

	// A remote row resolves.
	id, err := r.UpsertRemote(sampleRemoteOffer(222, "A"), now)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := r.GetRemoteByID(id)
	if err != nil {
		t.Fatalf("GetRemoteByID(remote): %v", err)
	}
	if got.RoutingNumber != 222 {
		t.Fatalf("routing = %d, want 222", got.RoutingNumber)
	}

	// A LOCAL row (routing 111) is NOT a remote offer → NotFound.
	bidder := uint64(5)
	local := &model.OTCOffer{
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &bidder,
		Direction:                   model.OTCDirectionSellInitiated,
		StockID:                     1,
		Ticker:                      "AAA",
		Quantity:                    decimal.NewFromInt(1),
		Status:                      model.OTCOfferStatusOpen,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   5,
	}
	if err := db.Create(local).Error; err != nil {
		t.Fatalf("seed local: %v", err)
	}
	if _, err := r.GetRemoteByID(local.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("GetRemoteByID(local) err = %v, want ErrRecordNotFound", err)
	}

	// A nonexistent id → NotFound.
	if _, err := r.GetRemoteByID(99999); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("GetRemoteByID(missing) err = %v, want ErrRecordNotFound", err)
	}
}
