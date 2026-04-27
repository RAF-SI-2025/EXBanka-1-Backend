package repository

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

// newSystemTypeScopingDB opens a fresh in-memory SQLite DB with all of the
// models touched by the (owner_type, owner_id) ownership tests auto-migrated.
// The legacy name is preserved so the test file still describes the original
// regression — the schema swapped from (user_id, system_type) to
// (owner_type, owner_id) in plan 2026-04-27-owner-type-schema.
func newSystemTypeScopingDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Order{},
		&model.Holding{},
		&model.CapitalGain{},
		&model.TaxCollection{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// sampleOrder returns a minimally-valid Order row for seeding the in-memory DB.
func sampleOrder(ownerType model.OwnerType, ownerID *uint64) *model.Order {
	return &model.Order{
		OwnerType:         ownerType,
		OwnerID:           ownerID,
		ListingID:         1,
		SecurityType:      "stock",
		Ticker:            "AAPL",
		Direction:         "buy",
		OrderType:         "limit",
		Quantity:          10,
		ContractSize:      1,
		PricePerUnit:      decimal.NewFromInt(100),
		ApproximatePrice:  decimal.NewFromInt(1000),
		Status:            "approved",
		RemainingPortions: 10,
		AccountID:         1,
	}
}

func TestOrderRepo_ListByOwner_FiltersOnOwnerType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewOrderRepository(db)

	// Two orders with the same owner_id semantics but different owner_types.
	uid := uint64(5)
	clientOrder := sampleOrder(model.OwnerClient, &uid)
	bankOrder := sampleOrder(model.OwnerBank, nil)
	if err := repo.Create(clientOrder); err != nil {
		t.Fatalf("create client order: %v", err)
	}
	if err := repo.Create(bankOrder); err != nil {
		t.Fatalf("create bank order: %v", err)
	}

	gotClient, totalClient, err := repo.ListByOwner(model.OwnerClient, &uid, OrderFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list client: %v", err)
	}
	if totalClient != 1 {
		t.Errorf("expected 1 row for client, got %d", totalClient)
	}
	if len(gotClient) != 1 || gotClient[0].ID != clientOrder.ID {
		t.Errorf("wrong client orders returned: %+v", gotClient)
	}

	gotBank, totalBank, err := repo.ListByOwner(model.OwnerBank, nil, OrderFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list bank: %v", err)
	}
	if totalBank != 1 {
		t.Errorf("expected 1 row for bank, got %d", totalBank)
	}
	if len(gotBank) != 1 || gotBank[0].ID != bankOrder.ID {
		t.Errorf("wrong bank orders returned: %+v", gotBank)
	}
}

func TestOrderRepo_GetByIDWithOwner_ChecksOwnerType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewOrderRepository(db)

	uid := uint64(5)
	clientOrder := sampleOrder(model.OwnerClient, &uid)
	if err := repo.Create(clientOrder); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Correct owner: returns the order.
	got, err := repo.GetByIDWithOwner(clientOrder.ID, model.OwnerClient, &uid)
	if err != nil {
		t.Fatalf("expected success for correct owner, got: %v", err)
	}
	if got == nil || got.ID != clientOrder.ID {
		t.Errorf("wrong order returned: %+v", got)
	}

	// Cross-owner-type access (bank): returns NotFound rather than leaking existence.
	if _, err := repo.GetByIDWithOwner(clientOrder.ID, model.OwnerBank, nil); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected ErrRecordNotFound for cross-owner access, got: %v", err)
	}

	// Different owner_id: also NotFound.
	other := uint64(6)
	if _, err := repo.GetByIDWithOwner(clientOrder.ID, model.OwnerClient, &other); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected ErrRecordNotFound for wrong owner_id, got: %v", err)
	}
}

// sampleHolding returns a minimally-valid Holding row for seeding tests.
// The accountID arg is stored as the audit "last-used" account; it is not
// part of the aggregation key (see holding_repository.Upsert).
func sampleHolding(ownerType model.OwnerType, ownerID *uint64, securityType string, securityID uint64, accountID uint64, qty int64) *model.Holding {
	return &model.Holding{
		OwnerType:     ownerType,
		OwnerID:       ownerID,
		UserFirstName: "Test",
		UserLastName:  "User",
		SecurityType:  securityType,
		SecurityID:    securityID,
		ListingID:     1,
		Ticker:        "AAPL",
		Name:          "Apple Inc.",
		Quantity:      qty,
		AveragePrice:  decimal.NewFromInt(100),
		AccountID:     accountID,
	}
}

