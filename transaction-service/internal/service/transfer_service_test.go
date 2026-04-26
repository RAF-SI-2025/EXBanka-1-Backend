package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	accountpb "github.com/exbanka/contract/accountpb"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/transaction-service/internal/model"
)

// ---- mockTransferRepo -------------------------------------------------------

type mockTransferRepo struct {
	transfers map[uint64]*model.Transfer
	nextID    uint64
}

func newMockTransferRepo() *mockTransferRepo {
	return &mockTransferRepo{transfers: make(map[uint64]*model.Transfer), nextID: 1}
}
func (r *mockTransferRepo) Create(t *model.Transfer) error {
	t.ID = r.nextID
	r.nextID++
	cp := *t
	r.transfers[t.ID] = &cp
	return nil
}
func (r *mockTransferRepo) GetByID(id uint64) (*model.Transfer, error) {
	if t, ok := r.transfers[id]; ok {
		cp := *t
		return &cp, nil
	}
	return nil, errNotFound
}
func (r *mockTransferRepo) GetByIdempotencyKey(key string) (*model.Transfer, error) {
	for _, t := range r.transfers {
		if t.IdempotencyKey == key {
			cp := *t
			return &cp, nil
		}
	}
	return nil, errNotFound
}
func (r *mockTransferRepo) UpdateStatus(id uint64, status string) error {
	if t, ok := r.transfers[id]; ok {
		t.Status = status
	}
	return nil
}
func (r *mockTransferRepo) UpdateStatusWithReason(id uint64, status, reason string) error {
	if t, ok := r.transfers[id]; ok {
		t.Status = status
		t.FailureReason = reason
	}
	return nil
}
func (r *mockTransferRepo) ListByAccountNumbers(_ []string, _, _ int) ([]model.Transfer, int64, error) {
	return nil, 0, nil
}

// ---- mockAccountClientForTransfer -------------------------------------------

type balanceCall struct {
	accountNumber string
	amount        string
}

type mockAccountClientForTransfer struct {
	calls          []balanceCall
	failOnCall     int // 0 = never fail; N = fail on the Nth UpdateBalance call
	callCount      int
	ownerOverrides map[string]uint64 // optional: account number → owner ID
}

func (m *mockAccountClientForTransfer) UpdateBalance(_ context.Context, req *accountpb.UpdateBalanceRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	m.callCount++
	m.calls = append(m.calls, balanceCall{req.AccountNumber, req.Amount})
	if m.failOnCall > 0 && m.callCount == m.failOnCall {
		return nil, errors.New("simulated UpdateBalance failure")
	}
	return &accountpb.AccountResponse{}, nil
}
func (m *mockAccountClientForTransfer) CreateAccount(_ context.Context, _ *accountpb.CreateAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) GetAccount(_ context.Context, _ *accountpb.GetAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) GetAccountByNumber(_ context.Context, req *accountpb.GetAccountByNumberRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	currency := "RSD"
	if req.AccountNumber == "TO-EUR-001" {
		currency = "EUR"
	}
	ownerID := uint64(1)
	if m.ownerOverrides != nil {
		if id, ok := m.ownerOverrides[req.AccountNumber]; ok {
			ownerID = id
		}
	}
	return &accountpb.AccountResponse{AccountNumber: req.AccountNumber, CurrencyCode: currency, OwnerId: ownerID}, nil
}
func (m *mockAccountClientForTransfer) ListAccountsByClient(_ context.Context, _ *accountpb.ListAccountsByClientRequest, _ ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) ListAllAccounts(_ context.Context, _ *accountpb.ListAllAccountsRequest, _ ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) UpdateAccountName(_ context.Context, _ *accountpb.UpdateAccountNameRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) UpdateAccountLimits(_ context.Context, _ *accountpb.UpdateAccountLimitsRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) UpdateAccountStatus(_ context.Context, _ *accountpb.UpdateAccountStatusRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) CreateCompany(_ context.Context, _ *accountpb.CreateCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) GetCompany(_ context.Context, _ *accountpb.GetCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) UpdateCompany(_ context.Context, _ *accountpb.UpdateCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) ListCurrencies(_ context.Context, _ *accountpb.ListCurrenciesRequest, _ ...grpc.CallOption) (*accountpb.ListCurrenciesResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) GetCurrency(_ context.Context, _ *accountpb.GetCurrencyRequest, _ ...grpc.CallOption) (*accountpb.CurrencyResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) GetLedgerEntries(_ context.Context, _ *accountpb.GetLedgerEntriesRequest, _ ...grpc.CallOption) (*accountpb.GetLedgerEntriesResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) ReserveFunds(_ context.Context, _ *accountpb.ReserveFundsRequest, _ ...grpc.CallOption) (*accountpb.ReserveFundsResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) ReleaseReservation(_ context.Context, _ *accountpb.ReleaseReservationRequest, _ ...grpc.CallOption) (*accountpb.ReleaseReservationResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) PartialSettleReservation(_ context.Context, _ *accountpb.PartialSettleReservationRequest, _ ...grpc.CallOption) (*accountpb.PartialSettleReservationResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) GetReservation(_ context.Context, _ *accountpb.GetReservationRequest, _ ...grpc.CallOption) (*accountpb.GetReservationResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) ReserveIncoming(_ context.Context, _ *accountpb.ReserveIncomingRequest, _ ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) CommitIncoming(_ context.Context, _ *accountpb.CommitIncomingRequest, _ ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error) {
	return nil, nil
}
func (m *mockAccountClientForTransfer) ReleaseIncoming(_ context.Context, _ *accountpb.ReleaseIncomingRequest, _ ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
	return nil, nil
}

