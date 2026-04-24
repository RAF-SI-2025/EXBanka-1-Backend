package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// ---------------------------------------------------------------------------
// Mock: TaxCollectionRepo
// ---------------------------------------------------------------------------

type mockTaxCollectionRepo struct {
	collections    []model.TaxCollection
	nextID         uint64
	usersWithGains []repository.TaxUserSummary
}

func newMockTaxCollectionRepo() *mockTaxCollectionRepo {
	return &mockTaxCollectionRepo{nextID: 1}
}

func (m *mockTaxCollectionRepo) Create(collection *model.TaxCollection) error {
	collection.ID = m.nextID
	m.nextID++
	m.collections = append(m.collections, *collection)
	return nil
}

func (m *mockTaxCollectionRepo) SumByUserYear(userID uint64, year int) (decimal.Decimal, error) {
	total := decimal.Zero
	for _, c := range m.collections {
		if c.UserID == userID && c.Year == year {
			total = total.Add(c.TaxAmountRSD)
		}
	}
	return total, nil
}

func (m *mockTaxCollectionRepo) SumByUserMonth(userID uint64, year, month int) (decimal.Decimal, error) {
	total := decimal.Zero
	for _, c := range m.collections {
		if c.UserID == userID && c.Year == year && c.Month == month {
			total = total.Add(c.TaxAmountRSD)
		}
	}
	return total, nil
}

func (m *mockTaxCollectionRepo) GetLastCollection(userID uint64) (*model.TaxCollection, error) {
	var last *model.TaxCollection
	for i := range m.collections {
		if m.collections[i].UserID == userID {
			last = &m.collections[i]
		}
	}
	return last, nil
}

func (m *mockTaxCollectionRepo) ListUsersWithGains(year, month int, filter repository.TaxFilter) ([]repository.TaxUserSummary, int64, error) {
	return m.usersWithGains, int64(len(m.usersWithGains)), nil
}

// ---------------------------------------------------------------------------
// Mock: CapitalGainRepo (enhanced for tax tests)
// ---------------------------------------------------------------------------

type mockTaxCapitalGainRepo struct {
	gains  []model.CapitalGain
	nextID uint64
}

func newMockTaxCapitalGainRepo() *mockTaxCapitalGainRepo {
	return &mockTaxCapitalGainRepo{nextID: 1}
}

func (m *mockTaxCapitalGainRepo) Create(gain *model.CapitalGain) error {
	gain.ID = m.nextID
	m.nextID++
	m.gains = append(m.gains, *gain)
	return nil
}

func (m *mockTaxCapitalGainRepo) ListByUser(userID uint64, page, pageSize int) ([]model.CapitalGain, int64, error) {
	var result []model.CapitalGain
	for _, g := range m.gains {
		if g.UserID == userID {
			result = append(result, g)
		}
	}
	total := int64(len(result))
	start := (page - 1) * pageSize
	if start >= len(result) {
		return nil, total, nil
	}
	end := start + pageSize
	if end > len(result) {
		end = len(result)
	}
	return result[start:end], total, nil
}

func (m *mockTaxCapitalGainRepo) SumByUserMonth(userID uint64, year, month int) ([]repository.AccountGainSummary, error) {
	type key struct {
		AccountID uint64
		Currency  string
	}
	agg := make(map[key]decimal.Decimal)
	for _, g := range m.gains {
		if g.UserID == userID && g.TaxYear == year && g.TaxMonth == month {
			k := key{AccountID: g.AccountID, Currency: g.Currency}
			agg[k] = agg[k].Add(g.TotalGain)
		}
	}
	var result []repository.AccountGainSummary
	for k, v := range agg {
		result = append(result, repository.AccountGainSummary{
			AccountID: k.AccountID,
			Currency:  k.Currency,
			TotalGain: v,
		})
	}
	return result, nil
}

