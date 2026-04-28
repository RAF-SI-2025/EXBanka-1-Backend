package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// ---------- mocks ----------

type fakeFundAccountClient struct {
	accounts map[uint64]*accountpb.AccountResponse

	debitCalls             []accountCall
	creditCalls            []accountCall
	getAccountCalls        int
	failGetAccount         error
	failDebitOnce          error            // returned once if non-nil, then cleared
	failCreditOnce         error            // same
	specificCreditFailures map[string]error // key = account number
}

type accountCall struct {
	AccountNumber  string
	Amount         string
	Memo           string
	IdempotencyKey string
}

func newFakeFundAccountClient() *fakeFundAccountClient {
	return &fakeFundAccountClient{
		accounts:               map[uint64]*accountpb.AccountResponse{},
		specificCreditFailures: map[string]error{},
	}
}

func (f *fakeFundAccountClient) addAccount(id uint64, accountNumber string, balance string) {
	f.accounts[id] = &accountpb.AccountResponse{
		Id:               id,
		AccountNumber:    accountNumber,
		Balance:          balance,
		AvailableBalance: balance,
	}
}

func (f *fakeFundAccountClient) GetAccount(_ context.Context, in *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error) {
	f.getAccountCalls++
	if f.failGetAccount != nil {
		return nil, f.failGetAccount
	}
	a, ok := f.accounts[in.Id]
	if !ok {
		return nil, errors.New("not found")
	}
	return a, nil
}

func (f *fakeFundAccountClient) CreditAccount(_ context.Context, accountNumber string, amount decimal.Decimal, memo, idemKey string) (*accountpb.AccountResponse, error) {
	if e, ok := f.specificCreditFailures[accountNumber]; ok {
		delete(f.specificCreditFailures, accountNumber)
		return nil, e
	}
	if f.failCreditOnce != nil {
		err := f.failCreditOnce
		f.failCreditOnce = nil
		return nil, err
	}
	f.creditCalls = append(f.creditCalls, accountCall{accountNumber, amount.String(), memo, idemKey})
	return &accountpb.AccountResponse{AccountNumber: accountNumber}, nil
}

func (f *fakeFundAccountClient) DebitAccount(_ context.Context, accountNumber string, amount decimal.Decimal, memo, idemKey string) (*accountpb.AccountResponse, error) {
	if f.failDebitOnce != nil {
		err := f.failDebitOnce
		f.failDebitOnce = nil
		return nil, err
	}
	f.debitCalls = append(f.debitCalls, accountCall{accountNumber, amount.String(), memo, idemKey})
	return &accountpb.AccountResponse{AccountNumber: accountNumber}, nil
}

func (f *fakeFundAccountClient) sumDebited(accountNumber string) decimal.Decimal {
	total := decimal.Zero
	for _, c := range f.debitCalls {
		if c.AccountNumber == accountNumber {
			d, _ := decimal.NewFromString(c.Amount)
			total = total.Add(d)
		}
	}
	return total
}

func (f *fakeFundAccountClient) sumCredited(accountNumber string) decimal.Decimal {
	total := decimal.Zero
	for _, c := range f.creditCalls {
		if c.AccountNumber == accountNumber {
			d, _ := decimal.NewFromString(c.Amount)
			total = total.Add(d)
		}
	}
	return total
}

type fakeFundExchangeClient struct {
	rate     string
	convert  string
	failNext error
}

func (f *fakeFundExchangeClient) Convert(_ context.Context, _ *exchangepb.ConvertRequest) (*exchangepb.ConvertResponse, error) {
	if f.failNext != nil {
		return nil, f.failNext
	}
	return &exchangepb.ConvertResponse{ConvertedAmount: f.convert, EffectiveRate: f.rate}, nil
}

// ---------- fixture ----------

