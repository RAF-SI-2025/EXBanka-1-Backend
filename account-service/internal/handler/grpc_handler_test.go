package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	kafkaprod "github.com/exbanka/account-service/internal/kafka"
	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	"github.com/exbanka/account-service/internal/service"
	pb "github.com/exbanka/contract/accountpb"
	clientpb "github.com/exbanka/contract/clientpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	shared "github.com/exbanka/contract/shared"
	"google.golang.org/grpc"
)

// ---------------------------------------------------------------------------
// Mock structs
// ---------------------------------------------------------------------------

type mockAccountSvc struct {
	createAccountFn         func(account *model.Account) error
	getAccountFn            func(id uint64) (*model.Account, error)
	getAccountByNumberFn    func(accountNumber string) (*model.Account, error)
	listAccountsByClientFn  func(clientID uint64, page, pageSize int) ([]model.Account, int64, error)
	listAllAccountsFn       func(nameFilter, numberFilter, typeFilter string, page, pageSize int) ([]model.Account, int64, error)
	updateAccountNameFn     func(id, clientID uint64, newName string, changedBy int64) error
	updateAccountLimitsFn   func(id uint64, dailyLimit, monthlyLimit *string, changedBy int64) error
	updateAccountStatusFn   func(id uint64, newStatus string, changedBy int64) error
	updateBalanceWithOptsFn func(accountNumber string, amount decimal.Decimal, updateAvailable bool, opts repository.UpdateBalanceOpts) error
}

func (m *mockAccountSvc) CreateAccount(account *model.Account) error {
	if m.createAccountFn != nil {
		return m.createAccountFn(account)
	}
	account.ID = 1
	account.AccountNumber = "111000100000000011"
	account.Status = "active"
	account.ExpiresAt = time.Now().AddDate(5, 0, 0)
	return nil
}

