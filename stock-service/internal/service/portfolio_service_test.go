package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// ---------------------------------------------------------------------------
// Mock implementations for PortfolioService
// ---------------------------------------------------------------------------

// mockHoldingRepo is an in-memory holding repository that replicates
// the weighted-average upsert logic from the real repository.
type mockHoldingRepo struct {
	holdings       map[uint64]*model.Holding
	nextID         uint64
	failNextUpsert error // if non-nil, next Upsert call will fail and clear this field
	failNextUpdate error // if non-nil, next Update call will fail and clear this field
	failNextDelete error // if non-nil, next Delete call will fail and clear this field
}

func newMockHoldingRepo() *mockHoldingRepo {
	return &mockHoldingRepo{holdings: make(map[uint64]*model.Holding), nextID: 1}
}

func (m *mockHoldingRepo) Upsert(holding *model.Holding) error {
	if m.failNextUpsert != nil {
		err := m.failNextUpsert
		m.failNextUpsert = nil
		return err
	}
	// Find existing by (user_id, security_type, security_id, account_id)
	for _, h := range m.holdings {
		if h.UserID == holding.UserID &&
			h.SecurityType == holding.SecurityType &&
			h.SecurityID == holding.SecurityID &&
			h.AccountID == holding.AccountID {
			// Weighted average price calculation (mirrors real repo)
			oldTotal := h.AveragePrice.Mul(decimal.NewFromInt(h.Quantity))
			newTotal := holding.AveragePrice.Mul(decimal.NewFromInt(holding.Quantity))
			totalQty := h.Quantity + holding.Quantity
			if totalQty > 0 {
				h.AveragePrice = oldTotal.Add(newTotal).Div(decimal.NewFromInt(totalQty))
			}
			h.Quantity = totalQty
			h.ListingID = holding.ListingID
			h.Ticker = holding.Ticker
			h.Name = holding.Name
			*holding = *h
			return nil
		}
	}
	// New holding
	holding.ID = m.nextID
	m.nextID++
	stored := *holding
	m.holdings[holding.ID] = &stored
	*holding = stored
	return nil
}

