package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	stockgrpc "github.com/exbanka/stock-service/internal/grpc"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

// mockOrderRepo is an in-memory order repository mock.
type mockOrderRepo struct {
	orders     map[uint64]*model.Order
	nextID     uint64
	deletedIDs []uint64
	// createErr, if non-nil, is returned from Create to simulate a DB failure
	// during persist_order_pending.
	createErr error
}

func newMockOrderRepo() *mockOrderRepo {
	return &mockOrderRepo{orders: make(map[uint64]*model.Order), nextID: 1}
}

func (m *mockOrderRepo) Create(order *model.Order) error {
	if m.createErr != nil {
		return m.createErr
	}
	order.ID = m.nextID
	m.nextID++
	stored := *order
	m.orders[order.ID] = &stored
	return nil
}

func (m *mockOrderRepo) GetByID(id uint64) (*model.Order, error) {
	o, ok := m.orders[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	copy := *o
	return &copy, nil
}

func (m *mockOrderRepo) GetByIDWithOwner(id, userID uint64, systemType string) (*model.Order, error) {
	o, ok := m.orders[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	if o.UserID != userID || o.SystemType != systemType {
		return nil, gorm.ErrRecordNotFound
	}
	copy := *o
	return &copy, nil
}

func (m *mockOrderRepo) Update(order *model.Order) error {
	if _, ok := m.orders[order.ID]; !ok {
		return gorm.ErrRecordNotFound
	}
	stored := *order
	m.orders[order.ID] = &stored
	return nil
}

func (m *mockOrderRepo) Delete(id uint64) error {
	m.deletedIDs = append(m.deletedIDs, id)
	delete(m.orders, id)
	return nil
}

func (m *mockOrderRepo) ListByUser(userID uint64, systemType string, filter repository.OrderFilter) ([]model.Order, int64, error) {
	var result []model.Order
	for _, o := range m.orders {
		if o.UserID == userID && o.SystemType == systemType {
			result = append(result, *o)
		}
	}
	return result, int64(len(result)), nil
}

func (m *mockOrderRepo) ListAll(filter repository.OrderFilter) ([]model.Order, int64, error) {
	var result []model.Order
	for _, o := range m.orders {
		result = append(result, *o)
	}
	return result, int64(len(result)), nil
}

func (m *mockOrderRepo) ListActiveApproved() ([]model.Order, error) {
	var result []model.Order
	for _, o := range m.orders {
		if o.Status == "approved" && !o.IsDone {
			result = append(result, *o)
		}
	}
	return result, nil
}

// mockOrderTxRepo is an in-memory order transaction repo.
type mockOrderTxRepo struct {
	txns []model.OrderTransaction
}

func newMockOrderTxRepo() *mockOrderTxRepo {
	return &mockOrderTxRepo{}
}

func (m *mockOrderTxRepo) Create(tx *model.OrderTransaction) error {
	m.txns = append(m.txns, *tx)
	return nil
}

// Update persists a modified OrderTransaction back to the in-memory store.
// Used by the fill saga's convert_amount step to record native/converted
// amounts and FX rate.
func (m *mockOrderTxRepo) Update(tx *model.OrderTransaction) error {
	for i, existing := range m.txns {
		if existing.ID == tx.ID {
			m.txns[i] = *tx
			return nil
		}
	}
	m.txns = append(m.txns, *tx)
	return nil
}

func (m *mockOrderTxRepo) ListByOrderID(orderID uint64) ([]model.OrderTransaction, error) {
	var result []model.OrderTransaction
	for _, t := range m.txns {
		if t.OrderID == orderID {
			result = append(result, t)
		}
	}
	return result, nil
}

// mockListingRepo returns a pre-configured listing.
type mockListingRepo struct {
	listings map[uint64]*model.Listing
}

func newMockListingRepo() *mockListingRepo {
	return &mockListingRepo{listings: make(map[uint64]*model.Listing)}
}

func (m *mockListingRepo) addListing(l *model.Listing) {
	m.listings[l.ID] = l
}

func (m *mockListingRepo) Create(listing *model.Listing) error {
	m.listings[listing.ID] = listing
	return nil
}

func (m *mockListingRepo) GetByID(id uint64) (*model.Listing, error) {
	l, ok := m.listings[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	copy := *l
	return &copy, nil
}

func (m *mockListingRepo) GetBySecurityIDAndType(securityID uint64, securityType string) (*model.Listing, error) {
	for _, l := range m.listings {
		if l.SecurityID == securityID && l.SecurityType == securityType {
			return l, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockListingRepo) ListBySecurityIDsAndType(securityIDs []uint64, securityType string) ([]model.Listing, error) {
	idSet := make(map[uint64]struct{}, len(securityIDs))
	for _, id := range securityIDs {
		idSet[id] = struct{}{}
	}
	var out []model.Listing
	for _, l := range m.listings {
		if l.SecurityType != securityType {
			continue
		}
		if _, ok := idSet[l.SecurityID]; ok {
			out = append(out, *l)
		}
	}
	return out, nil
}

func (m *mockListingRepo) Update(listing *model.Listing) error     { return nil }
func (m *mockListingRepo) UpsertBySecurity(l *model.Listing) error { return nil }
func (m *mockListingRepo) UpsertForOption(l *model.Listing) (*model.Listing, error) {
	return l, nil
}
func (m *mockListingRepo) ListAll() ([]model.Listing, error) { return nil, nil }
func (m *mockListingRepo) ListBySecurityType(t string) ([]model.Listing, error) {
	return nil, nil
}
func (m *mockListingRepo) UpdatePriceByTicker(securityType, ticker string, price, high, low decimal.Decimal) error {
	return nil
}

// mockSettingRepo returns configurable key-value pairs.
type mockSettingRepo struct {
	data map[string]string
}

func newMockSettingRepo() *mockSettingRepo {
	return &mockSettingRepo{data: make(map[string]string)}
}

func (m *mockSettingRepo) Get(key string) (string, error) {
	v, ok := m.data[key]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func (m *mockSettingRepo) Set(key, value string) error {
	m.data[key] = value
	return nil
}

// mockSecurityLookupRepo returns a configurable settlement date.
type mockSecurityLookupRepo struct {
	settlementDate time.Time
	err            error
}

func (m *mockSecurityLookupRepo) GetFuturesSettlementDate(securityID uint64) (time.Time, error) {
	return m.settlementDate, m.err
}

// mockProducer records published events.
type mockProducer struct {
	created   int
	approved  int
	declined  int
	cancelled int
}

func (m *mockProducer) PublishOrderCreated(ctx context.Context, msg interface{}) error {
	m.created++
	return nil
}

func (m *mockProducer) PublishOrderApproved(ctx context.Context, msg interface{}) error {
	m.approved++
	return nil
}

func (m *mockProducer) PublishOrderDeclined(ctx context.Context, msg interface{}) error {
	m.declined++
	return nil
}

func (m *mockProducer) PublishOrderCancelled(ctx context.Context, msg interface{}) error {
	m.cancelled++
	return nil
}

// ---------------------------------------------------------------------------
// Placement-saga test doubles (Task 12)
// ---------------------------------------------------------------------------

// mockSagaRepo records saga steps in-memory.
type mockSagaRepo struct {
	rows []*model.SagaLog
}

func newMockSagaRepo() *mockSagaRepo { return &mockSagaRepo{} }

func (r *mockSagaRepo) RecordStep(log *model.SagaLog) error {
	log.ID = uint64(len(r.rows) + 1)
	rowCopy := *log
	r.rows = append(r.rows, &rowCopy)
	return nil
}

func (r *mockSagaRepo) UpdateStatus(id uint64, version int64, newStatus, errMsg string) error {
	for _, row := range r.rows {
		if row.ID == id {
			row.Status = newStatus
			row.ErrorMessage = errMsg
			return nil
		}
	}
	return errors.New("not found")
}

// GetByStepName returns the most recent saga row matching (orderID, stepName).
// Satisfies the FillSagaLogRepo interface the fill saga's compensation path
// uses to link its compensation row to the forward settle_reservation step.
func (r *mockSagaRepo) GetByStepName(orderID uint64, stepName string) (*model.SagaLog, error) {
	var match *model.SagaLog
	for _, row := range r.rows {
		if row.OrderID == orderID && row.StepName == stepName {
			if match == nil || row.StepNumber > match.StepNumber {
				match = row
			}
		}
	}
	if match == nil {
		return nil, errors.New("not found")
	}
	return match, nil
}

// mockStubAccountClient implements the accountpb.AccountServiceClient interface
// (just the two methods CreateOrder touches). Separate from the portfolio
// service's mock to keep concerns scoped.
type mockStubAccountClient struct {
	// Per-account metadata consulted by GetAccount.
	accountCcy map[uint64]string
	// getAccountErr, when non-nil, causes GetAccount to fail.
	getAccountErr error
}

func newMockStubAccountClient() *mockStubAccountClient {
	return &mockStubAccountClient{accountCcy: make(map[uint64]string)}
}

func (m *mockStubAccountClient) GetAccount(_ context.Context, req *accountpb.GetAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if m.getAccountErr != nil {
		return nil, m.getAccountErr
	}
	ccy, ok := m.accountCcy[req.Id]
	if !ok {
		return nil, errors.New("account not found")
	}
	return &accountpb.AccountResponse{Id: req.Id, CurrencyCode: ccy}, nil
}

// Remaining AccountServiceClient methods — unused stubs to satisfy the interface.
func (m *mockStubAccountClient) CreateAccount(context.Context, *accountpb.CreateAccountRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) GetAccountByNumber(context.Context, *accountpb.GetAccountByNumberRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) ListAccountsByClient(context.Context, *accountpb.ListAccountsByClientRequest, ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) ListAllAccounts(context.Context, *accountpb.ListAllAccountsRequest, ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) UpdateAccountName(context.Context, *accountpb.UpdateAccountNameRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) UpdateAccountLimits(context.Context, *accountpb.UpdateAccountLimitsRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) UpdateAccountStatus(context.Context, *accountpb.UpdateAccountStatusRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) UpdateBalance(context.Context, *accountpb.UpdateBalanceRequest, ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) CreateCompany(context.Context, *accountpb.CreateCompanyRequest, ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) GetCompany(context.Context, *accountpb.GetCompanyRequest, ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) UpdateCompany(context.Context, *accountpb.UpdateCompanyRequest, ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) ListCurrencies(context.Context, *accountpb.ListCurrenciesRequest, ...grpc.CallOption) (*accountpb.ListCurrenciesResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) GetCurrency(context.Context, *accountpb.GetCurrencyRequest, ...grpc.CallOption) (*accountpb.CurrencyResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) GetLedgerEntries(context.Context, *accountpb.GetLedgerEntriesRequest, ...grpc.CallOption) (*accountpb.GetLedgerEntriesResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) ReserveFunds(context.Context, *accountpb.ReserveFundsRequest, ...grpc.CallOption) (*accountpb.ReserveFundsResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) ReleaseReservation(context.Context, *accountpb.ReleaseReservationRequest, ...grpc.CallOption) (*accountpb.ReleaseReservationResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) PartialSettleReservation(context.Context, *accountpb.PartialSettleReservationRequest, ...grpc.CallOption) (*accountpb.PartialSettleReservationResponse, error) {
	return nil, nil
}
func (m *mockStubAccountClient) GetReservation(context.Context, *accountpb.GetReservationRequest, ...grpc.CallOption) (*accountpb.GetReservationResponse, error) {
	return nil, nil
}

// fakeAccountClient implements AccountClientAPI (the narrow interface
// OrderService depends on).
type fakeAccountClient struct {
	stub          *mockStubAccountClient
	reserveCalls  []reserveCall
	releaseCalls  []uint64
	reserveErr    error
	releaseErr    error
}

type reserveCall struct {
	AccountID uint64
	OrderID   uint64
	Amount    decimal.Decimal
	Currency  string
}

func newFakeAccountClient() *fakeAccountClient {
	return &fakeAccountClient{stub: newMockStubAccountClient()}
}

func (f *fakeAccountClient) ReserveFunds(_ context.Context, accountID, orderID uint64, amount decimal.Decimal, currencyCode string) (*accountpb.ReserveFundsResponse, error) {
	if f.reserveErr != nil {
		return nil, f.reserveErr
	}
	f.reserveCalls = append(f.reserveCalls, reserveCall{AccountID: accountID, OrderID: orderID, Amount: amount, Currency: currencyCode})
	return &accountpb.ReserveFundsResponse{}, nil
}

func (f *fakeAccountClient) ReleaseReservation(_ context.Context, orderID uint64) (*accountpb.ReleaseReservationResponse, error) {
	if f.releaseErr != nil {
		return nil, f.releaseErr
	}
	f.releaseCalls = append(f.releaseCalls, orderID)
	return &accountpb.ReleaseReservationResponse{}, nil
}

func (f *fakeAccountClient) Stub() accountpb.AccountServiceClient { return f.stub }

// fakeExchangeClient implements exchangepb.ExchangeServiceClient (Convert only;
// other methods return errors because the placement saga never calls them).
type fakeExchangeClient struct {
	// convertResponses is keyed by "FROM/TO" and returns (amount, rate).
	convertResponses map[string]exchangeQuote
	convertCalls     []convertCall
}

type exchangeQuote struct {
	Amount decimal.Decimal
	Rate   decimal.Decimal
}

type convertCall struct {
	From   string
	To     string
	Amount string
}

func newFakeExchangeClient() *fakeExchangeClient {
	return &fakeExchangeClient{convertResponses: make(map[string]exchangeQuote)}
}

func (f *fakeExchangeClient) setRate(from, to string, amount, rate decimal.Decimal) {
	f.convertResponses[from+"/"+to] = exchangeQuote{Amount: amount, Rate: rate}
}

func (f *fakeExchangeClient) Convert(_ context.Context, req *exchangepb.ConvertRequest, _ ...grpc.CallOption) (*exchangepb.ConvertResponse, error) {
	f.convertCalls = append(f.convertCalls, convertCall{From: req.FromCurrency, To: req.ToCurrency, Amount: req.Amount})
	quote, ok := f.convertResponses[req.FromCurrency+"/"+req.ToCurrency]
	if !ok {
		return nil, errors.New("no rate configured for " + req.FromCurrency + "/" + req.ToCurrency)
	}
	return &exchangepb.ConvertResponse{
		ConvertedAmount: quote.Amount.String(),
		EffectiveRate:   quote.Rate.String(),
	}, nil
}

func (f *fakeExchangeClient) ListRates(context.Context, *exchangepb.ListRatesRequest, ...grpc.CallOption) (*exchangepb.ListRatesResponse, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeExchangeClient) GetRate(context.Context, *exchangepb.GetRateRequest, ...grpc.CallOption) (*exchangepb.RateResponse, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeExchangeClient) Calculate(context.Context, *exchangepb.CalculateRequest, ...grpc.CallOption) (*exchangepb.CalculateResponse, error) {
	return nil, errors.New("not implemented")
}

// fakeHoldingReservation implements HoldingReservationAPI.
type fakeHoldingReservation struct {
	reserveCalls []holdingReserveCall
	releaseCalls []uint64
	reserveErr   error
}

type holdingReserveCall struct {
	UserID       uint64
	SystemType   string
	SecurityType string
	SecurityID   uint64
	OrderID      uint64
	Qty          int64
}

func newFakeHoldingReservation() *fakeHoldingReservation {
	return &fakeHoldingReservation{}
}

func (f *fakeHoldingReservation) Reserve(_ context.Context, userID uint64, systemType, securityType string,
	securityID, orderID uint64, qty int64) (*ReserveHoldingResult, error) {
	if f.reserveErr != nil {
		return nil, f.reserveErr
	}
	f.reserveCalls = append(f.reserveCalls, holdingReserveCall{
		UserID: userID, SystemType: systemType, SecurityType: securityType,
		SecurityID: securityID, OrderID: orderID, Qty: qty,
	})
	return &ReserveHoldingResult{ReservationID: orderID, ReservedQuantity: qty, AvailableQuantity: 0}, nil
}

func (f *fakeHoldingReservation) Release(_ context.Context, orderID uint64) (*ReleaseHoldingResult, error) {
	f.releaseCalls = append(f.releaseCalls, orderID)
	return &ReleaseHoldingResult{ReleasedQuantity: 0, ReservedQuantity: 0}, nil
}

// fakeForexRepo implements ForexPairLookup.
type fakeForexRepo struct {
	pairs map[uint64]*model.ForexPair
}

func newFakeForexRepo() *fakeForexRepo { return &fakeForexRepo{pairs: make(map[uint64]*model.ForexPair)} }

func (f *fakeForexRepo) add(p *model.ForexPair) { f.pairs[p.ID] = p }

func (f *fakeForexRepo) GetByID(id uint64) (*model.ForexPair, error) {
	p, ok := f.pairs[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return p, nil
}

// fakeActuaryClient implements ActuaryClientAPI for placement-saga tests.
// Stores a per-employee ActuaryLimitInfo and records increment/decrement
// calls so tests can assert used_limit accounting.
type fakeActuaryClient struct {
	infos             map[uint64]*stockgrpc.ActuaryLimitInfo
	getErr            error
	incrementCalls    []limitCall
	decrementCalls    []limitCall
	incrementErr      error
	decrementErr      error
}

type limitCall struct {
	ActuaryID uint64
	Amount    decimal.Decimal
}

func newFakeActuaryClient() *fakeActuaryClient {
	return &fakeActuaryClient{infos: make(map[uint64]*stockgrpc.ActuaryLimitInfo)}
}

func (f *fakeActuaryClient) setInfo(employeeID uint64, actuaryID uint64, limit, used decimal.Decimal, needApproval bool) {
	f.infos[employeeID] = &stockgrpc.ActuaryLimitInfo{
		ID:           actuaryID,
		EmployeeID:   employeeID,
		Limit:        limit,
		UsedLimit:    used,
		NeedApproval: needApproval,
	}
}

func (f *fakeActuaryClient) GetActuaryLimit(_ context.Context, employeeID uint64) (*stockgrpc.ActuaryLimitInfo, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	info, ok := f.infos[employeeID]
	if !ok {
		return nil, stockgrpc.ErrActuaryNotFound
	}
	copy := *info
	return &copy, nil
}

func (f *fakeActuaryClient) IncrementUsedLimit(_ context.Context, actuaryID uint64, amountRSD decimal.Decimal) error {
	if f.incrementErr != nil {
		return f.incrementErr
	}
	f.incrementCalls = append(f.incrementCalls, limitCall{ActuaryID: actuaryID, Amount: amountRSD})
	// Apply the delta to every tracked info whose ID matches so subsequent
	// GetActuaryLimit calls see the new used_limit.
	for _, info := range f.infos {
		if info.ID == actuaryID {
			info.UsedLimit = info.UsedLimit.Add(amountRSD)
		}
	}
	return nil
}

func (f *fakeActuaryClient) DecrementUsedLimit(_ context.Context, actuaryID uint64, amountRSD decimal.Decimal) error {
	if f.decrementErr != nil {
		return f.decrementErr
	}
	f.decrementCalls = append(f.decrementCalls, limitCall{ActuaryID: actuaryID, Amount: amountRSD})
	for _, info := range f.infos {
		if info.ID == actuaryID {
			info.UsedLimit = info.UsedLimit.Sub(amountRSD)
			if info.UsedLimit.IsNegative() {
				info.UsedLimit = decimal.Zero
			}
		}
	}
	return nil
}

// fakeOrderSettings returns deterministic values for commission/slippage so the
// tests can assert exact reserved amounts.
type fakeOrderSettings struct {
	commission decimal.Decimal
	slippage   decimal.Decimal
}

func newFakeOrderSettings() *fakeOrderSettings {
	return &fakeOrderSettings{
		commission: decimal.NewFromFloat(0.0025),
		slippage:   decimal.NewFromFloat(0.05),
	}
}

func (f *fakeOrderSettings) CommissionRate() decimal.Decimal   { return f.commission }
func (f *fakeOrderSettings) MarketSlippagePct() decimal.Decimal { return f.slippage }

// ---------------------------------------------------------------------------
// Fixture builders
// ---------------------------------------------------------------------------

// rsdListing creates a stock listing at price 100 with an RSD-denominated
// exchange so actuary-limit tests don't need an exchange-service rate
// configured.
func rsdListing(id uint64) *model.Listing {
	return &model.Listing{
		ID:           id,
		SecurityID:   100,
		SecurityType: "stock",
		ExchangeID:   1,
		Exchange: model.StockExchange{
			ID:        1,
			Name:      "BELEX",
			Acronym:   "BELEX",
			Currency:  "RSD",
			TimeZone:  "+1",
			OpenTime:  "09:30",
			CloseTime: "16:00",
		},
		Price: decimal.NewFromInt(100),
		High:  decimal.NewFromInt(100),
		Low:   decimal.NewFromInt(100),
	}
}

// defaultListing creates a stock listing at price 100 with a USD exchange.
func defaultListing(id uint64) *model.Listing {
	return &model.Listing{
		ID:           id,
		SecurityID:   100,
		SecurityType: "stock",
		ExchangeID:   1,
		Exchange: model.StockExchange{
			ID:        1,
			Name:      "NYSE",
			Acronym:   "NYSE",
			Currency:  "USD",
			TimeZone:  "-5",
			OpenTime:  "09:30",
			CloseTime: "16:00",
		},
		Price: decimal.NewFromInt(100),
		High:  decimal.NewFromInt(100),
		Low:   decimal.NewFromInt(100),
	}
}

// orderServiceFixture bundles all test doubles for placement-saga tests.
type orderServiceFixture struct {
	svc            *OrderService
	orderRepo      *mockOrderRepo
	listingRepo    *mockListingRepo
	settingRepo    *mockSettingRepo
	securityRepo   *mockSecurityLookupRepo
	producer       *mockProducer
	sagaRepo       *mockSagaRepo
	accountClient  *fakeAccountClient
	exchangeClient *fakeExchangeClient
	holdingSvc     *fakeHoldingReservation
	forexRepo      *fakeForexRepo
	settings       *fakeOrderSettings
}

func newOrderServiceFixture() *orderServiceFixture {
	fx := &orderServiceFixture{
		orderRepo:      newMockOrderRepo(),
		listingRepo:    newMockListingRepo(),
		settingRepo:    newMockSettingRepo(),
		securityRepo:   &mockSecurityLookupRepo{},
		producer:       &mockProducer{},
		sagaRepo:       newMockSagaRepo(),
		accountClient:  newFakeAccountClient(),
		exchangeClient: newFakeExchangeClient(),
		holdingSvc:     newFakeHoldingReservation(),
		forexRepo:      newFakeForexRepo(),
		settings:       newFakeOrderSettings(),
	}
	// Enable testing mode so isAfterHours is skipped.
	_ = fx.settingRepo.Set("testing_mode", "true")

	fx.svc = NewOrderService(
		fx.orderRepo, newMockOrderTxRepo(), fx.listingRepo, fx.settingRepo,
		fx.securityRepo, fx.producer,
		fx.sagaRepo, fx.accountClient, fx.exchangeClient, fx.holdingSvc,
		fx.forexRepo, fx.settings,
	)
	return fx
}

// buildService is a back-compat helper retained for the non-CreateOrder tests
// (Approve/Decline/Cancel/GetOrder). It wires up a full OrderService via the
// fixture and returns the shared handles.
func buildService() (*OrderService, *mockOrderRepo, *mockListingRepo, *mockSettingRepo, *mockSecurityLookupRepo, *mockProducer, *fakeAccountClient) {
	fx := newOrderServiceFixture()
	// Seed a USD account so same-currency buys don't trip up.
	fx.accountClient.stub.accountCcy[1] = "USD"
	return fx.svc, fx.orderRepo, fx.listingRepo, fx.settingRepo, fx.securityRepo, fx.producer, fx.accountClient
}

// createDefaultOrder creates a simple market buy order via the service for the
// status-transition tests. Same-currency USD buy, qty=10.
func createDefaultOrder(t *testing.T, svc *OrderService, listingRepo *mockListingRepo) *model.Order {
	t.Helper()
	listing := defaultListing(1)
	listingRepo.addListing(listing)

	order, err := svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID:     42,
		SystemType: "employee",
		ListingID:  1,
		Direction:  "buy",
		OrderType:  "limit",
		Quantity:   10,
		LimitValue: ptrDec(100),
		AccountID:  1,
	})
	if err != nil {
		t.Fatalf("createDefaultOrder failed: %v", err)
	}
	return order
}

// ---------------------------------------------------------------------------
// Tests: CreateOrder — placement saga (Task 12)
// ---------------------------------------------------------------------------

func TestCreateOrder_Buy_Stock_SameCurrency_ReservesFunds(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(defaultListing(1))
	fx.accountClient.stub.accountCcy[77] = "USD" // same currency as listing

	order, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 5, SystemType: "employee", ListingID: 1, Direction: "buy",
		OrderType: "limit", Quantity: 10, LimitValue: ptrDec(100), AccountID: 77,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if order.Status != "approved" {
		t.Errorf("expected approved status after saga, got %s", order.Status)
	}
	if order.SagaID == "" {
		t.Error("saga_id not populated")
	}
	if order.ReservationAmount == nil {
		t.Fatal("reservation_amount not set")
	}
	if order.ReservationCurrency != "USD" {
		t.Errorf("reservation_currency: got %s want USD", order.ReservationCurrency)
	}
	if order.ReservationAccountID == nil || *order.ReservationAccountID != 77 {
		t.Errorf("reservation_account_id: got %v want 77", order.ReservationAccountID)
	}
	// Expect one ReserveFunds call on account 77 in USD.
	if len(fx.accountClient.reserveCalls) != 1 {
		t.Fatalf("expected 1 ReserveFunds call, got %d", len(fx.accountClient.reserveCalls))
	}
	rc := fx.accountClient.reserveCalls[0]
	if rc.AccountID != 77 || rc.Currency != "USD" {
		t.Errorf("unexpected reserve call: %+v", rc)
	}
	// Limit order: 10 * 100 * 1 * (1+0.0025) = 1002.5 USD (no slippage applied to limit orders).
	want := decimal.NewFromInt(1000).Mul(decimal.NewFromFloat(1.0025))
	if !rc.Amount.Equal(want) {
		t.Errorf("reserve amount: got %s want %s", rc.Amount.String(), want.String())
	}
	// Exchange-service NOT called for same-currency.
	if len(fx.exchangeClient.convertCalls) != 0 {
		t.Errorf("exchange Convert should not be called for same-currency, got %d calls", len(fx.exchangeClient.convertCalls))
	}
	// Order persisted with placement rate NIL.
	if order.PlacementRate != nil {
		t.Errorf("placement_rate should be nil for same-currency, got %v", order.PlacementRate)
	}
}

func TestCreateOrder_Buy_Stock_CrossCurrency_CallsConvert(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(defaultListing(1)) // USD listing
	fx.accountClient.stub.accountCcy[77] = "RSD" // cross-currency account
	// Listing.High is 100. Market buy, qty=10, contractSize=1:
	// native = 10 * 100 * 1 * (1+0.05) * (1+0.0025) = 1000 * 1.05 * 1.0025 = 1102.625 USD.
	// Configure exchange to return 110,262.5 RSD @ rate 100 RSD/USD.
	fx.exchangeClient.setRate("USD", "RSD", decimal.NewFromFloat(110262.5), decimal.NewFromInt(100))

	order, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 5, SystemType: "employee", ListingID: 1, Direction: "buy",
		OrderType: "market", Quantity: 10, AccountID: 77,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fx.exchangeClient.convertCalls) != 1 {
		t.Fatalf("expected 1 Convert call, got %d", len(fx.exchangeClient.convertCalls))
	}
	cc := fx.exchangeClient.convertCalls[0]
	if cc.From != "USD" || cc.To != "RSD" {
		t.Errorf("Convert call currencies: %+v", cc)
	}
	// Reserve should be in RSD for the converted amount.
	if len(fx.accountClient.reserveCalls) != 1 {
		t.Fatalf("expected 1 ReserveFunds call, got %d", len(fx.accountClient.reserveCalls))
	}
	rc := fx.accountClient.reserveCalls[0]
	if rc.Currency != "RSD" {
		t.Errorf("reserve currency: got %s want RSD", rc.Currency)
	}
	if !rc.Amount.Equal(decimal.NewFromFloat(110262.5)) {
		t.Errorf("reserve amount: got %s want 110262.5", rc.Amount.String())
	}
	if order.PlacementRate == nil || !order.PlacementRate.Equal(decimal.NewFromInt(100)) {
		t.Errorf("placement_rate: got %v want 100", order.PlacementRate)
	}
}

func TestCreateOrder_Buy_InsufficientFunds_RollsBack(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(defaultListing(1))
	fx.accountClient.stub.accountCcy[77] = "USD"
	// Force the ReserveFunds call to fail with FailedPrecondition.
	fx.accountClient.reserveErr = status.Error(codes.FailedPrecondition, "insufficient funds")

	_, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 5, SystemType: "employee", ListingID: 1, Direction: "buy",
		OrderType: "limit", Quantity: 10, LimitValue: ptrDec(100), AccountID: 77,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("code: got %s want FailedPrecondition", status.Code(err))
	}
	// The order row was persisted then compensated via Delete — no active rows left.
	if len(fx.orderRepo.orders) != 0 {
		t.Errorf("expected 0 active orders after rollback, got %d", len(fx.orderRepo.orders))
	}
	if len(fx.orderRepo.deletedIDs) != 1 {
		t.Errorf("expected 1 delete compensation, got %d", len(fx.orderRepo.deletedIDs))
	}
}

func TestCreateOrder_Forex_BuyHappyPath_NoConvertCall(t *testing.T) {
	fx := newOrderServiceFixture()
	// Forex listing: EUR/USD, pair-id=200, quote-currency USD.
	forexListing := &model.Listing{
		ID: 2, SecurityID: 200, SecurityType: "forex",
		ExchangeID: 1,
		Exchange:   model.StockExchange{ID: 1, Currency: "USD", TimeZone: "0"},
		Price:      decimal.NewFromFloat(1.10),
		High:       decimal.NewFromFloat(1.10),
	}
	fx.listingRepo.addListing(forexListing)
	fx.forexRepo.add(&model.ForexPair{
		ID: 200, Ticker: "EURUSD", BaseCurrency: "EUR", QuoteCurrency: "USD",
		ExchangeRate: decimal.NewFromFloat(1.10),
	})
	// Accounts: 77 is the user's USD (quote) account, 88 is the user's EUR (base) account.
	fx.accountClient.stub.accountCcy[77] = "USD"
	fx.accountClient.stub.accountCcy[88] = "EUR"

	baseAcct := uint64(88)
	order, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 5, SystemType: "employee", ListingID: 2, Direction: "buy",
		OrderType: "market", Quantity: 3, AccountID: 77, BaseAccountID: &baseAcct,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No exchange-service call for forex.
	if len(fx.exchangeClient.convertCalls) != 0 {
		t.Errorf("exchange Convert should not be called for forex, got %d", len(fx.exchangeClient.convertCalls))
	}
	if len(fx.accountClient.reserveCalls) != 1 {
		t.Fatalf("expected 1 ReserveFunds call, got %d", len(fx.accountClient.reserveCalls))
	}
	rc := fx.accountClient.reserveCalls[0]
	if rc.Currency != "USD" {
		t.Errorf("reserve currency: got %s want USD (quote)", rc.Currency)
	}
	if rc.AccountID != 77 {
		t.Errorf("reserve account: got %d want 77", rc.AccountID)
	}
	if order.BaseAccountID == nil || *order.BaseAccountID != 88 {
		t.Errorf("base_account_id on order: got %v want 88", order.BaseAccountID)
	}
	// Expected native: qty=3 * price=1.10 * contractSize=1000 * (1+0.05) * (1+0.0025)
	// = 3300 * 1.05 * 1.0025 = 3473.6625
	expected := decimal.NewFromInt(3300).Mul(decimal.NewFromFloat(1.05)).Mul(decimal.NewFromFloat(1.0025))
	if !rc.Amount.Equal(expected) {
		t.Errorf("reserve amount: got %s want %s", rc.Amount.String(), expected.String())
	}
}

func TestCreateOrder_Forex_MissingBaseAccount_Rejected(t *testing.T) {
	fx := newOrderServiceFixture()
	forexListing := &model.Listing{
		ID: 2, SecurityID: 200, SecurityType: "forex",
		Exchange: model.StockExchange{Currency: "USD", TimeZone: "0"},
		Price:    decimal.NewFromFloat(1.10),
		High:     decimal.NewFromFloat(1.10),
	}
	fx.listingRepo.addListing(forexListing)

	_, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 5, SystemType: "employee", ListingID: 2, Direction: "buy",
		OrderType: "market", Quantity: 3, AccountID: 77,
		// BaseAccountID intentionally nil.
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code: got %s want InvalidArgument", status.Code(err))
	}
	// No reservation and no order persisted.
	if len(fx.accountClient.reserveCalls) != 0 {
		t.Errorf("ReserveFunds should not be called, got %d", len(fx.accountClient.reserveCalls))
	}
	if len(fx.orderRepo.orders) != 0 {
		t.Errorf("expected 0 orders persisted, got %d", len(fx.orderRepo.orders))
	}
}

func TestCreateOrder_Forex_SellRejected(t *testing.T) {
	fx := newOrderServiceFixture()
	forexListing := &model.Listing{
		ID: 2, SecurityID: 200, SecurityType: "forex",
		Exchange: model.StockExchange{Currency: "USD", TimeZone: "0"},
		Price:    decimal.NewFromFloat(1.10),
	}
	fx.listingRepo.addListing(forexListing)

	baseAcct := uint64(88)
	_, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 5, SystemType: "employee", ListingID: 2, Direction: "sell",
		OrderType: "market", Quantity: 3, AccountID: 77, BaseAccountID: &baseAcct,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code: got %s want InvalidArgument", status.Code(err))
	}
}

func TestCreateOrder_Sell_Stock_ReservesHolding(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(defaultListing(1))
	fx.accountClient.stub.accountCcy[77] = "USD"

	order, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 5, SystemType: "client", ListingID: 1, Direction: "sell",
		OrderType: "market", Quantity: 30, AccountID: 77,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ReserveFunds NOT called — sells don't reserve funds at placement.
	if len(fx.accountClient.reserveCalls) != 0 {
		t.Errorf("sells must not reserve funds, got %d calls", len(fx.accountClient.reserveCalls))
	}
	// holdingReservationSvc.Reserve called with qty=30.
	if len(fx.holdingSvc.reserveCalls) != 1 {
		t.Fatalf("expected 1 holding Reserve call, got %d", len(fx.holdingSvc.reserveCalls))
	}
	hc := fx.holdingSvc.reserveCalls[0]
	if hc.Qty != 30 || hc.UserID != 5 || hc.SecurityType != "stock" {
		t.Errorf("unexpected holding reserve call: %+v", hc)
	}
	// ReservationAmount should be nil for sell orders.
	if order.ReservationAmount != nil {
		t.Errorf("sell order should not persist reservation_amount, got %v", order.ReservationAmount)
	}
	if order.Status != "approved" {
		t.Errorf("sell order status: got %s want approved", order.Status)
	}
}

func TestCreateOrder_Sell_Stock_InsufficientShares_Rejected(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(defaultListing(1))
	fx.accountClient.stub.accountCcy[77] = "USD"
	// Simulate holding reservation service returning insufficient-quantity.
	fx.holdingSvc.reserveErr = status.Error(codes.FailedPrecondition, "insufficient quantity")

	_, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 5, SystemType: "client", ListingID: 1, Direction: "sell",
		OrderType: "market", Quantity: 30, AccountID: 77,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("code: got %s want FailedPrecondition", status.Code(err))
	}
	// The order row was persisted then compensated via Delete.
	if len(fx.orderRepo.orders) != 0 {
		t.Errorf("expected 0 active orders after rollback, got %d", len(fx.orderRepo.orders))
	}
	if len(fx.orderRepo.deletedIDs) != 1 {
		t.Errorf("expected 1 delete compensation, got %d", len(fx.orderRepo.deletedIDs))
	}
}

