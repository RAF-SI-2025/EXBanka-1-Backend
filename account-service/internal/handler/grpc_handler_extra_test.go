package handler

// Additional handler-layer tests added to push account-service unit coverage
// toward >= 80 %. These cover the reservation / incoming-reservation /
// changelog gRPC entry points on AccountGRPCHandler that newGRPCHandlerFixture
// did not previously exercise.
//
// The reservation-related RPCs on AccountGRPCHandler wrap an inner
// ReservationHandler in IdempotencyRepository.Run, which opens its own
// outer transaction. To keep tests fast and avoid the SQLite single-conn
// deadlock between nested transactions, the inner ReservationHandler is
// built around the same mockReservationSvc used by reservation_handler_test.go.

import (
	"context"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	"github.com/exbanka/account-service/internal/service"
	pb "github.com/exbanka/contract/accountpb"
)

// openSQLite opens a fresh in-memory SQLite gorm DB at the given dsn. When
// maxConns > 0 the connection pool is capped (the standard
// SetMaxOpenConns(1) trick used elsewhere). When 0, the pool is left at
// gorm's default — required when an outer test transaction is held while
// an inner service opens its own transaction (multi-conn DB avoids the
// single-conn deadlock).
func openSQLite(t *testing.T, dsn string, maxConns int) (*gorm.DB, error) {
	t.Helper()
	dsn = strings.ReplaceAll(dsn, "/", "_")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if maxConns > 0 {
		sqlDB, dbErr := db.DB()
		if dbErr != nil {
			return nil, dbErr
		}
		sqlDB.SetMaxOpenConns(maxConns)
	}
	return db, nil
}

// newAccountHandlerWithReservation constructs an AccountGRPCHandler whose
// inner ReservationHandler delegates to a mockReservationSvc. The handler
// fixture's SQLite DB still backs the IdempotencyRepository so the
// idempotency-claim path is exercised end-to-end.
func newAccountHandlerWithReservation(t *testing.T) (*AccountGRPCHandler, *mockReservationSvc, *grpcHandlerFixture) {
	h, f := newGRPCHandlerFixture(t)
	mockRes := &mockReservationSvc{}
	h.reservation = newReservationHandlerForTest(mockRes)
	return h, mockRes, f
}

