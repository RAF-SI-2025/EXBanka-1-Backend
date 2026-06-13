package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kafkaprod "github.com/exbanka/client-service/internal/kafka"
	"github.com/exbanka/client-service/internal/model"
)

// stubEmailLookup is a test double for ClientEmailLookup (SP5 D1).
type stubEmailLookup struct {
	email  string
	err    error
	called bool
}

func (s *stubEmailLookup) GetEmailByID(_ int64) (string, error) {
	s.called = true
	return s.email, s.err
}

func TestParseDecimalOrZeroSvc(t *testing.T) {
	assert.True(t, parseDecimalOrZeroSvc("").IsZero(), "empty → zero")
	assert.True(t, parseDecimalOrZeroSvc("not-a-decimal").IsZero(), "malformed → zero")
	assert.True(t, parseDecimalOrZeroSvc("123.45").Equal(decimal.RequireFromString("123.45")), "valid → parsed")
}

func TestWithEmailLookup_WiresLookupAndReturnsSelf(t *testing.T) {
	svc := NewClientLimitService(newMockClientLimitRepo(), nil, nil, nil)
	stub := &stubEmailLookup{email: "x@y.z"}
	got := svc.WithEmailLookup(stub)
	require.Same(t, svc, got, "WithEmailLookup must return the same service for chaining")
	require.NotNil(t, svc.emailLookup)
}

// TestSetClientLimits_PublishesNotificationsAndEmail exercises the full
// notification fan-out at the tail of SetClientLimits: the changelog batch,
// PublishClientLimitsUpdated, PublishGeneralNotification, and the best-effort
// email send via the email lookup. A real Producer pointed at a dead broker
// with a cancelled context makes every publish call execute and fail fast; the
// failures are swallowed (logged) and SetClientLimits still returns the result.
func TestSetClientLimits_PublishesNotificationsAndEmail(t *testing.T) {
	prod := kafkaprod.NewProducer("localhost:9999")
	defer prod.Close()

	limitRepo := newMockClientLimitRepo()
	cl := &mockChangelogRepo{}
	stub := &stubEmailLookup{email: "client@example.com"}

	// nil userLimitSvc + nil replica → cap check skipped, so any limit passes.
	svc := NewClientLimitService(limitRepo, nil, prod, nil, cl).WithEmailLookup(stub)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	limit := model.ClientLimit{
		ClientID:      1,
		DailyLimit:    decimal.NewFromInt(50000),
		MonthlyLimit:  decimal.NewFromInt(500000),
		TransferLimit: decimal.NewFromInt(20000),
		SetByEmployee: 7,
	}
	result, err := svc.SetClientLimits(ctx, limit, 7)
	require.NoError(t, err, "publish failures must be swallowed, not surfaced")
	require.NotNil(t, result)
	assert.True(t, result.DailyLimit.Equal(decimal.NewFromInt(50000)))

	// Changelog batch was written (oldLimit non-nil via mock defaults).
	require.Len(t, cl.batches, 1)
	for _, e := range cl.batches[0] {
		assert.Equal(t, "client_limit", e.EntityType)
	}
	// Email lookup was consulted for the best-effort LIMIT_CHANGED email.
	assert.True(t, stub.called, "email lookup must be consulted when wired")
}

// TestSetClientLimits_EmailLookupErrorSkipsSend covers the false branch of the
// `email != "" && eErr == nil` guard: when the lookup errors, no email is sent
// but the call still succeeds.
func TestSetClientLimits_EmailLookupErrorSkipsSend(t *testing.T) {
	prod := kafkaprod.NewProducer("localhost:9999")
	defer prod.Close()

	limitRepo := newMockClientLimitRepo()
	stub := &stubEmailLookup{err: errors.New("client not found")}
	svc := NewClientLimitService(limitRepo, nil, prod, nil).WithEmailLookup(stub)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	limit := model.ClientLimit{
		ClientID:      2,
		DailyLimit:    decimal.NewFromInt(1000),
		MonthlyLimit:  decimal.NewFromInt(10000),
		TransferLimit: decimal.NewFromInt(500),
		SetByEmployee: 3,
	}
	result, err := svc.SetClientLimits(ctx, limit, 3)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, stub.called)
}