func TestHoldingRepo_ListByOwner_FiltersOnOwnerType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewHoldingRepository(db)

	uid := uint64(5)
	if err := db.Create(sampleHolding(model.OwnerClient, &uid, "stock", 100, 1, 10)).Error; err != nil {
		t.Fatalf("seed client holding: %v", err)
	}
	if err := db.Create(sampleHolding(model.OwnerBank, nil, "stock", 100, 2, 15)).Error; err != nil {
		t.Fatalf("seed bank holding: %v", err)
	}

	client, total, err := repo.ListByOwner(model.OwnerClient, &uid, HoldingFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list client: %v", err)
	}
	if total != 1 {
		t.Errorf("expected 1 client holding, got %d", total)
	}
	if len(client) != 1 || client[0].AccountID != 1 {
		t.Errorf("wrong client holdings returned: %+v", client)
	}

	bank, totalB, err := repo.ListByOwner(model.OwnerBank, nil, HoldingFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list bank: %v", err)
	}
	if totalB != 1 {
		t.Errorf("expected 1 bank holding, got %d", totalB)
	}
	if len(bank) != 1 || bank[0].AccountID != 2 {
		t.Errorf("wrong bank holdings returned: %+v", bank)
	}
}

func TestHoldingRepo_GetByOwnerAndSecurity_ChecksOwnerType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewHoldingRepository(db)

	uid := uint64(5)
	clientH := sampleHolding(model.OwnerClient, &uid, "stock", 100, 1, 10)
	if err := db.Create(clientH).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Correct match.
	got, err := repo.GetByOwnerAndSecurity(model.OwnerClient, &uid, "stock", 100)
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if got.ID != clientH.ID {
		t.Errorf("wrong holding returned: %+v", got)
	}

	// Wrong owner_type: NotFound.
	if _, err := repo.GetByOwnerAndSecurity(model.OwnerBank, nil, "stock", 100); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected ErrRecordNotFound for cross-owner access, got: %v", err)
	}
}