func TestCreateOrder_ListingNotFound(t *testing.T) {
	fx := newOrderServiceFixture()

	_, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 42, SystemType: "employee", ListingID: 999, Direction: "buy",
		OrderType: "market", Quantity: 10, AccountID: 1,
	})
	if err == nil {
		t.Fatal("expected error for missing listing")
	}
	if status.Code(err) != codes.NotFound {
		t.Errorf("code: got %s want NotFound", status.Code(err))
	}
}

func TestCreateOrder_LimitOrderRequiresLimitValue(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(defaultListing(1))
	fx.accountClient.stub.accountCcy[1] = "USD"

	_, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 42, SystemType: "employee", ListingID: 1, Direction: "buy",
		OrderType: "limit", Quantity: 10, AccountID: 1,
	})
	if err == nil {
		t.Fatal("expected error for limit order without limit_value")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code: got %s want InvalidArgument", status.Code(err))
	}
}

func TestCreateOrder_StopOrderRequiresStopValue(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(defaultListing(1))
	fx.accountClient.stub.accountCcy[1] = "USD"

	_, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 42, SystemType: "employee", ListingID: 1, Direction: "buy",
		OrderType: "stop", Quantity: 10, AccountID: 1,
	})
	if err == nil {
		t.Fatal("expected error for stop order without stop_value")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code: got %s want InvalidArgument", status.Code(err))
	}
}