func (m *mockAccountSvc) GetAccount(id uint64) (*model.Account, error) {
	if m.getAccountFn != nil {
		return m.getAccountFn(id)
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockAccountSvc) GetAccountByNumber(accountNumber string) (*model.Account, error) {
	if m.getAccountByNumberFn != nil {
		return m.getAccountByNumberFn(accountNumber)
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockAccountSvc) ListAccountsByClient(clientID uint64, page, pageSize int) ([]model.Account, int64, error) {
	if m.listAccountsByClientFn != nil {
		return m.listAccountsByClientFn(clientID, page, pageSize)
	}
	return nil, 0, nil
}

func (m *mockAccountSvc) ListAllAccounts(nameFilter, numberFilter, typeFilter string, page, pageSize int) ([]model.Account, int64, error) {
	if m.listAllAccountsFn != nil {
		return m.listAllAccountsFn(nameFilter, numberFilter, typeFilter, page, pageSize)
	}
	return nil, 0, nil
}

func (m *mockAccountSvc) UpdateAccountName(id, clientID uint64, newName string, changedBy int64) error {
	if m.updateAccountNameFn != nil {
		return m.updateAccountNameFn(id, clientID, newName, changedBy)
	}
	return nil
}

func (m *mockAccountSvc) UpdateAccountLimits(id uint64, dailyLimit, monthlyLimit *string, changedBy int64) error {
	if m.updateAccountLimitsFn != nil {
		return m.updateAccountLimitsFn(id, dailyLimit, monthlyLimit, changedBy)
	}
	return nil
}

func (m *mockAccountSvc) UpdateAccountStatus(id uint64, newStatus string, changedBy int64) error {
	if m.updateAccountStatusFn != nil {
		return m.updateAccountStatusFn(id, newStatus, changedBy)
	}
	return nil
}

func (m *mockAccountSvc) UpdateBalanceWithOpts(accountNumber string, amount decimal.Decimal, updateAvailable bool, opts repository.UpdateBalanceOpts) error {
	if m.updateBalanceWithOptsFn != nil {
		return m.updateBalanceWithOptsFn(accountNumber, amount, updateAvailable, opts)
	}
	return nil
}

type mockCompanySvc struct {
	createFn func(company *model.Company) error
	getFn    func(id uint64) (*model.Company, error)
	updateFn func(company *model.Company) error
}

func (m *mockCompanySvc) Create(company *model.Company) error {
	if m.createFn != nil {
		return m.createFn(company)
	}
	company.ID = 1
	return nil
}

func (m *mockCompanySvc) Get(id uint64) (*model.Company, error) {
	if m.getFn != nil {
		return m.getFn(id)
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockCompanySvc) Update(company *model.Company) error {
	if m.updateFn != nil {
		return m.updateFn(company)
	}
	return nil
}

type mockCurrencySvc struct {
	listFn      func() ([]model.Currency, error)
	getByCodeFn func(code string) (*model.Currency, error)
}

func (m *mockCurrencySvc) List() ([]model.Currency, error) {
	if m.listFn != nil {
		return m.listFn()
	}
	return nil, nil
}

func (m *mockCurrencySvc) GetByCode(code string) (*model.Currency, error) {
	if m.getByCodeFn != nil {
		return m.getByCodeFn(code)
	}
	return nil, gorm.ErrRecordNotFound
}

type mockLedgerSvc struct {
	getLedgerEntriesFn func(accountNumber string, page, pageSize int) ([]model.LedgerEntry, int64, error)
}

func (m *mockLedgerSvc) GetLedgerEntries(accountNumber string, page, pageSize int) ([]model.LedgerEntry, int64, error) {
	if m.getLedgerEntriesFn != nil {
		return m.getLedgerEntriesFn(accountNumber, page, pageSize)
	}
	return nil, 0, nil
}

type mockAccountProducer struct {
	publishAccountCreatedFn       func(ctx context.Context, msg kafkamsg.AccountCreatedMessage) error
	publishAccountStatusChangedFn func(ctx context.Context, msg kafkaprod.AccountStatusChangedMsg) error
	publishGeneralNotificationFn  func(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error
	sendEmailFn                   func(ctx context.Context, msg kafkamsg.SendEmailMessage) error

	accountCreatedCalls       []kafkamsg.AccountCreatedMessage
	accountStatusChangedCalls []kafkaprod.AccountStatusChangedMsg
	generalNotificationCalls  []kafkamsg.GeneralNotificationMessage
	sendEmailCalls            []kafkamsg.SendEmailMessage
}

func (m *mockAccountProducer) PublishAccountCreated(ctx context.Context, msg kafkamsg.AccountCreatedMessage) error {
	m.accountCreatedCalls = append(m.accountCreatedCalls, msg)
	if m.publishAccountCreatedFn != nil {
		return m.publishAccountCreatedFn(ctx, msg)
	}
	return nil
}

func (m *mockAccountProducer) PublishAccountStatusChanged(ctx context.Context, msg kafkaprod.AccountStatusChangedMsg) error {
	m.accountStatusChangedCalls = append(m.accountStatusChangedCalls, msg)
	if m.publishAccountStatusChangedFn != nil {
		return m.publishAccountStatusChangedFn(ctx, msg)
	}
	return nil
}

func (m *mockAccountProducer) PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error {
	m.generalNotificationCalls = append(m.generalNotificationCalls, msg)
	if m.publishGeneralNotificationFn != nil {
		return m.publishGeneralNotificationFn(ctx, msg)
	}
	return nil
}

func (m *mockAccountProducer) SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
	m.sendEmailCalls = append(m.sendEmailCalls, msg)
	if m.sendEmailFn != nil {
		return m.sendEmailFn(ctx, msg)
	}
	return nil
}

// mockClientClient is a stub for clientpb.ClientServiceClient.
type mockClientClient struct {
	getClientFn func(ctx context.Context, req *clientpb.GetClientRequest, opts ...grpc.CallOption) (*clientpb.ClientResponse, error)
}

func (m *mockClientClient) CreateClient(ctx context.Context, in *clientpb.CreateClientRequest, opts ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockClientClient) GetClient(ctx context.Context, in *clientpb.GetClientRequest, opts ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	if m.getClientFn != nil {
		return m.getClientFn(ctx, in, opts...)
	}
	return nil, errors.New("not implemented")
}

func (m *mockClientClient) GetClientByEmail(ctx context.Context, in *clientpb.GetClientByEmailRequest, opts ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockClientClient) ListClients(ctx context.Context, in *clientpb.ListClientsRequest, opts ...grpc.CallOption) (*clientpb.ListClientsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockClientClient) UpdateClient(ctx context.Context, in *clientpb.UpdateClientRequest, opts ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockClientClient) ListChangelog(ctx context.Context, in *clientpb.ListChangelogRequest, opts ...grpc.CallOption) (*clientpb.ListChangelogResponse, error) {
	return nil, errors.New("not implemented")
}

var _ clientpb.ClientServiceClient = (*mockClientClient)(nil)

// ---------------------------------------------------------------------------
// Constructor helper
// ---------------------------------------------------------------------------

type grpcHandlerFixture struct {
	accountSvc   *mockAccountSvc
	companySvc   *mockCompanySvc
	currencySvc  *mockCurrencySvc
	ledgerSvc    *mockLedgerSvc
	producer     *mockAccountProducer
	clientClient *mockClientClient
	db           *gorm.DB
	idem         *repository.IdempotencyRepository
}

// newHandlerTestDB returns an in-memory SQLite DB with the schema needed by
// the IdempotencyRepository (the only persistence the handler tests touch
// directly — service-level state is mocked). Each test gets its own DB so
// Run-cache hits across tests cannot leak.
func newHandlerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:handler_%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.IdempotencyRecord{}))
	return db
}

