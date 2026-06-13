package service

import (
	"context"
	"errors"
	"testing"

	"github.com/exbanka/client-service/internal/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Mock employeeLimitReader for replica tests
// ---------------------------------------------------------------------------

type mockEmployeeLimitReplica struct {
	data      map[uint64]model.EmployeeLimitReplica
	getErr    error
	upserted  []model.EmployeeLimitReplica
	upsertErr error
}

func newMockEmployeeLimitReplica() *mockEmployeeLimitReplica {
	return &mockEmployeeLimitReplica{data: make(map[uint64]model.EmployeeLimitReplica)}
}

func (m *mockEmployeeLimitReplica) GetByEmployeeID(_ context.Context, id uint64) (model.EmployeeLimitReplica, error) {
	if m.getErr != nil {
		return model.EmployeeLimitReplica{}, m.getErr
	}
	rep, ok := m.data[id]
	if !ok {
		return model.EmployeeLimitReplica{}, errors.New("employee limit replica not found")
	}
	return rep, nil
}

func (m *mockEmployeeLimitReplica) Upsert(_ context.Context, in model.EmployeeLimitReplica) error {
	if m.upsertErr != nil {
		return m.upsertErr
	}
	m.upserted = append(m.upserted, in)
	m.data[in.EmployeeID] = in
	return nil
}

// ---------------------------------------------------------------------------
// Replica-hit tests
// ---------------------------------------------------------------------------

// TestSetClientLimits_ReplicaHit_EnforcesCapWithoutGRPC verifies that when the
// employee-limit replica has a row the cap check is satisfied locally and the
// gRPC EmployeeLimitServiceClient is NOT called.
func TestSetClientLimits_ReplicaHit_EnforcesCapWithoutGRPC(t *testing.T) {
	limitRepo := newMockClientLimitRepo()
	replica := newMockEmployeeLimitReplica()
	replica.data[10] = model.EmployeeLimitReplica{
		EmployeeID:            10,
		MaxClientDailyLimit:   decimal.NewFromInt(100000),
		MaxClientMonthlyLimit: decimal.NewFromInt(1000000),
		Version:               1,
	}

	// Use nil gRPC svc: if replica is hit the gRPC path is never attempted.
	svc := NewClientLimitService(limitRepo, nil, nil, replica)

	// Under-cap limit — must succeed.
	limit := model.ClientLimit{
		ClientID:      1,
		DailyLimit:    decimal.NewFromInt(50000),
		MonthlyLimit:  decimal.NewFromInt(500000),
		TransferLimit: decimal.NewFromInt(10000),
		SetByEmployee: 10,
	}
	result, err := svc.SetClientLimits(context.Background(), limit, 10)
	require.NoError(t, err)
	assert.True(t, result.DailyLimit.Equal(decimal.NewFromInt(50000)))
}

// TestSetClientLimits_ReplicaHit_RejectsOverCapLimit verifies that when the
// replica has lower caps the over-cap client limit is rejected with ErrLimitsExceedEmployee.
func TestSetClientLimits_ReplicaHit_RejectsOverCapLimit(t *testing.T) {
	limitRepo := newMockClientLimitRepo()
	replica := newMockEmployeeLimitReplica()
	replica.data[10] = model.EmployeeLimitReplica{
		EmployeeID:            10,
		MaxClientDailyLimit:   decimal.NewFromInt(100000),
		MaxClientMonthlyLimit: decimal.NewFromInt(1000000),
		Version:               1,
	}

	svc := NewClientLimitService(limitRepo, nil, nil, replica)

	// Over-cap daily limit.
	limit := model.ClientLimit{
		ClientID:      1,
		DailyLimit:    decimal.NewFromInt(200000), // exceeds 100000
		MonthlyLimit:  decimal.NewFromInt(500000),
		SetByEmployee: 10,
	}
	_, err := svc.SetClientLimits(context.Background(), limit, 10)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLimitsExceedEmployee)
	assert.Contains(t, err.Error(), "daily limit")
	assert.Contains(t, err.Error(), "exceeds")
}