type investSagaFixture struct {
	svc      *FundService
	repo     *repository.FundRepository
	contribs *repository.FundContributionRepository
	pos      *repository.ClientFundPositionRepository
	holdings *repository.FundHoldingRepository
	saga     *fakeSagaRepo
	accounts *fakeFundAccountClient
	exch     *fakeFundExchangeClient
	fund     *model.InvestmentFund
}

func newInvestSagaFixture(t *testing.T) *investSagaFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.InvestmentFund{},
		&model.ClientFundPosition{},
		&model.FundContribution{},
		&model.FundHolding{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.NewFundRepository(db)
	contribs := repository.NewFundContributionRepository(db)
	positions := repository.NewClientFundPositionRepository(db)
	holdings := repository.NewFundHoldingRepository(db)
	saga := newFakeSagaRepo()
	accounts := newFakeFundAccountClient()
	exch := &fakeFundExchangeClient{}
	bac := &fakeBankAccountClient{nextID: 4000}

	svc := NewFundService(repo, bac, nil)
	svc = svc.WithSaga(saga, accounts, exch, contribs, positions, holdings, nil, nil)

	fund := &model.InvestmentFund{
		Name: "Alpha", ManagerEmployeeID: 25,
		MinimumContributionRSD: decimal.Zero, RSDAccountID: 4001, Active: true,
	}
	if err := repo.Create(fund); err != nil {
		t.Fatalf("seed fund: %v", err)
	}
	accounts.addAccount(4001, "FUND", "10000")
	accounts.addAccount(5001, "USER", "5000")

	return &investSagaFixture{
		svc: svc, repo: repo, contribs: contribs, pos: positions, holdings: holdings,
		saga: saga, accounts: accounts, exch: exch, fund: fund,
	}
}

// ---------- happy path ----------

func TestInvestSaga_RSDSource_HappyPath(t *testing.T) {
	fx := newInvestSagaFixture(t)
	out, err := fx.svc.Invest(context.Background(), InvestInput{
		FundID: fx.fund.ID, ActorUserID: 99, ActorSystemType: "client",
		SourceAccountID: 5001, Amount: decimal.NewFromInt(500), Currency: "RSD",
		OnBehalfOfType: "self",
	})
	if err != nil {
		t.Fatalf("invest: %v", err)
	}
	if out.Status != model.FundContributionStatusCompleted {
		t.Errorf("status = %q, want completed", out.Status)
	}
	if !fx.accounts.sumDebited("USER").Equal(decimal.NewFromInt(500)) {
		t.Errorf("source debit: got %s want 500", fx.accounts.sumDebited("USER"))
	}
	if !fx.accounts.sumCredited("FUND").Equal(decimal.NewFromInt(500)) {
		t.Errorf("fund credit: got %s want 500", fx.accounts.sumCredited("FUND"))
	}
	posUID := uint64(99)
	pos, err := fx.pos.GetByFundAndOwner(fx.fund.ID, model.OwnerClient, &posUID)
	if err != nil || !pos.TotalContributedRSD.Equal(decimal.NewFromInt(500)) {
		t.Errorf("position: %+v err=%v", pos, err)
	}
}

// ---------- minimum contribution ----------

func TestInvestSaga_BelowMinimum_RejectedWithoutSideEffects(t *testing.T) {
	fx := newInvestSagaFixture(t)
	fx.fund.MinimumContributionRSD = decimal.NewFromInt(10000)
	if err := fx.repo.Save(fx.fund); err != nil {
		t.Fatalf("save fund: %v", err)
	}
	_, err := fx.svc.Invest(context.Background(), InvestInput{
		FundID: fx.fund.ID, ActorUserID: 99, ActorSystemType: "client",
		SourceAccountID: 5001, Amount: decimal.NewFromInt(500), Currency: "RSD",
	})
	if err == nil || !strings.Contains(err.Error(), "minimum_contribution_not_met") {
		t.Errorf("err: %v", err)
	}
	if len(fx.accounts.debitCalls) != 0 || len(fx.accounts.creditCalls) != 0 {
		t.Errorf("no side effects expected, got %d debits %d credits", len(fx.accounts.debitCalls), len(fx.accounts.creditCalls))
	}
}

