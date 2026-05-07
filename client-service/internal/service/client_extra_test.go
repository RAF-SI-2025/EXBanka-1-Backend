package service

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/client-service/internal/cache"
	"github.com/exbanka/client-service/internal/model"
	"github.com/exbanka/contract/changelog"
	userpb "github.com/exbanka/contract/userpb"
)

// mockChangelogRepo records calls so tests can assert audit-trail writes.
type mockChangelogRepo struct {
	created []changelog.Entry
	batches [][]changelog.Entry
	err     error
}

func (m *mockChangelogRepo) Create(e changelog.Entry) error {
	m.created = append(m.created, e)
	return m.err
}

func (m *mockChangelogRepo) CreateBatch(entries []changelog.Entry) error {
	cp := make([]changelog.Entry, len(entries))
	copy(cp, entries)
	m.batches = append(m.batches, cp)
	return m.err
}

func TestUpdateClient_NoFieldsRecordsNoChangelog(t *testing.T) {
	repo := newMockClientRepo()
	cl := &mockChangelogRepo{}
	svc := NewClientService(repo, nil, nil, cl)

	c := validClient()
	require.NoError(t, svc.CreateClient(context.Background(), c))

	// No fields supplied -> no changelog entries should be created
	updated, err := svc.UpdateClient(c.ID, map[string]interface{}{}, 0)
	require.NoError(t, err)
	assert.Equal(t, c.FirstName, updated.FirstName)
	assert.Empty(t, cl.batches, "no changelog batch when nothing changed")
}

func TestUpdateClient_RecordsChangelogBatch(t *testing.T) {
	repo := newMockClientRepo()
	cl := &mockChangelogRepo{}
	svc := NewClientService(repo, nil, nil, cl)

	c := validClient()
	require.NoError(t, svc.CreateClient(context.Background(), c))

	_, err := svc.UpdateClient(c.ID, map[string]interface{}{
		"first_name":    "Jane",
		"last_name":     "Smith",
		"date_of_birth": int64(700000000),
		"gender":        "female",
		"phone":         "+38166",
		"address":       "Addr 2",
	}, 99)
	require.NoError(t, err)

	require.Len(t, cl.batches, 1)
	// Each modified field becomes a changelog entry
	require.GreaterOrEqual(t, len(cl.batches[0]), 5)
	for _, entry := range cl.batches[0] {
		assert.Equal(t, "client", entry.EntityType)
		assert.Equal(t, int64(c.ID), entry.EntityID)
		assert.Equal(t, int64(99), entry.ChangedBy)
	}
}

func TestUpdateClient_PasswordHashSilentlyDropped(t *testing.T) {
	repo := newMockClientRepo()
	cl := &mockChangelogRepo{}
	svc := NewClientService(repo, nil, nil, cl)
	c := validClient()
	require.NoError(t, svc.CreateClient(context.Background(), c))

	updated, err := svc.UpdateClient(c.ID, map[string]interface{}{
		"password_hash": "should-be-ignored",
		"first_name":    "Jane",
	}, 0)
	require.NoError(t, err)
	assert.Equal(t, "Jane", updated.FirstName)
	// password_hash is silently dropped before the field-walk loop, so the
	// only changelog entry should be for first_name. Verifies the
	// password_hash key never produced an audit row.
	require.Len(t, cl.batches, 1)
	for _, e := range cl.batches[0] {
		assert.NotEqual(t, "password_hash", e.FieldName, "password_hash must not appear in changelog")
	}
}