func TestAccountHandler_ReserveFunds_RequiresKey(t *testing.T) {
	h, _, _ := newAccountHandlerWithReservation(t)
	_, err := h.ReserveFunds(context.Background(), &pb.ReserveFundsRequest{
		AccountId: 1, OrderId: 1, Amount: "100", CurrencyCode: "RSD",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAccountHandler_ReserveFunds_Success(t *testing.T) {
	h, mockRes, _ := newAccountHandlerWithReservation(t)
	called := 0
	mockRes.reserveFn = func(_ context.Context, _, _ uint64, amt decimal.Decimal, _, _ string) (*service.ReserveFundsResult, error) {
		called++
		return &service.ReserveFundsResult{
			ReservationID: 5, ReservedBalance: amt, AvailableBalance: decimal.NewFromInt(900),
		}, nil
	}
	resp, err := h.ReserveFunds(context.Background(), &pb.ReserveFundsRequest{
		AccountId: 1, OrderId: 1, Amount: "100", CurrencyCode: "RSD", IdempotencyKey: "rf-1",
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(5), resp.ReservationId)

	// Replay returns cached response without re-invoking inner svc.
	resp2, err := h.ReserveFunds(context.Background(), &pb.ReserveFundsRequest{
		AccountId: 1, OrderId: 1, Amount: "100", CurrencyCode: "RSD", IdempotencyKey: "rf-1",
	})
	require.NoError(t, err)
	assert.Equal(t, resp.ReservationId, resp2.ReservationId)
	assert.Equal(t, 1, called, "inner svc must run exactly once across two idempotent calls")
}

func TestAccountHandler_ReleaseReservation_RequiresKey(t *testing.T) {
	h, _, _ := newAccountHandlerWithReservation(t)
	_, err := h.ReleaseReservation(context.Background(), &pb.ReleaseReservationRequest{OrderId: 1})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAccountHandler_ReleaseReservation_Success(t *testing.T) {
	h, mockRes, _ := newAccountHandlerWithReservation(t)
	mockRes.releaseFn = func(_ context.Context, _ uint64, _ string) (*service.ReleaseResult, error) {
		return &service.ReleaseResult{ReleasedAmount: decimal.NewFromInt(80), ReservedBalance: decimal.NewFromInt(20)}, nil
	}
	resp, err := h.ReleaseReservation(context.Background(), &pb.ReleaseReservationRequest{
		OrderId: 1, IdempotencyKey: "rl-1",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.ReleasedAmount)
}

func TestAccountHandler_PartialSettleReservation_RequiresKey(t *testing.T) {
	h, _, _ := newAccountHandlerWithReservation(t)
	_, err := h.PartialSettleReservation(context.Background(), &pb.PartialSettleReservationRequest{
		OrderId: 1, OrderTransactionId: 1, Amount: "10",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAccountHandler_PartialSettleReservation_Success(t *testing.T) {
	h, mockRes, _ := newAccountHandlerWithReservation(t)
	mockRes.partialFn = func(_ context.Context, _, _ uint64, amt decimal.Decimal, _, _ string) (*service.PartialSettleResult, error) {
		return &service.PartialSettleResult{
			SettledAmount: amt, RemainingReserved: decimal.Zero,
			BalanceAfter: decimal.NewFromInt(950), LedgerEntryID: 7,
		}, nil
	}
	resp, err := h.PartialSettleReservation(context.Background(), &pb.PartialSettleReservationRequest{
		OrderId: 1, OrderTransactionId: 99, Amount: "30", Memo: "settle",
		IdempotencyKey: "ps-1",
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(7), resp.LedgerEntryId)
}

func TestAccountHandler_GetReservation_NoIdempotencyNeeded(t *testing.T) {
	h, mockRes, _ := newAccountHandlerWithReservation(t)
	mockRes.getFn = func(_ context.Context, _ uint64, _ string) (string, decimal.Decimal, decimal.Decimal, []uint64, bool, error) {
		return "active", decimal.NewFromInt(100), decimal.Zero, nil, true, nil
	}
	resp, err := h.GetReservation(context.Background(), &pb.GetReservationRequest{OrderId: 1})
	require.NoError(t, err)
	assert.True(t, resp.Exists)
	assert.Equal(t, "active", resp.Status)
}

// ---------------------------------------------------------------------------
// Incoming reservation RPCs — same wrapping pattern. We use TWO separate
// SQLite databases: one drives the IdempotencyRepository (single-conn
// outer tx) and the other backs the IncomingReservationService (its own
// connection pool so its inner transaction does not deadlock against the
// outer one).
// ---------------------------------------------------------------------------

// newAccountHandlerWithIncoming wires AccountGRPCHandler with a real
// IncomingReservationService running over a dedicated SQLite database.
// Returns the handler, the seeded recipient account, and both DBs.
func newAccountHandlerWithIncoming(t *testing.T) (*AccountGRPCHandler, *model.Account) {
	t.Helper()

	// DB #1: handler outer-tx + idempotency records.
	idemDB := newHandlerTestDB(t)
	idem := repository.NewIdempotencyRepository(idemDB)

	// DB #2: incoming reservation service. Distinct dsn so it has its own
	// pool — multi-conn so an inner tx can run while the outer claim tx
	// from idemDB is still open.
	dsn := "file:incoming_" + t.Name() + "?mode=memory&cache=shared"
	innerDB, err := openSQLite(t, dsn, 0)
	require.NoError(t, err)
	require.NoError(t, innerDB.AutoMigrate(
		&model.Account{},
		&model.LedgerEntry{},
		&model.IncomingReservation{},
	))

	accountRepo := repository.NewAccountRepository(innerDB)
	resRepo := repository.NewIncomingReservationRepository(innerDB)
	inSvc := service.NewIncomingReservationService(innerDB, accountRepo, resRepo)

	acct := &model.Account{
		AccountNumber:    "111000100000888011",
		OwnerID:          10,
		CurrencyCode:     "RSD",
		AccountKind:      "current",
		AccountType:      "standard",
		Status:           "active",
		Balance:          decimal.NewFromInt(0),
		AvailableBalance: decimal.NewFromInt(0),
		IsBankAccount:    false,
		Version:          1,
	}
	require.NoError(t, accountRepo.Create(acct))

	mockSvc := &mockAccountSvc{
		getAccountByNumberFn: func(num string) (*model.Account, error) {
			return accountRepo.GetByNumber(num)
		},
	}
	h := &AccountGRPCHandler{
		accountService:      mockSvc,
		incomingReservation: inSvc,
		db:                  idemDB,
		idem:                idem,
	}
	return h, acct
}

func TestAccountHandler_ReserveIncoming_RequiresKey(t *testing.T) {
	h, _ := newGRPCHandlerFixture(t)
	_, err := h.ReserveIncoming(context.Background(), &pb.ReserveIncomingRequest{
		AccountNumber: "x", Amount: "10", Currency: "RSD", ReservationKey: "k1",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAccountHandler_ReserveIncoming_Success(t *testing.T) {
	h, acct := newAccountHandlerWithIncoming(t)
	resp, err := h.ReserveIncoming(context.Background(), &pb.ReserveIncomingRequest{
		AccountNumber: acct.AccountNumber, Amount: "100", Currency: "RSD",
		ReservationKey: "rkey-1", IdempotencyKey: "irk-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "rkey-1", resp.ReservationKey)
}

func TestAccountHandler_ReserveIncoming_BadAmount(t *testing.T) {
	h, acct := newAccountHandlerWithIncoming(t)
	_, err := h.ReserveIncoming(context.Background(), &pb.ReserveIncomingRequest{
		AccountNumber: acct.AccountNumber, Amount: "not-a-number", Currency: "RSD",
		ReservationKey: "rkey-x", IdempotencyKey: "irk-x",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAccountHandler_CommitIncoming_RequiresKey(t *testing.T) {
	h, _ := newGRPCHandlerFixture(t)
	_, err := h.CommitIncoming(context.Background(), &pb.CommitIncomingRequest{ReservationKey: "x"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAccountHandler_CommitIncoming_Success(t *testing.T) {
	h, acct := newAccountHandlerWithIncoming(t)
	_, err := h.ReserveIncoming(context.Background(), &pb.ReserveIncomingRequest{
		AccountNumber: acct.AccountNumber, Amount: "100", Currency: "RSD",
		ReservationKey: "rkey-c1", IdempotencyKey: "irk-c1",
	})
	require.NoError(t, err)

	resp, err := h.CommitIncoming(context.Background(), &pb.CommitIncomingRequest{
		ReservationKey: "rkey-c1", IdempotencyKey: "icm-c1",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.BalanceAfter)
}

func TestAccountHandler_CommitIncoming_NotFound(t *testing.T) {
	h, _ := newAccountHandlerWithIncoming(t)
	_, err := h.CommitIncoming(context.Background(), &pb.CommitIncomingRequest{
		ReservationKey: "missing-key", IdempotencyKey: "icm-missing",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestAccountHandler_ReleaseIncoming_RequiresKey(t *testing.T) {
	h, _ := newGRPCHandlerFixture(t)
	_, err := h.ReleaseIncoming(context.Background(), &pb.ReleaseIncomingRequest{ReservationKey: "x"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAccountHandler_ReleaseIncoming_Success(t *testing.T) {
	h, acct := newAccountHandlerWithIncoming(t)
	_, err := h.ReserveIncoming(context.Background(), &pb.ReserveIncomingRequest{
		AccountNumber: acct.AccountNumber, Amount: "30", Currency: "RSD",
		ReservationKey: "rkey-r1", IdempotencyKey: "irk-r1",
	})
	require.NoError(t, err)

	resp, err := h.ReleaseIncoming(context.Background(), &pb.ReleaseIncomingRequest{
		ReservationKey: "rkey-r1", IdempotencyKey: "irl-r1",
	})
	require.NoError(t, err)
	assert.True(t, resp.Released)
}

func TestAccountHandler_ReleaseIncoming_NotFound(t *testing.T) {
	h, _ := newAccountHandlerWithIncoming(t)
	_, err := h.ReleaseIncoming(context.Background(), &pb.ReleaseIncomingRequest{
		ReservationKey: "no-key", IdempotencyKey: "ilr-missing",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// ---------------------------------------------------------------------------
// ListChangelog — drive the real ChangelogService over a sqlite-friendly
// changelogs table.
// ---------------------------------------------------------------------------

func TestAccountHandler_ListChangelog_ValidatesEntityType(t *testing.T) {
	db := newHandlerTestDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS changelogs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		entity_type TEXT NOT NULL,
		entity_id INTEGER NOT NULL,
		action TEXT NOT NULL,
		field_name TEXT,
		old_value TEXT,
		new_value TEXT,
		changed_by INTEGER NOT NULL,
		changed_at DATETIME NOT NULL,
		reason TEXT
	)`).Error)
	repo := repository.NewChangelogRepository(db)
	svc := service.NewChangelogService(repo)
	h := &AccountGRPCHandler{changelogService: svc}

	_, err := h.ListChangelog(context.Background(), &pb.ListChangelogRequest{EntityType: "", EntityId: 1})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAccountHandler_ListChangelog_Empty(t *testing.T) {
	db := newHandlerTestDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS changelogs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		entity_type TEXT NOT NULL,
		entity_id INTEGER NOT NULL,
		action TEXT NOT NULL,
		field_name TEXT,
		old_value TEXT,
		new_value TEXT,
		changed_by INTEGER NOT NULL,
		changed_at DATETIME NOT NULL,
		reason TEXT
	)`).Error)
	repo := repository.NewChangelogRepository(db)
	svc := service.NewChangelogService(repo)
	h := &AccountGRPCHandler{changelogService: svc}

	resp, err := h.ListChangelog(context.Background(), &pb.ListChangelogRequest{
		EntityType: "account", EntityId: 1, Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), resp.Total)
	assert.Empty(t, resp.Entries)
}

// ---------------------------------------------------------------------------
// Constructor tests — exercise NewAccountGRPCHandler / NewBankAccountGRPCHandler
// purely for coverage of the wiring code.
// ---------------------------------------------------------------------------

func TestNewAccountGRPCHandler_Constructs(t *testing.T) {
	h := NewAccountGRPCHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	require.NotNil(t, h)
}

func TestNewBankAccountGRPCHandler_Constructs(t *testing.T) {
	h := NewBankAccountGRPCHandler(nil, nil)
	require.NotNil(t, h)
}