func (m *mockHoldingRepo) GetByID(id uint64) (*model.Holding, error) {
	h, ok := m.holdings[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	cp := *h
	return &cp, nil
}

func (m *mockHoldingRepo) Update(holding *model.Holding) error {
	if m.failNextUpdate != nil {
		err := m.failNextUpdate
		m.failNextUpdate = nil
		return err
	}
	if _, ok := m.holdings[holding.ID]; !ok {
		return gorm.ErrRecordNotFound
	}
	stored := *holding
	m.holdings[holding.ID] = &stored
	return nil
}

func (m *mockHoldingRepo) Delete(id uint64) error {
	if m.failNextDelete != nil {
		err := m.failNextDelete
		m.failNextDelete = nil
		return err
	}
	if _, ok := m.holdings[id]; !ok {
		return gorm.ErrRecordNotFound
	}
	delete(m.holdings, id)
	return nil
}

func (m *mockHoldingRepo) GetByUserAndSecurity(userID uint64, securityType string, securityID uint64, accountID uint64) (*model.Holding, error) {
	for _, h := range m.holdings {
		if h.UserID == userID && h.SecurityType == securityType && h.SecurityID == securityID && h.AccountID == accountID {
			cp := *h
			return &cp, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockHoldingRepo) ListByUser(userID uint64, filter repository.HoldingFilter) ([]model.Holding, int64, error) {
	var result []model.Holding
	for _, h := range m.holdings {
		if h.UserID == userID && h.Quantity > 0 {
			if filter.SecurityType != "" && h.SecurityType != filter.SecurityType {
				continue
			}
			result = append(result, *h)
		}
	}
	return result, int64(len(result)), nil
}

func (m *mockHoldingRepo) ListPublicOffers(filter repository.OTCFilter) ([]model.Holding, int64, error) {
	var result []model.Holding
	for _, h := range m.holdings {
		if h.PublicQuantity > 0 && h.SecurityType == "stock" {
			result = append(result, *h)
		}
	}
	return result, int64(len(result)), nil
}

// addHolding inserts a holding directly for test setup.
func (m *mockHoldingRepo) addHolding(h *model.Holding) {
	if h.ID == 0 {
		h.ID = m.nextID
		m.nextID++
	}
	stored := *h
	m.holdings[h.ID] = &stored
}

// mockCapitalGainRepo records created capital gains.
type mockCapitalGainRepo struct {
	gains          []model.CapitalGain
	nextID         uint64
	failNextCreate error // if non-nil, next Create call will fail and clear this field
}

func newMockCapitalGainRepo() *mockCapitalGainRepo {
	return &mockCapitalGainRepo{nextID: 1}
}

func (m *mockCapitalGainRepo) Create(gain *model.CapitalGain) error {
	if m.failNextCreate != nil {
		err := m.failNextCreate
		m.failNextCreate = nil
		return err
	}
	gain.ID = m.nextID
	m.nextID++
	m.gains = append(m.gains, *gain)
	return nil
}

func (m *mockCapitalGainRepo) ListByUser(userID uint64, page, pageSize int) ([]model.CapitalGain, int64, error) {
	var result []model.CapitalGain
	for _, g := range m.gains {
		if g.UserID == userID {
			result = append(result, g)
		}
	}
	return result, int64(len(result)), nil
}

func (m *mockCapitalGainRepo) SumByUserMonth(userID uint64, year, month int) ([]repository.AccountGainSummary, error) {
	return nil, nil
}

func (m *mockCapitalGainRepo) SumByUserYear(userID uint64, year int) ([]repository.AccountGainSummary, error) {
	return nil, nil
}

// mockStockRepo returns pre-configured stocks.
type mockStockRepo struct {
	stocks map[uint64]*model.Stock
}

func newMockStockRepo() *mockStockRepo {
	return &mockStockRepo{stocks: make(map[uint64]*model.Stock)}
}

func (m *mockStockRepo) Create(stock *model.Stock) error { return nil }
func (m *mockStockRepo) GetByTicker(ticker string) (*model.Stock, error) {
	return nil, gorm.ErrRecordNotFound
}
func (m *mockStockRepo) Update(stock *model.Stock) error         { return nil }
func (m *mockStockRepo) UpsertByTicker(stock *model.Stock) error { return nil }
func (m *mockStockRepo) List(filter repository.StockFilter) ([]model.Stock, int64, error) {
	return nil, 0, nil
}

func (m *mockStockRepo) GetByID(id uint64) (*model.Stock, error) {
	s, ok := m.stocks[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	cp := *s
	return &cp, nil
}

func (m *mockStockRepo) addStock(s *model.Stock) {
	m.stocks[s.ID] = s
}

// mockOptionRepo returns pre-configured options.
type mockOptionRepo struct {
	options map[uint64]*model.Option
}

func newMockOptionRepo() *mockOptionRepo {
	return &mockOptionRepo{options: make(map[uint64]*model.Option)}
}

func (m *mockOptionRepo) Create(o *model.Option) error { return nil }
func (m *mockOptionRepo) GetByTicker(ticker string) (*model.Option, error) {
	return nil, gorm.ErrRecordNotFound
}
func (m *mockOptionRepo) Update(o *model.Option) error         { return nil }
func (m *mockOptionRepo) UpsertByTicker(o *model.Option) error { return nil }
func (m *mockOptionRepo) List(filter repository.OptionFilter) ([]model.Option, int64, error) {
	return nil, 0, nil
}
func (m *mockOptionRepo) DeleteExpiredBefore(cutoff time.Time) (int64, error) { return 0, nil }

func (m *mockOptionRepo) GetByID(id uint64) (*model.Option, error) {
	o, ok := m.options[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	cp := *o
	return &cp, nil
}

func (m *mockOptionRepo) addOption(o *model.Option) {
	m.options[o.ID] = o
}

// mockAccountClient implements accountpb.AccountServiceClient.
// It records calls and returns configurable responses.
type mockAccountClient struct {
	accounts              map[uint64]*accountpb.AccountResponse
	updateBalErr          error
	getAccountErr         error
	updateBalCalls        []updateBalCall
	failUpdateForAccounts map[string]error // per-account-number errors for UpdateBalance
}

type updateBalCall struct {
	AccountNumber string
	Amount        string
}

func newMockAccountClient() *mockAccountClient {
	return &mockAccountClient{
		accounts:              make(map[uint64]*accountpb.AccountResponse),
		failUpdateForAccounts: make(map[string]error),
	}
}

func (m *mockAccountClient) addAccount(id uint64, accountNumber string) {
	m.accounts[id] = &accountpb.AccountResponse{
		Id:            id,
		AccountNumber: accountNumber,
	}
}

// failUpdateForAccount configures UpdateBalance to fail when called for a specific account number.
func (m *mockAccountClient) failUpdateForAccount(accountNumber string, err error) {
	m.failUpdateForAccounts[accountNumber] = err
}

func (m *mockAccountClient) GetAccount(_ context.Context, req *accountpb.GetAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if m.getAccountErr != nil {
		return nil, m.getAccountErr
	}
	resp, ok := m.accounts[req.Id]
	if !ok {
		return nil, errors.New("account not found")
	}
	return resp, nil
}

func (m *mockAccountClient) UpdateBalance(_ context.Context, req *accountpb.UpdateBalanceRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if m.updateBalErr != nil {
		return nil, m.updateBalErr
	}
	if err, ok := m.failUpdateForAccounts[req.AccountNumber]; ok {
		return nil, err
	}
	m.updateBalCalls = append(m.updateBalCalls, updateBalCall{
		AccountNumber: req.AccountNumber,
		Amount:        req.Amount,
	})
	return &accountpb.AccountResponse{}, nil
}

// Unused AccountServiceClient methods — stubbed to satisfy the interface.
func (m *mockAccountClient) CreateAccount(context.Context, *accountpb.CreateAccountRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClient) GetAccountByNumber(context.Context, *accountpb.GetAccountByNumberRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClient) ListAccountsByClient(context.Context, *accountpb.ListAccountsByClientRequest, ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (m *mockAccountClient) ListAllAccounts(context.Context, *accountpb.ListAllAccountsRequest, ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (m *mockAccountClient) UpdateAccountName(context.Context, *accountpb.UpdateAccountNameRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClient) UpdateAccountLimits(context.Context, *accountpb.UpdateAccountLimitsRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClient) UpdateAccountStatus(context.Context, *accountpb.UpdateAccountStatusRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClient) CreateCompany(context.Context, *accountpb.CreateCompanyRequest, ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockAccountClient) GetCompany(context.Context, *accountpb.GetCompanyRequest, ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockAccountClient) UpdateCompany(context.Context, *accountpb.UpdateCompanyRequest, ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockAccountClient) ListCurrencies(context.Context, *accountpb.ListCurrenciesRequest, ...grpc.CallOption) (*accountpb.ListCurrenciesResponse, error) {
	return nil, nil
}
func (m *mockAccountClient) GetCurrency(context.Context, *accountpb.GetCurrencyRequest, ...grpc.CallOption) (*accountpb.CurrencyResponse, error) {
	return nil, nil
}
func (m *mockAccountClient) GetLedgerEntries(context.Context, *accountpb.GetLedgerEntriesRequest, ...grpc.CallOption) (*accountpb.GetLedgerEntriesResponse, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Helper: build a PortfolioService with all mock dependencies
// ---------------------------------------------------------------------------

type portfolioMocks struct {
	holdingRepo     *mockHoldingRepo
	capitalGainRepo *mockCapitalGainRepo
	listingRepo     *mockListingRepo
	stockRepo       *mockStockRepo
	optionRepo      *mockOptionRepo
	accountClient   *mockAccountClient
}

func buildPortfolioService() (*PortfolioService, *portfolioMocks) {
	mocks := &portfolioMocks{
		holdingRepo:     newMockHoldingRepo(),
		capitalGainRepo: newMockCapitalGainRepo(),
		listingRepo:     newMockListingRepo(),
		stockRepo:       newMockStockRepo(),
		optionRepo:      newMockOptionRepo(),
		accountClient:   newMockAccountClient(),
	}

	nameResolver := func(userID uint64, systemType string) (string, string, error) {
		return "John", "Doe", nil
	}

	svc := NewPortfolioService(
		mocks.holdingRepo,
		mocks.capitalGainRepo,
		mocks.listingRepo,
		mocks.stockRepo,
		mocks.optionRepo,
		mocks.accountClient,
		nameResolver,
		"STATE-ACCT-001",
	)

	// Default account for tests
	mocks.accountClient.addAccount(1, "ACCT-001")

	return svc, mocks
}

// stockListing creates a stock listing with a given price on an exchange with USD currency.
func stockListing(id, securityID uint64, price float64) *model.Listing {
	return &model.Listing{
		ID:           id,
		SecurityID:   securityID,
		SecurityType: "stock",
		ExchangeID:   1,
		Exchange: model.StockExchange{
			ID:       1,
			Name:     "NYSE",
			Acronym:  "NYSE",
			Currency: "USD",
			TimeZone: "-5",
		},
		Price: decimal.NewFromFloat(price),
	}
}

// ---------------------------------------------------------------------------
// Tests: ProcessBuyFill
// ---------------------------------------------------------------------------

func TestPortfolio_ProcessBuyFill_NewHolding(t *testing.T) {
	svc, mocks := buildPortfolioService()

	listing := stockListing(1, 100, 50.00)
	mocks.listingRepo.addListing(listing)
	mocks.stockRepo.addStock(&model.Stock{ID: 100, Ticker: "AAPL", Name: "Apple Inc."})

	order := &model.Order{
		ID:           1,
		UserID:       42,
		SystemType:   "employee",
		ListingID:    1,
		SecurityType: "stock",
		Ticker:       "AAPL",
		Direction:    "buy",
		OrderType:    "market",
		Quantity:     10,
		Commission:   decimal.NewFromFloat(5.00),
		AccountID:    1,
	}
	txn := &model.OrderTransaction{
		ID:           1,
		OrderID:      1,
		Quantity:     10,
		PricePerUnit: decimal.NewFromFloat(50.00),
		TotalPrice:   decimal.NewFromFloat(500.00),
	}

	err := svc.ProcessBuyFill(order, txn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify holding was created
	holding, err := mocks.holdingRepo.GetByUserAndSecurity(42, "stock", 100, 1)
	if err != nil {
		t.Fatalf("holding not found: %v", err)
	}
	if holding.Quantity != 10 {
		t.Errorf("expected quantity 10, got %d", holding.Quantity)
	}
	if !holding.AveragePrice.Equal(decimal.NewFromFloat(50.00)) {
		t.Errorf("expected avg price 50.00, got %s", holding.AveragePrice)
	}
	if holding.Name != "Apple Inc." {
		t.Errorf("expected name 'Apple Inc.', got %q", holding.Name)
	}
	if holding.UserFirstName != "John" || holding.UserLastName != "Doe" {
		t.Errorf("expected user name 'John Doe', got '%s %s'", holding.UserFirstName, holding.UserLastName)
	}

	// Verify account was debited: total_price + proportional commission = 500 + 5 = 505
	if len(mocks.accountClient.updateBalCalls) != 2 {
		t.Fatalf("expected 2 UpdateBalance calls, got %d", len(mocks.accountClient.updateBalCalls))
	}
	debitCall := mocks.accountClient.updateBalCalls[0]
	expectedDebit := decimal.NewFromFloat(-505.00)
	actualDebit, _ := decimal.NewFromString(debitCall.Amount)
	if !actualDebit.Equal(expectedDebit) {
		t.Errorf("expected debit %s, got %s", expectedDebit, actualDebit)
	}

	// Verify bank commission credit
	commissionCall := mocks.accountClient.updateBalCalls[1]
	if commissionCall.AccountNumber != "STATE-ACCT-001" {
		t.Errorf("expected commission credit to STATE-ACCT-001, got %s", commissionCall.AccountNumber)
	}
	expectedComm := decimal.NewFromFloat(5.00)
	actualComm, _ := decimal.NewFromString(commissionCall.Amount)
	if !actualComm.Equal(expectedComm) {
		t.Errorf("expected commission %s, got %s", expectedComm, actualComm)
	}
}

func TestPortfolio_ProcessBuyFill_WeightedAverage(t *testing.T) {
	svc, mocks := buildPortfolioService()

	listing := stockListing(1, 100, 60.00)
	mocks.listingRepo.addListing(listing)
	mocks.stockRepo.addStock(&model.Stock{ID: 100, Ticker: "AAPL", Name: "Apple Inc."})

	// Pre-existing holding: 10 shares at $50
	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:       42,
		SystemType:   "employee",
		SecurityType: "stock",
		SecurityID:   100,
		ListingID:    1,
		Ticker:       "AAPL",
		Name:         "Apple Inc.",
		Quantity:     10,
		AveragePrice: decimal.NewFromFloat(50.00),
		AccountID:    1,
	})

	order := &model.Order{
		ID:           2,
		UserID:       42,
		SystemType:   "employee",
		ListingID:    1,
		SecurityType: "stock",
		Ticker:       "AAPL",
		Direction:    "buy",
		OrderType:    "market",
		Quantity:     10,
		Commission:   decimal.NewFromFloat(2.00),
		AccountID:    1,
	}
	txn := &model.OrderTransaction{
		ID:           2,
		OrderID:      2,
		Quantity:     10,
		PricePerUnit: decimal.NewFromFloat(60.00),
		TotalPrice:   decimal.NewFromFloat(600.00),
	}

	err := svc.ProcessBuyFill(order, txn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Weighted average: (10*50 + 10*60) / (10+10) = 1100/20 = 55
	holding, _ := mocks.holdingRepo.GetByUserAndSecurity(42, "stock", 100, 1)
	if holding.Quantity != 20 {
		t.Errorf("expected quantity 20, got %d", holding.Quantity)
	}
	expectedAvg := decimal.NewFromFloat(55.00)
	if !holding.AveragePrice.Equal(expectedAvg) {
		t.Errorf("expected avg price %s, got %s", expectedAvg, holding.AveragePrice)
	}
}

func TestPortfolio_ProcessBuyFill_PartialFill_ProportionalCommission(t *testing.T) {
	svc, mocks := buildPortfolioService()

	listing := stockListing(1, 100, 100.00)
	mocks.listingRepo.addListing(listing)
	mocks.stockRepo.addStock(&model.Stock{ID: 100, Ticker: "AAPL", Name: "Apple Inc."})

	// Order is for 20 shares total, but only 5 are filled in this txn
	order := &model.Order{
		ID:           3,
		UserID:       42,
		SystemType:   "employee",
		ListingID:    1,
		SecurityType: "stock",
		Ticker:       "AAPL",
		Direction:    "buy",
		OrderType:    "market",
		Quantity:     20,
		Commission:   decimal.NewFromFloat(8.00),
		AccountID:    1,
	}
	txn := &model.OrderTransaction{
		ID:           3,
		OrderID:      3,
		Quantity:     5,
		PricePerUnit: decimal.NewFromFloat(100.00),
		TotalPrice:   decimal.NewFromFloat(500.00),
	}

	err := svc.ProcessBuyFill(order, txn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Proportional commission: 8 * (5/20) = 2
	// Debit = 500 + 2 = 502
	debitCall := mocks.accountClient.updateBalCalls[0]
	actualDebit, _ := decimal.NewFromString(debitCall.Amount)
	expectedDebit := decimal.NewFromFloat(-502.00)
	if !actualDebit.Equal(expectedDebit) {
		t.Errorf("expected debit %s, got %s", expectedDebit, actualDebit)
	}

	// Bank commission = 2
	commCall := mocks.accountClient.updateBalCalls[1]
	actualComm, _ := decimal.NewFromString(commCall.Amount)
	expectedComm := decimal.NewFromFloat(2.00)
	if !actualComm.Equal(expectedComm) {
		t.Errorf("expected commission %s, got %s", expectedComm, actualComm)
	}
}

func TestPortfolio_ProcessBuyFill_ListingNotFound(t *testing.T) {
	svc, _ := buildPortfolioService()
	// No listing added

	order := &model.Order{
		ID:        1,
		UserID:    42,
		ListingID: 999,
		AccountID: 1,
	}
	txn := &model.OrderTransaction{Quantity: 5, PricePerUnit: decimal.NewFromInt(10), TotalPrice: decimal.NewFromInt(50)}

	err := svc.ProcessBuyFill(order, txn)
	if err == nil {
		t.Fatal("expected error for missing listing")
	}
}

// ---------------------------------------------------------------------------
// Tests: ProcessSellFill
// ---------------------------------------------------------------------------

func TestPortfolio_ProcessSellFill_PartialSell(t *testing.T) {
	svc, mocks := buildPortfolioService()

	listing := stockListing(1, 100, 60.00)
	listing.Exchange.Currency = "USD"
	mocks.listingRepo.addListing(listing)

	// Existing holding: 20 shares at avg $50
	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:       42,
		SystemType:   "employee",
		SecurityType: "stock",
		SecurityID:   100,
		ListingID:    1,
		Ticker:       "AAPL",
		Name:         "Apple Inc.",
		Quantity:     20,
		AveragePrice: decimal.NewFromFloat(50.00),
		AccountID:    1,
	})

	order := &model.Order{
		ID:           10,
		UserID:       42,
		SystemType:   "employee",
		ListingID:    1,
		SecurityType: "stock",
		Ticker:       "AAPL",
		Direction:    "sell",
		OrderType:    "market",
		Quantity:     5,
		Commission:   decimal.NewFromFloat(3.00),
		AccountID:    1,
	}
	txn := &model.OrderTransaction{
		ID:           10,
		OrderID:      10,
		Quantity:     5,
		PricePerUnit: decimal.NewFromFloat(60.00),
		TotalPrice:   decimal.NewFromFloat(300.00),
	}

	err := svc.ProcessSellFill(order, txn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Holding should decrease from 20 to 15
	holding, _ := mocks.holdingRepo.GetByUserAndSecurity(42, "stock", 100, 1)
	if holding.Quantity != 15 {
		t.Errorf("expected quantity 15, got %d", holding.Quantity)
	}
	// Average price should remain $50 (unchanged)
	if !holding.AveragePrice.Equal(decimal.NewFromFloat(50.00)) {
		t.Errorf("expected avg price 50, got %s", holding.AveragePrice)
	}

	// Capital gain: (60 - 50) * 5 = 50
	if len(mocks.capitalGainRepo.gains) != 1 {
		t.Fatalf("expected 1 capital gain, got %d", len(mocks.capitalGainRepo.gains))
	}
	gain := mocks.capitalGainRepo.gains[0]
	expectedGain := decimal.NewFromFloat(50.00)
	if !gain.TotalGain.Equal(expectedGain) {
		t.Errorf("expected total gain %s, got %s", expectedGain, gain.TotalGain)
	}
	if gain.Quantity != 5 {
		t.Errorf("expected gain quantity 5, got %d", gain.Quantity)
	}
	if !gain.BuyPricePerUnit.Equal(decimal.NewFromFloat(50.00)) {
		t.Errorf("expected buy price 50, got %s", gain.BuyPricePerUnit)
	}
	if !gain.SellPricePerUnit.Equal(decimal.NewFromFloat(60.00)) {
		t.Errorf("expected sell price 60, got %s", gain.SellPricePerUnit)
	}
	if gain.Currency != "USD" {
		t.Errorf("expected currency USD, got %s", gain.Currency)
	}

	// Credit: total_price - proportional commission = 300 - 3 = 297
	// (order.Quantity == txn.Quantity == 5, so full commission applies)
	debitCall := mocks.accountClient.updateBalCalls[0]
	actualCredit, _ := decimal.NewFromString(debitCall.Amount)
	expectedCredit := decimal.NewFromFloat(297.00)
	if !actualCredit.Equal(expectedCredit) {
		t.Errorf("expected credit %s, got %s", expectedCredit, actualCredit)
	}
}

func TestPortfolio_ProcessSellFill_NegativeGain(t *testing.T) {
	svc, mocks := buildPortfolioService()

	listing := stockListing(1, 100, 40.00)
	listing.Exchange.Currency = "USD"
	mocks.listingRepo.addListing(listing)

	// Existing holding: 10 shares at avg $50 — selling at a loss
	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:       42,
		SystemType:   "employee",
		SecurityType: "stock",
		SecurityID:   100,
		ListingID:    1,
		Ticker:       "AAPL",
		Quantity:     10,
		AveragePrice: decimal.NewFromFloat(50.00),
		AccountID:    1,
	})

	order := &model.Order{
		ID: 11, UserID: 42, SystemType: "employee", ListingID: 1,
		SecurityType: "stock", Ticker: "AAPL", Direction: "sell",
		Quantity: 10, Commission: decimal.Zero, AccountID: 1,
	}
	txn := &model.OrderTransaction{
		ID: 11, OrderID: 11, Quantity: 10,
		PricePerUnit: decimal.NewFromFloat(40.00),
		TotalPrice:   decimal.NewFromFloat(400.00),
	}

	err := svc.ProcessSellFill(order, txn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Capital gain should be negative: (40 - 50) * 10 = -100
	gain := mocks.capitalGainRepo.gains[0]
	expectedGain := decimal.NewFromFloat(-100.00)
	if !gain.TotalGain.Equal(expectedGain) {
		t.Errorf("expected total gain %s, got %s", expectedGain, gain.TotalGain)
	}
}

func TestPortfolio_ProcessSellFill_EntirePosition_Deleted(t *testing.T) {
	svc, mocks := buildPortfolioService()

	listing := stockListing(1, 100, 60.00)
	listing.Exchange.Currency = "USD"
	mocks.listingRepo.addListing(listing)

	holdingID := uint64(0) // will be assigned by addHolding
	h := &model.Holding{
		UserID:       42,
		SystemType:   "employee",
		SecurityType: "stock",
		SecurityID:   100,
		ListingID:    1,
		Ticker:       "AAPL",
		Quantity:     10,
		AveragePrice: decimal.NewFromFloat(50.00),
		AccountID:    1,
	}
	mocks.holdingRepo.addHolding(h)
	holdingID = h.ID

	order := &model.Order{
		ID: 12, UserID: 42, SystemType: "employee", ListingID: 1,
		SecurityType: "stock", Ticker: "AAPL", Direction: "sell",
		Quantity: 10, Commission: decimal.Zero, AccountID: 1,
	}
	txn := &model.OrderTransaction{
		ID: 12, OrderID: 12, Quantity: 10,
		PricePerUnit: decimal.NewFromFloat(60.00),
		TotalPrice:   decimal.NewFromFloat(600.00),
	}

	err := svc.ProcessSellFill(order, txn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Holding should be deleted
	_, err = mocks.holdingRepo.GetByID(holdingID)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("expected holding to be deleted, got err: %v", err)
	}
}

func TestPortfolio_ProcessSellFill_PublicQuantityAdjusted(t *testing.T) {
	svc, mocks := buildPortfolioService()

	listing := stockListing(1, 100, 60.00)
	listing.Exchange.Currency = "USD"
	mocks.listingRepo.addListing(listing)

	h := &model.Holding{
		UserID:         42,
		SystemType:     "employee",
		SecurityType:   "stock",
		SecurityID:     100,
		ListingID:      1,
		Ticker:         "AAPL",
		Quantity:       20,
		AveragePrice:   decimal.NewFromFloat(50.00),
		PublicQuantity: 15,
		AccountID:      1,
	}
	mocks.holdingRepo.addHolding(h)

	order := &model.Order{
		ID: 13, UserID: 42, SystemType: "employee", ListingID: 1,
		SecurityType: "stock", Ticker: "AAPL", Direction: "sell",
		Quantity: 10, Commission: decimal.Zero, AccountID: 1,
	}
	txn := &model.OrderTransaction{
		ID: 13, OrderID: 13, Quantity: 10,
		PricePerUnit: decimal.NewFromFloat(60.00),
		TotalPrice:   decimal.NewFromFloat(600.00),
	}

	err := svc.ProcessSellFill(order, txn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Quantity: 20 - 10 = 10. PublicQuantity was 15 but should be capped at 10.
	holding, _ := mocks.holdingRepo.GetByID(h.ID)
	if holding.Quantity != 10 {
		t.Errorf("expected quantity 10, got %d", holding.Quantity)
	}
	if holding.PublicQuantity != 10 {
		t.Errorf("expected public quantity capped at 10, got %d", holding.PublicQuantity)
	}
}

func TestPortfolio_ProcessSellFill_InsufficientQuantity(t *testing.T) {
	svc, mocks := buildPortfolioService()

	listing := stockListing(1, 100, 60.00)
	listing.Exchange.Currency = "USD"
	mocks.listingRepo.addListing(listing)

	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:       42,
		SystemType:   "employee",
		SecurityType: "stock",
		SecurityID:   100,
		ListingID:    1,
		Ticker:       "AAPL",
		Quantity:     5,
		AveragePrice: decimal.NewFromFloat(50.00),
		AccountID:    1,
	})

	order := &model.Order{
		ID: 14, UserID: 42, SystemType: "employee", ListingID: 1,
		SecurityType: "stock", Ticker: "AAPL", Direction: "sell",
		Quantity: 10, Commission: decimal.Zero, AccountID: 1,
	}
	txn := &model.OrderTransaction{
		ID: 14, OrderID: 14, Quantity: 10,
		PricePerUnit: decimal.NewFromFloat(60.00),
		TotalPrice:   decimal.NewFromFloat(600.00),
	}

	err := svc.ProcessSellFill(order, txn)
	if err == nil {
		t.Fatal("expected error for insufficient quantity")
	}
	if err.Error() != "insufficient holding quantity for sell" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPortfolio_ProcessSellFill_NoHolding(t *testing.T) {
	svc, mocks := buildPortfolioService()

	listing := stockListing(1, 100, 60.00)
	mocks.listingRepo.addListing(listing)

	order := &model.Order{
		ID: 15, UserID: 42, SystemType: "employee", ListingID: 1,
		SecurityType: "stock", Ticker: "AAPL", Direction: "sell",
		Quantity: 5, Commission: decimal.Zero, AccountID: 1,
	}
	txn := &model.OrderTransaction{
		ID: 15, OrderID: 15, Quantity: 5,
		PricePerUnit: decimal.NewFromFloat(60.00),
		TotalPrice:   decimal.NewFromFloat(300.00),
	}

	err := svc.ProcessSellFill(order, txn)
	if err == nil {
		t.Fatal("expected error when no holding exists")
	}
	if err.Error() != "holding not found for sell order" {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: MakePublic
// ---------------------------------------------------------------------------

func TestPortfolio_MakePublic_Success(t *testing.T) {
	svc, mocks := buildPortfolioService()

	h := &model.Holding{
		UserID:       42,
		SecurityType: "stock",
		SecurityID:   100,
		Quantity:     20,
		AveragePrice: decimal.NewFromFloat(50.00),
		AccountID:    1,
	}
	mocks.holdingRepo.addHolding(h)

	result, err := svc.MakePublic(h.ID, 42, 15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PublicQuantity != 15 {
		t.Errorf("expected public quantity 15, got %d", result.PublicQuantity)
	}
}

func TestPortfolio_MakePublic_ExceedsOwned(t *testing.T) {
	svc, mocks := buildPortfolioService()

	h := &model.Holding{
		UserID:       42,
		SecurityType: "stock",
		SecurityID:   100,
		Quantity:     10,
		AveragePrice: decimal.NewFromFloat(50.00),
		AccountID:    1,
	}
	mocks.holdingRepo.addHolding(h)

	_, err := svc.MakePublic(h.ID, 42, 15) // more than owned
	if err == nil {
		t.Fatal("expected error for quantity exceeding owned")
	}
	if err.Error() != "invalid public quantity" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPortfolio_MakePublic_NegativeQuantity(t *testing.T) {
	svc, mocks := buildPortfolioService()

	h := &model.Holding{
		UserID:       42,
		SecurityType: "stock",
		SecurityID:   100,
		Quantity:     10,
		AveragePrice: decimal.NewFromFloat(50.00),
		AccountID:    1,
	}
	mocks.holdingRepo.addHolding(h)

	_, err := svc.MakePublic(h.ID, 42, -1)
	if err == nil {
		t.Fatal("expected error for negative quantity")
	}
	if err.Error() != "invalid public quantity" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPortfolio_MakePublic_WrongUser(t *testing.T) {
	svc, mocks := buildPortfolioService()

	h := &model.Holding{
		UserID:       42,
		SecurityType: "stock",
		SecurityID:   100,
		Quantity:     10,
		AveragePrice: decimal.NewFromFloat(50.00),
		AccountID:    1,
	}
	mocks.holdingRepo.addHolding(h)

	_, err := svc.MakePublic(h.ID, 999, 5)
	if err == nil {
		t.Fatal("expected error for wrong user")
	}
	if err.Error() != "holding does not belong to user" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPortfolio_MakePublic_NotStock(t *testing.T) {
	svc, mocks := buildPortfolioService()

	h := &model.Holding{
		UserID:       42,
		SecurityType: "futures", // not stock
		SecurityID:   100,
		Quantity:     10,
		AveragePrice: decimal.NewFromFloat(50.00),
		AccountID:    1,
	}
	mocks.holdingRepo.addHolding(h)

	_, err := svc.MakePublic(h.ID, 42, 5)
	if err == nil {
		t.Fatal("expected error for non-stock holding")
	}
	if err.Error() != "only stocks can be made public for OTC trading" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPortfolio_MakePublic_NotFound(t *testing.T) {
	svc, _ := buildPortfolioService()

	_, err := svc.MakePublic(999, 42, 5)
	if err == nil {
		t.Fatal("expected error for non-existent holding")
	}
	if err.Error() != "holding not found" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPortfolio_MakePublic_SetToZero(t *testing.T) {
	svc, mocks := buildPortfolioService()

	h := &model.Holding{
		UserID:         42,
		SecurityType:   "stock",
		SecurityID:     100,
		Quantity:       10,
		PublicQuantity: 5,
		AveragePrice:   decimal.NewFromFloat(50.00),
		AccountID:      1,
	}
	mocks.holdingRepo.addHolding(h)

	result, err := svc.MakePublic(h.ID, 42, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PublicQuantity != 0 {
		t.Errorf("expected public quantity 0, got %d", result.PublicQuantity)
	}
}

// ---------------------------------------------------------------------------
// Tests: ListHoldings
// ---------------------------------------------------------------------------

func TestPortfolio_ListHoldings_ReturnsUserHoldings(t *testing.T) {
	svc, mocks := buildPortfolioService()

	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:       42,
		SecurityType: "stock",
		SecurityID:   100,
		Quantity:     10,
		AveragePrice: decimal.NewFromFloat(50.00),
		AccountID:    1,
	})
	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:       42,
		SecurityType: "stock",
		SecurityID:   200,
		Quantity:     5,
		AveragePrice: decimal.NewFromFloat(100.00),
		AccountID:    1,
	})
	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:       99, // different user
		SecurityType: "stock",
		SecurityID:   300,
		Quantity:     3,
		AveragePrice: decimal.NewFromFloat(200.00),
		AccountID:    1,
	})

	holdings, total, err := svc.ListHoldings(42, HoldingFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 2 {
		t.Errorf("expected 2 holdings, got %d", total)
	}
	if len(holdings) != 2 {
		t.Errorf("expected 2 holdings returned, got %d", len(holdings))
	}
}

func TestPortfolio_ListHoldings_FilterBySecurityType(t *testing.T) {
	svc, mocks := buildPortfolioService()

	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:       42,
		SecurityType: "stock",
		SecurityID:   100,
		Quantity:     10,
		AveragePrice: decimal.NewFromFloat(50.00),
		AccountID:    1,
	})
	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:       42,
		SecurityType: "futures",
		SecurityID:   200,
		Quantity:     5,
		AveragePrice: decimal.NewFromFloat(100.00),
		AccountID:    2,
	})

	holdings, total, err := svc.ListHoldings(42, HoldingFilter{SecurityType: "stock"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 {
		t.Errorf("expected 1 stock holding, got %d", total)
	}
	if len(holdings) != 1 {
		t.Errorf("expected 1 holding returned, got %d", len(holdings))
	}
}

func TestPortfolio_ListHoldings_EmptyForUnknownUser(t *testing.T) {
	svc, _ := buildPortfolioService()

	holdings, total, err := svc.ListHoldings(999, HoldingFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 0 {
		t.Errorf("expected 0 holdings, got %d", total)
	}
	if len(holdings) != 0 {
		t.Errorf("expected empty slice, got %d holdings", len(holdings))
	}
}

// ---------------------------------------------------------------------------
// Tests: GetCurrentPrice
// ---------------------------------------------------------------------------

func TestPortfolio_GetCurrentPrice_Success(t *testing.T) {
	svc, mocks := buildPortfolioService()

	listing := stockListing(1, 100, 123.45)
	mocks.listingRepo.addListing(listing)

	price, err := svc.GetCurrentPrice(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := decimal.NewFromFloat(123.45)
	if !price.Equal(expected) {
		t.Errorf("expected price %s, got %s", expected, price)
	}
}

func TestPortfolio_GetCurrentPrice_NotFound(t *testing.T) {
	svc, _ := buildPortfolioService()

	_, err := svc.GetCurrentPrice(999)
	if err == nil {
		t.Fatal("expected error for non-existent listing")
	}
}

// ---------------------------------------------------------------------------
// Tests: ExerciseOption — Call ITM
// ---------------------------------------------------------------------------

func TestPortfolio_ExerciseOption_CallITM(t *testing.T) {
	svc, mocks := buildPortfolioService()

	// Stock listing: current price $150
	stkListing := stockListing(10, 500, 150.00)
	stkListing.Exchange.Currency = "USD"
	mocks.listingRepo.addListing(stkListing)

	// Stock record
	mocks.stockRepo.addStock(&model.Stock{ID: 500, Ticker: "AAPL", Name: "Apple Inc."})

	// Option: call with strike $100, settlement in the future
	mocks.optionRepo.addOption(&model.Option{
		ID:             1,
		Ticker:         "AAPL240101C00100",
		OptionType:     "call",
		StockID:        500,
		StrikePrice:    decimal.NewFromFloat(100.00),
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
	})

	// User holds 2 option contracts
	h := &model.Holding{
		UserID:        42,
		SystemType:    "employee",
		UserFirstName: "John",
		UserLastName:  "Doe",
		SecurityType:  "option",
		SecurityID:    1,
		ListingID:     10,
		Ticker:        "AAPL240101C00100",
		Quantity:      2,
		AveragePrice:  decimal.NewFromFloat(5.00),
		AccountID:     1,
	}
	mocks.holdingRepo.addHolding(h)

	result, err := svc.ExerciseOption(h.ID, 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2 contracts * 100 shares = 200 shares affected
	if result.SharesAffected != 200 {
		t.Errorf("expected 200 shares affected, got %d", result.SharesAffected)
	}
	// Profit = (150 - 100) * 200 = 10000
	expectedProfit := decimal.NewFromFloat(10000.00)
	if !result.Profit.Equal(expectedProfit) {
		t.Errorf("expected profit %s, got %s", expectedProfit, result.Profit)
	}
	if result.ExercisedQuantity != 2 {
		t.Errorf("expected exercised quantity 2, got %d", result.ExercisedQuantity)
	}

	// Option holding should be deleted
	_, err = mocks.holdingRepo.GetByID(h.ID)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Error("expected option holding to be deleted")
	}

	// Stock holding should be created with strike price as avg cost
	stockHolding, err := mocks.holdingRepo.GetByUserAndSecurity(42, "stock", 500, 1)
	if err != nil {
		t.Fatalf("stock holding not found: %v", err)
	}
	if stockHolding.Quantity != 200 {
		t.Errorf("expected stock quantity 200, got %d", stockHolding.Quantity)
	}
	if !stockHolding.AveragePrice.Equal(decimal.NewFromFloat(100.00)) {
		t.Errorf("expected stock avg price 100, got %s", stockHolding.AveragePrice)
	}
	if stockHolding.Ticker != "AAPL" {
		t.Errorf("expected ticker AAPL, got %s", stockHolding.Ticker)
	}

	// Account should be debited: strike * shares = 100 * 200 = 20000
	if len(mocks.accountClient.updateBalCalls) < 1 {
		t.Fatal("expected at least 1 UpdateBalance call")
	}
	debitCall := mocks.accountClient.updateBalCalls[0]
	actualDebit, _ := decimal.NewFromString(debitCall.Amount)
	expectedDebit := decimal.NewFromFloat(-20000.00)
	if !actualDebit.Equal(expectedDebit) {
		t.Errorf("expected debit %s, got %s", expectedDebit, actualDebit)
	}
}

func TestPortfolio_ExerciseOption_CallOTM(t *testing.T) {
	svc, mocks := buildPortfolioService()

	// Stock listing: current price $80 (below strike)
	stkListing := stockListing(10, 500, 80.00)
	mocks.listingRepo.addListing(stkListing)

	mocks.optionRepo.addOption(&model.Option{
		ID:             1,
		OptionType:     "call",
		StockID:        500,
		StrikePrice:    decimal.NewFromFloat(100.00),
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
	})

	h := &model.Holding{
		UserID:       42,
		SecurityType: "option",
		SecurityID:   1,
		Quantity:     1,
		AveragePrice: decimal.NewFromFloat(3.00),
		AccountID:    1,
	}
	mocks.holdingRepo.addHolding(h)

	_, err := svc.ExerciseOption(h.ID, 42)
	if err == nil {
		t.Fatal("expected error for OTM call option")
	}
	if err.Error() != "call option is not in the money" {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: ExerciseOption — Put ITM
// ---------------------------------------------------------------------------

func TestPortfolio_ExerciseOption_PutITM(t *testing.T) {
	svc, mocks := buildPortfolioService()

	// Stock listing: current price $80 (below strike of $100)
	stkListing := stockListing(10, 500, 80.00)
	stkListing.Exchange.Currency = "USD"
	mocks.listingRepo.addListing(stkListing)

	mocks.stockRepo.addStock(&model.Stock{ID: 500, Ticker: "AAPL", Name: "Apple Inc."})

	mocks.optionRepo.addOption(&model.Option{
		ID:             2,
		Ticker:         "AAPL240101P00100",
		OptionType:     "put",
		StockID:        500,
		StrikePrice:    decimal.NewFromFloat(100.00),
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
	})

	// Option holding: 1 contract
	optH := &model.Holding{
		UserID:        42,
		SystemType:    "employee",
		UserFirstName: "John",
		UserLastName:  "Doe",
		SecurityType:  "option",
		SecurityID:    2,
		ListingID:     10,
		Ticker:        "AAPL240101P00100",
		Quantity:      1,
		AveragePrice:  decimal.NewFromFloat(5.00),
		AccountID:     1,
	}
	mocks.holdingRepo.addHolding(optH)

	// User must hold enough stock: 1 contract * 100 shares = 100 shares needed
	stockH := &model.Holding{
		UserID:       42,
		SystemType:   "employee",
		SecurityType: "stock",
		SecurityID:   500,
		ListingID:    10,
		Ticker:       "AAPL",
		Name:         "Apple Inc.",
		Quantity:     150,
		AveragePrice: decimal.NewFromFloat(90.00),
		AccountID:    1,
	}
	mocks.holdingRepo.addHolding(stockH)

	result, err := svc.ExerciseOption(optH.ID, 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1 contract * 100 = 100 shares affected
	if result.SharesAffected != 100 {
		t.Errorf("expected 100 shares affected, got %d", result.SharesAffected)
	}
	// Profit = (100 - 80) * 100 = 2000
	expectedProfit := decimal.NewFromFloat(2000.00)
	if !result.Profit.Equal(expectedProfit) {
		t.Errorf("expected profit %s, got %s", expectedProfit, result.Profit)
	}

	// Stock holding should decrease: 150 - 100 = 50
	updatedStock, _ := mocks.holdingRepo.GetByID(stockH.ID)
	if updatedStock.Quantity != 50 {
		t.Errorf("expected stock quantity 50, got %d", updatedStock.Quantity)
	}

	// Capital gain: (strike - avg_cost) * shares = (100 - 90) * 100 = 1000
	if len(mocks.capitalGainRepo.gains) != 1 {
		t.Fatalf("expected 1 capital gain, got %d", len(mocks.capitalGainRepo.gains))
	}
	gain := mocks.capitalGainRepo.gains[0]
	expectedGain := decimal.NewFromFloat(1000.00)
	if !gain.TotalGain.Equal(expectedGain) {
		t.Errorf("expected capital gain %s, got %s", expectedGain, gain.TotalGain)
	}

	// Option holding should be deleted
	_, err = mocks.holdingRepo.GetByID(optH.ID)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Error("expected option holding to be deleted")
	}

	// Account should be credited: strike * shares = 100 * 100 = 10000
	if len(mocks.accountClient.updateBalCalls) < 1 {
		t.Fatal("expected at least 1 UpdateBalance call")
	}
	creditCall := mocks.accountClient.updateBalCalls[0]
	actualCredit, _ := decimal.NewFromString(creditCall.Amount)
	expectedCredit := decimal.NewFromFloat(10000.00)
	if !actualCredit.Equal(expectedCredit) {
		t.Errorf("expected credit %s, got %s", expectedCredit, actualCredit)
	}
}

func TestPortfolio_ExerciseOption_PutOTM(t *testing.T) {
	svc, mocks := buildPortfolioService()

	// Stock price $120 > strike $100 → OTM for put
	stkListing := stockListing(10, 500, 120.00)
	mocks.listingRepo.addListing(stkListing)

	mocks.optionRepo.addOption(&model.Option{
		ID:             2,
		OptionType:     "put",
		StockID:        500,
		StrikePrice:    decimal.NewFromFloat(100.00),
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
	})

	h := &model.Holding{
		UserID:       42,
		SecurityType: "option",
		SecurityID:   2,
		Quantity:     1,
		AveragePrice: decimal.NewFromFloat(5.00),
		AccountID:    1,
	}
	mocks.holdingRepo.addHolding(h)

	_, err := svc.ExerciseOption(h.ID, 42)
	if err == nil {
		t.Fatal("expected error for OTM put option")
	}
	if err.Error() != "put option is not in the money" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPortfolio_ExerciseOption_PutInsufficientStock(t *testing.T) {
	svc, mocks := buildPortfolioService()

	// Stock price $80 < strike $100 → ITM
	stkListing := stockListing(10, 500, 80.00)
	mocks.listingRepo.addListing(stkListing)

	mocks.stockRepo.addStock(&model.Stock{ID: 500, Ticker: "AAPL", Name: "Apple Inc."})

	mocks.optionRepo.addOption(&model.Option{
		ID:             2,
		OptionType:     "put",
		StockID:        500,
		StrikePrice:    decimal.NewFromFloat(100.00),
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
	})

	// Option holding: 2 contracts = 200 shares needed
	h := &model.Holding{
		UserID:       42,
		SecurityType: "option",
		SecurityID:   2,
		Quantity:     2,
		AveragePrice: decimal.NewFromFloat(5.00),
		AccountID:    1,
	}
	mocks.holdingRepo.addHolding(h)

	// Only 50 shares of stock (need 200)
	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:       42,
		SecurityType: "stock",
		SecurityID:   500,
		Quantity:     50,
		AveragePrice: decimal.NewFromFloat(90.00),
		AccountID:    1,
	})

	_, err := svc.ExerciseOption(h.ID, 42)
	if err == nil {
		t.Fatal("expected error for insufficient stock")
	}
	if err.Error() != "insufficient stock holdings to exercise put option" {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: ExerciseOption — validation errors
// ---------------------------------------------------------------------------

func TestPortfolio_ExerciseOption_NotFound(t *testing.T) {
	svc, _ := buildPortfolioService()

	_, err := svc.ExerciseOption(999, 42)
	if err == nil {
		t.Fatal("expected error for non-existent holding")
	}
	if err.Error() != "holding not found" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPortfolio_ExerciseOption_WrongUser(t *testing.T) {
	svc, mocks := buildPortfolioService()

	h := &model.Holding{
		UserID:       42,
		SecurityType: "option",
		SecurityID:   1,
		Quantity:     1,
		AveragePrice: decimal.NewFromFloat(5.00),
		AccountID:    1,
	}
	mocks.holdingRepo.addHolding(h)

	_, err := svc.ExerciseOption(h.ID, 999) // wrong user
	if err == nil {
		t.Fatal("expected error for wrong user")
	}
	if err.Error() != "holding does not belong to user" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPortfolio_ExerciseOption_NotAnOption(t *testing.T) {
	svc, mocks := buildPortfolioService()

	h := &model.Holding{
		UserID:       42,
		SecurityType: "stock", // not an option
		SecurityID:   100,
		Quantity:     10,
		AveragePrice: decimal.NewFromFloat(50.00),
		AccountID:    1,
	}
	mocks.holdingRepo.addHolding(h)

	_, err := svc.ExerciseOption(h.ID, 42)
	if err == nil {
		t.Fatal("expected error for non-option holding")
	}
	if err.Error() != "holding is not an option" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPortfolio_ExerciseOption_Expired(t *testing.T) {
	svc, mocks := buildPortfolioService()

	mocks.optionRepo.addOption(&model.Option{
		ID:             1,
		OptionType:     "call",
		StockID:        500,
		StrikePrice:    decimal.NewFromFloat(100.00),
		SettlementDate: time.Now().Add(-24 * time.Hour), // expired
	})

	h := &model.Holding{
		UserID:       42,
		SecurityType: "option",
		SecurityID:   1,
		Quantity:     1,
		AveragePrice: decimal.NewFromFloat(5.00),
		AccountID:    1,
	}
	mocks.holdingRepo.addHolding(h)

	_, err := svc.ExerciseOption(h.ID, 42)
	if err == nil {
		t.Fatal("expected error for expired option")
	}
	if err.Error() != "option has expired (settlement date passed)" {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: Account interaction errors
// ---------------------------------------------------------------------------

func TestPortfolio_ProcessBuyFill_AccountDebitError(t *testing.T) {
	svc, mocks := buildPortfolioService()

	listing := stockListing(1, 100, 50.00)
	mocks.listingRepo.addListing(listing)
	mocks.stockRepo.addStock(&model.Stock{ID: 100, Ticker: "AAPL", Name: "Apple Inc."})
	mocks.accountClient.updateBalErr = errors.New("insufficient balance")

	order := &model.Order{
		ID: 1, UserID: 42, SystemType: "employee", ListingID: 1,
		SecurityType: "stock", Ticker: "AAPL", Direction: "buy",
		Quantity: 10, Commission: decimal.NewFromFloat(1.00), AccountID: 1,
	}
	txn := &model.OrderTransaction{
		ID: 1, OrderID: 1, Quantity: 10,
		PricePerUnit: decimal.NewFromFloat(50.00),
		TotalPrice:   decimal.NewFromFloat(500.00),
	}

	err := svc.ProcessBuyFill(order, txn)
	if err == nil {
		t.Fatal("expected error when account debit fails")
	}
	if err.Error() != "insufficient balance" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPortfolio_ProcessSellFill_AccountCreditError(t *testing.T) {
	svc, mocks := buildPortfolioService()

	listing := stockListing(1, 100, 60.00)
	listing.Exchange.Currency = "USD"
	mocks.listingRepo.addListing(listing)
	mocks.accountClient.updateBalErr = errors.New("account service unavailable")

	mocks.holdingRepo.addHolding(&model.Holding{
		UserID: 42, SystemType: "employee", SecurityType: "stock",
		SecurityID: 100, ListingID: 1, Ticker: "AAPL",
		Quantity: 10, AveragePrice: decimal.NewFromFloat(50.00), AccountID: 1,
	})

	order := &model.Order{
		ID: 20, UserID: 42, SystemType: "employee", ListingID: 1,
		SecurityType: "stock", Ticker: "AAPL", Direction: "sell",
		Quantity: 5, Commission: decimal.Zero, AccountID: 1,
	}
	txn := &model.OrderTransaction{
		ID: 20, OrderID: 20, Quantity: 5,
		PricePerUnit: decimal.NewFromFloat(60.00),
		TotalPrice:   decimal.NewFromFloat(300.00),
	}

	err := svc.ProcessSellFill(order, txn)
	if err == nil {
		t.Fatal("expected error when account credit fails")
	}
}

// ---------------------------------------------------------------------------
// Tests: ExerciseOption — Call compensation on holding upsert failure
// ---------------------------------------------------------------------------

func TestPortfolio_ExerciseOption_Call_CompensatesOnUpsertFailure(t *testing.T) {
	svc, mocks := buildPortfolioService()

	// Stock listing: current price $150
	stkListing := stockListing(10, 500, 150.00)
	stkListing.Exchange.Currency = "USD"
	mocks.listingRepo.addListing(stkListing)

	mocks.stockRepo.addStock(&model.Stock{ID: 500, Ticker: "AAPL", Name: "Apple Inc."})

	mocks.optionRepo.addOption(&model.Option{
		ID:             1,
		Ticker:         "AAPL240101C00100",
		OptionType:     "call",
		StockID:        500,
		StrikePrice:    decimal.NewFromFloat(100.00),
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
	})

	h := &model.Holding{
		UserID:        42,
		SystemType:    "employee",
		UserFirstName: "John",
		UserLastName:  "Doe",
		SecurityType:  "option",
		SecurityID:    1,
		ListingID:     10,
		Ticker:        "AAPL240101C00100",
		Quantity:      1,
		AveragePrice:  decimal.NewFromFloat(5.00),
		AccountID:     1,
	}
	mocks.holdingRepo.addHolding(h)

	// Make holding upsert fail
	mocks.holdingRepo.failNextUpsert = errors.New("db connection lost")

	_, err := svc.ExerciseOption(h.ID, 42)
	if err == nil {
		t.Fatal("expected error when holding upsert fails")
	}

	// Verify compensation: debit was 100 * 100 = 10000 (negative),
	// compensation should re-credit 10000 (positive)
	// First call is the debit (-10000), second call is the compensation (+10000)
	if len(mocks.accountClient.updateBalCalls) != 2 {
		t.Fatalf("expected 2 UpdateBalance calls (debit + compensation), got %d", len(mocks.accountClient.updateBalCalls))
	}

	debitCall := mocks.accountClient.updateBalCalls[0]
	debitAmount, _ := decimal.NewFromString(debitCall.Amount)
	expectedDebit := decimal.NewFromFloat(-10000.00)
	if !debitAmount.Equal(expectedDebit) {
		t.Errorf("expected debit %s, got %s", expectedDebit, debitAmount)
	}

	compensateCall := mocks.accountClient.updateBalCalls[1]
	compensateAmount, _ := decimal.NewFromString(compensateCall.Amount)
	expectedCompensation := decimal.NewFromFloat(10000.00)
	if !compensateAmount.Equal(expectedCompensation) {
		t.Errorf("expected compensation credit %s, got %s", expectedCompensation, compensateAmount)
	}
}

// ---------------------------------------------------------------------------
// Tests: ExerciseOption — Put compensation on holding update failure
// ---------------------------------------------------------------------------

func TestPortfolio_ExerciseOption_Put_CompensatesOnHoldingUpdateFailure(t *testing.T) {
	svc, mocks := buildPortfolioService()

	// Stock listing: current price $80 (below strike $100)
	stkListing := stockListing(10, 500, 80.00)
	stkListing.Exchange.Currency = "USD"
	mocks.listingRepo.addListing(stkListing)

	mocks.stockRepo.addStock(&model.Stock{ID: 500, Ticker: "AAPL", Name: "Apple Inc."})

	mocks.optionRepo.addOption(&model.Option{
		ID:             2,
		Ticker:         "AAPL240101P00100",
		OptionType:     "put",
		StockID:        500,
		StrikePrice:    decimal.NewFromFloat(100.00),
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
	})

	optH := &model.Holding{
		UserID:        42,
		SystemType:    "employee",
		UserFirstName: "John",
		UserLastName:  "Doe",
		SecurityType:  "option",
		SecurityID:    2,
		ListingID:     10,
		Ticker:        "AAPL240101P00100",
		Quantity:      1,
		AveragePrice:  decimal.NewFromFloat(5.00),
		AccountID:     1,
	}
	mocks.holdingRepo.addHolding(optH)

	// Stock holding: 150 shares at $90
	stockH := &model.Holding{
		UserID:       42,
		SystemType:   "employee",
		SecurityType: "stock",
		SecurityID:   500,
		ListingID:    10,
		Ticker:       "AAPL",
		Name:         "Apple Inc.",
		Quantity:     150,
		AveragePrice: decimal.NewFromFloat(90.00),
		AccountID:    1,
	}
	mocks.holdingRepo.addHolding(stockH)

	// Make holding update fail (stock holding decrease step)
	mocks.holdingRepo.failNextUpdate = errors.New("optimistic lock conflict")

	_, err := svc.ExerciseOption(optH.ID, 42)
	if err == nil {
		t.Fatal("expected error when stock holding update fails")
	}

	// Verify compensation: credit was 100 * 100 = 10000 (positive),
	// compensation should re-debit 10000 (negative)
	// First call is the credit (+10000), second call is the compensation (-10000)
	if len(mocks.accountClient.updateBalCalls) != 2 {
		t.Fatalf("expected 2 UpdateBalance calls (credit + compensation), got %d", len(mocks.accountClient.updateBalCalls))
	}

	creditCall := mocks.accountClient.updateBalCalls[0]
	creditAmount, _ := decimal.NewFromString(creditCall.Amount)
	expectedCredit := decimal.NewFromFloat(10000.00)
	if !creditAmount.Equal(expectedCredit) {
		t.Errorf("expected credit %s, got %s", expectedCredit, creditAmount)
	}

	compensateCall := mocks.accountClient.updateBalCalls[1]
	compensateAmount, _ := decimal.NewFromString(compensateCall.Amount)
	expectedCompensation := decimal.NewFromFloat(-10000.00)
	if !compensateAmount.Equal(expectedCompensation) {
		t.Errorf("expected compensation debit %s, got %s", expectedCompensation, compensateAmount)
	}
}

// ---------------------------------------------------------------------------
// Tests: ExerciseOption — Put compensation on capital gain creation failure
// ---------------------------------------------------------------------------

func TestPortfolio_ExerciseOption_Put_CompensatesOnCapitalGainFailure(t *testing.T) {
	svc, mocks := buildPortfolioService()

	stkListing := stockListing(10, 500, 80.00)
	stkListing.Exchange.Currency = "USD"
	mocks.listingRepo.addListing(stkListing)

	mocks.stockRepo.addStock(&model.Stock{ID: 500, Ticker: "AAPL", Name: "Apple Inc."})

	mocks.optionRepo.addOption(&model.Option{
		ID:             2,
		Ticker:         "AAPL240101P00100",
		OptionType:     "put",
		StockID:        500,
		StrikePrice:    decimal.NewFromFloat(100.00),
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
	})

	optH := &model.Holding{
		UserID:        42,
		SystemType:    "employee",
		UserFirstName: "John",
		UserLastName:  "Doe",
		SecurityType:  "option",
		SecurityID:    2,
		ListingID:     10,
		Ticker:        "AAPL240101P00100",
		Quantity:      1,
		AveragePrice:  decimal.NewFromFloat(5.00),
		AccountID:     1,
	}
	mocks.holdingRepo.addHolding(optH)

	stockH := &model.Holding{
		UserID:       42,
		SystemType:   "employee",
		SecurityType: "stock",
		SecurityID:   500,
		ListingID:    10,
		Ticker:       "AAPL",
		Name:         "Apple Inc.",
		Quantity:     150,
		AveragePrice: decimal.NewFromFloat(90.00),
		AccountID:    1,
	}
	mocks.holdingRepo.addHolding(stockH)

	// Make capital gain creation fail (after credit and holding update succeed)
	mocks.capitalGainRepo.failNextCreate = errors.New("capital gain db error")

	_, err := svc.ExerciseOption(optH.ID, 42)
	if err == nil {
		t.Fatal("expected error when capital gain creation fails")
	}

	// Verify compensation: credit was 100*100=10000, re-debit should happen
	// First call = credit (+10000), second call = compensation debit (-10000)
	if len(mocks.accountClient.updateBalCalls) != 2 {
		t.Fatalf("expected 2 UpdateBalance calls (credit + compensation), got %d", len(mocks.accountClient.updateBalCalls))
	}

	compensateCall := mocks.accountClient.updateBalCalls[1]
	compensateAmount, _ := decimal.NewFromString(compensateCall.Amount)
	expectedCompensation := decimal.NewFromFloat(-10000.00)
	if !compensateAmount.Equal(expectedCompensation) {
		t.Errorf("expected compensation debit %s, got %s", expectedCompensation, compensateAmount)
	}
}
