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
// models touched by the (user_id, system_type) ownership tests auto-migrated.
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
func sampleOrder(userID uint64, systemType string) *model.Order {
	return &model.Order{
		UserID:            userID,
		SystemType:        systemType,
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

func TestOrderRepo_ListByUser_FiltersOnSystemType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewOrderRepository(db)

	// Two orders with the same user_id but different system_types — the
	// classic cross-namespace collision that Task 2 is meant to isolate.
	clientOrder := sampleOrder(5, "client")
	employeeOrder := sampleOrder(5, "employee")
	if err := repo.Create(clientOrder); err != nil {
		t.Fatalf("create client order: %v", err)
	}
	if err := repo.Create(employeeOrder); err != nil {
		t.Fatalf("create employee order: %v", err)
	}

	gotClient, totalClient, err := repo.ListByUser(5, "client", OrderFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list client: %v", err)
	}
	if totalClient != 1 {
		t.Errorf("expected 1 row for client, got %d", totalClient)
	}
	if len(gotClient) != 1 || gotClient[0].ID != clientOrder.ID {
		t.Errorf("wrong client orders returned: %+v", gotClient)
	}

	gotEmployee, totalEmployee, err := repo.ListByUser(5, "employee", OrderFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list employee: %v", err)
	}
	if totalEmployee != 1 {
		t.Errorf("expected 1 row for employee, got %d", totalEmployee)
	}
	if len(gotEmployee) != 1 || gotEmployee[0].ID != employeeOrder.ID {
		t.Errorf("wrong employee orders returned: %+v", gotEmployee)
	}
}

func TestOrderRepo_ListByUser_EmptySystemTypeReturnsNothing(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewOrderRepository(db)

	if err := repo.Create(sampleOrder(5, "employee")); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Matches no rows — the system_type column is NOT NULL, so queries with
	// empty string filter never match. That's the intentional safe no-op
	// documented in the plan's fallback path.
	_, total, err := repo.ListByUser(5, "", OrderFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 0 {
		t.Errorf("expected 0 rows for empty system_type, got %d", total)
	}
}

func TestOrderRepo_GetByIDWithOwner_ChecksSystemType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewOrderRepository(db)

	employeeOrder := sampleOrder(5, "employee")
	if err := repo.Create(employeeOrder); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Correct owner: returns the order.
	got, err := repo.GetByIDWithOwner(employeeOrder.ID, 5, "employee")
	if err != nil {
		t.Fatalf("expected success for correct owner, got: %v", err)
	}
	if got == nil || got.ID != employeeOrder.ID {
		t.Errorf("wrong order returned: %+v", got)
	}

	// Cross-system-type access: returns NotFound rather than leaking existence.
	if _, err := repo.GetByIDWithOwner(employeeOrder.ID, 5, "client"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected ErrRecordNotFound for cross-system access, got: %v", err)
	}

	// Different user_id: also NotFound.
	if _, err := repo.GetByIDWithOwner(employeeOrder.ID, 6, "employee"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected ErrRecordNotFound for wrong user_id, got: %v", err)
	}
}

// sampleHolding returns a minimally-valid Holding row for seeding tests.
func sampleHolding(userID uint64, systemType, securityType string, securityID uint64, accountID uint64, qty int64) *model.Holding {
	return &model.Holding{
		UserID:        userID,
		SystemType:    systemType,
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

func TestHoldingRepo_ListByUser_FiltersOnSystemType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewHoldingRepository(db)

	if err := db.Create(sampleHolding(5, "client", "stock", 100, 1, 10)).Error; err != nil {
		t.Fatalf("seed client holding: %v", err)
	}
	if err := db.Create(sampleHolding(5, "employee", "stock", 100, 2, 15)).Error; err != nil {
		t.Fatalf("seed employee holding: %v", err)
	}

	client, total, err := repo.ListByUser(5, "client", HoldingFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list client: %v", err)
	}
	if total != 1 {
		t.Errorf("expected 1 client holding, got %d", total)
	}
	if len(client) != 1 || client[0].AccountID != 1 {
		t.Errorf("wrong client holdings returned: %+v", client)
	}

	employee, totalE, err := repo.ListByUser(5, "employee", HoldingFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list employee: %v", err)
	}
	if totalE != 1 {
		t.Errorf("expected 1 employee holding, got %d", totalE)
	}
	if len(employee) != 1 || employee[0].AccountID != 2 {
		t.Errorf("wrong employee holdings returned: %+v", employee)
	}
}

func TestHoldingRepo_GetByUserAndSecurity_ChecksSystemType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewHoldingRepository(db)

	employeeH := sampleHolding(5, "employee", "stock", 100, 1, 10)
	if err := db.Create(employeeH).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Correct match.
	got, err := repo.GetByUserAndSecurity(5, "employee", "stock", 100, 1)
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if got.ID != employeeH.ID {
		t.Errorf("wrong holding returned: %+v", got)
	}

	// Wrong system_type: NotFound.
	if _, err := repo.GetByUserAndSecurity(5, "client", "stock", 100, 1); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected ErrRecordNotFound for cross-system access, got: %v", err)
	}
}