func TestCreateOrder_ClientAutoApproved_ApprovedByStamp(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(defaultListing(1))
	fx.accountClient.stub.accountCcy[1] = "USD"

	order, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 99, SystemType: "client", ListingID: 1, Direction: "buy",
		OrderType: "limit", Quantity: 5, LimitValue: ptrDec(100), AccountID: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Behaviour change vs pre-Phase-2: orders reach "approved" only after the
	// full saga commits (ReserveFunds returns OK). The approved-by sentinel
	// is preserved for client-placed orders so downstream UI code is unchanged.
	if order.Status != "approved" {
		t.Errorf("expected status approved for client, got %s", order.Status)
	}
	if order.ApprovedBy != "no need for approval" {
		t.Errorf("expected approvedBy 'no need for approval', got %q", order.ApprovedBy)
	}
}

// ---------------------------------------------------------------------------
// Tests: ApproveOrder / DeclineOrder / CancelOrder / GetOrder
// (unchanged semantics — they operate on already-persisted orders)
// ---------------------------------------------------------------------------

func TestApproveOrder_Success(t *testing.T) {
	svc, orderRepo, listingRepo, _, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	// Force it to pending to test Approve (placement saga now approves on success).
	stored, _ := orderRepo.GetByID(order.ID)
	stored.Status = "pending"
	stored.ApprovedBy = ""
	_ = orderRepo.Update(stored)

	approved, err := svc.ApproveOrder(order.ID, 10, "Supervisor Smith")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if approved.Status != "approved" {
		t.Errorf("expected status approved, got %s", approved.Status)
	}
	if approved.ApprovedBy != "Supervisor Smith" {
		t.Errorf("expected approvedBy 'Supervisor Smith', got %q", approved.ApprovedBy)
	}
}

