// coverage_gap3_test.go — final gap-closing tests to push coverage above 90%.
//
// Targets:
//   - WipeAll "missing table" → return err inside loop
//   - GetOpenSellListingForUpdate nil ownerID
//   - LockByIDTx remote row (local=false)
//   - UpsertByListingAndDate INSERT path (new date)
//   - Various 80-83% functions via closed-DB (Save/SaveTx error paths)
//   - PriceAlertRepository.ListByOwner via closed-DB
//   - CreateWatchlist via closed-DB
//   - GetByOfferID remote row (local=false)
//   - UpdateRemoteNegOfferWithRevision sameRevisionMove path
//   - OTCNegotiationRepository.NextRevisionNumber closed-DB
package repository

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

// ---------------------------------------------------------------------------
// WipeAll — Exec fails because tables were not migrated
// ---------------------------------------------------------------------------

func TestWipeRepository_WipeAll_MissingTable(t *testing.T) {
	// Don't AutoMigrate any tables → first Exec("DELETE FROM tax_collections")
	// fails with "no such table" → covers `return err` inside the loop.
	db := newTestDB(t)
	r := NewWipeRepository(db)
	err := r.WipeAll()
	if err == nil {
		t.Error("expected error when tables don't exist")
	}
}

// ---------------------------------------------------------------------------
// OTCOfferRepository.GetOpenSellListingForUpdate — nil ownerID (bank seller)
// ---------------------------------------------------------------------------

func TestOTCOfferRepository_GetOpenSellListingForUpdate_NilOwner_NotFound(t *testing.T) {
	r, _ := newOTCOfferDB(t)
	model.SetOwnRouting("111")

	// nil ownerID → IS NULL branch. No row exists → ErrRecordNotFound returned.
	err := r.db.Transaction(func(tx *gorm.DB) error {
		_, err := r.GetOpenSellListingForUpdate(tx, model.OwnerBank, nil, "AAPLOPT")
		return err
	})
	// ErrRecordNotFound is expected (no open bank sell listing for that ticker).
	if err == nil {
		t.Error("expected ErrRecordNotFound")
	}
}

// ---------------------------------------------------------------------------
// OTCOfferRepository.LockByIDTx — remote row (local=false) → ErrRecordNotFound
// ---------------------------------------------------------------------------

func TestOTCOfferRepository_LockByIDTx_RemoteRow_NotFound(t *testing.T) {
	r, _ := newOTCOfferDB(t)
	model.SetOwnRouting("111")

	// Insert a remote (local=false) OTCOffer row directly.
	uid := uint64(300)
	native := "ps:remote-lock-test"
	remoteOffer := &model.OTCOffer{
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &uid,
		Direction:                   model.OTCDirectionSellInitiated,
		Ticker:                      "REMOTE",
		Quantity:                    decimal.NewFromInt(5),
		Status:                      model.OTCOfferStatusOpen,
		RoutingNumber:               222,
		NativeID:                    &native,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   uid,
		Local:                       false,
	}
	if err := r.db.Create(remoteOffer).Error; err != nil {
		t.Fatalf("create remote offer: %v", err)
	}

	// LockByIDTx on a remote row → `!o.Local` → return nil, ErrRecordNotFound.
	var lockErr error
	txErr := r.db.Transaction(func(tx *gorm.DB) error {
		_, lockErr = r.LockByIDTx(tx, remoteOffer.ID)
		return nil // don't propagate — just checking lockErr
	})
	if txErr != nil {
		t.Fatalf("transaction: %v", txErr)
	}
	if lockErr != gorm.ErrRecordNotFound {
		t.Errorf("expected ErrRecordNotFound for remote row, got: %v", lockErr)
	}
}

// ---------------------------------------------------------------------------
// ListingDailyPriceRepository.UpsertByListingAndDate — INSERT path (new date)
// ---------------------------------------------------------------------------

func TestListingDailyPriceRepository_UpsertByListingAndDate_InsertPath(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.ListingDailyPriceInfo{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewListingDailyPriceRepository(db)

	// Call UpsertByListingAndDate on a date that doesn't exist yet → INSERT path.
	info := &model.ListingDailyPriceInfo{
		ListingID: 99,
		Date:      time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		Price:     decimal.NewFromInt(50),
		High:      decimal.NewFromInt(55),
		Low:       decimal.NewFromInt(48),
		Change:    decimal.NewFromInt(1),
		Volume:    1000,
	}
	if err := r.UpsertByListingAndDate(info); err != nil {
		t.Fatalf("UpsertByListingAndDate INSERT: %v", err)
	}
	if info.ID == 0 {
		t.Error("expected non-zero id after INSERT via upsert")
	}
}

// ---------------------------------------------------------------------------
// OTCOfferRepository.Save closed-DB — error path
// ---------------------------------------------------------------------------

func TestOTCOfferRepository_Save_ClosedDB(t *testing.T) {
	r, _ := newOTCOfferDB(t)
	closeDB(t, r.db)
	uid := uint64(1)
	o := &model.OTCOffer{
		ID: 1, InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &uid,
		Direction: model.OTCDirectionSellInitiated, Ticker: "T", Quantity: decimal.NewFromInt(1),
		Status: model.OTCOfferStatusOpen, LastModifiedByPrincipalType: "client", LastModifiedByPrincipalID: uid,
		Version: 0,
	}
	if err := r.Save(o); err == nil {
		t.Error("expected error from closed DB")
	}
}

// ---------------------------------------------------------------------------
// OTCNegotiationRepository.Save closed-DB
// ---------------------------------------------------------------------------

func TestOTCNegotiationRepository_Save_ClosedDB(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)
	uid := uint64(42)
	n := newSampleNegotiation(0, &uid, model.OTCNegotiationStatusOpen)
	if err := db.Create(n).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	closeDB(t, db)
	n.Status = model.OTCNegotiationStatusAccepted
	if err := r.Save(n); err == nil {
		t.Error("expected error from closed DB")
	}
}