func TestHoldingRepo_FindOldestLongOptionHolding_ChecksSystemType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewHoldingRepository(db)

	// Only an employee holding exists for (user=5, option=200).
	if err := db.Create(sampleHolding(5, "employee", "option", 200, 1, 3)).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Lookup under the wrong system_type must return (nil, nil).
	h, err := repo.FindOldestLongOptionHolding(5, "client", 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h != nil {
		t.Errorf("expected nil holding for cross-system access, got: %+v", h)
	}

	// Correct system_type finds the holding.
	h, err = repo.FindOldestLongOptionHolding(5, "employee", 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h == nil {
		t.Fatal("expected employee holding to be found")
	}
}

func TestHoldingRepo_Upsert_SeparatesClientAndEmployee(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewHoldingRepository(db)

	// First Upsert: creates the employee-side holding.
	emp := sampleHolding(5, "employee", "stock", 100, 1, 10)
	if err := repo.Upsert(emp); err != nil {
		t.Fatalf("upsert employee: %v", err)
	}

	// Second Upsert with the same user_id / security / account but a
	// different system_type must create a fresh row rather than merging
	// into the employee row.
	client := sampleHolding(5, "client", "stock", 100, 1, 20)
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

	gotEmp, err := repo.GetByUserAndSecurity(5, "employee", "stock", 100, 1)
	if err != nil {
		t.Fatalf("get employee: %v", err)
	}
	if gotEmp.Quantity != 10 {
		t.Errorf("expected employee quantity unchanged at 10, got %d", gotEmp.Quantity)
	}
	gotClient, err := repo.GetByUserAndSecurity(5, "client", "stock", 100, 1)
	if err != nil {
		t.Fatalf("get client: %v", err)
	}
	if gotClient.Quantity != 20 {
		t.Errorf("expected client quantity 20, got %d", gotClient.Quantity)
	}
}

// sampleCapitalGain returns a minimally-valid CapitalGain row.
func sampleCapitalGain(userID uint64, systemType string, totalGain int64, year, month int) *model.CapitalGain {
	return &model.CapitalGain{
		UserID:           userID,
		SystemType:       systemType,
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

func TestCapitalGainRepo_ListByUser_FiltersOnSystemType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewCapitalGainRepository(db)

	if err := repo.Create(sampleCapitalGain(5, "client", 100, 2026, 4)); err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := repo.Create(sampleCapitalGain(5, "employee", 200, 2026, 4)); err != nil {
		t.Fatalf("create employee: %v", err)
	}

	records, total, err := repo.ListByUser(5, "client", 1, 10)
	if err != nil {
		t.Fatalf("list client: %v", err)
	}
	if total != 1 || len(records) != 1 {
		t.Errorf("expected 1 client record, got total=%d len=%d", total, len(records))
	}
	if len(records) == 1 && records[0].SystemType != "client" {
		t.Errorf("expected client record, got %s", records[0].SystemType)
	}

	records, total, err = repo.ListByUser(5, "employee", 1, 10)
	if err != nil {
		t.Fatalf("list employee: %v", err)
	}
	if total != 1 || len(records) != 1 {
		t.Errorf("expected 1 employee record, got total=%d len=%d", total, len(records))
	}
}

func TestCapitalGainRepo_SumByUserMonth_FiltersOnSystemType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewCapitalGainRepository(db)

	if err := repo.Create(sampleCapitalGain(5, "client", 100, 2026, 4)); err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := repo.Create(sampleCapitalGain(5, "employee", 200, 2026, 4)); err != nil {
		t.Fatalf("create employee: %v", err)
	}

	summaries, err := repo.SumByUserMonth(5, "client", 2026, 4)
	if err != nil {
		t.Fatalf("sum client: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if !summaries[0].TotalGain.Equal(decimal.NewFromInt(100)) {
		t.Errorf("expected total 100 for client, got %s", summaries[0].TotalGain)
	}

	summaries, err = repo.SumByUserMonth(5, "employee", 2026, 4)
	if err != nil {
		t.Fatalf("sum employee: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if !summaries[0].TotalGain.Equal(decimal.NewFromInt(200)) {
		t.Errorf("expected total 200 for employee, got %s", summaries[0].TotalGain)
	}
}

func TestCapitalGainRepo_SumByUserYear_FiltersOnSystemType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewCapitalGainRepository(db)

	if err := repo.Create(sampleCapitalGain(5, "client", 100, 2026, 1)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(sampleCapitalGain(5, "client", 50, 2026, 2)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(sampleCapitalGain(5, "employee", 999, 2026, 1)); err != nil {
		t.Fatal(err)
	}

	summaries, err := repo.SumByUserYear(5, "client", 2026)
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

func TestTaxCollectionRepo_SumByUser_FiltersOnSystemType(t *testing.T) {
	db := newSystemTypeScopingDB(t)
	repo := NewTaxCollectionRepository(db)

	tc1 := &model.TaxCollection{
		UserID: 5, SystemType: "client", Year: 2026, Month: 4,
		AccountID: 1, Currency: "RSD",
		TotalGain: decimal.NewFromInt(1000), TaxAmount: decimal.NewFromInt(150),
		TaxAmountRSD: decimal.NewFromInt(150),
	}
	tc2 := &model.TaxCollection{
		UserID: 5, SystemType: "employee", Year: 2026, Month: 4,
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

	clientTotal, err := repo.SumByUserMonth(5, "client", 2026, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !clientTotal.Equal(decimal.NewFromInt(150)) {
		t.Errorf("expected client sum 150, got %s", clientTotal)
	}

	employeeTotal, err := repo.SumByUserMonth(5, "employee", 2026, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !employeeTotal.Equal(decimal.NewFromInt(300)) {
		t.Errorf("expected employee sum 300, got %s", employeeTotal)
	}

	yearClient, err := repo.SumByUserYear(5, "client", 2026)
	if err != nil {
		t.Fatal(err)
	}
	if !yearClient.Equal(decimal.NewFromInt(150)) {
		t.Errorf("expected year client 150, got %s", yearClient)
	}
}