func newGRPCHandlerFixture(t *testing.T) (*AccountGRPCHandler, *grpcHandlerFixture) {
	t.Helper()
	db := newHandlerTestDB(t)
	idem := repository.NewIdempotencyRepository(db)
	f := &grpcHandlerFixture{
		accountSvc:   &mockAccountSvc{},
		companySvc:   &mockCompanySvc{},
		currencySvc:  &mockCurrencySvc{},
		ledgerSvc:    &mockLedgerSvc{},
		producer:     &mockAccountProducer{},
		clientClient: &mockClientClient{},
		db:           db,
		idem:         idem,
	}
	h := &AccountGRPCHandler{
		accountService:  f.accountSvc,
		companyService:  f.companySvc,
		currencyService: f.currencySvc,
		ledgerService:   f.ledgerSvc,
		producer:        f.producer,
		clientClient:    f.clientClient,
		db:              db,
		idem:            idem,
	}
	return h, f
}

func sampleAccount(id uint64) *model.Account {
	return &model.Account{
		ID:               id,
		AccountNumber:    "111000100000000011",
		AccountName:      "Test Account",
		OwnerID:          42,
		CurrencyCode:     "RSD",
		AccountKind:      "current",
		AccountType:      "standard",
		Status:           "active",
		Balance:          decimal.NewFromInt(1000),
		AvailableBalance: decimal.NewFromInt(1000),
		ReservedBalance:  decimal.Zero,
		MaintenanceFee:   decimal.NewFromInt(220),
		DailyLimit:       decimal.NewFromInt(1_000_000),
		MonthlyLimit:     decimal.NewFromInt(10_000_000),
		ExpiresAt:        time.Now().AddDate(5, 0, 0),
	}
}

// ---------------------------------------------------------------------------
// Sentinel passthrough — typed sentinels must reach the wire intact
// ---------------------------------------------------------------------------

