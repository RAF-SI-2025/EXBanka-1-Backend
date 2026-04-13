package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

// mockOrderRepo is an in-memory order repository mock.
type mockOrderRepo struct {
	orders map[uint64]*model.Order
	nextID uint64
}

func newMockOrderRepo() *mockOrderRepo {
	return &mockOrderRepo{orders: make(map[uint64]*model.Order), nextID: 1}
}

func (m *mockOrderRepo) Create(order *model.Order) error {
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

func (m *mockOrderRepo) Update(order *model.Order) error {
	if _, ok := m.orders[order.ID]; !ok {
		return gorm.ErrRecordNotFound
	}
	stored := *order
	m.orders[order.ID] = &stored
	return nil
}

func (m *mockOrderRepo) ListByUser(userID uint64, filter repository.OrderFilter) ([]model.Order, int64, error) {
	var result []model.Order
	for _, o := range m.orders {
		if o.UserID == userID {
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

func (m *mockListingRepo) Update(listing *model.Listing) error     { return nil }
func (m *mockListingRepo) UpsertBySecurity(l *model.Listing) error { return nil }
func (m *mockListingRepo) UpsertForOption(l *model.Listing) (*model.Listing, error) {
	return l, nil
}
func (m *mockListingRepo) ListAll() ([]model.Listing, error) { return nil, nil }
func (m *mockListingRepo) ListBySecurityType(t string) ([]model.Listing, error) {
	return nil, nil
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
// Helpers
// ---------------------------------------------------------------------------

// defaultListing creates a stock listing at price 100 with a basic exchange.
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
			TimeZone:  "-5",
			OpenTime:  "09:30",
			CloseTime: "16:00",
		},
		Price: decimal.NewFromInt(100),
	}
}

// buildService wires up an OrderService with all mock dependencies.
func buildService() (*OrderService, *mockOrderRepo, *mockListingRepo, *mockSettingRepo, *mockSecurityLookupRepo, *mockProducer) {
	orderRepo := newMockOrderRepo()
	txRepo := newMockOrderTxRepo()
	listingRepo := newMockListingRepo()
	settingRepo := newMockSettingRepo()
	secRepo := &mockSecurityLookupRepo{}
	producer := &mockProducer{}

	// Enable testing mode so isAfterHours is skipped.
	_ = settingRepo.Set("testing_mode", "true")

	svc := NewOrderService(orderRepo, txRepo, listingRepo, settingRepo, secRepo, producer)
	return svc, orderRepo, listingRepo, settingRepo, secRepo, producer
}

// createDefaultOrder creates a simple market buy order via the service.
func createDefaultOrder(t *testing.T, svc *OrderService, listingRepo *mockListingRepo) *model.Order {
	t.Helper()
	listing := defaultListing(1)
	listingRepo.addListing(listing)

	order, err := svc.CreateOrder(
		42,         // userID
		"employee", // systemType
		1,          // listingID
		nil,        // holdingID
		"buy",      // direction
		"market",   // orderType
		10,         // quantity
		nil,        // limitValue
		nil,        // stopValue
		false,      // allOrNone
		false,      // margin
		1,          // accountID
	)
	if err != nil {
		t.Fatalf("createDefaultOrder failed: %v", err)
	}
	return order
}

// ---------------------------------------------------------------------------
// Tests: CreateOrder
// ---------------------------------------------------------------------------

func TestCreateOrder_MarketBuy_EmployeePending(t *testing.T) {
	svc, _, listingRepo, _, _, _ := buildService()
	listing := defaultListing(1)
	listingRepo.addListing(listing)

	order, err := svc.CreateOrder(42, "employee", 1, nil, "buy", "market", 10, nil, nil, false, false, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status != "pending" {
		t.Errorf("expected status pending, got %s", order.Status)
	}
	if order.ApprovedBy != "" {
		t.Errorf("expected empty approvedBy for employee order, got %q", order.ApprovedBy)
	}
	if order.Quantity != 10 {
		t.Errorf("expected quantity 10, got %d", order.Quantity)
	}
	if order.RemainingPortions != 10 {
		t.Errorf("expected remainingPortions 10, got %d", order.RemainingPortions)
	}
	if order.Direction != "buy" {
		t.Errorf("expected direction buy, got %s", order.Direction)
	}
}

func TestCreateOrder_ClientAutoApproved(t *testing.T) {
	svc, _, listingRepo, _, _, _ := buildService()
	listing := defaultListing(1)
	listingRepo.addListing(listing)

	order, err := svc.CreateOrder(99, "client", 1, nil, "buy", "market", 5, nil, nil, false, false, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status != "approved" {
		t.Errorf("expected status approved for client, got %s", order.Status)
	}
	if order.ApprovedBy != "no need for approval" {
		t.Errorf("expected approvedBy 'no need for approval', got %q", order.ApprovedBy)
	}
}

func TestCreateOrder_ListingNotFound(t *testing.T) {
	svc, _, _, _, _, _ := buildService()

	_, err := svc.CreateOrder(42, "employee", 999, nil, "buy", "market", 10, nil, nil, false, false, 1)
	if err == nil {
		t.Fatal("expected error for missing listing")
	}
	if err.Error() != "listing not found" {
		t.Errorf("expected 'listing not found', got %q", err.Error())
	}
}

func TestCreateOrder_LimitOrderRequiresLimitValue(t *testing.T) {
	svc, _, listingRepo, _, _, _ := buildService()
	listingRepo.addListing(defaultListing(1))

	_, err := svc.CreateOrder(42, "employee", 1, nil, "buy", "limit", 10, nil, nil, false, false, 1)
	if err == nil {
		t.Fatal("expected error for limit order without limit_value")
	}
	if err.Error() != "limit_value required for limit/stop_limit orders" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCreateOrder_StopOrderRequiresStopValue(t *testing.T) {
	svc, _, listingRepo, _, _, _ := buildService()
	listingRepo.addListing(defaultListing(1))

	_, err := svc.CreateOrder(42, "employee", 1, nil, "buy", "stop", 10, nil, nil, false, false, 1)
	if err == nil {
		t.Fatal("expected error for stop order without stop_value")
	}
	if err.Error() != "stop_value required for stop/stop_limit orders" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCreateOrder_StopLimitRequiresBothValues(t *testing.T) {
	svc, _, listingRepo, _, _, _ := buildService()
	listingRepo.addListing(defaultListing(1))

	// Missing limit_value
	_, err := svc.CreateOrder(42, "employee", 1, nil, "buy", "stop_limit", 10, nil, ptrDec(50), false, false, 1)
	if err == nil {
		t.Fatal("expected error for stop_limit without limit_value")
	}

	// Missing stop_value
	_, err = svc.CreateOrder(42, "employee", 1, nil, "buy", "stop_limit", 10, ptrDec(100), nil, false, false, 1)
	if err == nil {
		t.Fatal("expected error for stop_limit without stop_value")
	}

	// Both present should succeed
	order, err := svc.CreateOrder(42, "employee", 1, nil, "buy", "stop_limit", 10, ptrDec(100), ptrDec(50), false, false, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.LimitValue == nil || !order.LimitValue.Equal(decimal.NewFromInt(100)) {
		t.Errorf("expected limitValue 100, got %v", order.LimitValue)
	}
	if order.StopValue == nil || !order.StopValue.Equal(decimal.NewFromInt(50)) {
		t.Errorf("expected stopValue 50, got %v", order.StopValue)
	}
}

func TestCreateOrder_LimitPriceUsedForApproxPrice(t *testing.T) {
	svc, _, listingRepo, _, _, _ := buildService()
	listingRepo.addListing(defaultListing(1)) // market price = 100

	limitVal := decimal.NewFromInt(50)
	order, err := svc.CreateOrder(42, "employee", 1, nil, "buy", "limit", 5, &limitVal, nil, false, false, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// contractSize=1, quantity=5, limitValue=50 => approxPrice = 1*50*5 = 250
	expected := decimal.NewFromInt(250)
	if !order.ApproximatePrice.Equal(expected) {
		t.Errorf("expected approxPrice %s, got %s", expected, order.ApproximatePrice)
	}
	// pricePerUnit should be the limit value, not market price
	if !order.PricePerUnit.Equal(decimal.NewFromInt(50)) {
		t.Errorf("expected pricePerUnit 50, got %s", order.PricePerUnit)
	}
}

func TestCreateOrder_ForexContractSize(t *testing.T) {
	svc, _, listingRepo, _, _, _ := buildService()
	forexListing := &model.Listing{
		ID:           2,
		SecurityID:   200,
		SecurityType: "forex",
		ExchangeID:   1,
		Exchange: model.StockExchange{
			ID:       1,
			TimeZone: "0",
		},
		Price: decimal.NewFromFloat(1.10),
	}
	listingRepo.addListing(forexListing)

	order, err := svc.CreateOrder(42, "employee", 2, nil, "buy", "market", 3, nil, nil, false, false, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.ContractSize != 1000 {
		t.Errorf("expected contractSize 1000 for forex, got %d", order.ContractSize)
	}
	// approxPrice = 1000 * 1.10 * 3 = 3300
	expected := decimal.NewFromFloat(1.10).Mul(decimal.NewFromInt(1000)).Mul(decimal.NewFromInt(3))
	if !order.ApproximatePrice.Equal(expected) {
		t.Errorf("expected approxPrice %s, got %s", expected, order.ApproximatePrice)
	}
}

// ---------------------------------------------------------------------------
// Tests: ApproveOrder
// ---------------------------------------------------------------------------

func TestApproveOrder_Success(t *testing.T) {
	svc, orderRepo, listingRepo, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

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

	// Verify persisted
	persisted, _ := orderRepo.GetByID(order.ID)
	if persisted.Status != "approved" {
		t.Errorf("persisted order status should be approved, got %s", persisted.Status)
	}
}

func TestApproveOrder_NotPending(t *testing.T) {
	svc, _, listingRepo, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	// Approve first
	_, err := svc.ApproveOrder(order.ID, 10, "Sup")
	if err != nil {
		t.Fatalf("first approve failed: %v", err)
	}

	// Try to approve again
	_, err = svc.ApproveOrder(order.ID, 10, "Sup")
	if err == nil {
		t.Fatal("expected error when approving non-pending order")
	}
	if err.Error() != "order is not pending" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApproveOrder_NotFound(t *testing.T) {
	svc, _, _, _, _, _ := buildService()

	_, err := svc.ApproveOrder(999, 10, "Sup")
	if err == nil {
		t.Fatal("expected error for non-existent order")
	}
	if err.Error() != "order not found" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApproveOrder_SettlementExpired(t *testing.T) {
	svc, orderRepo, listingRepo, _, secRepo, _ := buildService()

	// Create a futures listing
	futuresListing := &model.Listing{
		ID:           3,
		SecurityID:   300,
		SecurityType: "futures",
		ExchangeID:   1,
		Exchange: model.StockExchange{
			ID:       1,
			TimeZone: "0",
		},
		Price: decimal.NewFromInt(50),
	}
	listingRepo.addListing(futuresListing)

	order, err := svc.CreateOrder(42, "employee", 3, nil, "buy", "market", 2, nil, nil, false, false, 1)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Simulate expired settlement date
	secRepo.settlementDate = time.Now().Add(-24 * time.Hour)
	secRepo.err = nil

	_, err = svc.ApproveOrder(order.ID, 10, "Sup")
	if err == nil {
		t.Fatal("expected error for expired settlement date")
	}
	if err.Error() != "cannot approve: settlement date has passed" {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify order was not modified
	persisted, _ := orderRepo.GetByID(order.ID)
	if persisted.Status != "pending" {
		t.Errorf("order should remain pending, got %s", persisted.Status)
	}
}

// ---------------------------------------------------------------------------
// Tests: DeclineOrder
// ---------------------------------------------------------------------------

func TestDeclineOrder_Success(t *testing.T) {
	svc, orderRepo, listingRepo, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	declined, err := svc.DeclineOrder(order.ID, 10, "Supervisor Jones")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if declined.Status != "declined" {
		t.Errorf("expected status declined, got %s", declined.Status)
	}
	if declined.ApprovedBy != "Supervisor Jones" {
		t.Errorf("expected approvedBy 'Supervisor Jones', got %q", declined.ApprovedBy)
	}

	persisted, _ := orderRepo.GetByID(order.ID)
	if persisted.Status != "declined" {
		t.Errorf("persisted order should be declined, got %s", persisted.Status)
	}
}

func TestDeclineOrder_NotPending(t *testing.T) {
	svc, _, listingRepo, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	// Decline first
	_, _ = svc.DeclineOrder(order.ID, 10, "Sup")

	// Try again
	_, err := svc.DeclineOrder(order.ID, 10, "Sup")
	if err == nil {
		t.Fatal("expected error when declining non-pending order")
	}
	if err.Error() != "order is not pending" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDeclineOrder_NotFound(t *testing.T) {
	svc, _, _, _, _, _ := buildService()

	_, err := svc.DeclineOrder(999, 10, "Sup")
	if err == nil {
		t.Fatal("expected error for non-existent order")
	}
	if err.Error() != "order not found" {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: CancelOrder
// ---------------------------------------------------------------------------

func TestCancelOrder_Success(t *testing.T) {
	svc, orderRepo, listingRepo, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	cancelled, err := svc.CancelOrder(order.ID, 42)
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
	svc, _, listingRepo, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	_, err := svc.CancelOrder(order.ID, 999) // wrong user
	if err == nil {
		t.Fatal("expected error for wrong user")
	}
	if err.Error() != "order does not belong to user" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCancelOrder_AlreadyCompleted(t *testing.T) {
	svc, orderRepo, listingRepo, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	// Manually mark as done (simulates fully filled order)
	stored, _ := orderRepo.GetByID(order.ID)
	stored.IsDone = true
	stored.Status = "approved"
	_ = orderRepo.Update(stored)

	_, err := svc.CancelOrder(order.ID, 42)
	if err == nil {
		t.Fatal("expected error when cancelling completed order")
	}
	if err.Error() != "order is already completed" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCancelOrder_AlreadyDeclined(t *testing.T) {
	svc, _, listingRepo, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	// Decline first
	_, _ = svc.DeclineOrder(order.ID, 10, "Sup")

	// Cancel after decline
	_, err := svc.CancelOrder(order.ID, 42)
	if err == nil {
		t.Fatal("expected error when cancelling declined order")
	}
	if err.Error() != "order is already declined/cancelled" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCancelOrder_AlreadyCancelled(t *testing.T) {
	svc, _, listingRepo, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	// Cancel first
	_, _ = svc.CancelOrder(order.ID, 42)

	// Cancel again - hits IsDone check first because CancelOrder sets IsDone=true
	_, err := svc.CancelOrder(order.ID, 42)
	if err == nil {
		t.Fatal("expected error when double-cancelling order")
	}
	if err.Error() != "order is already completed" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCancelOrder_NotFound(t *testing.T) {
	svc, _, _, _, _, _ := buildService()

	_, err := svc.CancelOrder(999, 42)
	if err == nil {
		t.Fatal("expected error for non-existent order")
	}
	if err.Error() != "order not found" {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: GetOrder
// ---------------------------------------------------------------------------

func TestGetOrder_Success(t *testing.T) {
	svc, _, listingRepo, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	got, txns, err := svc.GetOrder(order.ID, 42)
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
	svc, _, listingRepo, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	_, _, err := svc.GetOrder(order.ID, 999)
	if err == nil {
		t.Fatal("expected error for wrong user")
	}
	if err.Error() != "order does not belong to user" {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: calculateCommission (exported helper tested indirectly)
// ---------------------------------------------------------------------------

func TestCalculateCommission_MarketOrder(t *testing.T) {
	// 14% of 30 = 4.20 (under cap of 7)
	c := calculateCommission("market", decimal.NewFromInt(30))
	expected := decimal.NewFromFloat(4.20)
	if !c.Equal(expected) {
		t.Errorf("expected commission %s, got %s", expected, c)
	}
}

func TestCalculateCommission_MarketOrder_Cap(t *testing.T) {
	// 14% of 100 = 14 -> capped at 7
	c := calculateCommission("market", decimal.NewFromInt(100))
	expected := decimal.NewFromFloat(7)
	if !c.Equal(expected) {
		t.Errorf("expected commission %s (cap), got %s", expected, c)
	}
}

func TestCalculateCommission_LimitOrder(t *testing.T) {
	// 24% of 30 = 7.20 (under cap of 12)
	c := calculateCommission("limit", decimal.NewFromInt(30))
	expected := decimal.NewFromFloat(7.20)
	if !c.Equal(expected) {
		t.Errorf("expected commission %s, got %s", expected, c)
	}
}

func TestCalculateCommission_LimitOrder_Cap(t *testing.T) {
	// 24% of 100 = 24 -> capped at 12
	c := calculateCommission("limit", decimal.NewFromInt(100))
	expected := decimal.NewFromFloat(12)
	if !c.Equal(expected) {
		t.Errorf("expected commission %s (cap), got %s", expected, c)
	}
}

func TestCalculateCommission_StopLimitOrder(t *testing.T) {
	// stop_limit uses same formula as limit: 24% of 40 = 9.60
	c := calculateCommission("stop_limit", decimal.NewFromInt(40))
	expected := decimal.NewFromFloat(9.60)
	if !c.Equal(expected) {
		t.Errorf("expected commission %s, got %s", expected, c)
	}
}

func TestCalculateCommission_StopOrder(t *testing.T) {
	// stop uses market formula: 14% of 20 = 2.80
	c := calculateCommission("stop", decimal.NewFromInt(20))
	expected := decimal.NewFromFloat(2.80)
	if !c.Equal(expected) {
		t.Errorf("expected commission %s, got %s", expected, c)
	}
}

// ---------------------------------------------------------------------------
// Tests: Full status transition workflows
// ---------------------------------------------------------------------------

func TestStatusTransition_PendingToApprovedToCancelled(t *testing.T) {
	svc, _, listingRepo, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	// Approve
	approved, err := svc.ApproveOrder(order.ID, 10, "Sup")
	if err != nil {
		t.Fatalf("approve failed: %v", err)
	}
	if approved.Status != "approved" {
		t.Fatalf("expected approved, got %s", approved.Status)
	}

	// Cancel approved (not done) order should succeed
	cancelled, err := svc.CancelOrder(order.ID, 42)
	if err != nil {
		t.Fatalf("cancel approved order failed: %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Errorf("expected cancelled, got %s", cancelled.Status)
	}
}

func TestStatusTransition_CannotApproveDeclinedOrder(t *testing.T) {
	svc, _, listingRepo, _, _, _ := buildService()
	order := createDefaultOrder(t, svc, listingRepo)

	// Decline
	_, err := svc.DeclineOrder(order.ID, 10, "Sup")
	if err != nil {
		t.Fatalf("decline failed: %v", err)
	}

	// Attempt approve
	_, err = svc.ApproveOrder(order.ID, 10, "Sup")
	if err == nil {
		t.Fatal("expected error when approving declined order")
	}
	if err.Error() != "order is not pending" {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func ptrDec(v int64) *decimal.Decimal {
	d := decimal.NewFromInt(v)
	return &d
}