func TestApproveOrder_NotPending(t *testing.T) {
	svc, _, listingRepo, _, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)
	// The saga already approved the order, so the next Approve should fail.
	_, err := svc.ApproveOrder(order.ID, 10, "Sup")
	if err == nil {
		t.Fatal("expected error when approving non-pending order")
	}
	if err.Error() != "order is not pending" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApproveOrder_NotFound(t *testing.T) {
	svc, _, _, _, _, _, _ := buildService()

	_, err := svc.ApproveOrder(999, 10, "Sup")
	if err == nil {
		t.Fatal("expected error for non-existent order")
	}
	if err.Error() != "order not found" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApproveOrder_SettlementExpired(t *testing.T) {
	svc, orderRepo, listingRepo, _, secRepo, _, _ := buildService()

	futuresListing := &model.Listing{
		ID: 3, SecurityID: 300, SecurityType: "futures", ExchangeID: 1,
		Exchange: model.StockExchange{ID: 1, Currency: "USD", TimeZone: "0"},
		Price:    decimal.NewFromInt(50),
		High:     decimal.NewFromInt(50),
	}
	listingRepo.addListing(futuresListing)

	order, err := svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 42, SystemType: "employee", ListingID: 3, Direction: "buy",
		OrderType: "limit", Quantity: 2, LimitValue: ptrDec(50), AccountID: 1,
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	// Force pending to test ApproveOrder settlement check.
	stored, _ := orderRepo.GetByID(order.ID)
	stored.Status = "pending"
	_ = orderRepo.Update(stored)

	secRepo.settlementDate = time.Now().Add(-24 * time.Hour)
	secRepo.err = nil

	_, err = svc.ApproveOrder(order.ID, 10, "Sup")
	if err == nil {
		t.Fatal("expected error for expired settlement date")
	}
	if err.Error() != "cannot approve: settlement date has passed" {
		t.Errorf("unexpected error: %v", err)
	}

	persisted, _ := orderRepo.GetByID(order.ID)
	if persisted.Status != "pending" {
		t.Errorf("order should remain pending, got %s", persisted.Status)
	}
}

func TestDeclineOrder_Success(t *testing.T) {
	svc, orderRepo, listingRepo, _, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)
	// Force pending so Decline can run (saga auto-approved it).
	stored, _ := orderRepo.GetByID(order.ID)
	stored.Status = "pending"
	_ = orderRepo.Update(stored)

	declined, err := svc.DeclineOrder(order.ID, 10, "Supervisor Jones")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if declined.Status != "declined" {
		t.Errorf("expected status declined, got %s", declined.Status)
	}
}

