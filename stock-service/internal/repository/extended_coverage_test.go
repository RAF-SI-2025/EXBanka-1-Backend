// Tests for miscellaneous previously-uncovered repository methods:
//   - CapitalGainRepository.DeleteByIdempotencyKey, MarkCollected
//   - ClientFundPositionRepository.DecrementContribution, ListByFund
//   - FundContributionRepository.GetBySagaID
//   - FundHoldingRepository.ListBySecurityID
//   - HoldingRepository.DB, LockByIDTx, SaveTx, ListBySecurityID
//   - OrderTransactionRepository.GetByID, Delete
//   - OTCOfferRepository uncovered tx-variants
//   - SagaLogRepository.FindLatestCompensationRow, HasCompensations, MarkDeadLetter
//   - ListingDailyPriceRepository.UpsertManyByListingAndDate, decimalFromFloat
//   - ListingRepository.ListByIDs
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
// CapitalGainRepository — DeleteByIdempotencyKey, MarkCollected
// ---------------------------------------------------------------------------

func TestCapitalGainRepository_DeleteByIdempotencyKey(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.CapitalGain{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewCapitalGainRepository(db)

	uid := uint64(1)
	key := "idem-del-test"
	_ = r.Create(&model.CapitalGain{
		OwnerType:        model.OwnerClient,
		OwnerID:          &uid,
		SecurityType:     "stock",
		Ticker:           "AAPL",
		Quantity:         1,
		BuyPricePerUnit:  decimal.NewFromInt(100),
		SellPricePerUnit: decimal.NewFromInt(150),
		TotalGain:        decimal.NewFromInt(50),
		Currency:         "RSD",
		AccountID:        1,
		TaxYear:          2026,
		TaxMonth:         1,
		IdempotencyKey:   &key,
	})

	// delete existing
	if err := r.DeleteByIdempotencyKey(key); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// delete non-existent should be no-op
	if err := r.DeleteByIdempotencyKey("no-such-key"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestCapitalGainRepository_MarkCollected(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.CapitalGain{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewCapitalGainRepository(db)

	uid := uint64(2)
	now := time.Now()
	_ = r.Create(&model.CapitalGain{
		OwnerType:        model.OwnerClient,
		OwnerID:          &uid,
		SecurityType:     "stock",
		Ticker:           "GOOG",
		Quantity:         2,
		BuyPricePerUnit:  decimal.NewFromInt(100),
		SellPricePerUnit: decimal.NewFromInt(200),
		TotalGain:        decimal.NewFromInt(200),
		Currency:         "RSD",
		AccountID:        10,
		TaxYear:          now.Year(),
		TaxMonth:         int(now.Month()),
	})

	if err := r.MarkCollected(model.OwnerClient, &uid, now.Year(), int(now.Month()), 10, "RSD", 99); err != nil {
		t.Fatalf("mark collected: %v", err)
	}

	// verify tax_collection_id is set
	var rows []model.CapitalGain
	db.Where("owner_id = ? AND account_id = ?", uid, 10).Find(&rows)
	if len(rows) == 0 || rows[0].TaxCollectionID == nil {
		t.Error("expected tax_collection_id to be set")
	}
}

// ---------------------------------------------------------------------------
// ClientFundPositionRepository — DecrementContribution, ListByFund
// ---------------------------------------------------------------------------

func TestClientFundPositionRepository_DecrementContribution(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.ClientFundPosition{}, &model.FundPositionSettlement{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewClientFundPositionRepository(db)

	uid := uint64(1)
	// increment first to create the row
	if err := r.IncrementContribution(1, model.OwnerClient, &uid, decimal.NewFromInt(1000), 1); err != nil {
		t.Fatalf("increment: %v", err)
	}

	// decrement using a different contributionID (2) so the settlement is new
	if err := r.DecrementContribution(1, model.OwnerClient, &uid, decimal.NewFromInt(300), 2); err != nil {
		t.Fatalf("decrement: %v", err)
	}

	got, err := r.GetByFundAndOwner(1, model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.TotalContributedRSD.Equal(decimal.NewFromInt(700)) {
		t.Errorf("expected 700, got %s", got.TotalContributedRSD)
	}
}

func TestClientFundPositionRepository_ListByFund(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.ClientFundPosition{}, &model.FundPositionSettlement{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewClientFundPositionRepository(db)

	for i := uint64(1); i <= 3; i++ {
		uid := i
		if err := r.IncrementContribution(5, model.OwnerClient, &uid, decimal.NewFromInt(100), 1); err != nil {
			t.Fatalf("increment %d: %v", i, err)
		}
	}

	rows, err := r.ListByFund(5)
	if err != nil {
		t.Fatalf("list by fund: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("expected 3, got %d", len(rows))
	}
}

// ---------------------------------------------------------------------------
// FundContributionRepository — GetBySagaID
// ---------------------------------------------------------------------------

func TestFundContributionRepository_GetBySagaID(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.FundContribution{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundContributionRepository(db)

	uid := uint64(1)
	c := &model.FundContribution{
		FundID: 1, OwnerType: model.OwnerClient, OwnerID: &uid,
		Direction: "invest", AmountNative: decimal.NewFromInt(100),
		NativeCurrency: "RSD", AmountRSD: decimal.NewFromInt(100),
		FeeRSD: decimal.NewFromInt(1), Status: "pending",
		SourceOrTargetAccountID: 1,
		SagaID:                  "saga-fund-001",
	}
	if err := r.Create(c); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := r.GetBySagaID("saga-fund-001")
	if err != nil {
		t.Fatalf("get by saga id: %v", err)
	}
	if got.SagaID != "saga-fund-001" {
		t.Errorf("saga_id mismatch: %q", got.SagaID)
	}

	// empty saga id → not found
	if _, err := r.GetBySagaID(""); err == nil {
		t.Error("expected error for empty saga id")
	}

	// unknown saga id → not found
	if _, err := r.GetBySagaID("no-such-saga"); err == nil {
		t.Error("expected error for unknown saga id")
	}
}

// ---------------------------------------------------------------------------
// FundHoldingRepository — ListBySecurityID
// ---------------------------------------------------------------------------

func TestFundHoldingRepository_ListBySecurityID(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.FundHolding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundHoldingRepository(db)

	// 2 holdings for securityID=42 across 2 different funds
	for _, fundID := range []uint64{1, 2} {
		h := &model.FundHolding{
			FundID: fundID, SecurityType: "stock", SecurityID: 42,
			Quantity:        10,
			AveragePriceRSD: decimal.NewFromInt(100),
		}
		if err := r.Upsert(h); err != nil {
			t.Fatalf("upsert fund %d: %v", fundID, err)
		}
	}
	// 1 holding for a different securityID
	h3 := &model.FundHolding{
		FundID: 3, SecurityType: "stock", SecurityID: 99,
		Quantity:        5,
		AveragePriceRSD: decimal.NewFromInt(50),
	}
	if err := r.Upsert(h3); err != nil {
		t.Fatalf("upsert fund 3: %v", err)
	}

	rows, err := r.ListBySecurityID(42)
	if err != nil {
		t.Fatalf("list by security id: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2, got %d", len(rows))
	}
}

// ---------------------------------------------------------------------------
// HoldingRepository — DB, LockByIDTx, SaveTx, ListBySecurityID
// ---------------------------------------------------------------------------

func TestHoldingRepository_DB(t *testing.T) {
	db := newHoldingTestDB(t)
	r := NewHoldingRepository(db)
	if r.DB() != db {
		t.Error("DB() should return underlying db")
	}
}

func TestHoldingRepository_LockByIDTx(t *testing.T) {
	db := newHoldingTestDB(t)
	r := NewHoldingRepository(db)

	uid := uint64(10)
	_ = r.Upsert(context.Background(), &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		SecurityType: "stock", SecurityID: 1,
		Ticker: "AAPL", Quantity: 5,
		AveragePrice: decimal.NewFromInt(100),
	})

	var locked *model.Holding
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		locked, err = r.LockByIDTx(tx, 1)
		return err
	})
	if err != nil {
		t.Fatalf("LockByIDTx: %v", err)
	}
	if locked == nil || locked.Ticker != "AAPL" {
		t.Errorf("locked holding wrong: %+v", locked)
	}

	// not found
	err = db.Transaction(func(tx *gorm.DB) error {
		_, err := r.LockByIDTx(tx, 9999)
		return err
	})
	if err == nil {
		t.Error("expected error for missing holding")
	}
}

func TestHoldingRepository_SaveTx(t *testing.T) {
	db := newHoldingTestDB(t)
	r := NewHoldingRepository(db)

	uid := uint64(11)
	h := &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		SecurityType: "stock", SecurityID: 2,
		Ticker: "MSFT", Quantity: 10,
		AveragePrice: decimal.NewFromInt(200),
	}
	_ = r.Upsert(context.Background(), h)

	err := db.Transaction(func(tx *gorm.DB) error {
		h.Quantity = 20
		return r.SaveTx(tx, h)
	})
	if err != nil {
		t.Fatalf("SaveTx: %v", err)
	}
	got, _ := r.GetByID(h.ID)
	if got.Quantity != 20 {
		t.Errorf("quantity=%d want 20", got.Quantity)
	}
}

func TestHoldingRepository_ListBySecurityID(t *testing.T) {
	db := newHoldingTestDB(t)
	r := NewHoldingRepository(db)

	// create holdings for securityID=5 across 3 different owners
	for i := uint64(1); i <= 3; i++ {
		uid := i
		_ = r.Upsert(context.Background(), &model.Holding{
			OwnerType: model.OwnerClient, OwnerID: &uid,
			SecurityType: "stock", SecurityID: 5,
			Ticker: "IBM", Quantity: int64(i * 10),
			AveragePrice: decimal.NewFromInt(50),
		})
	}
	// one holding for different security
	uid := uint64(99)
	_ = r.Upsert(context.Background(), &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		SecurityType: "stock", SecurityID: 99,
		Ticker: "OTHER", Quantity: 5,
		AveragePrice: decimal.NewFromInt(10),
	})

	rows, err := r.ListBySecurityID(5)
	if err != nil {
		t.Fatalf("list by security id: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("expected 3, got %d", len(rows))
	}
}

// ---------------------------------------------------------------------------
// OrderTransactionRepository — GetByID, Delete
// ---------------------------------------------------------------------------

func TestOrderTransactionRepository_GetByID_And_Delete(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.Order{}, &model.OrderTransaction{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	uid := uint64(1)
	o := &model.Order{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		ListingID: 1, SecurityType: "stock", Ticker: "AAPL",
		Direction: "buy", OrderType: "market", Quantity: 5,
		PricePerUnit:     decimal.NewFromInt(100),
		ApproximatePrice: decimal.NewFromInt(500),
		Status:           "filled",
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}

	r := NewOrderTransactionRepository(db)
	tx := &model.OrderTransaction{
		OrderID:      o.ID,
		Quantity:     5,
		PricePerUnit: decimal.NewFromInt(100),
		TotalPrice:   decimal.NewFromInt(500),
		ExecutedAt:   time.Now(),
	}
	if err := r.Create(tx); err != nil {
		t.Fatalf("create tx: %v", err)
	}

	// GetByID
	got, err := r.GetByID(tx.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Quantity != 5 {
		t.Errorf("quantity=%d want 5", got.Quantity)
	}

	// GetByID not found
	if _, err := r.GetByID(9999); err == nil {
		t.Error("expected error for missing tx")
	}

	// Delete
	if err := r.Delete(tx.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.GetByID(tx.ID); err == nil {
		t.Error("expected error after delete")
	}
}

// ---------------------------------------------------------------------------
// SagaLogRepository — FindLatestCompensationRow, HasCompensations, MarkDeadLetter
// ---------------------------------------------------------------------------

func TestSagaLogRepository_FindLatestCompensationRow(t *testing.T) {
	db := newTestDB(t)
	r := NewSagaLogRepository(db)

	// empty sagaID should return nil,nil
	row, err := r.FindLatestCompensationRow("", "step1")
	if err != nil {
		t.Fatalf("empty sagaID: %v", err)
	}
	if row != nil {
		t.Error("expected nil row for empty sagaID")
	}

	// No compensation rows yet
	row2, err := r.FindLatestCompensationRow("saga-abc", "step1")
	if err != nil {
		t.Fatalf("no rows: %v", err)
	}
	if row2 != nil {
		t.Error("expected nil row for saga with no compensation")
	}

	// Insert a compensation row
	log := &model.SagaLog{
		SagaID:         "saga-abc",
		OrderID:        1,
		StepNumber:     1,
		StepName:       "step1",
		Status:         model.SagaStatusCompensating,
		IsCompensation: true,
	}
	if err := r.RecordStep(log); err != nil {
		t.Fatalf("record: %v", err)
	}

	row3, err := r.FindLatestCompensationRow("saga-abc", "step1")
	if err != nil {
		t.Fatalf("find compensation: %v", err)
	}
	if row3 == nil || row3.ID != log.ID {
		t.Errorf("expected row id=%d, got %v", log.ID, row3)
	}
}

func TestSagaLogRepository_HasCompensations(t *testing.T) {
	db := newTestDB(t)
	r := NewSagaLogRepository(db)

	// empty sagaID
	has, err := r.HasCompensations("")
	if err != nil {
		t.Fatalf("empty sagaID: %v", err)
	}
	if has {
		t.Error("expected false for empty sagaID")
	}

	// saga with no compensation rows
	has2, err := r.HasCompensations("saga-xyz")
	if err != nil {
		t.Fatalf("no rows: %v", err)
	}
	if has2 {
		t.Error("expected false for saga with no compensations")
	}

	// insert a compensation row
	log := &model.SagaLog{
		SagaID: "saga-xyz", OrderID: 1,
		StepNumber:     1,
		StepName:       "compensate-step",
		Status:         model.SagaStatusCompensating,
		IsCompensation: true,
	}
	if err := r.RecordStep(log); err != nil {
		t.Fatalf("record: %v", err)
	}

	has3, err := r.HasCompensations("saga-xyz")
	if err != nil {
		t.Fatalf("has compensations: %v", err)
	}
	if !has3 {
		t.Error("expected true after inserting compensation row")
	}
}

func TestSagaLogRepository_MarkDeadLetter(t *testing.T) {
	db := newTestDB(t)
	r := NewSagaLogRepository(db)

	log := &model.SagaLog{
		SagaID: "saga-dl", OrderID: 1,
		StepNumber:     1,
		StepName:       "step1",
		Status:         model.SagaStatusCompensating,
		IsCompensation: true,
	}
	if err := r.RecordStep(log); err != nil {
		t.Fatalf("record: %v", err)
	}

	if err := r.MarkDeadLetter(log.ID); err != nil {
		t.Fatalf("mark dead letter: %v", err)
	}

	got, _ := r.GetByID(log.ID)
	if got.Status != "dead_letter" {
		t.Errorf("expected dead_letter status, got %q", got.Status)
	}

	// non-existent row should return ErrOptimisticLock
	if err := r.MarkDeadLetter(9999); err == nil {
		t.Error("expected error for missing saga log")
	}
}

// ---------------------------------------------------------------------------
// ListingDailyPriceRepository — decimalFromFloat, UpsertManyByListingAndDate
// ---------------------------------------------------------------------------

func TestListingDailyPriceRepository_DecimalFromFloat(t *testing.T) {
	// decimalFromFloat is a package-level function tested by ensuring it
	// converts the float properly.
	v := decimalFromFloat(3.14)
	expected := decimal.NewFromFloat(3.14)
	if !v.Equal(expected) {
		t.Errorf("decimalFromFloat(3.14)=%s want %s", v, expected)
	}
}

func TestListingDailyPriceRepository_UpsertManyByListingAndDate(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.ListingDailyPriceInfo{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewListingDailyPriceRepository(db)

	now := time.Now().UTC().Truncate(24 * time.Hour)
	infos := []model.ListingDailyPriceInfo{
		{ListingID: 1, Date: now, Price: decimal.NewFromInt(100), High: decimal.NewFromInt(110), Low: decimal.NewFromInt(90), Change: decimal.NewFromInt(5), Volume: 1000},
		{ListingID: 1, Date: now.AddDate(0, 0, -1), Price: decimal.NewFromInt(95), High: decimal.NewFromInt(100), Low: decimal.NewFromInt(85), Change: decimal.NewFromInt(-3), Volume: 800},
		{ListingID: 2, Date: now, Price: decimal.NewFromInt(200), High: decimal.NewFromInt(210), Low: decimal.NewFromInt(195), Change: decimal.NewFromInt(10), Volume: 500},
	}

	// empty slice is a no-op
	if err := r.UpsertManyByListingAndDate(nil); err != nil {
		t.Fatalf("upsert nil: %v", err)
	}

	if err := r.UpsertManyByListingAndDate(infos); err != nil {
		t.Fatalf("upsert many: %v", err)
	}

	// verify we have 3 rows
	var count int64
	db.Model(&model.ListingDailyPriceInfo{}).Count(&count)
	if count != 3 {
		t.Errorf("expected 3 rows, got %d", count)
	}

	// upsert again with updated prices (should update not duplicate)
	updated := []model.ListingDailyPriceInfo{
		{ListingID: 1, Date: now, Price: decimal.NewFromInt(105), High: decimal.NewFromInt(115), Low: decimal.NewFromInt(95), Change: decimal.NewFromInt(8), Volume: 1200},
	}
	if err := r.UpsertManyByListingAndDate(updated); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	var count2 int64
	db.Model(&model.ListingDailyPriceInfo{}).Count(&count2)
	if count2 != 3 {
		t.Errorf("expected still 3 rows after upsert update, got %d", count2)
	}
}

// ---------------------------------------------------------------------------
// ListingRepository — ListByIDs
// ---------------------------------------------------------------------------

func TestListingRepository_ListByIDs(t *testing.T) {
	db := newListingTestDB(t)
	r := NewListingRepository(db)

	// seed 3 listings
	var ids []uint64
	for i := 1; i <= 3; i++ {
		l := &model.Listing{SecurityID: uint64(i * 10), SecurityType: "stock", Price: decimal.NewFromFloat(float64(i * 100))}
		if err := r.Create(l); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		ids = append(ids, l.ID)
	}

	// empty ids → nil result
	got, err := r.ListByIDs(nil)
	if err != nil {
		t.Fatalf("list nil: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for empty ids")
	}

	// fetch 2 of the 3
	got2, err := r.ListByIDs(ids[:2])
	if err != nil {
		t.Fatalf("list 2: %v", err)
	}
	if len(got2) != 2 {
		t.Errorf("expected 2, got %d", len(got2))
	}

	// unknown ids
	got3, err := r.ListByIDs([]uint64{9998, 9999})
	if err != nil {
		t.Fatalf("list unknown: %v", err)
	}
	if len(got3) != 0 {
		t.Errorf("expected 0 for unknown ids, got %d", len(got3))
	}
}

// ---------------------------------------------------------------------------
// OTCOfferRepository — uncovered tx-path methods
// ---------------------------------------------------------------------------

func newOTCOfferTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.OTCOffer{}, &model.OTCNegotiation{}); err != nil {
		t.Fatalf("migrate otc_offers: %v", err)
	}
	return db
}

func seedOTCOffer(t *testing.T, db *gorm.DB) *model.OTCOffer {
	t.Helper()
	uid := uint64(1)
	o := &model.OTCOffer{
		Local:                       true,
		Ticker:                      "AAPL",
		Direction:                   model.OTCDirectionSellInitiated,
		Status:                      model.OTCOfferStatusOpen,
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &uid,
		Quantity:                    decimal.NewFromInt(100),
		Public:                      true,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   1,
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("seed otc offer: %v", err)
	}
	return o
}

func TestOTCOfferRepository_GetByIDTx(t *testing.T) {
	db := newOTCOfferTestDB(t)
	r := NewOTCOfferRepository(db)
	o := seedOTCOffer(t, db)

	var got *model.OTCOffer
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		got, err = r.GetByIDTx(tx, o.ID)
		return err
	})
	if err != nil {
		t.Fatalf("GetByIDTx: %v", err)
	}
	if got.ID != o.ID {
		t.Errorf("id mismatch")
	}

	// not found
	err = db.Transaction(func(tx *gorm.DB) error {
		_, err := r.GetByIDTx(tx, 9999)
		return err
	})
	if err == nil {
		t.Error("expected error for missing offer")
	}
}

func TestOTCOfferRepository_SaveTx(t *testing.T) {
	db := newOTCOfferTestDB(t)
	r := NewOTCOfferRepository(db)
	o := seedOTCOffer(t, db)

	err := db.Transaction(func(tx *gorm.DB) error {
		o.Status = model.OTCOfferStatusCancelled
		return r.SaveTx(tx, o)
	})
	if err != nil {
		t.Fatalf("SaveTx: %v", err)
	}
	got, _ := r.GetByID(o.ID)
	if got.Status != model.OTCOfferStatusCancelled {
		t.Errorf("expected cancelled, got %q", got.Status)
	}
}

func TestOTCOfferRepository_CountOpenByOwnerTickerDirection(t *testing.T) {
	db := newOTCOfferTestDB(t)
	r := NewOTCOfferRepository(db)
	o := seedOTCOffer(t, db)

	n, err := r.CountOpenByOwnerTickerDirection(o.InitiatorOwnerType, o.InitiatorOwnerID, "AAPL", model.OTCDirectionSellInitiated)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1, got %d", n)
	}

	// bank owner (nil id)
	n2, err := r.CountOpenByOwnerTickerDirection(model.OwnerBank, nil, "AAPL", model.OTCDirectionSellInitiated)
	if err != nil {
		t.Fatalf("count bank: %v", err)
	}
	if n2 != 0 {
		t.Errorf("expected 0 bank, got %d", n2)
	}
}

func TestOTCOfferRepository_GetOpenSellListingForUpdate(t *testing.T) {
	db := newOTCOfferTestDB(t)
	r := NewOTCOfferRepository(db)
	o := seedOTCOffer(t, db)

	var got *model.OTCOffer
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		got, err = r.GetOpenSellListingForUpdate(tx, o.InitiatorOwnerType, o.InitiatorOwnerID, "AAPL")
		return err
	})
	if err != nil {
		t.Fatalf("GetOpenSellListingForUpdate: %v", err)
	}
	if got.ID != o.ID {
		t.Errorf("id mismatch")
	}
}

func TestOTCOfferRepository_ListPublicOptionOffersForPeer(t *testing.T) {
	db := newOTCOfferTestDB(t)
	r := NewOTCOfferRepository(db)
	_ = seedOTCOffer(t, db) // local, sell-initiated, public

	rows, err := r.ListPublicOptionOffersForPeer()
	if err != nil {
		t.Fatalf("list public: %v", err)
	}
	// The seeded offer has public=true, local=true, sell-initiated and open status
	if len(rows) != 1 {
		t.Errorf("expected 1, got %d", len(rows))
	}
}

func TestOTCOfferRepository_OutstandingCommittedQuantityTx(t *testing.T) {
	db := newOTCOfferTestDB(t)
	r := NewOTCOfferRepository(db)
	o := seedOTCOffer(t, db)

	var qty decimal.Decimal
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		qty, err = r.OutstandingCommittedQuantityTx(tx, o.ID)
		return err
	})
	if err != nil {
		t.Fatalf("outstanding qty: %v", err)
	}
	// no accepted negotiations → zero
	if !qty.IsZero() {
		t.Errorf("expected 0, got %s", qty)
	}
}

func TestOTCOfferRepository_SumActiveQuantityForSellerExcludingOfferTx(t *testing.T) {
	db := newOTCOfferTestDB(t)
	if err := db.AutoMigrate(&model.OptionContract{}); err != nil {
		t.Fatalf("migrate option_contracts: %v", err)
	}
	r := NewOTCOfferRepository(db)
	o := seedOTCOffer(t, db)

	var sum decimal.Decimal
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		sum, err = r.SumActiveQuantityForSellerExcludingOfferTx(tx, o.InitiatorOwnerType, o.InitiatorOwnerID, 1, o.ID)
		return err
	})
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	_ = sum // result is zero in test env; we just verify no error
}