func TestSetClientLimits_RecordsChangelogWhenOldLimitExists(t *testing.T) {
	limitRepo := newMockClientLimitRepo()
	cl := &mockChangelogRepo{}
	empSvc := &mockEmployeeLimitSvc{maxClientDaily: "1000000", maxClientMonthly: "10000000"}
	svc := NewClientLimitService(limitRepo, empSvc, nil, cl)

	limit := model.ClientLimit{
		ClientID:      1,
		DailyLimit:    decimal.NewFromInt(50000),
		MonthlyLimit:  decimal.NewFromInt(500000),
		TransferLimit: decimal.NewFromInt(20000),
		SetByEmployee: 7,
	}
	_, err := svc.SetClientLimits(context.Background(), limit, 7)
	require.NoError(t, err)
	require.Len(t, cl.batches, 1, "changelog batch must be written for set-limits")
	for _, entry := range cl.batches[0] {
		assert.Equal(t, "client_limit", entry.EntityType)
		assert.Equal(t, int64(1), entry.EntityID)
	}
}

func TestSetClientLimits_InvalidMaxDailyDecimal(t *testing.T) {
	limitRepo := newMockClientLimitRepo()
	empSvc := &mockEmployeeLimitSvc{maxClientDaily: "not-a-number", maxClientMonthly: "1000000"}
	svc := NewClientLimitService(limitRepo, empSvc, nil)
	limit := model.ClientLimit{
		ClientID:      1,
		DailyLimit:    decimal.NewFromInt(100),
		MonthlyLimit:  decimal.NewFromInt(1000),
		SetByEmployee: 1,
	}
	_, err := svc.SetClientLimits(context.Background(), limit, 0)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidEmployeeLimits))
}

func TestSetClientLimits_InvalidMaxMonthlyDecimal(t *testing.T) {
	limitRepo := newMockClientLimitRepo()
	empSvc := &mockEmployeeLimitSvc{maxClientDaily: "1000000", maxClientMonthly: "not-a-number"}
	svc := NewClientLimitService(limitRepo, empSvc, nil)
	limit := model.ClientLimit{
		ClientID:      1,
		DailyLimit:    decimal.NewFromInt(100),
		MonthlyLimit:  decimal.NewFromInt(1000),
		SetByEmployee: 1,
	}
	_, err := svc.SetClientLimits(context.Background(), limit, 0)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidEmployeeLimits))
}

func TestGetClient_RepoError(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)
	_, err := svc.GetClient(99999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// errorRepo always returns errors — covers persistence-failure branch.
type errorClientRepo struct{}

func (errorClientRepo) Create(_ *model.Client) error            { return errors.New("disk full") }
func (errorClientRepo) GetByID(_ uint64) (*model.Client, error) { return nil, errors.New("io") }
func (errorClientRepo) GetByEmail(_ string) (*model.Client, error) {
	return nil, errors.New("io")
}
func (errorClientRepo) Update(_ *model.Client) error { return errors.New("io") }
func (errorClientRepo) List(_, _ string, _, _ int) ([]model.Client, int64, error) {
	return nil, 0, errors.New("io")
}

func TestCreateClient_RepoFailure(t *testing.T) {
	svc := NewClientService(errorClientRepo{}, nil, nil)
	c := validClient()
	err := svc.CreateClient(context.Background(), c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create client")
}

func TestUpdateClient_RepoUpdateFailure(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)
	c := validClient()
	require.NoError(t, svc.CreateClient(context.Background(), c))

	// Replace repo Update path: re-wire with one that errors.
	failingSvc := NewClientService(failingUpdateRepo{repo: repo}, nil, nil)
	_, err := failingSvc.UpdateClient(c.ID, map[string]interface{}{"first_name": "X"}, 0)
	require.Error(t, err)
}

// failingUpdateRepo wraps a working repo but errors on Update.
type failingUpdateRepo struct{ repo *mockClientRepo }

func (f failingUpdateRepo) Create(c *model.Client) error             { return f.repo.Create(c) }
func (f failingUpdateRepo) GetByID(id uint64) (*model.Client, error) { return f.repo.GetByID(id) }
func (f failingUpdateRepo) GetByEmail(e string) (*model.Client, error) {
	return f.repo.GetByEmail(e)
}
func (f failingUpdateRepo) Update(_ *model.Client) error { return errors.New("update failed") }
func (f failingUpdateRepo) List(e, n string, p, ps int) ([]model.Client, int64, error) {
	return f.repo.List(e, n, p, ps)
}

func TestSetClientLimits_RepoUpsertFailure(t *testing.T) {
	failingRepo := failingUpsertLimitRepo{
		mockClientLimitRepo: *newMockClientLimitRepo(),
	}
	svc := NewClientLimitService(&failingRepo, nil, nil)
	_, err := svc.SetClientLimits(context.Background(), model.ClientLimit{
		ClientID: 1, DailyLimit: decimal.NewFromInt(1), MonthlyLimit: decimal.NewFromInt(2),
	}, 0)
	require.Error(t, err)
}

type failingUpsertLimitRepo struct{ mockClientLimitRepo }

func (f *failingUpsertLimitRepo) Upsert(_ *model.ClientLimit) error {
	return errors.New("disk full")
}

// Ensures GetEmployeeLimits returns valid empty fields and lookup error
// short-circuits before any decimal parsing is attempted.
func TestSetClientLimits_EmptyEmployeeLimits(t *testing.T) {
	limitRepo := newMockClientLimitRepo()
	empSvc := &mockEmployeeLimitSvc{maxClientDaily: "", maxClientMonthly: ""}
	svc := NewClientLimitService(limitRepo, empSvc, nil)
	limit := model.ClientLimit{
		ClientID:      1,
		DailyLimit:    decimal.NewFromInt(1),
		MonthlyLimit:  decimal.NewFromInt(1),
		TransferLimit: decimal.NewFromInt(1),
		SetByEmployee: 1,
	}
	_, err := svc.SetClientLimits(context.Background(), limit, 0)
	// Empty string is parsed as decimal.Zero by the inner NewFromString;
	// limits 1 > 0 -> exceeds-employee error.
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrLimitsExceedEmployee) || errors.Is(err, ErrInvalidEmployeeLimits))
}