func TestDeclineOrder_NotPending(t *testing.T) {
	svc, _, listingRepo, _, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)
	// Order is already approved from the saga; Decline should reject.
	_, err := svc.DeclineOrder(order.ID, 10, "Sup")
	if err == nil {
		t.Fatal("expected error when declining non-pending order")
	}
}

func TestDeclineOrder_NotFound(t *testing.T) {
	svc, _, _, _, _, _, _ := buildService()
	_, err := svc.DeclineOrder(999, 10, "Sup")
	if err == nil {
		t.Fatal("expected error for non-existent order")
	}
}

func TestCancelOrder_Success(t *testing.T) {
	svc, orderRepo, listingRepo, _, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	cancelled, err := svc.CancelOrder(order.ID, 42, "employee")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Errorf("expected status cancelled, got %s", cancelled.Status)
	}
	if !cancelled.IsDone {
		t.Error("expected isDone true after cancel")
	}

	persisted, _ := orderRepo.GetByID(order.ID)
	if persisted.Status != "cancelled" {
		t.Errorf("persisted order should be cancelled, got %s", persisted.Status)
	}
}

func TestCancelOrder_WrongUser(t *testing.T) {
	svc, _, listingRepo, _, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	_, err := svc.CancelOrder(order.ID, 999, "employee")
	if err == nil {
		t.Fatal("expected error for wrong user")
	}
	// With (user_id, system_type) ownership enforced at the repo layer,
	// cross-owner access returns "order not found" rather than leaking
	// existence to a different owner.
	if err.Error() != "order not found" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCancelOrder_AlreadyCompleted(t *testing.T) {
	svc, orderRepo, listingRepo, _, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	stored, _ := orderRepo.GetByID(order.ID)
	stored.IsDone = true
	_ = orderRepo.Update(stored)

	_, err := svc.CancelOrder(order.ID, 42, "employee")
	if err == nil {
		t.Fatal("expected error when cancelling completed order")
	}
}

func TestCancelOrder_NotFound(t *testing.T) {
	svc, _, _, _, _, _, _ := buildService()
	_, err := svc.CancelOrder(999, 42, "employee")
	if err == nil {
		t.Fatal("expected error for non-existent order")
	}
}

func TestGetOrder_Success(t *testing.T) {
	svc, _, listingRepo, _, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	got, txns, err := svc.GetOrder(order.ID, 42, "employee")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != order.ID {
		t.Errorf("expected order ID %d, got %d", order.ID, got.ID)
	}
	if len(txns) != 0 {
		t.Errorf("expected 0 transactions, got %d", len(txns))
	}
}

func TestGetOrder_WrongUser(t *testing.T) {
	svc, _, listingRepo, _, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	_, _, err := svc.GetOrder(order.ID, 999, "employee")
	if err == nil {
		t.Fatal("expected error for wrong user")
	}
}

// ---------------------------------------------------------------------------
// Tests: Actuary limit enforcement (P0 authz gate)
// ---------------------------------------------------------------------------

func TestCreateOrder_Employee_LimitSpent_RemainsPending(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(rsdListing(1))
	// Account is denominated in RSD so reservation → RSD path is same-currency
	// and no exchange call is needed.
	fx.accountClient.stub.accountCcy[77] = "RSD"
	// Attach an actuary whose limit is already fully spent (UsedLimit == Limit).
	// Any further order needs supervisor approval, regardless of size.
	actuary := newFakeActuaryClient()
	actuary.setInfo(21 /*employeeID*/, 99 /*actuaryLimitID*/, decimal.NewFromInt(1000), decimal.NewFromInt(1000), true)
	fx.svc.WithActuaryClient(actuary)

	order, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 21, SystemType: "employee", ListingID: 1, Direction: "buy",
		OrderType: "limit", Quantity: 5, LimitValue: ptrDec(100), AccountID: 77,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status != "pending" {
		t.Errorf("expected status=pending (limit already spent), got %s", order.Status)
	}
	if order.ApprovedBy != "" {
		t.Errorf("expected empty approved_by on pending order, got %q", order.ApprovedBy)
	}
	if len(fx.accountClient.reserveCalls) != 1 {
		t.Errorf("expected 1 reserve call, got %d", len(fx.accountClient.reserveCalls))
	}
	if len(actuary.incrementCalls) != 0 {
		t.Errorf("pending order must not increment used_limit, got %d calls", len(actuary.incrementCalls))
	}
	if order.LimitActuaryID == nil || *order.LimitActuaryID != 99 {
		t.Errorf("LimitActuaryID: got %v want 99", order.LimitActuaryID)
	}
	if order.LimitAmountRSD == nil {
		t.Fatal("LimitAmountRSD not persisted on pending order")
	}
}

// A big order that would overflow the remaining budget still auto-approves as
// long as the actuary has not yet spent their full limit. The next order after
// this one will find UsedLimit >= Limit and require approval.
func TestCreateOrder_Employee_WouldOverflow_ButLimitNotSpent_AutoApproves(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(rsdListing(1))
	fx.accountClient.stub.accountCcy[77] = "RSD"
	actuary := newFakeActuaryClient()
	// Limit 1_000, UsedLimit 0 — plenty of headroom by the old "remaining"
	// rule would fail on a 10_025 RSD order, but the new rule auto-approves
	// because UsedLimit < Limit.
	actuary.setInfo(21, 99, decimal.NewFromInt(1000), decimal.Zero, true)
	fx.svc.WithActuaryClient(actuary)

	order, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 21, SystemType: "employee", ListingID: 1, Direction: "buy",
		OrderType: "limit", Quantity: 100, LimitValue: ptrDec(100), AccountID: 77,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status != "approved" {
		t.Errorf("expected auto-approved (limit not yet spent), got %s", order.Status)
	}
	if len(actuary.incrementCalls) != 1 {
		t.Errorf("expected 1 increment call, got %d", len(actuary.incrementCalls))
	}
}

// A freshly created EmployeeAgent has Limit=0 by default. Treat Limit=0 as
// "not configured yet" — the agent can place orders until a supervisor sets
// their real limit. Otherwise every new agent would be blocked on their very
// first order.
func TestCreateOrder_Employee_LimitNotConfigured_AutoApproves(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(rsdListing(1))
	fx.accountClient.stub.accountCcy[77] = "RSD"
	actuary := newFakeActuaryClient()
	actuary.setInfo(21, 99, decimal.Zero /* Limit */, decimal.Zero /* UsedLimit */, true /* NeedApproval */)
	fx.svc.WithActuaryClient(actuary)

	order, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 21, SystemType: "employee", ListingID: 1, Direction: "buy",
		OrderType: "limit", Quantity: 10, LimitValue: ptrDec(100), AccountID: 77,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status != "approved" {
		t.Errorf("expected auto-approved (Limit=0 means not configured), got %s", order.Status)
	}
	if len(actuary.incrementCalls) != 1 {
		t.Errorf("expected used_limit still incremented, got %d calls", len(actuary.incrementCalls))
	}
}

func TestCreateOrder_Employee_UnderLimit_AutoApproves(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(rsdListing(1))
	fx.accountClient.stub.accountCcy[77] = "RSD"
	actuary := newFakeActuaryClient()
	actuary.setInfo(21, 99, decimal.NewFromInt(1_000_000), decimal.Zero, true)
	fx.svc.WithActuaryClient(actuary)

	// Small 5-share order: 5 * 100 * (1+0.0025) = 501.25 RSD — well under 1_000_000.
	order, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 21, SystemType: "employee", ListingID: 1, Direction: "buy",
		OrderType: "limit", Quantity: 5, LimitValue: ptrDec(100), AccountID: 77,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status != "approved" {
		t.Errorf("expected status=approved (under-limit), got %s", order.Status)
	}
	// used_limit incremented by the RSD-equivalent of the reservation.
	if len(actuary.incrementCalls) != 1 {
		t.Fatalf("expected 1 IncrementUsedLimit call, got %d", len(actuary.incrementCalls))
	}
	call := actuary.incrementCalls[0]
	if call.ActuaryID != 99 {
		t.Errorf("increment ActuaryID: got %d want 99", call.ActuaryID)
	}
	expected := decimal.NewFromInt(500).Mul(decimal.NewFromFloat(1.0025))
	if !call.Amount.Equal(expected) {
		t.Errorf("increment amount: got %s want %s", call.Amount.String(), expected.String())
	}
}

func TestCreateOrder_Employee_NeedApprovalFalse_AutoApproves(t *testing.T) {
	// Even if the order would exceed the budget, need_approval=false bypasses
	// the gate (e.g., for supervisors).
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(rsdListing(1))
	fx.accountClient.stub.accountCcy[77] = "RSD"
	actuary := newFakeActuaryClient()
	actuary.setInfo(21, 99, decimal.NewFromInt(1000), decimal.Zero, false /* NO approval required */)
	fx.svc.WithActuaryClient(actuary)

	order, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 21, SystemType: "employee", ListingID: 1, Direction: "buy",
		OrderType: "limit", Quantity: 100, LimitValue: ptrDec(100), AccountID: 77,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status != "approved" {
		t.Errorf("expected auto-approved with need_approval=false, got %s", order.Status)
	}
	// used_limit still bumped (for visibility / daily rollup).
	if len(actuary.incrementCalls) != 1 {
		t.Errorf("expected 1 increment call, got %d", len(actuary.incrementCalls))
	}
}

func TestCreateOrder_Client_NoLimitCheck(t *testing.T) {
	// Clients aren't actuaries; they never have limits even if the client
	// wrapper is wired.
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(defaultListing(1))
	fx.accountClient.stub.accountCcy[77] = "USD"
	actuary := newFakeActuaryClient()
	// Intentionally configure NO info for the client's "userID" (it is not an
	// actuary); even if we did, the SystemType check would skip it.
	fx.svc.WithActuaryClient(actuary)

	order, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 99, SystemType: "client", ListingID: 1, Direction: "buy",
		OrderType: "limit", Quantity: 1000, LimitValue: ptrDec(100), AccountID: 77,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status != "approved" {
		t.Errorf("client order: expected approved, got %s", order.Status)
	}
	// No actuary RPC calls for clients.
	if len(actuary.incrementCalls) != 0 {
		t.Errorf("client order must not hit actuary RPC, got %d increment calls", len(actuary.incrementCalls))
	}
	if order.LimitActuaryID != nil {
		t.Errorf("client order must not carry LimitActuaryID, got %v", order.LimitActuaryID)
	}
}

func TestCreateOrder_Employee_NotAnActuary_PassesThrough(t *testing.T) {
	// Non-actuary employee (GetActuaryInfo returns ErrActuaryNotFound). The
	// order should auto-approve with no limit RPC side effects.
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(defaultListing(1))
	fx.accountClient.stub.accountCcy[77] = "USD"
	actuary := newFakeActuaryClient()
	// No info registered for userID=21 → ErrActuaryNotFound.
	fx.svc.WithActuaryClient(actuary)

	order, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 21, SystemType: "employee", ListingID: 1, Direction: "buy",
		OrderType: "limit", Quantity: 10, LimitValue: ptrDec(100), AccountID: 77,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status != "approved" {
		t.Errorf("non-actuary employee: expected approved, got %s", order.Status)
	}
	if len(actuary.incrementCalls) != 0 {
		t.Errorf("non-actuary employee: expected 0 increment calls, got %d", len(actuary.incrementCalls))
	}
}

func TestApproveOrder_IncrementsUsedLimit(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(rsdListing(1))
	fx.accountClient.stub.accountCcy[77] = "RSD"
	actuary := newFakeActuaryClient()
	// Limit fully spent → new orders require supervisor approval.
	actuary.setInfo(21, 99, decimal.NewFromInt(1000), decimal.NewFromInt(1000), true)
	fx.svc.WithActuaryClient(actuary)

	// Place an order that needs approval (limit already spent).
	order, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 21, SystemType: "employee", ListingID: 1, Direction: "buy",
		OrderType: "limit", Quantity: 100, LimitValue: ptrDec(100), AccountID: 77,
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if order.Status != "pending" {
		t.Fatalf("precondition: expected pending, got %s", order.Status)
	}
	if len(actuary.incrementCalls) != 0 {
		t.Fatalf("precondition: no increment on pending placement, got %d", len(actuary.incrementCalls))
	}

	// Supervisor approves.
	approved, err := fx.svc.ApproveOrder(order.ID, 5, "Supervisor Smith")
	if err != nil {
		t.Fatalf("approve failed: %v", err)
	}
	if approved.Status != "approved" {
		t.Errorf("expected approved, got %s", approved.Status)
	}
	// Now used_limit should be bumped.
	if len(actuary.incrementCalls) != 1 {
		t.Fatalf("expected 1 increment after approve, got %d", len(actuary.incrementCalls))
	}
	call := actuary.incrementCalls[0]
	if call.ActuaryID != 99 {
		t.Errorf("increment ActuaryID: got %d want 99", call.ActuaryID)
	}
	// Expected amount = 100 * 100 * (1+0.0025) = 10025
	expected := decimal.NewFromInt(10000).Mul(decimal.NewFromFloat(1.0025))
	if !call.Amount.Equal(expected) {
		t.Errorf("increment amount: got %s want %s", call.Amount.String(), expected.String())
	}
}

func TestCancelOrder_Approved_DecrementsUnfilledPortion(t *testing.T) {
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(rsdListing(1))
	fx.accountClient.stub.accountCcy[77] = "RSD"
	actuary := newFakeActuaryClient()
	actuary.setInfo(21, 99, decimal.NewFromInt(1_000_000), decimal.Zero, true)
	fx.svc.WithActuaryClient(actuary)

	// Place an under-limit order → auto-approved, used_limit bumped.
	order, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 21, SystemType: "employee", ListingID: 1, Direction: "buy",
		OrderType: "limit", Quantity: 10, LimitValue: ptrDec(100), AccountID: 77,
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if order.Status != "approved" {
		t.Fatalf("precondition: expected approved, got %s", order.Status)
	}

	// Simulate a 50% fill: set RemainingPortions to 5 of the 10 original qty.
	stored, _ := fx.orderRepo.GetByID(order.ID)
	stored.RemainingPortions = 5
	_ = fx.orderRepo.Update(stored)

	// Cancel the order (still approved, not done).
	_, err = fx.svc.CancelOrder(order.ID, 21, "employee")
	if err != nil {
		t.Fatalf("cancel failed: %v", err)
	}

	// Expect exactly 1 decrement call with 50% of the original RSD amount.
	if len(actuary.decrementCalls) != 1 {
		t.Fatalf("expected 1 decrement call, got %d", len(actuary.decrementCalls))
	}
	call := actuary.decrementCalls[0]
	// Original amount: 10*100*(1+0.0025) = 1002.5. Half = 501.25.
	expected := decimal.NewFromInt(1000).Mul(decimal.NewFromFloat(1.0025)).Mul(decimal.NewFromFloat(0.5))
	if !call.Amount.Equal(expected) {
		t.Errorf("decrement amount: got %s want %s", call.Amount.String(), expected.String())
	}
	if call.ActuaryID != 99 {
		t.Errorf("decrement ActuaryID: got %d want 99", call.ActuaryID)
	}
}

func TestCancelOrder_Pending_NoDecrement(t *testing.T) {
	// Cancelling a pending order (i.e., never approved → never counted against
	// used_limit) must NOT decrement.
	fx := newOrderServiceFixture()
	fx.listingRepo.addListing(rsdListing(1))
	fx.accountClient.stub.accountCcy[77] = "RSD"
	actuary := newFakeActuaryClient()
	// Limit already fully spent → new order lands in pending.
	actuary.setInfo(21, 99, decimal.NewFromInt(1000), decimal.NewFromInt(1000), true)
	fx.svc.WithActuaryClient(actuary)

	order, err := fx.svc.CreateOrder(context.Background(), CreateOrderRequest{
		UserID: 21, SystemType: "employee", ListingID: 1, Direction: "buy",
		OrderType: "limit", Quantity: 100, LimitValue: ptrDec(100), AccountID: 77,
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if order.Status != "pending" {
		t.Fatalf("precondition: expected pending, got %s", order.Status)
	}

	// Cancel the pending order.
	_, err = fx.svc.CancelOrder(order.ID, 21, "employee")
	if err != nil {
		t.Fatalf("cancel failed: %v", err)
	}

	if len(actuary.decrementCalls) != 0 {
		t.Errorf("pending-order cancel must not decrement, got %d calls", len(actuary.decrementCalls))
	}
}

// ---------------------------------------------------------------------------
// Tests: calculateCommission
// ---------------------------------------------------------------------------

func TestCalculateCommission_MarketOrder(t *testing.T) {
	c := calculateCommission("market", decimal.NewFromInt(30))
	expected := decimal.NewFromFloat(4.20)
	if !c.Equal(expected) {
		t.Errorf("expected commission %s, got %s", expected, c)
	}
}

func TestCalculateCommission_MarketOrder_Cap(t *testing.T) {
	c := calculateCommission("market", decimal.NewFromInt(100))
	expected := decimal.NewFromFloat(7)
	if !c.Equal(expected) {
		t.Errorf("expected commission %s (cap), got %s", expected, c)
	}
}

func TestCalculateCommission_LimitOrder(t *testing.T) {
	c := calculateCommission("limit", decimal.NewFromInt(30))
	expected := decimal.NewFromFloat(7.20)
	if !c.Equal(expected) {
		t.Errorf("expected commission %s, got %s", expected, c)
	}
}

func TestCalculateCommission_LimitOrder_Cap(t *testing.T) {
	c := calculateCommission("limit", decimal.NewFromInt(100))
	expected := decimal.NewFromFloat(12)
	if !c.Equal(expected) {
		t.Errorf("expected commission %s (cap), got %s", expected, c)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func ptrDec(v int64) *decimal.Decimal {
	d := decimal.NewFromInt(v)
	return &d
}
