package service

// dividend_service_test.go — E4.6 unit tests (Plan E, 2026-05-28)

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// ── Fake account client ────────────────────────────────────────────────────

type fakeDividendAccountClient struct {
	accounts  map[uint64]*accountpb.AccountResponse
	credits   []creditCall
	getErr    error
	creditErr error
}

type creditCall struct {
	AccountNumber string
	Amount        decimal.Decimal
	Memo          string
	Key           string
}

func newFakeDividendAccountClient() *fakeDividendAccountClient {
	return &fakeDividendAccountClient{
		accounts: make(map[uint64]*accountpb.AccountResponse),
	}
}

func (f *fakeDividendAccountClient) addAccount(id uint64, number string) {
	f.accounts[id] = &accountpb.AccountResponse{Id: id, AccountNumber: number}
}

func (f *fakeDividendAccountClient) GetAccount(_ context.Context, in *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if a, ok := f.accounts[in.Id]; ok {
		return a, nil
	}
	return nil, errors.New("account not found")
}

func (f *fakeDividendAccountClient) CreditAccount(_ context.Context, accountNumber string, amount decimal.Decimal, memo, idempotencyKey string) (*accountpb.AccountResponse, error) {
	if f.creditErr != nil {
		return nil, f.creditErr
	}
	f.credits = append(f.credits, creditCall{AccountNumber: accountNumber, Amount: amount, Memo: memo, Key: idempotencyKey})
	return &accountpb.AccountResponse{AccountNumber: accountNumber}, nil
}

func (f *fakeDividendAccountClient) DebitAccount(_ context.Context, accountNumber string, amount decimal.Decimal, memo, idempotencyKey string) (*accountpb.AccountResponse, error) {
	return &accountpb.AccountResponse{AccountNumber: accountNumber}, nil
}

// ── Helper: open in-memory SQLite DB and migrate all needed models ─────────

func openDividendTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.DividendPayment{},
		&model.DividendPayout{},
		&model.FundDividendPayment{},
		&model.Holding{},
		&model.FundHolding{},
		&model.InvestmentFund{},
		&model.ClientFundPosition{},
		&model.FundPositionSettlement{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// ── Helper: build a DividendService wired to in-memory DB ─────────────────

func newDividendService(db *gorm.DB, accounts FundAccountClient) *DividendService {
	return NewDividendService(
		db,
		repository.NewDividendPaymentRepository(db),
		repository.NewDividendPayoutRepository(db),
		repository.NewFundDividendPaymentRepository(db),
		repository.NewHoldingRepository(db),
		repository.NewFundHoldingRepository(db),
		repository.NewFundRepository(db),
		repository.NewClientFundPositionRepository(db),
		accounts,
	)
}

// ── Test: Declare is idempotent on (security_id, payment_date) ────────────

func TestDividendDeclare_Idempotent(t *testing.T) {
	db := openDividendTestDB(t)
	svc := newDividendService(db, newFakeDividendAccountClient())
	ctx := context.Background()

	date := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	first, err := svc.Declare(ctx, 42, "AAPL", decimal.NewFromFloat(50), date, 1)
	if err != nil {
		t.Fatalf("first declare: %v", err)
	}
	if first.ID == 0 {
		t.Fatal("expected non-zero ID")
	}
	if first.Status != "declared" {
		t.Errorf("expected status=declared, got %s", first.Status)
	}

	// Second declare with same (security_id, payment_date) should return existing.
	second, err := svc.Declare(ctx, 42, "AAPL", decimal.NewFromFloat(50), date, 2)
	if err != nil {
		t.Fatalf("second declare: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("expected same ID %d, got %d (idempotency violated)", first.ID, second.ID)
	}
}

// ── Test: Payout — 3 client holdings + 1 bank holding + 1 fund holding ───

func TestDividendPayout_MixedHolders(t *testing.T) {
	db := openDividendTestDB(t)
	accounts := newFakeDividendAccountClient()
	svc := newDividendService(db, accounts)
	ctx := context.Background()

	// Seed a fund (needed for fund holding payout).
	fund := &model.InvestmentFund{
		Name:              "TestFund",
		ManagerEmployeeID: 1,
		RSDAccountID:      99,
		FundType:          model.FundTypeOpen,
		FundStatus:        model.FundStatusOpen,
	}
	db.Create(fund)
	accounts.addAccount(99, "FUND-RSD")

	// Create 3 client holdings.
	clientHoldings := []model.Holding{
		{OwnerType: "client", OwnerID: ptr(uint64(1)), Ticker: "AAPL", SecurityType: "stock", SecurityID: 10, ListingID: 1, Quantity: 100, AccountID: 10, UserFirstName: "A", UserLastName: "B", AveragePrice: decimal.NewFromFloat(100)},
		{OwnerType: "client", OwnerID: ptr(uint64(2)), Ticker: "AAPL", SecurityType: "stock", SecurityID: 10, ListingID: 1, Quantity: 50, AccountID: 11, UserFirstName: "C", UserLastName: "D", AveragePrice: decimal.NewFromFloat(100)},
		{OwnerType: "client", OwnerID: ptr(uint64(3)), Ticker: "AAPL", SecurityType: "stock", SecurityID: 10, ListingID: 1, Quantity: 200, AccountID: 12, UserFirstName: "E", UserLastName: "F", AveragePrice: decimal.NewFromFloat(100)},
	}
	for i := range clientHoldings {
		db.Create(&clientHoldings[i])
		accounts.addAccount(clientHoldings[i].AccountID, "CLIENT-"+clientHoldings[i].Ticker+"-"+string(rune('1'+i)))
	}
	accounts.accounts[10] = &accountpb.AccountResponse{Id: 10, AccountNumber: "CLIENT-1"}
	accounts.accounts[11] = &accountpb.AccountResponse{Id: 11, AccountNumber: "CLIENT-2"}
	accounts.accounts[12] = &accountpb.AccountResponse{Id: 12, AccountNumber: "CLIENT-3"}

	// Create 1 bank holding.
	bankHolding := model.Holding{
		OwnerType:     "bank",
		OwnerID:       nil,
		Ticker:        "AAPL",
		SecurityType:  "stock",
		SecurityID:    10,
		ListingID:     1,
		Quantity:      500,
		AccountID:     20,
		UserFirstName: "Bank",
		UserLastName:  "",
		AveragePrice:  decimal.NewFromFloat(100),
	}
	db.Create(&bankHolding)
	accounts.addAccount(20, "BANK-RSD")

	// Create 1 fund holding.
	fundHolding := model.FundHolding{
		FundID:          fund.ID,
		SecurityType:    "stock",
		SecurityID:      10,
		Quantity:        300,
		AveragePriceRSD: decimal.NewFromFloat(100),
	}
	db.Create(&fundHolding)

	// Seed 2 fund investors (so the per-investor snapshot has 2 entries).
	investor1 := model.ClientFundPosition{FundID: fund.ID, OwnerType: "client", OwnerID: ptr(uint64(10)), TotalContributedRSD: decimal.NewFromFloat(6000)}
	investor2 := model.ClientFundPosition{FundID: fund.ID, OwnerType: "client", OwnerID: ptr(uint64(11)), TotalContributedRSD: decimal.NewFromFloat(4000)}
	db.Create(&investor1)
	db.Create(&investor2)

	// Declare + payout.
	date := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	amtPerShare := decimal.NewFromFloat(10) // 10 RSD/share
	payment, err := svc.Declare(ctx, 10, "AAPL", amtPerShare, date, 99)
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	summary, err := svc.Payout(ctx, payment.ID)
	if err != nil {
		t.Fatalf("payout: %v", err)
	}

	// 3 client direct payouts + 1 bank direct payout = 4 direct payouts.
	if summary.PayoutsCreated != 4 {
		t.Errorf("expected 4 direct payouts, got %d", summary.PayoutsCreated)
	}
	// 1 fund payout.
	if summary.FundPayouts != 1 {
		t.Errorf("expected 1 fund payout, got %d", summary.FundPayouts)
	}

	// Verify client tax = 15%, net = 85%.
	var clientPayouts []model.DividendPayout
	db.Where("holding_owner_type = 'client'").Find(&clientPayouts)
	if len(clientPayouts) != 3 {
		t.Fatalf("expected 3 client payouts, got %d", len(clientPayouts))
	}
	for _, p := range clientPayouts {
		expectedGross := amtPerShare.Mul(decimal.NewFromInt(p.Shares))
		expectedTax := expectedGross.Mul(decimal.NewFromFloat(0.15)).Round(4)
		expectedNet := expectedGross.Sub(expectedTax)
		if !p.TaxAmountRSD.Equal(expectedTax) {
			t.Errorf("client payout %d: expected tax %s, got %s", p.ID, expectedTax, p.TaxAmountRSD)
		}
		if !p.NetAmountRSD.Equal(expectedNet) {
			t.Errorf("client payout %d: expected net %s, got %s", p.ID, expectedNet, p.NetAmountRSD)
		}
	}

	// Verify bank payout has no tax.
	var bankPayouts []model.DividendPayout
	db.Where("holding_owner_type = 'bank'").Find(&bankPayouts)
	if len(bankPayouts) != 1 {
		t.Fatalf("expected 1 bank payout, got %d", len(bankPayouts))
	}
	bankPayout := bankPayouts[0]
	expectedBankGross := amtPerShare.Mul(decimal.NewFromInt(500))
	if !bankPayout.TaxAmountRSD.IsZero() {
		t.Errorf("bank payout should have zero tax, got %s", bankPayout.TaxAmountRSD)
	}
	if !bankPayout.GrossAmountRSD.Equal(expectedBankGross) {
		t.Errorf("bank payout gross: expected %s, got %s", expectedBankGross, bankPayout.GrossAmountRSD)
	}

	// Verify fund payout.
	var fundPayouts []model.DividendPayout
	db.Where("holding_owner_type = 'investment_fund'").Find(&fundPayouts)
	if len(fundPayouts) != 1 {
		t.Fatalf("expected 1 fund payout, got %d", len(fundPayouts))
	}
	expectedFundGross := amtPerShare.Mul(decimal.NewFromInt(300))
	if !fundPayouts[0].TaxAmountRSD.IsZero() {
		t.Errorf("fund payout should have zero tax, got %s", fundPayouts[0].TaxAmountRSD)
	}
	if !fundPayouts[0].GrossAmountRSD.Equal(expectedFundGross) {
		t.Errorf("fund payout gross: expected %s, got %s", expectedFundGross, fundPayouts[0].GrossAmountRSD)
	}

	// Verify FundDividendPayment snapshot was created.
	var fdps []model.FundDividendPayment
	db.Where("fund_id = ?", fund.ID).Find(&fdps)
	if len(fdps) != 1 {
		t.Fatalf("expected 1 fund_dividend_payment, got %d", len(fdps))
	}
	if fdps[0].PerInvestorSnapshot == "" || fdps[0].PerInvestorSnapshot == "null" {
		t.Error("per_investor_snapshot should not be empty")
	}

	// Verify payment is marked paid_out.
	p2, err := svc.GetPayment(payment.ID)
	if err != nil {
		t.Fatalf("get payment: %v", err)
	}
	if p2.Status != "paid_out" {
		t.Errorf("expected status=paid_out, got %s", p2.Status)
	}
}

// ── Test: Payout is idempotent (double call doesn't double-credit) ─────────

func TestDividendPayout_Idempotent(t *testing.T) {
	db := openDividendTestDB(t)
	accounts := newFakeDividendAccountClient()
	svc := newDividendService(db, accounts)
	ctx := context.Background()

	// One client holding with account.
	h := model.Holding{
		OwnerType:     "client",
		OwnerID:       ptr(uint64(1)),
		Ticker:        "AAPL",
		SecurityType:  "stock",
		SecurityID:    10,
		ListingID:     1,
		Quantity:      100,
		AccountID:     5,
		UserFirstName: "X",
		UserLastName:  "Y",
		AveragePrice:  decimal.NewFromFloat(100),
	}
	db.Create(&h)
	accounts.addAccount(5, "ACCT-001")

	date := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	payment, err := svc.Declare(ctx, 10, "AAPL", decimal.NewFromFloat(10), date, 1)
	if err != nil {
		t.Fatalf("declare: %v", err)
	}

	// First payout.
	_, err = svc.Payout(ctx, payment.ID)
	if err != nil {
		t.Fatalf("first payout: %v", err)
	}
	firstCreditCount := len(accounts.credits)

	// Second payout attempt on an already-paid-out payment should error.
	_, err = svc.Payout(ctx, payment.ID)
	if err == nil {
		t.Error("expected error on second payout of already paid-out payment")
	}

	// Credit should not have been called again.
	if len(accounts.credits) != firstCreditCount {
		t.Errorf("expected %d credit calls, got %d (double-payout detected)", firstCreditCount, len(accounts.credits))
	}
}

// ── Test: SumDividendsByFund returns correct total ─────────────────────────

func TestDividendSumByFund(t *testing.T) {
	db := openDividendTestDB(t)
	svc := newDividendService(db, newFakeDividendAccountClient())

	// Manually insert two fund_dividend_payments.
	db.Create(&model.FundDividendPayment{DividendPaymentID: 1, FundID: 7, AmountRSD: decimal.NewFromFloat(500), PerInvestorSnapshot: "[]"})
	db.Create(&model.FundDividendPayment{DividendPaymentID: 2, FundID: 7, AmountRSD: decimal.NewFromFloat(300), PerInvestorSnapshot: "[]"})
	db.Create(&model.FundDividendPayment{DividendPaymentID: 3, FundID: 8, AmountRSD: decimal.NewFromFloat(999), PerInvestorSnapshot: "[]"})

	total, err := svc.SumDividendsByFund(7)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if !total.Equal(decimal.NewFromFloat(800)) {
		t.Errorf("expected 800, got %s", total)
	}
}

// ptr is a convenience helper for creating *uint64 values in tests.
func ptr(v uint64) *uint64 { return &v }