func TestSentinel_Passthrough_AccountHandler(t *testing.T) {
	cases := []struct {
		name     string
		sentinel error
		code     codes.Code
	}{
		{"ErrAccountNotFound", service.ErrAccountNotFound, codes.NotFound},
		{"ErrInvalidAccount", service.ErrInvalidAccount, codes.InvalidArgument},
		{"ErrInvalidStatus", service.ErrInvalidStatus, codes.InvalidArgument},
		{"ErrCompanyDuplicate", service.ErrCompanyDuplicate, codes.AlreadyExists},
		{"ErrAccountInactive", service.ErrAccountInactive, codes.FailedPrecondition},
		{"ErrLastBankAccount", service.ErrLastBankAccount, codes.FailedPrecondition},
		{"ErrInsufficientBalance", service.ErrInsufficientBalance, codes.FailedPrecondition},
		{"ErrSpendingLimitExceeded", service.ErrSpendingLimitExceeded, codes.ResourceExhausted},
		{"ErrCurrencyNotFound", service.ErrCurrencyNotFound, codes.NotFound},
		{"ErrCompanyNotFound", service.ErrCompanyNotFound, codes.NotFound},
		// shared optimistic-lock sentinel maps to Aborted
		{"shared.ErrOptimisticLock", shared.ErrOptimisticLock, codes.Aborted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ok := status.FromError(tc.sentinel)
			require.True(t, ok, "sentinel %s lacks GRPCStatus", tc.name)
			assert.Equal(t, tc.code, s.Code())
			// errors.Is must continue to work after wrapping
			wrapped := fmt.Errorf("op: %w", tc.sentinel)
			assert.True(t, errors.Is(wrapped, tc.sentinel))
		})
	}
}

// ---------------------------------------------------------------------------
// CreateAccount tests
// ---------------------------------------------------------------------------