func (m *mockTaxCapitalGainRepo) SumByUserYear(userID uint64, year int) ([]repository.AccountGainSummary, error) {
	type key struct {
		AccountID uint64
		Currency  string
	}
	agg := make(map[key]decimal.Decimal)
	for _, g := range m.gains {
		if g.UserID == userID && g.TaxYear == year {
			k := key{AccountID: g.AccountID, Currency: g.Currency}
			agg[k] = agg[k].Add(g.TotalGain)
		}
	}
	var result []repository.AccountGainSummary
	for k, v := range agg {
		result = append(result, repository.AccountGainSummary{
			AccountID: k.AccountID,
			Currency:  k.Currency,
			TotalGain: v,
		})
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Mock: ExchangeServiceClient
// ---------------------------------------------------------------------------

type mockExchangeClient struct {
	convertRate map[string]decimal.Decimal
	convertErr  error
}

func newMockExchangeClient() *mockExchangeClient {
	return &mockExchangeClient{convertRate: make(map[string]decimal.Decimal)}
}

func (m *mockExchangeClient) Convert(_ context.Context, req *exchangepb.ConvertRequest, _ ...grpc.CallOption) (*exchangepb.ConvertResponse, error) {
	if m.convertErr != nil {
		return nil, m.convertErr
	}
	key := req.FromCurrency + "/" + req.ToCurrency
	rate, ok := m.convertRate[key]
	if !ok {
		rate = decimal.NewFromInt(1)
	}
	amount, _ := decimal.NewFromString(req.Amount)
	converted := amount.Mul(rate)
	return &exchangepb.ConvertResponse{
		ConvertedAmount: converted.StringFixed(4),
	}, nil
}

func (m *mockExchangeClient) ListRates(_ context.Context, _ *exchangepb.ListRatesRequest, _ ...grpc.CallOption) (*exchangepb.ListRatesResponse, error) {
	return nil, nil
}

func (m *mockExchangeClient) GetRate(_ context.Context, _ *exchangepb.GetRateRequest, _ ...grpc.CallOption) (*exchangepb.RateResponse, error) {
	return nil, nil
}

func (m *mockExchangeClient) Calculate(_ context.Context, _ *exchangepb.CalculateRequest, _ ...grpc.CallOption) (*exchangepb.CalculateResponse, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Mock: AccountServiceClient (for TaxService)
// ---------------------------------------------------------------------------

type mockTaxAccountClient struct {
	accounts       map[uint64]*accountpb.AccountResponse
	updateBalErr   error
	getAccountErr  error
	updateBalCalls []taxUpdateBalCall
}

type taxUpdateBalCall struct {
	AccountNumber string
	Amount        string
}

func newMockTaxAccountClient() *mockTaxAccountClient {
	return &mockTaxAccountClient{accounts: make(map[uint64]*accountpb.AccountResponse)}
}

func (m *mockTaxAccountClient) addAccount(id uint64, accountNumber string) {
	m.accounts[id] = &accountpb.AccountResponse{
		Id:            id,
		AccountNumber: accountNumber,
	}
}

func (m *mockTaxAccountClient) GetAccount(_ context.Context, req *accountpb.GetAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if m.getAccountErr != nil {
		return nil, m.getAccountErr
	}
	resp, ok := m.accounts[req.Id]
	if !ok {
		return nil, context.DeadlineExceeded
	}
	return resp, nil
}

func (m *mockTaxAccountClient) UpdateBalance(_ context.Context, req *accountpb.UpdateBalanceRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if m.updateBalErr != nil {
		return nil, m.updateBalErr
	}
	m.updateBalCalls = append(m.updateBalCalls, taxUpdateBalCall{
		AccountNumber: req.AccountNumber,
		Amount:        req.Amount,
	})
	return &accountpb.AccountResponse{}, nil
}

func (m *mockTaxAccountClient) CreateAccount(context.Context, *accountpb.CreateAccountRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) GetAccountByNumber(context.Context, *accountpb.GetAccountByNumberRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) ListAccountsByClient(context.Context, *accountpb.ListAccountsByClientRequest, ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) ListAllAccounts(context.Context, *accountpb.ListAllAccountsRequest, ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) UpdateAccountName(context.Context, *accountpb.UpdateAccountNameRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) UpdateAccountLimits(context.Context, *accountpb.UpdateAccountLimitsRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) UpdateAccountStatus(context.Context, *accountpb.UpdateAccountStatusRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) CreateCompany(context.Context, *accountpb.CreateCompanyRequest, ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) GetCompany(context.Context, *accountpb.GetCompanyRequest, ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) UpdateCompany(context.Context, *accountpb.UpdateCompanyRequest, ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) ListCurrencies(context.Context, *accountpb.ListCurrenciesRequest, ...grpc.CallOption) (*accountpb.ListCurrenciesResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) GetCurrency(context.Context, *accountpb.GetCurrencyRequest, ...grpc.CallOption) (*accountpb.CurrencyResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) GetLedgerEntries(context.Context, *accountpb.GetLedgerEntriesRequest, ...grpc.CallOption) (*accountpb.GetLedgerEntriesResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) ReserveFunds(context.Context, *accountpb.ReserveFundsRequest, ...grpc.CallOption) (*accountpb.ReserveFundsResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) ReleaseReservation(context.Context, *accountpb.ReleaseReservationRequest, ...grpc.CallOption) (*accountpb.ReleaseReservationResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) PartialSettleReservation(context.Context, *accountpb.PartialSettleReservationRequest, ...grpc.CallOption) (*accountpb.PartialSettleReservationResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) GetReservation(context.Context, *accountpb.GetReservationRequest, ...grpc.CallOption) (*accountpb.GetReservationResponse, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Helper: build TaxService with mocks
// ---------------------------------------------------------------------------

type taxMocks struct {
	capitalGainRepo   *mockTaxCapitalGainRepo
	taxCollectionRepo *mockTaxCollectionRepo
	holdingRepo       *mockHoldingRepo
	accountClient     *mockTaxAccountClient
	exchangeClient    *mockExchangeClient
}

func buildTaxService() (*TaxService, *taxMocks) {
	mocks := &taxMocks{
		capitalGainRepo:   newMockTaxCapitalGainRepo(),
		taxCollectionRepo: newMockTaxCollectionRepo(),
		holdingRepo:       newMockHoldingRepo(),
		accountClient:     newMockTaxAccountClient(),
		exchangeClient:    newMockExchangeClient(),
	}

	svc := NewTaxService(
		mocks.capitalGainRepo,
		mocks.taxCollectionRepo,
		mocks.holdingRepo,
		mocks.accountClient,
		mocks.exchangeClient,
		"STATE-RSD-001",
	)

	return svc, mocks
}

// ---------------------------------------------------------------------------
// Tests: GetUserTaxSummary — positive capital gain => 15% tax liability
// ---------------------------------------------------------------------------

func TestTaxService_GetUserTaxSummary_PositiveGain(t *testing.T) {
	svc, mocks := buildTaxService()

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// Record a positive capital gain of 1000 RSD for user 42
	_ = mocks.capitalGainRepo.Create(&model.CapitalGain{
		UserID:           42,
		SystemType:       "employee",
		SecurityType:     "stock",
		Ticker:           "AAPL",
		Quantity:         10,
		BuyPricePerUnit:  decimal.NewFromInt(100),
		SellPricePerUnit: decimal.NewFromInt(200),
		TotalGain:        decimal.NewFromInt(1000),
		Currency:         "RSD",
		AccountID:        1,
		TaxYear:          year,
		TaxMonth:         month,
	})

	paidThisYear, unpaidThisMonth, err := svc.GetUserTaxSummary(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No collections yet => paid this year = 0
	if !paidThisYear.IsZero() {
		t.Errorf("expected taxPaidThisYear 0, got %s", paidThisYear)
	}

	// Tax = 1000 * 0.15 = 150 RSD, nothing collected yet
	expectedUnpaid := decimal.NewFromInt(150)
	if !unpaidThisMonth.Equal(expectedUnpaid) {
		t.Errorf("expected taxUnpaidThisMonth %s, got %s", expectedUnpaid, unpaidThisMonth)
	}
}

// ---------------------------------------------------------------------------
// Tests: GetUserTaxSummary — negative capital gain => no tax
// ---------------------------------------------------------------------------

func TestTaxService_GetUserTaxSummary_NegativeGain(t *testing.T) {
	svc, mocks := buildTaxService()

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// Record a loss of -500 RSD
	_ = mocks.capitalGainRepo.Create(&model.CapitalGain{
		UserID:           42,
		SystemType:       "employee",
		SecurityType:     "stock",
		Ticker:           "AAPL",
		Quantity:         5,
		BuyPricePerUnit:  decimal.NewFromInt(200),
		SellPricePerUnit: decimal.NewFromInt(100),
		TotalGain:        decimal.NewFromInt(-500),
		Currency:         "RSD",
		AccountID:        1,
		TaxYear:          year,
		TaxMonth:         month,
	})

	paidThisYear, unpaidThisMonth, err := svc.GetUserTaxSummary(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !paidThisYear.IsZero() {
		t.Errorf("expected taxPaidThisYear 0, got %s", paidThisYear)
	}
	if !unpaidThisMonth.IsZero() {
		t.Errorf("expected taxUnpaidThisMonth 0 for negative gain, got %s", unpaidThisMonth)
	}
}

// ---------------------------------------------------------------------------
// Tests: ListTaxRecords delegates to repo with year/month
// ---------------------------------------------------------------------------

func TestTaxService_ListTaxRecords(t *testing.T) {
	svc, mocks := buildTaxService()

	mocks.taxCollectionRepo.usersWithGains = []repository.TaxUserSummary{
		{UserID: 1, SystemType: "employee", TotalDebtRSD: decimal.NewFromInt(100)},
		{UserID: 2, SystemType: "client", TotalDebtRSD: decimal.NewFromInt(200)},
	}

	summaries, total, err := svc.ListTaxRecords(2026, 4, TaxFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if total != 2 {
		t.Errorf("expected total 2, got %d", total)
	}
	if len(summaries) != 2 {
		t.Errorf("expected 2 summaries, got %d", len(summaries))
	}
}

// ---------------------------------------------------------------------------
// Tests: ListUserTaxRecords returns only that user's records
// ---------------------------------------------------------------------------

func TestTaxService_ListUserTaxRecords_OnlyUserRecords(t *testing.T) {
	svc, mocks := buildTaxService()

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// User 42 gains
	_ = mocks.capitalGainRepo.Create(&model.CapitalGain{
		UserID: 42, SystemType: "employee", Ticker: "AAPL", Quantity: 10,
		BuyPricePerUnit: decimal.NewFromInt(100), SellPricePerUnit: decimal.NewFromInt(150),
		TotalGain: decimal.NewFromInt(500), Currency: "RSD", AccountID: 1,
		TaxYear: year, TaxMonth: month,
	})
	_ = mocks.capitalGainRepo.Create(&model.CapitalGain{
		UserID: 42, SystemType: "employee", Ticker: "GOOG", Quantity: 5,
		BuyPricePerUnit: decimal.NewFromInt(200), SellPricePerUnit: decimal.NewFromInt(300),
		TotalGain: decimal.NewFromInt(500), Currency: "RSD", AccountID: 1,
		TaxYear: year, TaxMonth: month,
	})
	// User 99 gain (should not appear)
	_ = mocks.capitalGainRepo.Create(&model.CapitalGain{
		UserID: 99, SystemType: "client", Ticker: "MSFT", Quantity: 3,
		BuyPricePerUnit: decimal.NewFromInt(300), SellPricePerUnit: decimal.NewFromInt(400),
		TotalGain: decimal.NewFromInt(300), Currency: "RSD", AccountID: 2,
		TaxYear: year, TaxMonth: month,
	})

	records, total, err := svc.ListUserTaxRecords(42, 1, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if total != 2 {
		t.Errorf("expected total 2, got %d", total)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 records, got %d", len(records))
	}
	for _, r := range records {
		if r.UserID != 42 {
			t.Errorf("expected all records for user 42, got user %d", r.UserID)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: GetUserTaxSummary with foreign currency conversion
// ---------------------------------------------------------------------------

func TestTaxService_GetUserTaxSummary_ForeignCurrency(t *testing.T) {
	svc, mocks := buildTaxService()

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// 1 USD = 117 RSD
	mocks.exchangeClient.convertRate["USD/RSD"] = decimal.NewFromInt(117)

	// Record a positive capital gain of 100 USD
	_ = mocks.capitalGainRepo.Create(&model.CapitalGain{
		UserID:           42,
		SystemType:       "employee",
		SecurityType:     "stock",
		Ticker:           "AAPL",
		Quantity:         10,
		BuyPricePerUnit:  decimal.NewFromInt(50),
		SellPricePerUnit: decimal.NewFromInt(60),
		TotalGain:        decimal.NewFromInt(100),
		Currency:         "USD",
		AccountID:        1,
		TaxYear:          year,
		TaxMonth:         month,
	})

	_, unpaidThisMonth, err := svc.GetUserTaxSummary(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Tax = 100 * 0.15 = 15 USD => 15 * 117 = 1755 RSD
	expectedUnpaid := decimal.NewFromInt(1755)
	if !unpaidThisMonth.Equal(expectedUnpaid) {
		t.Errorf("expected taxUnpaidThisMonth %s, got %s", expectedUnpaid, unpaidThisMonth)
	}
}

// ---------------------------------------------------------------------------
// Tests: CollectTax — sums unpaid gains, calculates 15%, debits + credits
// ---------------------------------------------------------------------------

func TestTaxService_CollectTax_PositiveGain(t *testing.T) {
	svc, mocks := buildTaxService()

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	mocks.accountClient.addAccount(1, "USER-ACCT-001")

	// Record a positive capital gain of 2000 RSD
	_ = mocks.capitalGainRepo.Create(&model.CapitalGain{
		UserID:           42,
		SystemType:       "employee",
		SecurityType:     "stock",
		Ticker:           "AAPL",
		Quantity:         20,
		BuyPricePerUnit:  decimal.NewFromInt(100),
		SellPricePerUnit: decimal.NewFromInt(200),
		TotalGain:        decimal.NewFromInt(2000),
		Currency:         "RSD",
		AccountID:        1,
		TaxYear:          year,
		TaxMonth:         month,
	})

	// ListUsersWithGains returns user 42 with debt
	mocks.taxCollectionRepo.usersWithGains = []repository.TaxUserSummary{
		{UserID: 42, SystemType: "employee", TotalDebtRSD: decimal.NewFromInt(300)},
	}

	collectedCount, totalRSD, failedCount, err := svc.CollectTax(year, month)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if failedCount != 0 {
		t.Errorf("expected 0 failures, got %d", failedCount)
	}
	if collectedCount != 1 {
		t.Errorf("expected 1 collected, got %d", collectedCount)
	}

	// Tax = 2000 * 0.15 = 300 RSD
	expectedTax := decimal.NewFromInt(300)
	if !totalRSD.Equal(expectedTax) {
		t.Errorf("expected totalRSD %s, got %s", expectedTax, totalRSD)
	}

	// Verify debit from user account
	if len(mocks.accountClient.updateBalCalls) < 1 {
		t.Fatal("expected at least 1 UpdateBalance call")
	}
	debitCall := mocks.accountClient.updateBalCalls[0]
	if debitCall.AccountNumber != "USER-ACCT-001" {
		t.Errorf("expected debit from USER-ACCT-001, got %s", debitCall.AccountNumber)
	}
	debitAmount, _ := decimal.NewFromString(debitCall.Amount)
	if !debitAmount.Equal(decimal.NewFromInt(-300).Round(4)) {
		t.Errorf("expected debit -300, got %s", debitAmount)
	}

	// Verify credit to state account
	if len(mocks.accountClient.updateBalCalls) < 2 {
		t.Fatal("expected 2 UpdateBalance calls")
	}
	creditCall := mocks.accountClient.updateBalCalls[1]
	if creditCall.AccountNumber != "STATE-RSD-001" {
		t.Errorf("expected credit to STATE-RSD-001, got %s", creditCall.AccountNumber)
	}

	// Verify TaxCollection record created
	if len(mocks.taxCollectionRepo.collections) != 1 {
		t.Fatalf("expected 1 tax collection, got %d", len(mocks.taxCollectionRepo.collections))
	}
	tc := mocks.taxCollectionRepo.collections[0]
	if tc.UserID != 42 {
		t.Errorf("expected collection for user 42, got %d", tc.UserID)
	}
	if !tc.TaxAmountRSD.Equal(expectedTax) {
		t.Errorf("expected TaxAmountRSD %s, got %s", expectedTax, tc.TaxAmountRSD)
	}
}