// ---- mockBankAccountClient --------------------------------------------------

type mockBankAccountClient struct {
	accounts []*accountpb.AccountResponse
	listErr  error
}

func (m *mockBankAccountClient) CreateBankAccount(_ context.Context, _ *accountpb.CreateBankAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockBankAccountClient) ListBankAccounts(_ context.Context, _ *accountpb.ListBankAccountsRequest, _ ...grpc.CallOption) (*accountpb.ListBankAccountsResponse, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return &accountpb.ListBankAccountsResponse{Accounts: m.accounts}, nil
}
func (m *mockBankAccountClient) DeleteBankAccount(_ context.Context, _ *accountpb.DeleteBankAccountRequest, _ ...grpc.CallOption) (*accountpb.DeleteBankAccountResponse, error) {
	return nil, nil
}
func (m *mockBankAccountClient) GetBankRSDAccount(_ context.Context, _ *accountpb.GetBankRSDAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, nil
}
func (m *mockBankAccountClient) DebitBankAccount(_ context.Context, _ *accountpb.BankAccountOpRequest, _ ...grpc.CallOption) (*accountpb.BankAccountOpResponse, error) {
	return nil, nil
}
func (m *mockBankAccountClient) CreditBankAccount(_ context.Context, _ *accountpb.BankAccountOpRequest, _ ...grpc.CallOption) (*accountpb.BankAccountOpResponse, error) {
	return nil, nil
}

// ---- helpers ----------------------------------------------------------------

func standardBankAccounts() []*accountpb.AccountResponse {
	return []*accountpb.AccountResponse{
		{AccountNumber: "BANK-RSD-001", CurrencyCode: "RSD"},
		{AccountNumber: "BANK-EUR-001", CurrencyCode: "EUR"},
	}
}

func buildCrossCurrencyTransfer(repo *mockTransferRepo) *model.Transfer {
	t := &model.Transfer{
		FromAccountNumber: "FROM-RSD-001",
		ToAccountNumber:   "TO-EUR-001",
		FromCurrency:      "RSD",
		ToCurrency:        "EUR",
		InitialAmount:     decimal.NewFromInt(10000),
		FinalAmount:       decimal.NewFromInt(100), // ~100 EUR
		Commission:        decimal.NewFromInt(10),
		ExchangeRate:      decimal.NewFromFloat(0.01),
		Status:            "pending_verification",
	}
	_ = repo.Create(t)
	return t
}

func newCrossCurrencyTransferService(accountClient *mockAccountClientForTransfer, bankClient *mockBankAccountClient) (*TransferService, *mockTransferRepo) {
	repo := newMockTransferRepo()
	feeSvc := &FeeService{repo: &mockFeeRepo{}}
	svc := NewTransferService(repo, nil, accountClient, bankClient, feeSvc, nil, nil)
	// MaxAttempts=1 ensures each UpdateBalance is attempted exactly once,
	// so failOnCall indices are deterministic and tests run without sleep delays.
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}
	return svc, repo
}

// ---- Tests ------------------------------------------------------------------