// ---------- cross-currency ----------

func TestInvestSaga_CrossCurrency_PopulatesFxRate(t *testing.T) {
	fx := newInvestSagaFixture(t)
	fx.exch.rate = "117"
	fx.exch.convert = "5850"
	out, err := fx.svc.Invest(context.Background(), InvestInput{
		FundID: fx.fund.ID, ActorUserID: 99, ActorSystemType: "client",
		SourceAccountID: 5001, Amount: decimal.NewFromInt(50), Currency: "EUR",
	})
	if err != nil {
		t.Fatalf("invest: %v", err)
	}
	if !out.AmountRSD.Equal(decimal.NewFromInt(5850)) {
		t.Errorf("amount_rsd %s want 5850", out.AmountRSD)
	}
	if out.FxRate == nil || !out.FxRate.Equal(decimal.NewFromInt(117)) {
		t.Errorf("fx %v", out.FxRate)
	}
	// fund credit is in RSD (converted), source debit is native EUR
	if !fx.accounts.sumDebited("USER").Equal(decimal.NewFromInt(50)) {
		t.Errorf("source debit native: got %s", fx.accounts.sumDebited("USER"))
	}
	if !fx.accounts.sumCredited("FUND").Equal(decimal.NewFromInt(5850)) {
		t.Errorf("fund credit converted: got %s", fx.accounts.sumCredited("FUND"))
	}
}

// ---------- compensation: debit_source fails ----------

func TestInvestSaga_DebitSourceFails_NoStateChange(t *testing.T) {
	fx := newInvestSagaFixture(t)
	fx.accounts.failDebitOnce = errors.New("boom")
	_, err := fx.svc.Invest(context.Background(), InvestInput{
		FundID: fx.fund.ID, ActorUserID: 99, ActorSystemType: "client",
		SourceAccountID: 5001, Amount: decimal.NewFromInt(500), Currency: "RSD",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if len(fx.accounts.creditCalls) != 0 {
		t.Errorf("expected no credits, got %d", len(fx.accounts.creditCalls))
	}
	// Contribution row should be marked failed.
	rows, _, _ := fx.contribs.ListByFund(fx.fund.ID, 1, 10)
	if len(rows) != 1 || rows[0].Status != model.FundContributionStatusFailed {
		t.Errorf("contribution: %+v", rows)
	}
}

// ---------- compensation: credit_fund fails → reverse source debit ----------

func TestInvestSaga_CreditFundFails_RefundsSource(t *testing.T) {
	fx := newInvestSagaFixture(t)
	fx.accounts.specificCreditFailures["FUND"] = errors.New("fund credit boom")
	_, err := fx.svc.Invest(context.Background(), InvestInput{
		FundID: fx.fund.ID, ActorUserID: 99, ActorSystemType: "client",
		SourceAccountID: 5001, Amount: decimal.NewFromInt(500), Currency: "RSD",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	// One debit (the original) and one compensating credit to USER.
	if !fx.accounts.sumDebited("USER").Equal(decimal.NewFromInt(500)) {
		t.Errorf("source debit: got %s", fx.accounts.sumDebited("USER"))
	}
	if !fx.accounts.sumCredited("USER").Equal(decimal.NewFromInt(500)) {
		t.Errorf("source compensation credit: got %s", fx.accounts.sumCredited("USER"))
	}
	if !fx.accounts.sumCredited("FUND").IsZero() {
		t.Errorf("fund net credit should be 0, got %s", fx.accounts.sumCredited("FUND"))
	}
	rows, _, _ := fx.contribs.ListByFund(fx.fund.ID, 1, 10)
	if len(rows) != 1 || rows[0].Status != model.FundContributionStatusFailed {
		t.Errorf("contribution: %+v", rows)
	}
}
