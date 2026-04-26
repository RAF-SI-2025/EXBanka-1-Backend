package service

import (
	"context"
	"errors"
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

func (m *mockTaxCollectionRepo) SumByUserYear(userID uint64, systemType string, year int) (decimal.Decimal, error) {
	total := decimal.Zero
	for _, c := range m.collections {
		if c.UserID == userID && c.SystemType == systemType && c.Year == year {
			total = total.Add(c.TaxAmountRSD)
		}
	}
	return total, nil
}

func (m *mockTaxCollectionRepo) SumByUserMonth(userID uint64, systemType string, year, month int) (decimal.Decimal, error) {
	total := decimal.Zero
	for _, c := range m.collections {
		if c.UserID == userID && c.SystemType == systemType && c.Year == year && c.Month == month {
			total = total.Add(c.TaxAmountRSD)
		}
	}
	return total, nil
}

func (m *mockTaxCollectionRepo) GetLastCollection(userID uint64, systemType string) (*model.TaxCollection, error) {
	var last *model.TaxCollection
	for i := range m.collections {
		if m.collections[i].UserID == userID && m.collections[i].SystemType == systemType {
			last = &m.collections[i]
		}
	}
	return last, nil
}

func (m *mockTaxCollectionRepo) SumByUserAllTime(userID uint64, systemType string) (decimal.Decimal, error) {
	total := decimal.Zero
	for _, c := range m.collections {
		if c.UserID == userID && c.SystemType == systemType {
			total = total.Add(c.TaxAmountRSD)
		}
	}
	return total, nil
}

func (m *mockTaxCollectionRepo) ListByUser(userID uint64, systemType string, page, pageSize int) ([]model.TaxCollection, int64, error) {
	out := []model.TaxCollection{}
	for i := range m.collections {
		if m.collections[i].UserID == userID && m.collections[i].SystemType == systemType {
			out = append(out, m.collections[i])
		}
	}
	return out, int64(len(out)), nil
}

func (m *mockTaxCollectionRepo) CountByKey(userID uint64, systemType string, year, month int, accountID uint64, currency string) (int64, error) {
	var count int64
	for _, c := range m.collections {
		if c.UserID == userID && c.SystemType == systemType && c.Year == year && c.Month == month && c.AccountID == accountID && c.Currency == currency {
			count++
		}
	}
	return count, nil
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

func (m *mockTaxCapitalGainRepo) ListByUser(userID uint64, systemType string, page, pageSize int) ([]model.CapitalGain, int64, error) {
	var result []model.CapitalGain
	for _, g := range m.gains {
		if g.UserID == userID && g.SystemType == systemType {
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

func (m *mockTaxCapitalGainRepo) SumByUserMonth(userID uint64, systemType string, year, month int) ([]repository.AccountGainSummary, error) {
	type key struct {
		AccountID uint64
		Currency  string
	}
	agg := make(map[key]decimal.Decimal)
	for _, g := range m.gains {
		if g.UserID == userID && g.SystemType == systemType && g.TaxYear == year && g.TaxMonth == month {
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

func (m *mockTaxCapitalGainRepo) SumUncollectedByUserMonth(userID uint64, systemType string, year, month int) ([]repository.AccountGainSummary, error) {
	type key struct {
		AccountID uint64
		Currency  string
	}
	agg := make(map[key]decimal.Decimal)
	for _, g := range m.gains {
		if g.UserID == userID && g.SystemType == systemType && g.TaxYear == year && g.TaxMonth == month && g.TaxCollectionID == nil {
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

func (m *mockTaxCapitalGainRepo) MarkCollected(userID uint64, systemType string, year, month int, accountID uint64, currency string, taxCollectionID uint64) error {
	for i := range m.gains {
		g := &m.gains[i]
		if g.UserID == userID && g.SystemType == systemType && g.TaxYear == year && g.TaxMonth == month && g.AccountID == accountID && g.Currency == currency && g.TaxCollectionID == nil {
			id := taxCollectionID
			g.TaxCollectionID = &id
		}
	}
	return nil
}

func (m *mockTaxCapitalGainRepo) SumByUserYear(userID uint64, systemType string, year int) ([]repository.AccountGainSummary, error) {
	type key struct {
		AccountID uint64
		Currency  string
	}
	agg := make(map[key]decimal.Decimal)
	for _, g := range m.gains {
		if g.UserID == userID && g.SystemType == systemType && g.TaxYear == year {
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

func (m *mockTaxCapitalGainRepo) SumByUserAllTime(userID uint64, systemType string) ([]repository.AccountGainSummary, error) {
	type key struct {
		AccountID uint64
		Currency  string
	}
	agg := make(map[key]decimal.Decimal)
	for _, g := range m.gains {
		if g.UserID == userID && g.SystemType == systemType {
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

func (m *mockTaxCapitalGainRepo) CountByUserYear(userID uint64, systemType string, year int) (int64, error) {
	var count int64
	for _, g := range m.gains {
		if g.UserID == userID && g.SystemType == systemType && g.TaxYear == year {
			count++
		}
	}
	return count, nil
}

// ---------------------------------------------------------------------------
// Mock: ExchangeServiceClient
// ---------------------------------------------------------------------------

type mockExchangeClient struct {
	convertRate map[string]decimal.Decimal
	convertErr  error
	// convertCalls records every Convert call for assertions in fill-saga tests.
	convertCalls []struct {
		From   string
		To     string
		Amount string
	}
}

func newMockExchangeClient() *mockExchangeClient {
	return &mockExchangeClient{convertRate: make(map[string]decimal.Decimal)}
}

// setRate is used by the fill-saga tests to configure a USD→RSD (or similar) rate.
func (m *mockExchangeClient) setRate(from, to string, rate decimal.Decimal) {
	m.convertRate[from+"/"+to] = rate
}

func (m *mockExchangeClient) Convert(_ context.Context, req *exchangepb.ConvertRequest, _ ...grpc.CallOption) (*exchangepb.ConvertResponse, error) {
	if m.convertErr != nil {
		return nil, m.convertErr
	}
	m.convertCalls = append(m.convertCalls, struct {
		From   string
		To     string
		Amount string
	}{req.FromCurrency, req.ToCurrency, req.Amount})
	key := req.FromCurrency + "/" + req.ToCurrency
	rate, ok := m.convertRate[key]
	if !ok {
		rate = decimal.NewFromInt(1)
	}
	amount, _ := decimal.NewFromString(req.Amount)
	converted := amount.Mul(rate)
	return &exchangepb.ConvertResponse{
		ConvertedAmount: converted.StringFixed(4),
		EffectiveRate:   rate.String(),
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
	accounts            map[uint64]*accountpb.AccountResponse
	updateBalErr        error
	failOnAccountNumber string // when non-empty, UpdateBalance returns an error for this account number specifically
	getAccountErr       error
	updateBalCalls      []taxUpdateBalCall
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
	// Record the call BEFORE the targeted failure check so tests can assert
	// that the failing leg was actually attempted (and that the dedup retry
	// reuses the same idempotency key).
	m.updateBalCalls = append(m.updateBalCalls, taxUpdateBalCall{
		AccountNumber: req.AccountNumber,
		Amount:        req.Amount,
	})
	if m.failOnAccountNumber != "" && req.AccountNumber == m.failOnAccountNumber {
		return nil, errors.New("simulated UpdateBalance failure for " + req.AccountNumber)
	}
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
func (m *mockTaxAccountClient) ReserveIncoming(context.Context, *accountpb.ReserveIncomingRequest, ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) CommitIncoming(context.Context, *accountpb.CommitIncomingRequest, ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error) {
	return nil, nil
}
func (m *mockTaxAccountClient) ReleaseIncoming(context.Context, *accountpb.ReleaseIncomingRequest, ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
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

	paidThisYear, unpaidThisMonth, err := svc.GetUserTaxSummary(42, "employee")
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

	paidThisYear, unpaidThisMonth, err := svc.GetUserTaxSummary(42, "employee")
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

	records, total, err := svc.ListUserTaxRecords(42, "employee", 1, 10)
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

	_, unpaidThisMonth, err := svc.GetUserTaxSummary(42, "employee")
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

	// The capital-gain row must be stamped with the new collection ID so that
	// a subsequent CollectTax call in the same month doesn't re-tax it.
	if len(mocks.capitalGainRepo.gains) != 1 {
		t.Fatalf("expected 1 capital_gain row in mock, got %d", len(mocks.capitalGainRepo.gains))
	}
	if mocks.capitalGainRepo.gains[0].TaxCollectionID == nil {
		t.Error("expected capital_gain.tax_collection_id to be set after collection")
	} else if *mocks.capitalGainRepo.gains[0].TaxCollectionID != tc.ID {
		t.Errorf("expected capital_gain.tax_collection_id=%d, got %d", tc.ID, *mocks.capitalGainRepo.gains[0].TaxCollectionID)
	}
}

// ---------------------------------------------------------------------------
// CollectTax — incremental: a second call in the same month taxes only the
// NEW profit realised since the first call, not the original profit again.
// ---------------------------------------------------------------------------

func TestTaxService_CollectTax_IncrementalNewProfit(t *testing.T) {
	svc, mocks := buildTaxService()

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	mocks.accountClient.addAccount(1, "USER-ACCT-001")
	mocks.taxCollectionRepo.usersWithGains = []repository.TaxUserSummary{
		{UserID: 42, SystemType: "employee", TotalDebtRSD: decimal.NewFromInt(150)},
	}

	// First gain: 1000 RSD profit → 150 RSD tax.
	_ = mocks.capitalGainRepo.Create(&model.CapitalGain{
		UserID: 42, SystemType: "employee", SecurityType: "stock", Ticker: "AAPL",
		Quantity: 10, BuyPricePerUnit: decimal.NewFromInt(100), SellPricePerUnit: decimal.NewFromInt(200),
		TotalGain: decimal.NewFromInt(1000), Currency: "RSD", AccountID: 1,
		TaxYear: year, TaxMonth: month,
	})

	_, firstTotal, _, err := svc.CollectTax(year, month)
	if err != nil {
		t.Fatalf("first CollectTax: %v", err)
	}
	if !firstTotal.Equal(decimal.NewFromInt(150)) {
		t.Fatalf("first CollectTax: expected 150 RSD, got %s", firstTotal)
	}

	// Second gain lands: another 500 RSD profit → 75 RSD tax on the delta only.
	_ = mocks.capitalGainRepo.Create(&model.CapitalGain{
		UserID: 42, SystemType: "employee", SecurityType: "stock", Ticker: "MSFT",
		Quantity: 5, BuyPricePerUnit: decimal.NewFromInt(100), SellPricePerUnit: decimal.NewFromInt(200),
		TotalGain: decimal.NewFromInt(500), Currency: "RSD", AccountID: 1,
		TaxYear: year, TaxMonth: month,
	})

	collectedCount, secondTotal, failedCount, err := svc.CollectTax(year, month)
	if err != nil {
		t.Fatalf("second CollectTax: %v", err)
	}
	if failedCount != 0 {
		t.Errorf("expected 0 failures, got %d", failedCount)
	}
	if collectedCount != 1 {
		t.Errorf("expected 1 user collected on second call, got %d", collectedCount)
	}
	// 500 RSD × 15% = 75 RSD — NOT 1500 × 15% = 225 (double-taxing old profit),
	// NOT 0 (the silent-skip pre-fix bug).
	if !secondTotal.Equal(decimal.NewFromInt(75)) {
		t.Errorf("second CollectTax: expected 75 RSD on new profit only, got %s", secondTotal)
	}

	// Two TaxCollection rows total (one per call).
	if len(mocks.taxCollectionRepo.collections) != 2 {
		t.Fatalf("expected 2 tax collections, got %d", len(mocks.taxCollectionRepo.collections))
	}

	// Idempotency keys for the second batch must differ from the first so
	// account-service doesn't dedupe the second debit/credit as a replay.
	// updateBalCalls order: [1st-debit, 1st-credit, 2nd-debit, 2nd-credit].
	if len(mocks.accountClient.updateBalCalls) != 4 {
		t.Fatalf("expected 4 UpdateBalance calls across both collections, got %d", len(mocks.accountClient.updateBalCalls))
	}
}

// ---------------------------------------------------------------------------
// CollectTax — clicking the button again with no new profit is a no-op.
// ---------------------------------------------------------------------------

func TestTaxService_CollectTax_NoNewProfitIsNoOp(t *testing.T) {
	svc, mocks := buildTaxService()

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	mocks.accountClient.addAccount(1, "USER-ACCT-001")
	mocks.taxCollectionRepo.usersWithGains = []repository.TaxUserSummary{
		{UserID: 42, SystemType: "employee", TotalDebtRSD: decimal.NewFromInt(150)},
	}
	_ = mocks.capitalGainRepo.Create(&model.CapitalGain{
		UserID: 42, SystemType: "employee", SecurityType: "stock", Ticker: "AAPL",
		Quantity: 10, BuyPricePerUnit: decimal.NewFromInt(100), SellPricePerUnit: decimal.NewFromInt(200),
		TotalGain: decimal.NewFromInt(1000), Currency: "RSD", AccountID: 1,
		TaxYear: year, TaxMonth: month,
	})

	if _, _, _, err := svc.CollectTax(year, month); err != nil {
		t.Fatalf("first CollectTax: %v", err)
	}

	collectedCount, totalRSD, failedCount, err := svc.CollectTax(year, month)
	if err != nil {
		t.Fatalf("second CollectTax: %v", err)
	}
	if collectedCount != 0 {
		t.Errorf("expected collectedCount=0 when nothing new to tax, got %d", collectedCount)
	}
	if failedCount != 0 {
		t.Errorf("expected failedCount=0, got %d", failedCount)
	}
	if !totalRSD.IsZero() {
		t.Errorf("expected totalRSD=0, got %s", totalRSD)
	}
	if len(mocks.taxCollectionRepo.collections) != 1 {
		t.Errorf("expected exactly 1 tax_collection row after no-op second call, got %d", len(mocks.taxCollectionRepo.collections))
	}
}

// ---------------------------------------------------------------------------
// CollectTax — credit-leg failure: when the user-debit succeeds but the
// state-credit fails, NO TaxCollection row is persisted and the capital_gain
// rows stay uncollected, so a future CollectTax call retries the credit
// under the same idempotency key (account-service safely dedups the debit).
// ---------------------------------------------------------------------------

func TestTaxService_CollectTax_CreditFailure_DoesNotLockInMissingCredit(t *testing.T) {
	svc, mocks := buildTaxService()

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	mocks.accountClient.addAccount(1, "USER-ACCT-001")
	mocks.taxCollectionRepo.usersWithGains = []repository.TaxUserSummary{
		{UserID: 42, SystemType: "employee", TotalDebtRSD: decimal.NewFromInt(150)},
	}
	_ = mocks.capitalGainRepo.Create(&model.CapitalGain{
		UserID: 42, SystemType: "employee", SecurityType: "stock", Ticker: "AAPL",
		Quantity: 10, BuyPricePerUnit: decimal.NewFromInt(100), SellPricePerUnit: decimal.NewFromInt(200),
		TotalGain: decimal.NewFromInt(1000), Currency: "RSD", AccountID: 1,
		TaxYear: year, TaxMonth: month,
	})

	// Fail every call to the state RSD account (credit leg).
	mocks.accountClient.failOnAccountNumber = "STATE-RSD-001"

	collectedCount, totalRSD, failedCount, err := svc.CollectTax(year, month)
	if err != nil {
		t.Fatalf("CollectTax: %v", err)
	}
	if collectedCount != 0 {
		t.Errorf("collectedCount: got %d, want 0 (credit failed → user counted as failed, not collected)", collectedCount)
	}
	if failedCount != 1 {
		t.Errorf("failedCount: got %d, want 1", failedCount)
	}
	if !totalRSD.IsZero() {
		t.Errorf("totalRSD: got %s, want 0 (credit never landed)", totalRSD)
	}

	// Both the user-debit and the state-credit attempt should have been made.
	if len(mocks.accountClient.updateBalCalls) != 2 {
		t.Fatalf("expected 2 UpdateBalance calls (debit + failing credit attempt), got %d", len(mocks.accountClient.updateBalCalls))
	}
	if mocks.accountClient.updateBalCalls[0].AccountNumber != "USER-ACCT-001" {
		t.Errorf("first call should be user debit, got %s", mocks.accountClient.updateBalCalls[0].AccountNumber)
	}
	if mocks.accountClient.updateBalCalls[1].AccountNumber != "STATE-RSD-001" {
		t.Errorf("second call should be state credit attempt, got %s", mocks.accountClient.updateBalCalls[1].AccountNumber)
	}

	// Critical invariants: NO TaxCollection row, NO MarkCollected stamping.
	if len(mocks.taxCollectionRepo.collections) != 0 {
		t.Errorf("expected 0 tax_collection rows when credit failed, got %d", len(mocks.taxCollectionRepo.collections))
	}
	if len(mocks.capitalGainRepo.gains) != 1 {
		t.Fatalf("expected 1 capital_gain row, got %d", len(mocks.capitalGainRepo.gains))
	}
	if mocks.capitalGainRepo.gains[0].TaxCollectionID != nil {
		t.Errorf("capital_gain.tax_collection_id should remain NULL when credit failed, got %v", *mocks.capitalGainRepo.gains[0].TaxCollectionID)
	}

	// Now simulate the credit recovery: state account is healthy on retry.
	// Account-service dedups the debit (same idempotency key) and finally
	// applies the credit. The retry must reuse the SAME attempt number → same
	// idempotency keys → no double-debit. We assert this by checking the
	// TaxCollection row that lands has the same Year/Month/Account/Currency
	// tuple and the second debit/credit pair go through cleanly.
	mocks.accountClient.failOnAccountNumber = ""
	mocks.accountClient.updateBalCalls = nil

	collectedCount2, totalRSD2, failedCount2, err := svc.CollectTax(year, month)
	if err != nil {
		t.Fatalf("retry CollectTax: %v", err)
	}
	if collectedCount2 != 1 {
		t.Errorf("retry collectedCount: got %d, want 1", collectedCount2)
	}
	if failedCount2 != 0 {
		t.Errorf("retry failedCount: got %d, want 0", failedCount2)
	}
	if !totalRSD2.Equal(decimal.NewFromInt(150)) {
		t.Errorf("retry totalRSD: got %s, want 150", totalRSD2)
	}
	if len(mocks.taxCollectionRepo.collections) != 1 {
		t.Errorf("expected 1 tax_collection row after retry, got %d", len(mocks.taxCollectionRepo.collections))
	}
	// Retry must use attempt=1 (because the failed run never persisted a
	// collection row, so CountByKey is still 0). The idempotency keys must
	// therefore end with "-a1", which is what account-service would have
	// dedup'd against.
	if len(mocks.accountClient.updateBalCalls) != 2 {
		t.Fatalf("retry: expected 2 UpdateBalance calls, got %d", len(mocks.accountClient.updateBalCalls))
	}
}