func TestHoldingRepo_FindOldestLongOptionHolding_ChecksOwnerType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewHoldingRepository(db)

	// Only a client holding exists for (owner=5, option=200).
	uid := uint64(5)
	if err := db.Create(sampleHolding(model.OwnerClient, &uid, "option", 200, 1, 3)).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Lookup under bank owner_type must return (nil, nil).
	h, err := repo.FindOldestLongOptionHolding(model.OwnerBank, nil, 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h != nil {
		t.Errorf("expected nil holding for cross-owner access, got: %+v", h)
	}

	// Correct owner_type finds the holding.
	h, err = repo.FindOldestLongOptionHolding(model.OwnerClient, &uid, 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h == nil {
		t.Fatal("expected client holding to be found")
	}
}

func TestHoldingRepo_Upsert_SeparatesClientAndBank(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewHoldingRepository(db)

	// First Upsert: creates the bank-side holding.
	bank := sampleHolding(model.OwnerBank, nil, "stock", 100, 1, 10)
	if err := repo.Upsert(bank); err != nil {
		t.Fatalf("upsert bank: %v", err)
	}

	// Second Upsert with same security/account but different owner_type
	// must create a fresh row rather than merging.
	uid := uint64(5)
	client := sampleHolding(model.OwnerClient, &uid, "stock", 100, 1, 20)
	if err := repo.Upsert(client); err != nil {
		t.Fatalf("upsert client: %v", err)
	}

	// Both rows should exist.
	var count int64
	if err := db.Model(&model.Holding{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 rows after upserts, got %d", count)
	}

	gotBank, err := repo.GetByOwnerAndSecurity(model.OwnerBank, nil, "stock", 100)
	if err != nil {
		t.Fatalf("get bank: %v", err)
	}
	if gotBank.Quantity != 10 {
		t.Errorf("expected bank quantity unchanged at 10, got %d", gotBank.Quantity)
	}
	gotClient, err := repo.GetByOwnerAndSecurity(model.OwnerClient, &uid, "stock", 100)
	if err != nil {
		t.Fatalf("get client: %v", err)
	}
	if gotClient.Quantity != 20 {
		t.Errorf("expected client quantity 20, got %d", gotClient.Quantity)
	}
}

// sampleCapitalGain returns a minimally-valid CapitalGain row.
func sampleCapitalGain(ownerType model.OwnerType, ownerID *uint64, totalGain int64, year, month int) *model.CapitalGain {
	return &model.CapitalGain{
		OwnerType:        ownerType,
		OwnerID:          ownerID,
		SecurityType:     "stock",
		Ticker:           "AAPL",
		Quantity:         5,
		BuyPricePerUnit:  decimal.NewFromInt(100),
		SellPricePerUnit: decimal.NewFromInt(120),
		TotalGain:        decimal.NewFromInt(totalGain),
		Currency:         "RSD",
		AccountID:        1,
		TaxYear:          year,
		TaxMonth:         month,
	}
}

func TestCapitalGainRepo_ListByOwner_FiltersOnOwnerType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewCapitalGainRepository(db)

	uid := uint64(5)
	if err := repo.Create(sampleCapitalGain(model.OwnerClient, &uid, 100, 2026, 4)); err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := repo.Create(sampleCapitalGain(model.OwnerBank, nil, 200, 2026, 4)); err != nil {
		t.Fatalf("create bank: %v", err)
	}

	records, total, err := repo.ListByOwner(model.OwnerClient, &uid, 1, 10)
	if err != nil {
		t.Fatalf("list client: %v", err)
	}
	if total != 1 || len(records) != 1 {
		t.Errorf("expected 1 client record, got total=%d len=%d", total, len(records))
	}
	if len(records) == 1 && records[0].OwnerType != model.OwnerClient {
		t.Errorf("expected client record, got %s", records[0].OwnerType)
	}

	records, total, err = repo.ListByOwner(model.OwnerBank, nil, 1, 10)
	if err != nil {
		t.Fatalf("list bank: %v", err)
	}
	if total != 1 || len(records) != 1 {
		t.Errorf("expected 1 bank record, got total=%d len=%d", total, len(records))
	}
}

func TestCapitalGainRepo_SumByOwnerMonth_FiltersOnOwnerType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewCapitalGainRepository(db)

	uid := uint64(5)
	if err := repo.Create(sampleCapitalGain(model.OwnerClient, &uid, 100, 2026, 4)); err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := repo.Create(sampleCapitalGain(model.OwnerBank, nil, 200, 2026, 4)); err != nil {
		t.Fatalf("create bank: %v", err)
	}

	summaries, err := repo.SumByOwnerMonth(model.OwnerClient, &uid, 2026, 4)
	if err != nil {
		t.Fatalf("sum client: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if !summaries[0].TotalGain.Equal(decimal.NewFromInt(100)) {
		t.Errorf("expected total 100 for client, got %s", summaries[0].TotalGain)
	}

	summaries, err = repo.SumByOwnerMonth(model.OwnerBank, nil, 2026, 4)
	if err != nil {
		t.Fatalf("sum bank: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if !summaries[0].TotalGain.Equal(decimal.NewFromInt(200)) {
		t.Errorf("expected total 200 for bank, got %s", summaries[0].TotalGain)
	}
}

func TestCapitalGainRepo_SumByOwnerYear_FiltersOnOwnerType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewCapitalGainRepository(db)

	uid := uint64(5)
	if err := repo.Create(sampleCapitalGain(model.OwnerClient, &uid, 100, 2026, 1)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(sampleCapitalGain(model.OwnerClient, &uid, 50, 2026, 2)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(sampleCapitalGain(model.OwnerBank, nil, 999, 2026, 1)); err != nil {
		t.Fatal(err)
	}

	summaries, err := repo.SumByOwnerYear(model.OwnerClient, &uid, 2026)
	if err != nil {
		t.Fatalf("sum client: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if !summaries[0].TotalGain.Equal(decimal.NewFromInt(150)) {
		t.Errorf("expected 150 for client, got %s", summaries[0].TotalGain)
	}
}

func TestTaxCollectionRepo_SumByOwner_FiltersOnOwnerType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewTaxCollectionRepository(db)

	uid := uint64(5)
	tc1 := &model.TaxCollection{
		OwnerType: model.OwnerClient, OwnerID: &uid, Year: 2026, Month: 4,
		AccountID: 1, Currency: "RSD",
		TotalGain: decimal.NewFromInt(1000), TaxAmount: decimal.NewFromInt(150),
		TaxAmountRSD: decimal.NewFromInt(150),
	}
	tc2 := &model.TaxCollection{
		OwnerType: model.OwnerBank, OwnerID: nil, Year: 2026, Month: 4,
		AccountID: 2, Currency: "RSD",
		TotalGain: decimal.NewFromInt(2000), TaxAmount: decimal.NewFromInt(300),
		TaxAmountRSD: decimal.NewFromInt(300),
	}
	if err := repo.Create(tc1); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(tc2); err != nil {
		t.Fatal(err)
	}

	clientTotal, err := repo.SumByOwnerMonth(model.OwnerClient, &uid, 2026, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !clientTotal.Equal(decimal.NewFromInt(150)) {
		t.Errorf("expected client sum 150, got %s", clientTotal)
	}

	bankTotal, err := repo.SumByOwnerMonth(model.OwnerBank, nil, 2026, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !bankTotal.Equal(decimal.NewFromInt(300)) {
		t.Errorf("expected bank sum 300, got %s", bankTotal)
	}

	yearClient, err := repo.SumByOwnerYear(model.OwnerClient, &uid, 2026)
	if err != nil {
		t.Fatal(err)
	}
	if !yearClient.Equal(decimal.NewFromInt(150)) {
		t.Errorf("expected year client 150, got %s", yearClient)
	}
}
