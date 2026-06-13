// optimistic_lock_batch_test.go — covers the GORM SQLite UPSERT clobber bug in
// eight repository Update/Save functions and many remaining branch-level gaps.
package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/stock-service/internal/model"
)

// ---------------------------------------------------------------------------
// FuturesRepository.Update — ErrOptimisticLock (stale version)
// ---------------------------------------------------------------------------

func TestFuturesRepository_Update_OptimisticLock(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.FuturesContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFuturesRepository(db)

	ex := &model.StockExchange{
		Name: "FutOpt", Acronym: "FO", MICCode: "FOXX",
		Currency: "USD", TimeZone: "UTC", OpenTime: "09:00", CloseTime: "17:00",
	}
	db.Create(ex)
	f := &model.FuturesContract{
		Ticker: "CLX26", Name: "Crude Oil", ExchangeID: ex.ID,
		ContractSize: 100, ContractUnit: "barrels",
		SettlementDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		Price:          decimal.NewFromInt(80),
	}
	if err := r.Create(f); err != nil {
		t.Fatalf("create: %v", err)
	}

	stale := &model.FuturesContract{}
	db.First(stale, f.ID)
	winner := &model.FuturesContract{}
	db.First(winner, f.ID)

	winner.Name = "Crude Oil (winner)"
	if err := r.Update(winner); err != nil {
		t.Fatalf("winner update: %v", err)
	}

	stale.Name = "Crude Oil (stale)"
	if err := r.Update(stale); !errors.Is(err, ErrOptimisticLock) {
		t.Errorf("expected ErrOptimisticLock, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ForexPairRepository.Update — ErrOptimisticLock
// ---------------------------------------------------------------------------

func TestForexPairRepository_Update_OptimisticLock(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.StockExchange{}, &model.ForexPair{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewForexPairRepository(db)

	ex := &model.StockExchange{
		Name: "FXE", Acronym: "FXE", MICCode: "FXEX",
		Currency: "USD", TimeZone: "UTC", OpenTime: "00:00", CloseTime: "23:59",
	}
	db.Create(ex)
	fp := &model.ForexPair{
		Ticker: "GBPUSD", Name: "GBP/USD", BaseCurrency: "GBP", QuoteCurrency: "USD",
		ExchangeRate: decimal.NewFromFloat(1.25), Liquidity: "high", ExchangeID: ex.ID,
	}
	if err := r.Create(fp); err != nil {
		t.Fatalf("create: %v", err)
	}

	stale := &model.ForexPair{}
	db.First(stale, fp.ID)
	winner := &model.ForexPair{}
	db.First(winner, fp.ID)

	winner.ExchangeRate = decimal.NewFromFloat(1.30)
	if err := r.Update(winner); err != nil {
		t.Fatalf("winner update: %v", err)
	}

	stale.ExchangeRate = decimal.NewFromFloat(0.99)
	if err := r.Update(stale); !errors.Is(err, ErrOptimisticLock) {
		t.Errorf("expected ErrOptimisticLock, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// OrderRepository.Update — ErrOptimisticLock
// ---------------------------------------------------------------------------

func TestOrderRepository_Update_OptimisticLock(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.Order{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewOrderRepository(db)

	uid := uint64(5)
	o := &model.Order{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		ListingID: 1, SecurityType: "stock", Ticker: "MSFT",
		Direction: "buy", OrderType: "market",
		Quantity:         5,
		PricePerUnit:     decimal.NewFromInt(200),
		ApproximatePrice: decimal.NewFromInt(1000),
		Status:           "pending",
	}
	if err := r.Create(o); err != nil {
		t.Fatalf("create: %v", err)
	}

	stale := &model.Order{}
	db.First(stale, o.ID)
	winner := &model.Order{}
	db.First(winner, o.ID)

	winner.Status = "approved"
	if err := r.Update(winner); err != nil {
		t.Fatalf("winner update: %v", err)
	}

	stale.Status = "cancelled"
	if err := r.Update(stale); !errors.Is(err, ErrOptimisticLock) {
		t.Errorf("expected ErrOptimisticLock, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// OptionRepository.Update — ErrOptimisticLock
// ---------------------------------------------------------------------------

func TestOptionRepository_Update_OptimisticLock(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.Stock{}, &model.Option{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewOptionRepository(db)

	opt := &model.Option{
		Ticker: "AAPL260601C00200000", Name: "AAPL Call", StockID: 1,
		OptionType:     "call",
		StrikePrice:    decimal.NewFromInt(200),
		Premium:        decimal.NewFromInt(10),
		SettlementDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := r.Create(opt); err != nil {
		t.Fatalf("create: %v", err)
	}

	stale := &model.Option{}
	db.First(stale, opt.ID)
	winner := &model.Option{}
	db.First(winner, opt.ID)

	winner.Premium = decimal.NewFromInt(12)
	if err := r.Update(winner); err != nil {
		t.Fatalf("winner update: %v", err)
	}

	stale.Premium = decimal.NewFromInt(1)
	if err := r.Update(stale); !errors.Is(err, ErrOptimisticLock) {
		t.Errorf("expected ErrOptimisticLock, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// HoldingRepository.Update and SaveTx — ErrOptimisticLock
// ---------------------------------------------------------------------------

func TestHoldingRepository_Update_OptimisticLock(t *testing.T) {
	db := newHoldingCreditTestDB(t)
	r := NewHoldingRepository(db)

	uid := uint64(301)
	h := &model.Holding{
		OwnerType:    model.OwnerClient,
		OwnerID:      &uid,
		SecurityType: "stock",
		SecurityID:   55,
		Ticker:       "UPD",
		Name:         "Update Test",
		Quantity:     100,
		AveragePrice: decimal.NewFromInt(50),
		AccountID:    1,
	}
	if err := db.Create(h).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	stale := &model.Holding{}
	db.First(stale, h.ID)
	winner := &model.Holding{}
	db.First(winner, h.ID)

	winner.Quantity = 90
	if err := r.Update(winner); err != nil {
		t.Fatalf("winner update: %v", err)
	}

	stale.Quantity = 1
	if err := r.Update(stale); !errors.Is(err, ErrOptimisticLock) {
		t.Errorf("expected ErrOptimisticLock from Update, got: %v", err)
	}
}

func TestHoldingRepository_SaveTx_OptimisticLock(t *testing.T) {
	db := newHoldingCreditTestDB(t)
	r := NewHoldingRepository(db)

	uid := uint64(302)
	h := &model.Holding{
		OwnerType:    model.OwnerClient,
		OwnerID:      &uid,
		SecurityType: "stock",
		SecurityID:   56,
		Ticker:       "STX",
		Name:         "SaveTx Test",
		Quantity:     50,
		AveragePrice: decimal.NewFromInt(60),
		AccountID:    2,
	}
	if err := db.Create(h).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	stale := &model.Holding{}
	db.First(stale, h.ID)
	winner := &model.Holding{}
	db.First(winner, h.ID)

	err := db.Transaction(func(tx *gorm.DB) error {
		winner.Quantity = 45
		return r.SaveTx(tx, winner)
	})
	if err != nil {
		t.Fatalf("winner: %v", err)
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		stale.Quantity = 1
		return r.SaveTx(tx, stale)
	})
	if !errors.Is(err, ErrOptimisticLock) {
		t.Errorf("expected ErrOptimisticLock from SaveTx, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// OTCOfferRepository.Save and SaveTx — ErrOptimisticLock
// ---------------------------------------------------------------------------

func newOTCOfferTestOffer(uid uint64, stockID uint64, ticker string) *model.OTCOffer {
	uid2 := uid
	return &model.OTCOffer{
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &uid2,
		Direction:                   model.OTCDirectionSellInitiated,
		StockID:                     stockID,
		Ticker:                      ticker,
		Quantity:                    decimal.NewFromInt(10),
		Status:                      model.OTCOfferStatusOpen,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   uid,
	}
}

func TestOTCOfferRepository_Save_OptimisticLock(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.OTCOffer{}, &model.OTCOfferRevision{}, &model.OTCOfferReadReceipt{}, &model.OptionContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewOTCOfferRepository(db)

	o := newOTCOfferTestOffer(401, 1, "OPT1")
	if err := r.Create(o); err != nil {
		t.Fatalf("create: %v", err)
	}

	stale := &model.OTCOffer{}
	db.First(stale, o.ID)
	winner := &model.OTCOffer{}
	db.First(winner, o.ID)

	winner.Status = model.OTCOfferStatusPending
	if err := r.Save(winner); err != nil {
		t.Fatalf("winner save: %v", err)
	}

	stale.Status = model.OTCOfferStatusCancelled
	if err := r.Save(stale); !errors.Is(err, ErrOptimisticLock) {
		t.Errorf("expected ErrOptimisticLock from Save, got: %v", err)
	}
}

func TestOTCOfferRepository_SaveTx_OptimisticLock(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.OTCOffer{}, &model.OTCOfferRevision{}, &model.OTCOfferReadReceipt{}, &model.OptionContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewOTCOfferRepository(db)

	o := newOTCOfferTestOffer(402, 2, "OPT2")
	if err := r.Create(o); err != nil {
		t.Fatalf("create: %v", err)
	}

	stale := &model.OTCOffer{}
	db.First(stale, o.ID)
	winner := &model.OTCOffer{}
	db.First(winner, o.ID)

	err := db.Transaction(func(tx *gorm.DB) error {
		winner.Status = model.OTCOfferStatusPending
		return r.SaveTx(tx, winner)
	})
	if err != nil {
		t.Fatalf("winner: %v", err)
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		stale.Status = model.OTCOfferStatusCancelled
		return r.SaveTx(tx, stale)
	})
	if !errors.Is(err, ErrOptimisticLock) {
		t.Errorf("expected ErrOptimisticLock from SaveTx, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// FundRepository.Save — RowsAffected==0 path after concurrent update
// ---------------------------------------------------------------------------

func TestFundRepository_Save_StaleVersion(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.InvestmentFund{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundRepository(db)

	f := &model.InvestmentFund{Name: "OLFund", ManagerEmployeeID: 1, RSDAccountID: 100, Active: true}
	if err := r.Create(f); err != nil {
		t.Fatalf("create: %v", err)
	}

	stale := &model.InvestmentFund{}
	db.First(stale, f.ID)
	winner := &model.InvestmentFund{}
	db.First(winner, f.ID)

	winner.Name = "OLFund-winner"
	if err := r.Save(winner); err != nil {
		t.Fatalf("winner: %v", err)
	}

	stale.Name = "OLFund-stale"
	// After the fix, stale save with old version → RowsAffected==0 → error.
	if err := r.Save(stale); err == nil {
		t.Error("expected error for stale version save")
	}
}

// ---------------------------------------------------------------------------
// HoldingReservationRepository.InsertIfAbsent —
// OTCContractID, CrossbankTxID, and PeerOptionContractID idempotent re-read paths
// ---------------------------------------------------------------------------

func TestHoldingReservation_InsertIfAbsent_OTCContractID_Idempotent(t *testing.T) {
	db := newHoldingReservationTestDB(t)
	repo := NewHoldingReservationRepository(db)
	h := seedHolding(t, db)

	otcID := uint64(8001)
	r := &model.HoldingReservation{
		HoldingID: h.ID, OTCContractID: &otcID, Quantity: 20,
		Status: model.HoldingReservationStatusActive,
	}
	inserted, row, err := repo.InsertIfAbsent(r)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if !inserted {
		t.Error("expected inserted=true on first call")
	}

	retry := &model.HoldingReservation{
		HoldingID: h.ID, OTCContractID: &otcID, Quantity: 999,
		Status: model.HoldingReservationStatusActive,
	}
	inserted2, row2, err := repo.InsertIfAbsent(retry)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if inserted2 {
		t.Error("expected inserted=false on idempotent call")
	}
	if row2.ID != row.ID {
		t.Errorf("expected same row id: got %d want %d", row2.ID, row.ID)
	}
}

func TestHoldingReservation_InsertIfAbsent_CrossbankTxID_Idempotent(t *testing.T) {
	db := newHoldingReservationTestDB(t)
	repo := NewHoldingReservationRepository(db)
	h := seedHolding(t, db)

	txID := "bank-222:cross-uuid-xyz"
	r := &model.HoldingReservation{
		HoldingID: h.ID, CrossbankTxID: &txID, Quantity: 30,
		Status: model.HoldingReservationStatusActive,
	}
	inserted, row, err := repo.InsertIfAbsent(r)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if !inserted {
		t.Error("expected inserted=true on first call")
	}

	retry := &model.HoldingReservation{
		HoldingID: h.ID, CrossbankTxID: &txID, Quantity: 888,
		Status: model.HoldingReservationStatusActive,
	}
	inserted2, row2, err := repo.InsertIfAbsent(retry)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if inserted2 {
		t.Error("expected inserted=false on idempotent call")
	}
	if row2.ID != row.ID {
		t.Errorf("expected same row id: got %d want %d", row2.ID, row.ID)
	}
}

func TestHoldingReservation_InsertIfAbsent_PeerOptionContractID_Idempotent(t *testing.T) {
	db := newHoldingReservationTestDB(t)
	repo := NewHoldingReservationRepository(db)
	h := seedHolding(t, db)

	peerID := uint64(9001)
	r := &model.HoldingReservation{
		HoldingID: h.ID, PeerOptionContractID: &peerID, Quantity: 15,
		Status: model.HoldingReservationStatusActive,
	}
	inserted, row, err := repo.InsertIfAbsent(r)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if !inserted {
		t.Error("expected inserted=true on first call")
	}

	retry := &model.HoldingReservation{
		HoldingID: h.ID, PeerOptionContractID: &peerID, Quantity: 777,
		Status: model.HoldingReservationStatusActive,
	}
	inserted2, row2, err := repo.InsertIfAbsent(retry)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if inserted2 {
		t.Error("expected inserted=false on idempotent call")
	}
	if row2.ID != row.ID {
		t.Errorf("expected same row id: got %d want %d", row2.ID, row.ID)
	}
}

// ---------------------------------------------------------------------------
// OTCNegotiationRepository.ListByBidder — bank-owner and statuses-filter paths
// ---------------------------------------------------------------------------

func TestOTCNegotiationRepository_ListByBidder_BankOwnerAndStatuses(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)

	// Bank owner path: ownerID == nil → covers `q.Where("bidder_owner_id IS NULL")` branch.
	rows, total, err := r.ListByBidder(model.OwnerBank, nil, nil, 1, 20)
	if err != nil {
		t.Fatalf("ListByBidder bank: %v", err)
	}
	if total != 0 || len(rows) != 0 {
		t.Errorf("expected empty bank list, got %d", len(rows))
	}

	// Insert a client negotiation.
	bidder := uint64(501)
	neg := newSampleNegotiation(99, &bidder, model.OTCNegotiationStatusOpen)
	if err := db.Create(neg).Error; err != nil {
		t.Fatalf("seed neg: %v", err)
	}

	// Non-empty statuses → covers `q.Where("status IN ?", statuses)` branch.
	rows2, total2, err := r.ListByBidder(
		model.OwnerClient, &bidder,
		[]string{model.OTCNegotiationStatusOpen}, 1, 20,
	)
	if err != nil {
		t.Fatalf("ListByBidder with statuses: %v", err)
	}
	if total2 != 1 || len(rows2) != 1 {
		t.Errorf("expected 1 row, got total=%d", total2)
	}

	// Status filter that matches nothing.
	rows3, total3, err := r.ListByBidder(
		model.OwnerClient, &bidder,
		[]string{model.OTCNegotiationStatusRejected}, 1, 20,
	)
	if err != nil {
		t.Fatalf("ListByBidder with non-matching statuses: %v", err)
	}
	if total3 != 0 || len(rows3) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows3))
	}
}

// ---------------------------------------------------------------------------
// stampHoldingSaga — exercised via HoldingRepository.Upsert with saga context
// ---------------------------------------------------------------------------

func TestHoldingRepository_Upsert_StampsSagaContext(t *testing.T) {
	db := newHoldingCreditTestDB(t)
	r := NewHoldingRepository(db)

	ctx := saga.WithSagaID(context.Background(), "saga-stamp-1")
	ctx = saga.WithSagaStep(ctx, saga.StepTransferOwnership)

	uid := uint64(601)
	h := &model.Holding{
		OwnerType:    model.OwnerClient,
		OwnerID:      &uid,
		SecurityType: "stock",
		SecurityID:   77,
		Ticker:       "SGS",
		Name:         "Saga Stamp Test",
		Quantity:     10,
		AveragePrice: decimal.NewFromInt(100),
		AccountID:    1,
	}
	if err := r.Upsert(ctx, h); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var got model.Holding
	db.First(&got, "security_type = ? AND security_id = ?", "stock", uint64(77))
	if got.SagaID == nil || *got.SagaID != "saga-stamp-1" {
		t.Errorf("expected SagaID=saga-stamp-1, got: %v", got.SagaID)
	}
	if got.SagaStep == nil || *got.SagaStep != string(saga.StepTransferOwnership) {
		t.Errorf("expected SagaStep=%s, got: %v", saga.StepTransferOwnership, got.SagaStep)
	}
}

// ---------------------------------------------------------------------------
// FundContributionRepository.UpdateStatus — not-found (RowsAffected==0) path
// ---------------------------------------------------------------------------

func TestFundContributionRepository_UpdateStatus_NotFound(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.FundContribution{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundContributionRepository(db)

	if err := r.UpdateStatus(99999, "completed"); err == nil {
		t.Error("expected error when updating non-existent fund contribution")
	}
}

// ---------------------------------------------------------------------------
// FundContributionRepository.ListByFund — covers the pageSize/page default path
// ---------------------------------------------------------------------------

func TestFundContributionRepository_ListByFund_Pagination(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.FundContribution{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewFundContributionRepository(db)

	uid := uint64(701)
	for i := 0; i < 3; i++ {
		s := fmt.Sprintf("saga-%d", i)
		fc := &model.FundContribution{
			FundID:    1,
			OwnerType: model.OwnerClient,
			OwnerID:   &uid,
			AmountRSD: decimal.NewFromInt(int64(1000 + i*100)),
			Status:    "pending",
			SagaID:    s,
		}
		db.Create(fc)
	}
	rows, total, err := r.ListByFund(1, 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 || len(rows) != 3 {
		t.Errorf("expected 3 rows, got total=%d len=%d", total, len(rows))
	}
}

// ---------------------------------------------------------------------------
// ClientFundPositionRepository.IncrementContribution — idempotent replay path
// (covers the `if ins.RowsAffected == 0 { return nil }` branch)
// ---------------------------------------------------------------------------

func TestClientFundPositionRepository_IncrementContribution_Replay(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.ClientFundPosition{}, &model.FundPositionSettlement{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewClientFundPositionRepository(db)

	uid := uint64(801)
	delta := decimal.NewFromInt(500)
	const contribID = uint64(1001)

	if err := r.IncrementContribution(2, model.OwnerClient, &uid, delta, contribID); err != nil {
		t.Fatalf("first increment: %v", err)
	}
	pos, _ := r.GetByFundAndOwner(2, model.OwnerClient, &uid)
	if !pos.TotalContributedRSD.Equal(delta) {
		t.Errorf("expected %s, got %s", delta, pos.TotalContributedRSD)
	}

	// Second call with same contribID → settlement already exists → no-op.
	if err := r.IncrementContribution(2, model.OwnerClient, &uid, delta, contribID); err != nil {
		t.Fatalf("replay increment: %v", err)
	}
	posAfter, _ := r.GetByFundAndOwner(2, model.OwnerClient, &uid)
	if !posAfter.TotalContributedRSD.Equal(delta) {
		t.Errorf("replay should be no-op: expected %s, got %s", delta, posAfter.TotalContributedRSD)
	}
}

// ---------------------------------------------------------------------------
// OTCNegotiationRepository.GetByID and LockByID — not-found paths
// ---------------------------------------------------------------------------

func TestOTCNegotiationRepository_GetByID_NotFound(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)
	if _, err := r.GetByID(99999); err == nil {
		t.Error("expected not-found error")
	}
}

func TestOTCNegotiationRepository_LockByID_NotFound(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)
	err := db.Transaction(func(tx *gorm.DB) error {
		_, e := r.LockByID(tx, 99999)
		return e
	})
	if err == nil {
		t.Error("expected not-found error from LockByID")
	}
}