// Ensures NewClientLimitService variadic optional args don't panic when
// no changelog repo is supplied.
func TestNewClientLimitService_NoOptionalChangelogRepo(t *testing.T) {
	svc := NewClientLimitService(newMockClientLimitRepo(), nil, nil)
	require.NotNil(t, svc)
}

// employeeLimitSvc compile-time check: mockEmployeeLimitSvc must satisfy interface.
var _ userpb.EmployeeLimitServiceClient = (*mockEmployeeLimitSvc)(nil)

func TestGetClient_WithCache(t *testing.T) {
	mr := miniredis.RunT(t)
	cc, err := cache.NewRedisCache(mr.Addr())
	require.NoError(t, err)
	defer cc.Close()

	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, cc)

	c := validClient()
	require.NoError(t, svc.CreateClient(context.Background(), c))

	// First Get -> miss in cache, hit repo, populate cache
	got1, err := svc.GetClient(c.ID)
	require.NoError(t, err)
	assert.Equal(t, "John", got1.FirstName)

	// Second Get -> hit in cache (no repo call needed)
	got2, err := svc.GetClient(c.ID)
	require.NoError(t, err)
	assert.Equal(t, "John", got2.FirstName)
}

func TestUpdateClient_WithCache_InvalidatesEntry(t *testing.T) {
	mr := miniredis.RunT(t)
	cc, err := cache.NewRedisCache(mr.Addr())
	require.NoError(t, err)
	defer cc.Close()

	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, cc)

	c := validClient()
	require.NoError(t, svc.CreateClient(context.Background(), c))
	// Populate cache
	_, err = svc.GetClient(c.ID)
	require.NoError(t, err)

	// Update should delete the cache entry.
	_, err = svc.UpdateClient(c.ID, map[string]interface{}{"first_name": "Jane"}, 0)
	require.NoError(t, err)

	got, err := svc.GetClient(c.ID)
	require.NoError(t, err)
	assert.Equal(t, "Jane", got.FirstName, "cache should reflect new value after update")
}