func TestCreateAccount_Success(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.createAccountFn = func(account *model.Account) error {
		account.ID = 10
		account.AccountNumber = "111000100000099011"
		account.Status = "active"
		account.ExpiresAt = time.Now().AddDate(5, 0, 0)
		return nil
	}
	// Return the account when getAccountFn is called with post-update fetch.
	f.accountSvc.getAccountByNumberFn = func(accountNumber string) (*model.Account, error) {
		return sampleAccount(10), nil
	}

	resp, err := h.CreateAccount(context.Background(), &pb.CreateAccountRequest{
		OwnerId:      42,
		AccountKind:  "current",
		CurrencyCode: "RSD",
		EmployeeId:   1,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, uint64(10), resp.Id)
	assert.Len(t, f.producer.accountCreatedCalls, 1)
	assert.Len(t, f.producer.generalNotificationCalls, 1)
}

func TestCreateAccount_ServiceError(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.createAccountFn = func(account *model.Account) error {
		return service.ErrInvalidAccount
	}

	_, err := h.CreateAccount(context.Background(), &pb.CreateAccountRequest{
		OwnerId:      42,
		AccountKind:  "savings",
		CurrencyCode: "RSD",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Empty(t, f.producer.accountCreatedCalls)
}

// ---------------------------------------------------------------------------
// GetAccount tests
// ---------------------------------------------------------------------------

func TestGetAccount_Success(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.getAccountFn = func(id uint64) (*model.Account, error) {
		return sampleAccount(id), nil
	}

	resp, err := h.GetAccount(context.Background(), &pb.GetAccountRequest{Id: 5})
	require.NoError(t, err)
	assert.Equal(t, uint64(5), resp.Id)
	assert.Equal(t, "RSD", resp.CurrencyCode)
}

func TestGetAccount_NotFound(t *testing.T) {
	h, _ := newGRPCHandlerFixture(t)

	_, err := h.GetAccount(context.Background(), &pb.GetAccountRequest{Id: 999})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestGetAccount_ServiceError(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.getAccountFn = func(id uint64) (*model.Account, error) {
		return nil, errors.New("db connection error")
	}

	_, err := h.GetAccount(context.Background(), &pb.GetAccountRequest{Id: 1})
	require.Error(t, err)
	// Untyped errors now pass through as Unknown — see TestSentinel_Passthrough
	// for the typed contract.
	assert.Equal(t, codes.Unknown, status.Code(err))
}

// ---------------------------------------------------------------------------
// GetAccountByNumber tests
// ---------------------------------------------------------------------------

func TestGetAccountByNumber_Success(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.getAccountByNumberFn = func(accountNumber string) (*model.Account, error) {
		a := sampleAccount(7)
		a.AccountNumber = accountNumber
		return a, nil
	}

	resp, err := h.GetAccountByNumber(context.Background(), &pb.GetAccountByNumberRequest{AccountNumber: "111000100000099011"})
	require.NoError(t, err)
	assert.Equal(t, "111000100000099011", resp.AccountNumber)
}

func TestGetAccountByNumber_NotFound(t *testing.T) {
	h, _ := newGRPCHandlerFixture(t)

	_, err := h.GetAccountByNumber(context.Background(), &pb.GetAccountByNumberRequest{AccountNumber: "NONEXISTENT"})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// ---------------------------------------------------------------------------
// ListAccountsByClient tests
// ---------------------------------------------------------------------------

func TestListAccountsByClient_Success(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.listAccountsByClientFn = func(clientID uint64, page, pageSize int) ([]model.Account, int64, error) {
		return []model.Account{
			*sampleAccount(1),
			*sampleAccount(2),
		}, 2, nil
	}

	resp, err := h.ListAccountsByClient(context.Background(), &pb.ListAccountsByClientRequest{ClientId: 42, Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.Total)
	assert.Len(t, resp.Accounts, 2)
}

func TestListAccountsByClient_ServiceError(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.listAccountsByClientFn = func(clientID uint64, page, pageSize int) ([]model.Account, int64, error) {
		return nil, 0, errors.New("db down")
	}

	_, err := h.ListAccountsByClient(context.Background(), &pb.ListAccountsByClientRequest{ClientId: 42})
	require.Error(t, err)
	assert.Equal(t, codes.Unknown, status.Code(err))
}

// ---------------------------------------------------------------------------
// ListAllAccounts tests
// ---------------------------------------------------------------------------

func TestListAllAccounts_Success(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.listAllAccountsFn = func(nameFilter, numberFilter, typeFilter string, page, pageSize int) ([]model.Account, int64, error) {
		return []model.Account{*sampleAccount(1)}, 1, nil
	}

	resp, err := h.ListAllAccounts(context.Background(), &pb.ListAllAccountsRequest{Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	assert.Len(t, resp.Accounts, 1)
}

func TestListAllAccounts_ServiceError(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.listAllAccountsFn = func(_, _, _ string, _, _ int) ([]model.Account, int64, error) {
		return nil, 0, errors.New("db down")
	}

	_, err := h.ListAllAccounts(context.Background(), &pb.ListAllAccountsRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unknown, status.Code(err))
}

// ---------------------------------------------------------------------------
// UpdateAccountName tests
// ---------------------------------------------------------------------------

func TestUpdateAccountName_Success(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.updateAccountNameFn = func(id, clientID uint64, newName string, changedBy int64) error {
		return nil
	}
	f.accountSvc.getAccountFn = func(id uint64) (*model.Account, error) {
		a := sampleAccount(id)
		a.AccountName = "Updated Name"
		return a, nil
	}

	resp, err := h.UpdateAccountName(context.Background(), &pb.UpdateAccountNameRequest{
		Id:       1,
		ClientId: 42,
		NewName:  "Updated Name",
	})
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", resp.AccountName)
}

func TestUpdateAccountName_NotFound(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.updateAccountNameFn = func(id, clientID uint64, newName string, changedBy int64) error {
		return gorm.ErrRecordNotFound
	}

	_, err := h.UpdateAccountName(context.Background(), &pb.UpdateAccountNameRequest{
		Id:       999,
		ClientId: 42,
		NewName:  "New Name",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUpdateAccountName_ServiceError(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.updateAccountNameFn = func(id, clientID uint64, newName string, changedBy int64) error {
		return service.ErrCompanyDuplicate
	}

	_, err := h.UpdateAccountName(context.Background(), &pb.UpdateAccountNameRequest{
		Id:       1,
		ClientId: 42,
		NewName:  "Taken",
	})
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

// ---------------------------------------------------------------------------
// UpdateAccountLimits tests
// ---------------------------------------------------------------------------

func TestUpdateAccountLimits_Success(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.updateAccountLimitsFn = func(id uint64, dailyLimit, monthlyLimit *string, changedBy int64) error {
		return nil
	}
	f.accountSvc.getAccountFn = func(id uint64) (*model.Account, error) {
		return sampleAccount(id), nil
	}

	dailyLimitStr := "50000"
	resp, err := h.UpdateAccountLimits(context.Background(), &pb.UpdateAccountLimitsRequest{
		Id:         1,
		DailyLimit: &dailyLimitStr,
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(1), resp.Id)
}

func TestUpdateAccountLimits_NotFound(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.updateAccountLimitsFn = func(id uint64, dailyLimit, monthlyLimit *string, changedBy int64) error {
		return gorm.ErrRecordNotFound
	}

	_, err := h.UpdateAccountLimits(context.Background(), &pb.UpdateAccountLimitsRequest{Id: 999})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUpdateAccountLimits_InvalidValue(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.updateAccountLimitsFn = func(id uint64, dailyLimit, monthlyLimit *string, changedBy int64) error {
		return service.ErrInvalidAccount
	}

	badVal := "not-a-number"
	_, err := h.UpdateAccountLimits(context.Background(), &pb.UpdateAccountLimitsRequest{
		Id:         1,
		DailyLimit: &badVal,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// ---------------------------------------------------------------------------
// UpdateAccountStatus tests
// ---------------------------------------------------------------------------

func TestUpdateAccountStatus_Success(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.updateAccountStatusFn = func(id uint64, newStatus string, changedBy int64) error {
		return nil
	}
	f.accountSvc.getAccountFn = func(id uint64) (*model.Account, error) {
		a := sampleAccount(id)
		a.Status = "inactive"
		return a, nil
	}

	resp, err := h.UpdateAccountStatus(context.Background(), &pb.UpdateAccountStatusRequest{
		Id:     1,
		Status: "inactive",
	})
	require.NoError(t, err)
	assert.Equal(t, "inactive", resp.Status)
	assert.Len(t, f.producer.accountStatusChangedCalls, 1)
}

func TestUpdateAccountStatus_NotFound(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.updateAccountStatusFn = func(id uint64, newStatus string, changedBy int64) error {
		return gorm.ErrRecordNotFound
	}

	_, err := h.UpdateAccountStatus(context.Background(), &pb.UpdateAccountStatusRequest{
		Id:     999,
		Status: "inactive",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUpdateAccountStatus_AlreadyInState(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.updateAccountStatusFn = func(id uint64, newStatus string, changedBy int64) error {
		return service.ErrAccountInactive
	}

	_, err := h.UpdateAccountStatus(context.Background(), &pb.UpdateAccountStatusRequest{
		Id:     1,
		Status: "active",
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// ---------------------------------------------------------------------------
// UpdateBalance tests
// ---------------------------------------------------------------------------

func TestUpdateBalance_Success(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.updateBalanceWithOptsFn = func(accountNumber string, amount decimal.Decimal, updateAvailable bool, opts repository.UpdateBalanceOpts) error {
		return nil
	}
	f.accountSvc.getAccountByNumberFn = func(accountNumber string) (*model.Account, error) {
		return sampleAccount(1), nil
	}

	resp, err := h.UpdateBalance(context.Background(), &pb.UpdateBalanceRequest{
		AccountNumber:   "111000100000099011",
		Amount:          "500",
		UpdateAvailable: true,
		IdempotencyKey:  "ub-success-1",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestUpdateBalance_NotFound(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.updateBalanceWithOptsFn = func(accountNumber string, amount decimal.Decimal, updateAvailable bool, opts repository.UpdateBalanceOpts) error {
		return gorm.ErrRecordNotFound
	}

	_, err := h.UpdateBalance(context.Background(), &pb.UpdateBalanceRequest{
		AccountNumber:  "NONEXISTENT",
		Amount:         "100",
		IdempotencyKey: "ub-notfound-1",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUpdateBalance_InsufficientFunds(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.updateBalanceWithOptsFn = func(accountNumber string, amount decimal.Decimal, updateAvailable bool, opts repository.UpdateBalanceOpts) error {
		return service.ErrInsufficientBalance
	}

	_, err := h.UpdateBalance(context.Background(), &pb.UpdateBalanceRequest{
		AccountNumber:  "111000100000099011",
		Amount:         "-99999999",
		IdempotencyKey: "ub-insufficient-1",
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// ---------------------------------------------------------------------------
// CreateCompany tests
// ---------------------------------------------------------------------------

func TestCreateCompany_Success(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.companySvc.createFn = func(company *model.Company) error {
		company.ID = 5
		return nil
	}

	resp, err := h.CreateCompany(context.Background(), &pb.CreateCompanyRequest{
		CompanyName:        "Acme Corp",
		RegistrationNumber: "12345678",
		TaxNumber:          "123456789",
		OwnerId:            10,
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(5), resp.Id)
	assert.Equal(t, "Acme Corp", resp.CompanyName)
}

func TestCreateCompany_ServiceError(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.companySvc.createFn = func(company *model.Company) error {
		return service.ErrCompanyDuplicate
	}

	_, err := h.CreateCompany(context.Background(), &pb.CreateCompanyRequest{
		CompanyName:        "Dupe Corp",
		RegistrationNumber: "12345678",
		TaxNumber:          "123456789",
		OwnerId:            10,
	})
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

// ---------------------------------------------------------------------------
// GetCompany tests
// ---------------------------------------------------------------------------

func TestGetCompany_Success(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.companySvc.getFn = func(id uint64) (*model.Company, error) {
		return &model.Company{
			ID:                 id,
			CompanyName:        "Test Corp",
			RegistrationNumber: "12345678",
			TaxNumber:          "123456789",
			OwnerID:            10,
		}, nil
	}

	resp, err := h.GetCompany(context.Background(), &pb.GetCompanyRequest{Id: 3})
	require.NoError(t, err)
	assert.Equal(t, uint64(3), resp.Id)
	assert.Equal(t, "Test Corp", resp.CompanyName)
}

func TestGetCompany_NotFound(t *testing.T) {
	h, _ := newGRPCHandlerFixture(t)

	_, err := h.GetCompany(context.Background(), &pb.GetCompanyRequest{Id: 999})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// ---------------------------------------------------------------------------
// UpdateCompany tests
// ---------------------------------------------------------------------------

func TestUpdateCompany_Success(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	existingCompany := &model.Company{
		ID:                 1,
		CompanyName:        "Old Name",
		RegistrationNumber: "12345678",
		TaxNumber:          "123456789",
		OwnerID:            10,
	}
	f.companySvc.getFn = func(id uint64) (*model.Company, error) {
		return existingCompany, nil
	}
	f.companySvc.updateFn = func(company *model.Company) error {
		return nil
	}

	newName := "New Name"
	resp, err := h.UpdateCompany(context.Background(), &pb.UpdateCompanyRequest{
		Id:          1,
		CompanyName: &newName,
	})
	require.NoError(t, err)
	assert.Equal(t, "New Name", resp.CompanyName)
}

func TestUpdateCompany_NotFound(t *testing.T) {
	h, _ := newGRPCHandlerFixture(t)

	newName := "X"
	_, err := h.UpdateCompany(context.Background(), &pb.UpdateCompanyRequest{Id: 999, CompanyName: &newName})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUpdateCompany_UpdateError(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.companySvc.getFn = func(id uint64) (*model.Company, error) {
		return &model.Company{ID: id, CompanyName: "Test", RegistrationNumber: "12345678", TaxNumber: "123456789", OwnerID: 1}, nil
	}
	f.companySvc.updateFn = func(company *model.Company) error {
		return errors.New("db write failed")
	}

	newName := "Updated"
	_, err := h.UpdateCompany(context.Background(), &pb.UpdateCompanyRequest{Id: 1, CompanyName: &newName})
	require.Error(t, err)
	assert.Equal(t, codes.Unknown, status.Code(err))
}

// ---------------------------------------------------------------------------
// ListCurrencies tests
// ---------------------------------------------------------------------------

func TestListCurrencies_Success(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.currencySvc.listFn = func() ([]model.Currency, error) {
		return []model.Currency{
			{ID: 1, Code: "RSD", Name: "Serbian Dinar", Symbol: "din", Active: true},
			{ID: 2, Code: "EUR", Name: "Euro", Symbol: "€", Active: true},
		}, nil
	}

	resp, err := h.ListCurrencies(context.Background(), &pb.ListCurrenciesRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Currencies, 2)
	assert.Equal(t, "RSD", resp.Currencies[0].Code)
}

func TestListCurrencies_ServiceError(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.currencySvc.listFn = func() ([]model.Currency, error) {
		return nil, errors.New("db down")
	}

	_, err := h.ListCurrencies(context.Background(), &pb.ListCurrenciesRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unknown, status.Code(err))
}

// ---------------------------------------------------------------------------
// GetCurrency tests
// ---------------------------------------------------------------------------

func TestGetCurrency_Success(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.currencySvc.getByCodeFn = func(code string) (*model.Currency, error) {
		return &model.Currency{ID: 1, Code: code, Name: "Euro", Symbol: "€", Active: true}, nil
	}

	resp, err := h.GetCurrency(context.Background(), &pb.GetCurrencyRequest{Code: "EUR"})
	require.NoError(t, err)
	assert.Equal(t, "EUR", resp.Code)
	assert.True(t, resp.Active)
}

func TestGetCurrency_NotFound(t *testing.T) {
	h, _ := newGRPCHandlerFixture(t)

	_, err := h.GetCurrency(context.Background(), &pb.GetCurrencyRequest{Code: "ZZZ"})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// ---------------------------------------------------------------------------
// GetLedgerEntries tests
// ---------------------------------------------------------------------------

func TestGetLedgerEntries_Success(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	now := time.Now()
	f.ledgerSvc.getLedgerEntriesFn = func(accountNumber string, page, pageSize int) ([]model.LedgerEntry, int64, error) {
		return []model.LedgerEntry{
			{
				ID:            1,
				AccountNumber: accountNumber,
				EntryType:     "credit",
				Amount:        decimal.NewFromInt(500),
				BalanceBefore: decimal.NewFromInt(500),
				BalanceAfter:  decimal.NewFromInt(1000),
				Description:   "deposit",
				CreatedAt:     now,
			},
		}, 1, nil
	}

	resp, err := h.GetLedgerEntries(context.Background(), &pb.GetLedgerEntriesRequest{
		AccountNumber: "111000100000099011",
		Page:          1,
		PageSize:      10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.TotalCount)
	assert.Len(t, resp.Entries, 1)
	assert.Equal(t, "credit", resp.Entries[0].EntryType)
}

func TestGetLedgerEntries_Defaults_AppliedWhenZero(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	var gotPage, gotPageSize int
	f.ledgerSvc.getLedgerEntriesFn = func(accountNumber string, page, pageSize int) ([]model.LedgerEntry, int64, error) {
		gotPage = page
		gotPageSize = pageSize
		return nil, 0, nil
	}

	_, err := h.GetLedgerEntries(context.Background(), &pb.GetLedgerEntriesRequest{
		AccountNumber: "ACC001",
		Page:          0,
		PageSize:      0,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, gotPage, "page should default to 1")
	assert.Equal(t, 20, gotPageSize, "page_size should default to 20")
}

func TestGetLedgerEntries_ServiceError(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.ledgerSvc.getLedgerEntriesFn = func(accountNumber string, page, pageSize int) ([]model.LedgerEntry, int64, error) {
		return nil, 0, errors.New("db down")
	}

	_, err := h.GetLedgerEntries(context.Background(), &pb.GetLedgerEntriesRequest{AccountNumber: "ACC001"})
	require.Error(t, err)
	assert.Equal(t, codes.Unknown, status.Code(err))
}

// ---------------------------------------------------------------------------
// Compile-time interface assertions
// ---------------------------------------------------------------------------

var (
	_ accountSvcFacade  = (*mockAccountSvc)(nil)
	_ companySvcFacade  = (*mockCompanySvc)(nil)
	_ currencySvcFacade = (*mockCurrencySvc)(nil)
	_ ledgerSvcFacade   = (*mockLedgerSvc)(nil)
	_ accountProducer   = (*mockAccountProducer)(nil)
)