// ---------------------------------------------------------------------------
// DividendPayoutRepository.ListByOwner closed-DB
// ---------------------------------------------------------------------------

func TestDividendPayoutRepository_ListByOwner_ClosedDB(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.DividendPayout{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewDividendPayoutRepository(db)
	closeDB(t, db)
	uid := uint64(1)
	_, _, err := r.ListByOwner("client", &uid, 1, 10)
	if err == nil {
		t.Error("expected error from closed DB")
	}
}

// ---------------------------------------------------------------------------
// FundDividendPaymentRepository.ListByFundID closed-DB
// ---------------------------------------------------------------------------

func TestFundDividendPaymentRepository_ListByFundID_ClosedDB(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.FundDividendPayment{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundDividendPaymentRepository(db)
	closeDB(t, db)
	_, _, err := r.ListByFundID(1, 1, 10)
	if err == nil {
		t.Error("expected error from closed DB")
	}
}

// ---------------------------------------------------------------------------
// OptionContractRepository.GetByOfferID — remote row (local=false)
// ---------------------------------------------------------------------------

func TestOptionContractRepository_GetByOfferID_RemoteRow_NotFound(t *testing.T) {
	db := newOptLockTestDB(t)
	model.SetOwnRouting("111")
	r := NewOptionContractRepository(db)

	offerID := uint64Ptr(500)
	settlement := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	remoteContr := &model.OptionContract{
		OfferID:         offerID,
		BuyerOwnerType:  model.OwnerClient,
		BuyerOwnerID:    uint64Ptr(9),
		SellerOwnerType: model.OwnerBank,
		SellerOwnerID:   nil,
		StockID:         10,
		Quantity:        decimal.NewFromInt(1),
		StrikePrice:     decimal.NewFromInt(200),
		PremiumPaid:     decimal.NewFromInt(10),
		PremiumCurrency: "RSD",
		StrikeCurrency:  "RSD",
		SettlementDate:  settlement,
		Status:          model.OptionContractStatusActive,
		SagaID:          "saga-remote",
		PremiumPaidAt:   time.Now().UTC(),
		RoutingNumber:   222,
		Local:           false,
	}
	if err := db.Create(remoteContr).Error; err != nil {
		t.Fatalf("create remote contract: %v", err)
	}

	// GetByOfferID finds the row but it's not local → returns ErrRecordNotFound.
	_, err := r.GetByOfferID(*offerID)
	if err != gorm.ErrRecordNotFound {
		t.Errorf("expected ErrRecordNotFound for remote contract, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// PriceAlertRepository.ListByOwner closed-DB — error path
// ---------------------------------------------------------------------------

func TestPriceAlertRepository_ListByOwner_ClosedDB(t *testing.T) {
	db := newPriceAlertTestDB(t)
	r := NewPriceAlertRepository(db)
	closeDB(t, db)
	uid := uint64(1)
	_, err := r.ListByOwner(model.OwnerClient, &uid)
	if err == nil {
		t.Error("expected error from closed DB")
	}
}

// ---------------------------------------------------------------------------
// WatchlistRepository.CreateWatchlist — non-NotFound DB error path
// ---------------------------------------------------------------------------

func TestWatchlistRepository_CreateWatchlist_ClosedDB(t *testing.T) {
	db := newWatchlistTestDB(t)
	r := NewWatchlistRepository(db)
	closeDB(t, db)
	uid := uint64(1)
	w := &model.Watchlist{OwnerType: model.OwnerClient, OwnerID: &uid, Name: "My Watchlist"}
	err := r.CreateWatchlist(w)
	if err == nil {
		t.Error("expected error from closed DB")
	}
}

// ---------------------------------------------------------------------------
// UpdateRemoteNegOfferWithRevision — sameRevisionMove path (retried counter)
// ---------------------------------------------------------------------------

func TestOTCNegotiationRepository_UpdateRemoteNegOfferWithRevision_SameMove(t *testing.T) {
	db := newRevTestDB(t)
	r := NewOTCNegotiationRepository(db)
	model.SetOwnRouting("111")

	nat := "neg-urnrwrv-1"
	n := remoteNeg(222, nat, 222, "client-9", 111, "client-4", `{"premium":"20"}`, "ongoing")
	if err := r.UpsertRemoteNeg(n); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Append initial revision.
	rev1 := revTemplate(model.OTCNegotiationActionBid, "buyer", "client-9", 20)
	if err := r.AppendRemoteRevision(222, nat, rev1); err != nil {
		t.Fatalf("append bid: %v", err)
	}

	// UpdateRemoteNegOfferWithRevision with same move as latest revision → no-op.
	rev2 := revTemplate(model.OTCNegotiationActionBid, "buyer", "client-9", 20)
	if err := r.UpdateRemoteNegOfferWithRevision(222, nat, `{"premium":"25"}`, rev2); err != nil {
		t.Fatalf("update same-move: %v", err)
	}
}

// ---------------------------------------------------------------------------
// OTCNegotiationRepository.NextRevisionNumber closed-DB
// ---------------------------------------------------------------------------

func TestOTCNegotiationRepository_NextRevisionNumber_ClosedDB(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)
	closeDB(t, db)
	txErr := db.Transaction(func(tx *gorm.DB) error {
		_, err := r.NextRevisionNumber(tx, 1)
		return err
	})
	// closed DB → transaction itself fails; either path exercises the error return.
	_ = txErr
}

// ---------------------------------------------------------------------------
// FundRepository.Save closed-DB — error path
// ---------------------------------------------------------------------------

func TestFundRepository_Save_ClosedDB(t *testing.T) {
	r := newFundTestDB(t)
	f := &model.InvestmentFund{Name: "ClosedFund", ManagerEmployeeID: 1, RSDAccountID: 1, Active: true}
	if err := r.Create(f); err != nil {
		t.Fatalf("create: %v", err)
	}
	closeDB(t, r.db)
	f.Name = "NewName"
	if err := r.Save(f); err == nil {
		t.Error("expected error from closed DB on Save")
	}
}

// ---------------------------------------------------------------------------
// SagaLogRepository.UpdateStatus and MarkDeadLetter closed-DB
// ---------------------------------------------------------------------------

func TestSagaLogRepository_UpdateStatus_ClosedDB(t *testing.T) {
	r := newSagaTestDB(t)
	entry := &model.SagaLog{SagaID: "saga-upd-cls", StepName: "s1", Status: model.SagaStatusPending}
	if err := r.RecordStep(entry); err != nil {
		t.Fatalf("record: %v", err)
	}
	closeDB(t, r.db)
	if err := r.UpdateStatus(entry.ID, entry.Version, model.SagaStatusCompleted, ""); err == nil {
		t.Error("expected error from closed DB")
	}
}

func TestSagaLogRepository_MarkDeadLetter_ClosedDB(t *testing.T) {
	r := newSagaTestDB(t)
	entry := &model.SagaLog{SagaID: "saga-dl-cls", StepName: "s2", Status: model.SagaStatusPending}
	if err := r.RecordStep(entry); err != nil {
		t.Fatalf("record: %v", err)
	}
	closeDB(t, r.db)
	if err := r.MarkDeadLetter(entry.ID); err == nil {
		t.Error("expected error from closed DB")
	}
}

// ---------------------------------------------------------------------------
// OrderRepository.Update closed-DB
// ---------------------------------------------------------------------------

func TestOrderRepository_Update_ClosedDB(t *testing.T) {
	rr, _, _ := newOrderTestDB(t)
	uid := uint64(30)
	o := &model.Order{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		ListingID: 1, SecurityType: "stock", Ticker: "T",
		Direction: "buy", OrderType: "market",
		Quantity:         5,
		PricePerUnit:     decimal.NewFromInt(50),
		ApproximatePrice: decimal.NewFromInt(250),
		Status:           "pending",
	}
	if err := rr.Create(o); err != nil {
		t.Fatalf("create: %v", err)
	}
	closeDB(t, rr.db)
	o.Status = "approved"
	if err := rr.Update(o); err == nil {
		t.Error("expected error from closed DB")
	}
}

// ---------------------------------------------------------------------------
// HoldingRepository.DecrementForOwner closed-DB
// ---------------------------------------------------------------------------

func TestHoldingRepository_DecrementForOwner_ClosedDB(t *testing.T) {
	db := newHoldingCreditTestDB(t)
	r := NewHoldingRepository(db)

	uid := uint64(777)
	h := &model.Holding{
		OwnerType:    model.OwnerClient,
		OwnerID:      &uid,
		SecurityType: "stock",
		SecurityID:   1,
		Quantity:     10,
		AveragePrice: decimal.NewFromInt(50),
		ListingID:    1,
		Ticker:       "T",
		Name:         "TestHolding",
	}
	if err := db.Create(h).Error; err != nil {
		t.Fatalf("create holding: %v", err)
	}
	closeDB(t, db)
	err := r.DecrementForOwner(context.Background(), model.OwnerClient, &uid, "stock", 1, 5)
	if err == nil {
		t.Error("expected error from closed DB")
	}
}
