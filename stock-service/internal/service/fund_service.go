package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared/outbox"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// BankAccountClient is the minimal subset of accountpb.BankAccountServiceClient
// FundService needs. Stated as an interface so tests can stub it.
type BankAccountClient interface {
	CreateBankAccount(ctx context.Context, in *accountpb.CreateBankAccountRequest) (*accountpb.AccountResponse, error)
}

// FundAccountClient is the minimal account-service surface invest/redeem
// sagas use. Tests stub it.
type FundAccountClient interface {
	GetAccount(ctx context.Context, in *accountpb.GetAccountRequest) (*accountpb.AccountResponse, error)
	CreditAccount(ctx context.Context, accountNumber string, amount decimal.Decimal, memo, idempotencyKey string) (*accountpb.AccountResponse, error)
	DebitAccount(ctx context.Context, accountNumber string, amount decimal.Decimal, memo, idempotencyKey string) (*accountpb.AccountResponse, error)
}

// FundExchangeClient is the minimal exchange-service surface needed for
// cross-currency invest.
type FundExchangeClient interface {
	Convert(ctx context.Context, in *exchangepb.ConvertRequest) (*exchangepb.ConvertResponse, error)
}

// FundSettings exposes the settings the fund saga reads (currently only the
// redemption fee rate).
type FundSettings interface {
	GetDecimal(key string) (decimal.Decimal, error)
}

const bankSentinelUserID uint64 = 1_000_000_000

type FundService struct {
	repo              *repository.FundRepository
	bankAccountClient BankAccountClient
	producer          *kafkaprod.Producer

	// saga deps (optional; wired via WithSaga). Nil → Invest/Redeem return
	// errSagaDepsNotWired. Tests that exercise plain CRUD can skip the wiring.
	sagaRepo         SagaLogRepo
	accounts         FundAccountClient
	exchange         FundExchangeClient
	contribs         *repository.FundContributionRepository
	positions        *repository.ClientFundPositionRepository
	holdings         *repository.FundHoldingRepository
	settings         FundSettings
	bankRSDAccountFn func(context.Context) (string, uint64, error)

	// position-reads deps (optional; wired via WithPositionReads).
	listingRepo *repository.ListingRepository

	// liquidation dep (optional; wired via WithLiquidation). When nil,
	// Redeem's insufficient-cash path returns ErrInsufficientFundCash directly.
	orderPlacer FundOrderPlacer

	// Outbox: when wired (via WithOutbox), post-saga Kafka publishes
	// (stock.fund-invested, stock.fund-redeemed, plus fund-created /
	// fund-updated lifecycle events) go through the transactional outbox
	// instead of best-effort producer.PublishRaw. The drainer goroutine
	// asynchronously publishes pending rows so a crash between business
	// commit and Kafka send no longer drops events. When nil, the legacy
	// direct-publish path is used so unit tests that don't wire a DB still
	// work.
	outbox   *outbox.Outbox
	outboxDB *gorm.DB
}

// WithOutbox wires the transactional outbox + the GORM handle the saga
// uses to enqueue rows. Callers that don't wire this fall back to the
// legacy direct-publish path (best-effort, may drop on crash).
func (s *FundService) WithOutbox(ob *outbox.Outbox, db *gorm.DB) *FundService {
	cp := *s
	cp.outbox = ob
	cp.outboxDB = db
	return &cp
}

func NewFundService(repo *repository.FundRepository, bankAccountClient BankAccountClient, producer *kafkaprod.Producer) *FundService {
	return &FundService{repo: repo, bankAccountClient: bankAccountClient, producer: producer}
}

// WithSaga returns a copy of the receiver wired with the dependencies needed
// by Invest / Redeem. Call sites that exercise plain CRUD (Create / Update /
// Get / List) can skip this and Invest/Redeem will reject with an explicit
// "saga deps not wired" error.
func (s *FundService) WithSaga(
	sagaRepo SagaLogRepo,
	accounts FundAccountClient,
	exchange FundExchangeClient,
	contribs *repository.FundContributionRepository,
	positions *repository.ClientFundPositionRepository,
	holdings *repository.FundHoldingRepository,
	settings FundSettings,
	bankRSDAccountFn func(context.Context) (string, uint64, error),
) *FundService {
	cp := *s
	cp.sagaRepo = sagaRepo
	cp.accounts = accounts
	cp.exchange = exchange
	cp.contribs = contribs
	cp.positions = positions
	cp.holdings = holdings
	cp.settings = settings
	cp.bankRSDAccountFn = bankRSDAccountFn
	return &cp
}

var errSagaDepsNotWired = fmt.Errorf("fund saga dependencies not wired (Invest/Redeem unavailable): %w", ErrFundSagaUnavailable)

