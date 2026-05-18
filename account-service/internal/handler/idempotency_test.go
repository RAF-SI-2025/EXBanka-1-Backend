// Idempotency-contract tests for AccountGRPCHandler. Verifies the wiring
// added in Plan 2026-04-27 Task 8 — UpdateBalance is the lighthouse case
// for the IdempotencyRepository.Run pattern that subsequent tasks roll out
// to the rest of the saga-callee handlers.
package handler

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	pb "github.com/exbanka/contract/accountpb"
)

// TestUpdateBalance_Idempotent_ReturnsCachedResponse asserts that a second
// call with the same idempotency_key returns the cached response WITHOUT
// invoking the underlying service layer a second time. This is the core
// at-most-once guarantee saga compensators rely on.
func TestUpdateBalance_Idempotent_ReturnsCachedResponse(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)

	calls := 0
	f.accountSvc.updateBalanceWithOptsFn = func(accountNumber string, amount decimal.Decimal, updateAvailable bool, opts repository.UpdateBalanceOpts) error {
		calls++
		return nil
	}
	f.accountSvc.getAccountByNumberFn = func(accountNumber string) (*model.Account, error) {
		acct := sampleAccount(1)
		acct.Balance = decimal.NewFromInt(1234)
		return acct, nil
	}

	req := &pb.UpdateBalanceRequest{
		AccountNumber:   "111000100000099011",
		Amount:          "100",
		UpdateAvailable: true,
		IdempotencyKey:  "saga-step-001",
	}

	resp1, err := h.UpdateBalance(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp1)

	resp2, err := h.UpdateBalance(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp2)

	// Same response data on cache hit.
	assert.Equal(t, resp1.Balance, resp2.Balance,
		"cache hit must return identical response payload")
	assert.Equal(t, resp1.AccountNumber, resp2.AccountNumber)

	// Service layer is invoked exactly once across both RPC calls.
	assert.Equal(t, 1, calls,
		"underlying UpdateBalanceWithOpts must be called exactly once across two idempotent RPCs, got %d", calls)

	// Cache record exists in the IdempotencyRecord table.
	var rec model.IdempotencyRecord
	require.NoError(t, f.db.First(&rec, "key = ?", "saga-step-001").Error)
	assert.NotEmpty(t, rec.ResponseBlob, "cached response blob must be persisted")
}

// TestUpdateBalance_MissingIdempotencyKey_Rejected asserts that requests
// without an idempotency_key fail fast with InvalidArgument. Saga callers
// MUST always supply a key — silently allowing requests through would let
// retries duplicate side effects.
func TestUpdateBalance_MissingIdempotencyKey_Rejected(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)

	calls := 0
	f.accountSvc.updateBalanceWithOptsFn = func(accountNumber string, amount decimal.Decimal, updateAvailable bool, opts repository.UpdateBalanceOpts) error {
		calls++
		return nil
	}

	_, err := h.UpdateBalance(context.Background(), &pb.UpdateBalanceRequest{
		AccountNumber:   "111000100000099011",
		Amount:          "100",
		UpdateAvailable: true,
		// IdempotencyKey deliberately omitted.
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err),
		"missing idempotency_key must produce InvalidArgument; got %v", err)

	assert.Equal(t, 0, calls,
		"service layer must NOT be invoked when idempotency_key is missing, got %d calls", calls)
}

// TestUpdateBalance_DifferentKeys_BothExecute asserts that the cache is
// keyed per idempotency_key — two requests with different keys must each
// run the underlying service. This guards against an accidental
// false-positive where every call returns the first cached response.
func TestUpdateBalance_DifferentKeys_BothExecute(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)

	calls := 0
	f.accountSvc.updateBalanceWithOptsFn = func(accountNumber string, amount decimal.Decimal, updateAvailable bool, opts repository.UpdateBalanceOpts) error {
		calls++
		return nil
	}
	f.accountSvc.getAccountByNumberFn = func(accountNumber string) (*model.Account, error) {
		return sampleAccount(1), nil
	}

	for i, key := range []string{"k-a", "k-b"} {
		_, err := h.UpdateBalance(context.Background(), &pb.UpdateBalanceRequest{
			AccountNumber:   "111000100000099011",
			Amount:          "100",
			UpdateAvailable: true,
			IdempotencyKey:  key,
		})
		require.NoError(t, err, "call %d with key %q failed", i, key)
	}
	assert.Equal(t, 2, calls,
		"distinct idempotency keys must each execute the service layer, got %d calls", calls)
}