func TestValidateTransfer(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		amount  float64
		wantErr bool
	}{
		{"valid transfer", "ACC001", "ACC002", 100.0, false},
		{"same account", "ACC001", "ACC001", 100.0, true},
		{"zero amount", "ACC001", "ACC002", 0.0, true},
		{"negative amount", "ACC001", "ACC002", -50.0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTransfer(tt.from, tt.to, decimal.NewFromFloat(tt.amount))
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestExecuteTransfer_CrossCurrency_HappyPath(t *testing.T) {
	accountClient := &mockAccountClientForTransfer{}
	bankClient := &mockBankAccountClient{accounts: standardBankAccounts()}
	svc, repo := newCrossCurrencyTransferService(accountClient, bankClient)

	transfer := buildCrossCurrencyTransfer(repo)
	err := svc.ExecuteTransfer(context.Background(), transfer.ID)
	require.NoError(t, err)

	require.Len(t, accountClient.calls, 4, "exactly 4 UpdateBalance calls for cross-currency")

	totalDebit := transfer.InitialAmount.Add(transfer.Commission) // 10010

	assert.Equal(t, "FROM-RSD-001", accountClient.calls[0].accountNumber)
	assert.Equal(t, totalDebit.Neg().StringFixed(4), accountClient.calls[0].amount)

	assert.Equal(t, "BANK-RSD-001", accountClient.calls[1].accountNumber)
	assert.Equal(t, totalDebit.StringFixed(4), accountClient.calls[1].amount)

	assert.Equal(t, "BANK-EUR-001", accountClient.calls[2].accountNumber)
	assert.Equal(t, transfer.FinalAmount.Neg().StringFixed(4), accountClient.calls[2].amount)

	assert.Equal(t, "TO-EUR-001", accountClient.calls[3].accountNumber)
	assert.Equal(t, transfer.FinalAmount.StringFixed(4), accountClient.calls[3].amount)

	persisted, _ := repo.GetByID(transfer.ID)
	assert.Equal(t, "completed", persisted.Status)
}

func TestExecuteTransfer_CrossCurrency_Step1Fails(t *testing.T) {
	accountClient := &mockAccountClientForTransfer{failOnCall: 1}
	bankClient := &mockBankAccountClient{accounts: standardBankAccounts()}
	svc, repo := newCrossCurrencyTransferService(accountClient, bankClient)

	transfer := buildCrossCurrencyTransfer(repo)
	err := svc.ExecuteTransfer(context.Background(), transfer.ID)

	assert.Error(t, err)
	assert.Len(t, accountClient.calls, 1, "only step 1 attempted — no compensation needed")

	persisted, _ := repo.GetByID(transfer.ID)
	assert.Equal(t, "failed", persisted.Status)
}

func TestExecuteTransfer_CrossCurrency_Step2Fails(t *testing.T) {
	accountClient := &mockAccountClientForTransfer{failOnCall: 2}
	bankClient := &mockBankAccountClient{accounts: standardBankAccounts()}
	svc, repo := newCrossCurrencyTransferService(accountClient, bankClient)

	transfer := buildCrossCurrencyTransfer(repo)
	err := svc.ExecuteTransfer(context.Background(), transfer.ID)

	assert.Error(t, err)
	require.Len(t, accountClient.calls, 3) // step1 + step2(fail) + reverse-step1

	totalDebit := transfer.InitialAmount.Add(transfer.Commission)
	assert.Equal(t, "FROM-RSD-001", accountClient.calls[2].accountNumber, "reversal targets FROM account")
	assert.Equal(t, totalDebit.StringFixed(4), accountClient.calls[2].amount, "reversal is positive (refund)")

	persisted, _ := repo.GetByID(transfer.ID)
	assert.Equal(t, "failed", persisted.Status)
}

func TestExecuteTransfer_CrossCurrency_Step4Fails(t *testing.T) {
	accountClient := &mockAccountClientForTransfer{failOnCall: 4}
	bankClient := &mockBankAccountClient{accounts: standardBankAccounts()}
	svc, repo := newCrossCurrencyTransferService(accountClient, bankClient)

	transfer := buildCrossCurrencyTransfer(repo)
	err := svc.ExecuteTransfer(context.Background(), transfer.ID)

	assert.Error(t, err)
	assert.Len(t, accountClient.calls, 7, "4 forward + 3 reversals when step 4 fails")

	persisted, _ := repo.GetByID(transfer.ID)
	assert.Equal(t, "failed", persisted.Status)
}

// TestExecuteTransfer_CrossCurrency_Step3Fails verifies steps 2 and 1 are reversed.
func TestExecuteTransfer_CrossCurrency_Step3Fails(t *testing.T) {
	accountClient := &mockAccountClientForTransfer{failOnCall: 3}
	bankClient := &mockBankAccountClient{accounts: standardBankAccounts()}
	svc, repo := newCrossCurrencyTransferService(accountClient, bankClient)

	transfer := buildCrossCurrencyTransfer(repo)
	err := svc.ExecuteTransfer(context.Background(), transfer.ID)

	assert.Error(t, err)
	// Calls: step1, step2, step3(fail), reverse-step2, reverse-step1 = 5 total
	require.Len(t, accountClient.calls, 5)

	totalDebit := transfer.InitialAmount.Add(transfer.Commission)

	// reverse-step2: debit bank FROM-currency account (undo the credit)
	assert.Equal(t, "BANK-RSD-001", accountClient.calls[3].accountNumber, "reversal targets bank FROM account")
	assert.Equal(t, totalDebit.Neg().StringFixed(4), accountClient.calls[3].amount, "reversal debits bank FROM account")

	// reverse-step1: credit user FROM account (refund)
	assert.Equal(t, "FROM-RSD-001", accountClient.calls[4].accountNumber, "reversal targets user FROM account")
	assert.Equal(t, totalDebit.StringFixed(4), accountClient.calls[4].amount, "reversal credits user FROM account")

	persisted, _ := repo.GetByID(transfer.ID)
	assert.Equal(t, "failed", persisted.Status)
}

// TestExecuteTransfer_CrossCurrency_BankAccountListFails verifies that a ListBankAccounts
// error marks the transfer failed and makes no balance changes.
func TestExecuteTransfer_CrossCurrency_BankAccountListFails(t *testing.T) {
	accountClient := &mockAccountClientForTransfer{}
	bankClient := &mockBankAccountClient{listErr: errors.New("bank service unavailable")}
	svc, repo := newCrossCurrencyTransferService(accountClient, bankClient)

	transfer := buildCrossCurrencyTransfer(repo)
	err := svc.ExecuteTransfer(context.Background(), transfer.ID)

	assert.Error(t, err)
	assert.Empty(t, accountClient.calls, "no balance changes when ListBankAccounts fails")

	persisted, _ := repo.GetByID(transfer.ID)
	assert.Equal(t, "failed", persisted.Status)
}

func TestExecuteTransfer_CrossCurrency_NoBankAccount(t *testing.T) {
	accountClient := &mockAccountClientForTransfer{}
	// Only RSD bank account — no EUR.
	bankClient := &mockBankAccountClient{accounts: []*accountpb.AccountResponse{
		{AccountNumber: "BANK-RSD-001", CurrencyCode: "RSD"},
	}}
	svc, repo := newCrossCurrencyTransferService(accountClient, bankClient)

	transfer := buildCrossCurrencyTransfer(repo)
	err := svc.ExecuteTransfer(context.Background(), transfer.ID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "EUR", "error must name the missing currency")
	assert.Empty(t, accountClient.calls, "no balance changes when bank account is missing")

	persisted, _ := repo.GetByID(transfer.ID)
	assert.Equal(t, "failed", persisted.Status)
}

func TestFindBankAccountByCurrency_Found(t *testing.T) {
	num, err := findBankAccountByCurrency(standardBankAccounts(), "EUR")
	require.NoError(t, err)
	assert.Equal(t, "BANK-EUR-001", num)
}

func TestFindBankAccountByCurrency_NotFound(t *testing.T) {
	_, err := findBankAccountByCurrency(standardBankAccounts(), "USD")
	assert.Error(t, err)
}

// ---- mockExchangeClient -----------------------------------------------------

type mockExchangeClient struct {
	convertedAmount decimal.Decimal
	effectiveRate   decimal.Decimal
	err             error
}

func (m *mockExchangeClient) ConvertViaRSD(_ context.Context, _, _ string, _ decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
	return m.convertedAmount, m.effectiveRate, m.err
}

// ---- new CreateTransfer tests -----------------------------------------------

// TestCreateTransfer_SameCurrency_ZeroCommission verifies that a same-currency
// transfer ("prenos") sets commission to zero regardless of fee rules.
func TestCreateTransfer_SameCurrency_ZeroCommission(t *testing.T) {
	repo := newMockTransferRepo()
	feeSvc := newTestFeeService(0, 0.5) // 0.5% fee would apply if cross-currency
	svc := NewTransferService(repo, nil, nil, nil, feeSvc, nil, nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}

	t.Run("same RSD to RSD", func(t *testing.T) {
		tr := &model.Transfer{
			FromAccountNumber: "ACC-RSD-A",
			ToAccountNumber:   "ACC-RSD-B",
			FromCurrency:      "RSD",
			ToCurrency:        "RSD",
			InitialAmount:     decimal.NewFromInt(5000),
		}
		require.NoError(t, svc.CreateTransfer(context.Background(), tr))
		assert.True(t, tr.Commission.IsZero(), "prenos must have zero commission")
		assert.True(t, tr.FinalAmount.Equal(tr.InitialAmount), "final amount equals initial for prenos")
		assert.True(t, tr.ExchangeRate.Equal(decimal.NewFromInt(1)), "exchange rate is 1 for prenos")
		assert.Equal(t, "pending_verification", tr.Status)
	})

	t.Run("empty currencies treated as same-currency", func(t *testing.T) {
		tr := &model.Transfer{
			FromAccountNumber: "ACC-RSD-C",
			ToAccountNumber:   "ACC-RSD-D",
			FromCurrency:      "",
			ToCurrency:        "",
			InitialAmount:     decimal.NewFromInt(1000),
		}
		require.NoError(t, svc.CreateTransfer(context.Background(), tr))
		assert.True(t, tr.Commission.IsZero(), "empty currencies → prenos → zero commission")
		assert.True(t, tr.FinalAmount.Equal(tr.InitialAmount))
	})
}

// TestCreateTransfer_CrossCurrency_CommissionApplied verifies that a cross-currency
// transfer calculates fee and uses the exchange client to determine the final amount.
// FeeValue is a percentage number: 0.1 means 0.1%, so 0.1% of 10000 = 10.
func TestCreateTransfer_CrossCurrency_CommissionApplied(t *testing.T) {
	repo := newMockTransferRepo()
	feeSvc := newTestFeeService(0, 0.1) // 0.1% fee on all amounts (FeeValue=0.1 → amount*0.1/100)

	exchangeClient := &mockExchangeClient{
		convertedAmount: decimal.NewFromFloat(50.0),
		effectiveRate:   decimal.NewFromFloat(0.005),
	}

	svc := NewTransferService(repo, exchangeClient, nil, nil, feeSvc, nil, nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}

	tr := &model.Transfer{
		FromAccountNumber: "ACC-RSD-001",
		ToAccountNumber:   "ACC-EUR-001",
		FromCurrency:      "RSD",
		ToCurrency:        "EUR",
		InitialAmount:     decimal.NewFromInt(10000),
	}
	require.NoError(t, svc.CreateTransfer(context.Background(), tr))

	// 10000 * 0.1 / 100 = 10 RSD commission
	expectedCommission := decimal.NewFromInt(10000).Mul(decimal.NewFromFloat(0.1)).Div(decimal.NewFromInt(100))
	assert.True(t, tr.Commission.Equal(expectedCommission),
		"commission must be 0.1%% of 10000 = 10; got %s", tr.Commission.String())
	assert.False(t, tr.Commission.IsZero(),
		"cross-currency transfer must have non-zero commission")
	assert.True(t, tr.FinalAmount.Equal(decimal.NewFromFloat(50.0)),
		"final amount must equal exchange-converted value; got %s", tr.FinalAmount.String())
	assert.True(t, tr.ExchangeRate.Equal(decimal.NewFromFloat(0.005)),
		"exchange rate must match client response; got %s", tr.ExchangeRate.String())
	assert.Equal(t, "pending_verification", tr.Status)
}

// TestCreateTransfer_CrossClientOwnership_Rejected verifies that CreateTransfer
// rejects a transfer when the two accounts belong to different clients.
func TestCreateTransfer_CrossClientOwnership_Rejected(t *testing.T) {
	repo := newMockTransferRepo()
	feeSvc := &FeeService{repo: &mockFeeRepo{}}

	// ownerOverrides: FROM belongs to client 1, TO belongs to client 2
	accountClient := &mockAccountClientForTransfer{
		ownerOverrides: map[string]uint64{
			"ACC-CLIENT-1": 1,
			"ACC-CLIENT-2": 2,
		},
	}

	svc := NewTransferService(repo, nil, accountClient, nil, feeSvc, nil, nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}

	tr := &model.Transfer{
		FromAccountNumber: "ACC-CLIENT-1",
		ToAccountNumber:   "ACC-CLIENT-2",
		FromCurrency:      "RSD",
		ToCurrency:        "RSD",
		InitialAmount:     decimal.NewFromInt(500),
	}
	err := svc.CreateTransfer(context.Background(), tr)
	require.Error(t, err, "cross-client transfer must be rejected")
	assert.Contains(t, err.Error(), "same client",
		"error message must mention same-client requirement")

	// No transfer record must have been persisted
	assert.Empty(t, repo.transfers, "rejected transfer must not be saved")
}
