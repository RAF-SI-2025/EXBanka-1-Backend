package repository

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// ---------------------------------------------------------------------------
// OTCOfferRepository: cover all methods directly.
// ---------------------------------------------------------------------------

func newOTCOfferDB(t *testing.T) (*OTCOfferRepository, *OTCOfferRevisionRepository) {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(
		&model.OTCOffer{}, &model.OTCOfferRevision{}, &model.OTCOfferReadReceipt{},
		&model.OptionContract{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewOTCOfferRepository(db), NewOTCOfferRevisionRepository(db)
}

func sampleSellOffer(uid uint64, stockID uint64, qty int64, status string) *model.OTCOffer {
	uid2 := uid
	return &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &uid2,
		Direction: model.OTCDirectionSellInitiated, StockID: stockID,
		Quantity:                    decimal.NewFromInt(qty),
		Status:                      status,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   uid,
	}
}

func TestOTCOfferRepository_Crud(t *testing.T) {
	r, _ := newOTCOfferDB(t)
	o := sampleSellOffer(7, 42, 10, model.OTCOfferStatusPending)
	if err := r.Create(o); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := r.GetByID(o.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.StockID != 42 {
		t.Errorf("stock id mismatch")
	}
	if r.DB() == nil {
		t.Error("DB() returned nil")
	}
	got.Status = model.OTCOfferStatusRejected
	if err := r.Save(got); err != nil {
		t.Fatalf("save: %v", err)
	}
}

func TestOTCOfferRepository_ListByOwner(t *testing.T) {
	r, _ := newOTCOfferDB(t)
	uid := uint64(7)
	_ = r.Create(sampleSellOffer(7, 42, 10, model.OTCOfferStatusPending))
	_ = r.Create(sampleSellOffer(7, 43, 5, model.OTCOfferStatusAccepted))
	rows, total, err := r.ListByOwner(model.OwnerClient, &uid, "initiator", nil, 0, 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 || len(rows) != 2 {
		t.Errorf("got %d/%d", total, len(rows))
	}
	_, total, err = r.ListByOwner(model.OwnerClient, &uid, "initiator", []string{model.OTCOfferStatusPending}, 0, 1, 10)
	if err != nil {
		t.Fatalf("list filter: %v", err)
	}
	if total != 1 {
		t.Errorf("filter status: total=%d", total)
	}
	rows, _, err = r.ListByOwner(model.OwnerClient, &uid, "either", nil, 42, 1, 10)
	if err != nil {
		t.Fatalf("list either: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("filter stock: len=%d", len(rows))
	}
	rows, _, err = r.ListByOwner(model.OwnerClient, &uid, "counterparty", nil, 0, 1, 10)
	if err != nil {
		t.Fatalf("list cp: %v", err)
	}
	// Counterparty role: nobody set as counterparty.
	if len(rows) != 0 {
		t.Errorf("expected 0 cp, got %d", len(rows))
	}
}

func TestOTCOfferRepository_SumActiveQuantityForSeller(t *testing.T) {
	r, _ := newOTCOfferDB(t)
	uid := uint64(7)
	_ = r.Create(sampleSellOffer(7, 42, 10, model.OTCOfferStatusPending))
	_ = r.Create(sampleSellOffer(7, 42, 5, model.OTCOfferStatusCountered))
	_ = r.Create(sampleSellOffer(7, 42, 100, model.OTCOfferStatusRejected))
	got, err := r.SumActiveQuantityForSeller(model.OwnerClient, &uid, 42)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got.Equal(decimal.NewFromInt(15)) {
		t.Errorf("got %s want 15 (rejected excluded)", got)
	}
}

// ---------------------------------------------------------------------------
// OTCOfferRevisionRepository
// ---------------------------------------------------------------------------

func TestOTCOfferRevisionRepository_AppendAndList(t *testing.T) {
	r, revRepo := newOTCOfferDB(t)
	o := sampleSellOffer(7, 42, 10, model.OTCOfferStatusPending)
	_ = r.Create(o)
	rev := &model.OTCOfferRevision{
		OfferID: o.ID, RevisionNumber: 1,
		Quantity: o.Quantity, StrikePrice: decimal.Zero,
		Premium: decimal.Zero, SettlementDate: time.Time{},
		ModifiedByPrincipalType: "client", ModifiedByPrincipalID: 7,
		Action: model.OTCActionCreate,
	}
	if err := revRepo.Append(rev); err != nil {
		t.Fatalf("append: %v", err)
	}
	rows, err := revRepo.ListByOffer(o.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d", len(rows))
	}
	next, err := revRepo.NextRevisionNumber(o.ID)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if next != 2 {
		t.Errorf("next=%d want 2", next)
	}
}

// ---------------------------------------------------------------------------
// TaxCollectionRepository
// ---------------------------------------------------------------------------

func TestTaxCollectionRepository_Ops(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.TaxCollection{}, &model.CapitalGain{}, &model.Holding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := NewTaxCollectionRepository(db)
	uid := uint64(7)
	c := &model.TaxCollection{
		OwnerType: model.OwnerClient, OwnerID: &uid,
		Year: 2026, Month: 5, AccountID: 1, Currency: "RSD",
		TaxAmountRSD: decimal.NewFromInt(15),
	}
	if err := r.Create(c); err != nil {
		t.Fatalf("create: %v", err)
	}
	yr, err := r.SumByOwnerYear(model.OwnerClient, &uid, 2026)
	if err != nil {
		t.Fatalf("year: %v", err)
	}
	if !yr.Equal(decimal.NewFromInt(15)) {
		t.Errorf("yr=%s", yr)
	}
	mo, err := r.SumByOwnerMonth(model.OwnerClient, &uid, 2026, 5)
	if err != nil {
		t.Fatalf("month: %v", err)
	}
	if !mo.Equal(decimal.NewFromInt(15)) {
		t.Errorf("mo=%s", mo)
	}
	all, err := r.SumByOwnerAllTime(model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if !all.Equal(decimal.NewFromInt(15)) {
		t.Errorf("all=%s", all)
	}
	cnt, err := r.CountByKey(model.OwnerClient, &uid, 2026, 5, 1, "RSD")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if cnt != 1 {
		t.Errorf("cnt=%d", cnt)
	}
	last, err := r.GetLastCollection(model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("last: %v", err)
	}
	if last.ID != c.ID {
		t.Errorf("id mismatch")
	}
	rows, total, err := r.ListByOwner(model.OwnerClient, &uid, 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Errorf("got %d/%d", total, len(rows))
	}
}

// TestListOwnersWithGains_ExcludesBankOwners verifies the Profit Banke
// exemption: bank-owned capital gains (actuary trading on behalf of the bank)
// must never be returned for tax collection. Spec
// docs/superpowers/specs/2026-06-04-options-premium-tax-design.md §3.2.
func TestListOwnersWithGains_ExcludesBankOwners(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.CapitalGain{}, &model.TaxCollection{}, &model.Holding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := NewTaxCollectionRepository(db)
	cgRepo := NewCapitalGainRepository(db)

	clientID := uint64(5)
	if err := cgRepo.Create(&model.CapitalGain{
		OwnerType: model.OwnerClient, OwnerID: &clientID, OTC: true, SecurityType: "option",
		Ticker: "AAPL", Quantity: 50, BuyPricePerUnit: decimal.Zero, SellPricePerUnit: decimal.NewFromInt(23),
		TotalGain: decimal.NewFromInt(1150), Currency: "USD", AccountID: 11, TaxYear: 2026, TaxMonth: 6,
	}); err != nil {
		t.Fatalf("seed client: %v", err)
	}
	if err := cgRepo.Create(&model.CapitalGain{
		OwnerType: model.OwnerBank, OwnerID: nil, OTC: true, SecurityType: "option",
		Ticker: "AAPL", Quantity: 50, BuyPricePerUnit: decimal.Zero, SellPricePerUnit: decimal.NewFromInt(23),
		TotalGain: decimal.NewFromInt(1150), Currency: "USD", AccountID: 12, TaxYear: 2026, TaxMonth: 6,
	}); err != nil {
		t.Fatalf("seed bank: %v", err)
	}

	rows, _, err := repo.ListOwnersWithGains(2026, 6, TaxFilter{Page: 1, PageSize: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, r := range rows {
		if r.OwnerType == string(model.OwnerBank) {
			t.Fatalf("bank owner must be excluded from tax collection, got %+v", r)
		}
	}
	if len(rows) != 1 || rows[0].OwnerType != string(model.OwnerClient) {
		t.Fatalf("expected exactly the client owner, got %+v", rows)
	}
}

// ---------------------------------------------------------------------------
// SagaLogRepository: WithTx / IsForwardCompleted
// ---------------------------------------------------------------------------

func TestSagaLogRepository_WithTxAndIsForwardCompleted(t *testing.T) {
	db := newTestDB(t)
	r := NewSagaLogRepository(db)
	row := &model.SagaLog{
		SagaID: "s-1", OrderID: 99, StepNumber: 1, StepName: "do_x",
		Status: model.SagaStatusCompleted, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	tx := db.Begin()
	if r.WithTx(tx) == nil {
		t.Error("WithTx nil")
	}
	tx.Rollback()
	got, err := r.IsForwardCompleted("s-1", "do_x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got {
		t.Errorf("expected completed=true")
	}
	got, err = r.IsForwardCompleted("s-1", "other")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got {
		t.Errorf("expected completed=false for unknown step")
	}
}