// WithPositionReads wires the listing repo. The other deps needed for
// position reads (accounts, exchange, holdings, positions) are already wired
// by WithSaga. Without WithPositionReads the rich list-positions methods
// fall back to plain (contribution-only) rows.
func (s *FundService) WithPositionReads(listingRepo *repository.ListingRepository) *FundService {
	cp := *s
	cp.listingRepo = listingRepo
	return &cp
}

type CreateFundInput struct {
	ActorEmployeeID        int64
	Name                   string
	Description            string
	MinimumContributionRSD decimal.Decimal
}

func (s *FundService) Create(ctx context.Context, in CreateFundInput) (*model.InvestmentFund, error) {
	if in.Name == "" || len(in.Name) > 128 {
		return nil, fmt.Errorf("name must be 1-128 chars: %w", ErrFundInvalidInput)
	}
	if len(in.Description) > 2000 {
		return nil, fmt.Errorf("description must be <= 2000 chars: %w", ErrFundInvalidInput)
	}
	if in.MinimumContributionRSD.IsNegative() {
		return nil, fmt.Errorf("minimum_contribution_rsd must be >= 0: %w", ErrFundInvalidInput)
	}

	acct, err := s.bankAccountClient.CreateBankAccount(ctx, &accountpb.CreateBankAccountRequest{
		CurrencyCode: "RSD",
		AccountKind:  "current",
		AccountName:  fmt.Sprintf("Fund: %s", in.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("create RSD account: %w", err)
	}

	f := &model.InvestmentFund{
		Name:                   in.Name,
		Description:            in.Description,
		ManagerEmployeeID:      in.ActorEmployeeID,
		MinimumContributionRSD: in.MinimumContributionRSD,
		RSDAccountID:           acct.Id,
		Active:                 true,
	}
	if err := s.repo.Create(f); err != nil {
		// Compensation: log only — manual cleanup of the orphan bank account
		// is acceptable for this MVP.
		log.Printf("WARN: fund create failed after bank account %d created: %v", acct.Id, err)
		return nil, err
	}

	if s.producer != nil {
		payload := kafkamsg.StockFundCreatedMessage{
			MessageID:         uuid.NewString(),
			OccurredAt:        time.Now().UTC().Format(time.RFC3339),
			FundID:            f.ID,
			Name:              f.Name,
			ManagerEmployeeID: f.ManagerEmployeeID,
			RSDAccountID:      f.RSDAccountID,
			CreatedAt:         f.CreatedAt.Format(time.RFC3339),
		}
		if data, err := json.Marshal(payload); err == nil {
			publishSagaEvent(ctx, s.outbox, s.outboxDB, s.producer, kafkamsg.TopicStockFundCreated, data, "")
		}
	}
	return f, nil
}

type UpdateFundInput struct {
	ActorEmployeeID        int64
	FundID                 uint64
	Name                   *string
	Description            *string
	MinimumContributionRSD *decimal.Decimal
	Active                 *bool
}

func (s *FundService) Update(ctx context.Context, in UpdateFundInput) (*model.InvestmentFund, error) {
	f, err := s.repo.GetByID(in.FundID)
	if err != nil {
		return nil, err
	}
	if in.ActorEmployeeID != f.ManagerEmployeeID {
		return nil, fmt.Errorf("caller is not the fund manager: %w", ErrFundNotManager)
	}
	changed := []string{}
	if in.Name != nil && *in.Name != f.Name {
		f.Name = *in.Name
		changed = append(changed, "name")
	}
	if in.Description != nil && *in.Description != f.Description {
		f.Description = *in.Description
		changed = append(changed, "description")
	}
	if in.MinimumContributionRSD != nil && !in.MinimumContributionRSD.Equal(f.MinimumContributionRSD) {
		f.MinimumContributionRSD = *in.MinimumContributionRSD
		changed = append(changed, "minimum_contribution_rsd")
	}
	if in.Active != nil && *in.Active != f.Active {
		f.Active = *in.Active
		changed = append(changed, "active")
	}
	if len(changed) == 0 {
		return f, nil
	}
	if err := s.repo.Save(f); err != nil {
		return nil, err
	}

	if s.producer != nil {
		payload := kafkamsg.StockFundUpdatedMessage{
			MessageID:     uuid.NewString(),
			OccurredAt:    time.Now().UTC().Format(time.RFC3339),
			FundID:        f.ID,
			ChangedFields: changed,
			UpdatedAt:     f.UpdatedAt.Format(time.RFC3339),
		}
		if data, err := json.Marshal(payload); err == nil {
			publishSagaEvent(ctx, s.outbox, s.outboxDB, s.producer, kafkamsg.TopicStockFundUpdated, data, "")
		}
	}
	return f, nil
}

func (s *FundService) GetByID(id uint64) (*model.InvestmentFund, error) {
	return s.repo.GetByID(id)
}

func (s *FundService) List(search string, active *bool, page, pageSize int) ([]model.InvestmentFund, int64, error) {
	return s.repo.List(search, active, page, pageSize)
}