// TestSetClientLimits_ReplicaHit_RejectsOverCapMonthly verifies monthly cap
// enforcement via the replica path.
func TestSetClientLimits_ReplicaHit_RejectsOverCapMonthly(t *testing.T) {
	limitRepo := newMockClientLimitRepo()
	replica := newMockEmployeeLimitReplica()
	replica.data[10] = model.EmployeeLimitReplica{
		EmployeeID:            10,
		MaxClientDailyLimit:   decimal.NewFromInt(500000),
		MaxClientMonthlyLimit: decimal.NewFromInt(1000000),
		Version:               1,
	}

	svc := NewClientLimitService(limitRepo, nil, nil, replica)

	limit := model.ClientLimit{
		ClientID:      1,
		DailyLimit:    decimal.NewFromInt(200000),
		MonthlyLimit:  decimal.NewFromInt(2000000), // exceeds 1000000
		SetByEmployee: 10,
	}
	_, err := svc.SetClientLimits(context.Background(), limit, 10)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLimitsExceedEmployee)
	assert.Contains(t, err.Error(), "monthly limit")
}

// ---------------------------------------------------------------------------
// Replica-miss → gRPC fallback + backfill tests
// ---------------------------------------------------------------------------

// TestSetClientLimits_ReplicaMiss_FallsBackToGRPC verifies that on a replica
// miss the service calls GetEmployeeLimits exactly once and succeeds.
func TestSetClientLimits_ReplicaMiss_FallsBackToGRPC(t *testing.T) {
	limitRepo := newMockClientLimitRepo()
	// Replica is empty — every GetByEmployeeID returns not-found.
	replica := newMockEmployeeLimitReplica()

	empSvc := &mockEmployeeLimitSvc{
		maxClientDaily:   "500000",
		maxClientMonthly: "5000000",
	}
	svc := NewClientLimitService(limitRepo, empSvc, nil, replica)

	limit := model.ClientLimit{
		ClientID:      1,
		DailyLimit:    decimal.NewFromInt(200000),
		MonthlyLimit:  decimal.NewFromInt(2000000),
		TransferLimit: decimal.NewFromInt(50000),
		SetByEmployee: 7,
	}
	result, err := svc.SetClientLimits(context.Background(), limit, 7)
	require.NoError(t, err)
	assert.True(t, result.DailyLimit.Equal(decimal.NewFromInt(200000)))
}

// TestSetClientLimits_ReplicaMiss_BackfillsReplica verifies that after a gRPC
// fallback the replica is populated so the next call is a hit.
func TestSetClientLimits_ReplicaMiss_BackfillsReplica(t *testing.T) {
	limitRepo := newMockClientLimitRepo()
	replica := newMockEmployeeLimitReplica()

	empSvc := &mockEmployeeLimitSvc{
		maxClientDaily:   "300000",
		maxClientMonthly: "3000000",
	}
	svc := NewClientLimitService(limitRepo, empSvc, nil, replica)

	limit := model.ClientLimit{
		ClientID:      2,
		DailyLimit:    decimal.NewFromInt(100000),
		MonthlyLimit:  decimal.NewFromInt(1000000),
		TransferLimit: decimal.NewFromInt(20000),
		SetByEmployee: 5,
	}
	_, err := svc.SetClientLimits(context.Background(), limit, 5)
	require.NoError(t, err)

	// Replica must have been backfilled for employee 5.
	require.Len(t, replica.upserted, 1, "replica must be backfilled after gRPC fallback")
	assert.Equal(t, uint64(5), replica.upserted[0].EmployeeID)
	assert.Equal(t, int64(0), replica.upserted[0].Version, "backfill Version must be 0")
	assert.True(t, replica.upserted[0].MaxClientDailyLimit.Equal(decimal.NewFromInt(300000)))
	assert.True(t, replica.upserted[0].MaxClientMonthlyLimit.Equal(decimal.NewFromInt(3000000)))
}
