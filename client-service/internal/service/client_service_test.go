package service

import (
	"context"
	"errors"
	"testing"

	"github.com/exbanka/client-service/internal/model"
	"github.com/exbanka/client-service/internal/repository"
	kafkamsg "github.com/exbanka/contract/kafka"
	userpb "github.com/exbanka/contract/userpb"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Mock clientEventProducer
// ---------------------------------------------------------------------------

type mockClientProducer struct {
	lastCreated *kafkamsg.ClientCreatedMessage
	lastUpdated *kafkamsg.ClientCreatedMessage
}

func (m *mockClientProducer) PublishClientCreated(_ context.Context, msg kafkamsg.ClientCreatedMessage) error {
	cp := msg
	m.lastCreated = &cp
	return nil
}

func (m *mockClientProducer) PublishClientUpdated(_ context.Context, msg kafkamsg.ClientCreatedMessage) error {
	cp := msg
	m.lastUpdated = &cp
	return nil
}

// ---------------------------------------------------------------------------
// Validation tests (unchanged from original)
// ---------------------------------------------------------------------------

func TestValidateJMBG(t *testing.T) {
	tests := []struct {
		name    string
		jmbg    string
		wantErr bool
		errMsg  string // expected substring in error message (empty = no check)
	}{
		{"valid 13 digits", "0101990710024", false, ""},
		{"empty", "", true, "13 digits"},
		{"too short", "12345", true, "13 digits"},
		{"too long", "12345678901234", true, "13 digits"},
		{"contains letters", "012345678901a", true, "only digits"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateJMBG(tt.jmbg)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg, "error message should be specific")
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEmail(t *testing.T) {
	tests := []struct {
		name    string
		email   string
		wantErr bool
		errMsg  string // expected substring in error message (empty = no check)
	}{
		{"valid email", "test@example.com", false, ""},
		{"empty", "", true, "must not be empty"},
		{"no at sign", "testexample.com", true, "@"},
		{"no domain", "test@", true, "domain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEmail(tt.email)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg, "error message should be specific")
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Mock ClientRepo
// ---------------------------------------------------------------------------

type mockClientRepo struct {
	clients  map[uint64]*model.Client
	nextID   uint64
	createFn func(c *model.Client) error // optional override
}

func newMockClientRepo() *mockClientRepo {
	return &mockClientRepo{clients: make(map[uint64]*model.Client), nextID: 1}
}

func (m *mockClientRepo) Create(client *model.Client) error {
	if m.createFn != nil {
		return m.createFn(client)
	}
	// Simulate the real repo under gorm's TranslateError: a unique-constraint
	// violation surfaces as the portable gorm.ErrDuplicatedKey sentinel (NOT a
	// driver string that echoes the colliding email/JMBG).
	for _, c := range m.clients {
		if c.Email == client.Email || c.JMBG == client.JMBG {
			return gorm.ErrDuplicatedKey
		}
	}
	client.ID = m.nextID
	m.nextID++
	stored := *client
	m.clients[client.ID] = &stored
	return nil
}

func (m *mockClientRepo) GetByID(id uint64) (*model.Client, error) {
	c, ok := m.clients[id]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := *c
	return &cp, nil
}

func (m *mockClientRepo) GetByEmail(email string) (*model.Client, error) {
	for _, c := range m.clients {
		if c.Email == email {
			cp := *c
			return &cp, nil
		}
	}
	return nil, errors.New("not found")
}

func (m *mockClientRepo) Update(client *model.Client) error {
	// Email collision with another client → gorm.ErrDuplicatedKey (see Create).
	for _, c := range m.clients {
		if c.Email == client.Email && c.ID != client.ID {
			return gorm.ErrDuplicatedKey
		}
	}
	stored := *client
	m.clients[client.ID] = &stored
	return nil
}

func (m *mockClientRepo) List(emailFilter, nameFilter string, page, pageSize int) ([]model.Client, int64, error) {
	var result []model.Client
	for _, c := range m.clients {
		match := true
		if emailFilter != "" {
			if !containsCaseInsensitive(c.Email, emailFilter) {
				match = false
			}
		}
		if nameFilter != "" {
			if !containsCaseInsensitive(c.FirstName, nameFilter) && !containsCaseInsensitive(c.LastName, nameFilter) {
				match = false
			}
		}
		if match {
			result = append(result, *c)
		}
	}
	total := int64(len(result))
	offset := (page - 1) * pageSize
	if offset >= len(result) {
		return nil, total, nil
	}
	end := offset + pageSize
	if end > len(result) {
		end = len(result)
	}
	return result[offset:end], total, nil
}

func containsCaseInsensitive(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		// Simple contains check (case insensitive approximation for test purposes)
		func() bool {
			for i := 0; i+len(substr) <= len(s); i++ {
				if equalFold(s[i:i+len(substr)], substr) {
					return true
				}
			}
			return false
		}())
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Mock ClientLimitRepo
// ---------------------------------------------------------------------------

type mockClientLimitRepo struct {
	limits map[int64]*model.ClientLimit
}

func newMockClientLimitRepo() *mockClientLimitRepo {
	return &mockClientLimitRepo{limits: make(map[int64]*model.ClientLimit)}
}

func (m *mockClientLimitRepo) GetByClientID(clientID int64) (*model.ClientLimit, error) {
	l, ok := m.limits[clientID]
	if !ok {
		// Return defaults, matching repository behaviour
		return &model.ClientLimit{
			ClientID:      clientID,
			DailyLimit:    decimal.NewFromInt(100000),
			MonthlyLimit:  decimal.NewFromInt(1000000),
			TransferLimit: decimal.NewFromInt(50000),
		}, nil
	}
	cp := *l
	return &cp, nil
}

func (m *mockClientLimitRepo) Upsert(limit *model.ClientLimit) error {
	stored := *limit
	if _, exists := m.limits[limit.ClientID]; !exists {
		// Simulate DB column default:1 on first insert.
		stored.Version = 1
	} else {
		// Simulate ON CONFLICT DO UPDATE SET version = version + 1.
		stored.Version = m.limits[limit.ClientID].Version + 1
	}
	m.limits[limit.ClientID] = &stored
	return nil
}

// ---------------------------------------------------------------------------
// Mock EmployeeLimitServiceClient (gRPC)
// ---------------------------------------------------------------------------

type mockEmployeeLimitSvc struct {
	maxClientDaily   string
	maxClientMonthly string
	err              error
}

func (m *mockEmployeeLimitSvc) GetEmployeeLimits(_ context.Context, _ *userpb.EmployeeLimitRequest, _ ...grpc.CallOption) (*userpb.EmployeeLimitResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &userpb.EmployeeLimitResponse{
		MaxClientDailyLimit:   m.maxClientDaily,
		MaxClientMonthlyLimit: m.maxClientMonthly,
	}, nil
}

func (m *mockEmployeeLimitSvc) SetEmployeeLimits(_ context.Context, _ *userpb.SetEmployeeLimitsRequest, _ ...grpc.CallOption) (*userpb.EmployeeLimitResponse, error) {
	return nil, nil
}

func (m *mockEmployeeLimitSvc) ApplyLimitTemplate(_ context.Context, _ *userpb.ApplyLimitTemplateRequest, _ ...grpc.CallOption) (*userpb.EmployeeLimitResponse, error) {
	return nil, nil
}

func (m *mockEmployeeLimitSvc) ListLimitTemplates(_ context.Context, _ *userpb.ListLimitTemplatesRequest, _ ...grpc.CallOption) (*userpb.ListLimitTemplatesResponse, error) {
	return nil, nil
}

func (m *mockEmployeeLimitSvc) CreateLimitTemplate(_ context.Context, _ *userpb.CreateLimitTemplateRequest, _ ...grpc.CallOption) (*userpb.LimitTemplateResponse, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Helper: create a valid Client template
// ---------------------------------------------------------------------------

func validClient() *model.Client {
	return &model.Client{
		FirstName:   "John",
		LastName:    "Doe",
		DateOfBirth: 631152000, // 1990-01-01
		Gender:      "male",
		Email:       "john@example.com",
		Phone:       "+381641234567",
		Address:     "Main St 1, Belgrade",
		JMBG:        "0101990710024",
	}
}

// ---------------------------------------------------------------------------
// Client CRUD tests
// ---------------------------------------------------------------------------

func TestCreateClient_Valid(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)

	client := validClient()
	err := svc.CreateClient(context.Background(), client)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), client.ID, "auto-assigned ID")

	// Verify all fields persisted in repo
	persisted, err := repo.GetByID(1)
	require.NoError(t, err)
	assert.Equal(t, "John", persisted.FirstName)
	assert.Equal(t, "Doe", persisted.LastName)
	assert.Equal(t, int64(631152000), persisted.DateOfBirth)
	assert.Equal(t, "male", persisted.Gender)
	assert.Equal(t, "john@example.com", persisted.Email)
	assert.Equal(t, "+381641234567", persisted.Phone)
	assert.Equal(t, "Main St 1, Belgrade", persisted.Address)
	assert.Equal(t, "0101990710024", persisted.JMBG)
}

func TestCreateClient_DuplicateEmail(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)

	c1 := validClient()
	require.NoError(t, svc.CreateClient(context.Background(), c1))

	c2 := validClient()
	c2.JMBG = "0201990710025" // different JMBG, same email
	err := svc.CreateClient(context.Background(), c2)
	require.Error(t, err)
	// Maps to the 409 sentinel — and the clean message must NOT echo the email (PII).
	assert.ErrorIs(t, err, ErrClientAlreadyExists)
	assert.NotContains(t, err.Error(), "john@example.com")
}

func TestCreateClient_DuplicateJMBG(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)

	c1 := validClient()
	require.NoError(t, svc.CreateClient(context.Background(), c1))

	c2 := validClient()
	c2.Email = "other@example.com" // different email, same JMBG
	err := svc.CreateClient(context.Background(), c2)
	require.Error(t, err)
	// Maps to the 409 sentinel — and the clean message must NOT echo the JMBG (PII).
	assert.ErrorIs(t, err, ErrClientAlreadyExists)
	assert.NotContains(t, err.Error(), "0101990710024")
}

func TestCreateClient_InvalidJMBG(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)

	c := validClient()
	c.JMBG = "123" // too short
	err := svc.CreateClient(context.Background(), c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "13 digits")
}

func TestCreateClient_InvalidEmail(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)

	c := validClient()
	c.Email = "not-an-email"
	err := svc.CreateClient(context.Background(), c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "@")
}

func TestGetClient_Found(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)

	c := validClient()
	require.NoError(t, svc.CreateClient(context.Background(), c))

	got, err := svc.GetClient(c.ID)
	require.NoError(t, err)
	assert.Equal(t, c.ID, got.ID)
	assert.Equal(t, "John", got.FirstName)
	assert.Equal(t, "john@example.com", got.Email)
}

func TestGetClient_NotFound(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)

	_, err := svc.GetClient(999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetClientByEmail_Found(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)

	c := validClient()
	require.NoError(t, svc.CreateClient(context.Background(), c))

	got, err := svc.GetByEmail("john@example.com")
	require.NoError(t, err)
	assert.Equal(t, c.ID, got.ID)
	assert.Equal(t, "John", got.FirstName)
}

func TestGetClientByEmail_NotFound(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)

	_, err := svc.GetByEmail("nobody@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestListClients_Pagination(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)

	// Create 5 clients
	for i := 0; i < 5; i++ {
		c := validClient()
		c.Email = "user" + string(rune('a'+i)) + "@example.com"
		c.JMBG = "010199071002" + string(rune('0'+i))
		c.FirstName = "User" + string(rune('A'+i))
		require.NoError(t, svc.CreateClient(context.Background(), c))
	}

	// Page 1 of size 3
	clients, total, err := svc.ListClients("", "", 1, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total, "total count should be 5")
	assert.Len(t, clients, 3, "page 1 should return 3 clients")

	// Page 2 of size 3
	clients2, total2, err := svc.ListClients("", "", 2, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total2, "total count should still be 5")
	assert.Len(t, clients2, 2, "page 2 should return 2 remaining clients")
}

func TestListClients_DefaultsForInvalidPagination(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)

	c := validClient()
	require.NoError(t, svc.CreateClient(context.Background(), c))

	// page=0 and pageSize=0 should be normalised to defaults (1, 20)
	clients, total, err := svc.ListClients("", "", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, clients, 1)
}

func TestUpdateClient_FieldsChanged(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)

	c := validClient()
	require.NoError(t, svc.CreateClient(context.Background(), c))

	updated, err := svc.UpdateClient(c.ID, map[string]interface{}{
		"first_name": "Jane",
		"last_name":  "Smith",
		"email":      "jane@example.com",
		"phone":      "+381649999999",
		"address":    "New St 2, Novi Sad",
		"gender":     "female",
	}, 0)
	require.NoError(t, err)
	assert.Equal(t, "Jane", updated.FirstName)
	assert.Equal(t, "Smith", updated.LastName)
	assert.Equal(t, "jane@example.com", updated.Email)
	assert.Equal(t, "+381649999999", updated.Phone)
	assert.Equal(t, "New St 2, Novi Sad", updated.Address)
	assert.Equal(t, "female", updated.Gender)

	// JMBG should be unchanged (immutable)
	assert.Equal(t, "0101990710024", updated.JMBG)
}

func TestUpdateClient_EmailUniquenessEnforced(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)

	c1 := validClient()
	require.NoError(t, svc.CreateClient(context.Background(), c1))

	c2 := validClient()
	c2.Email = "other@example.com"
	c2.JMBG = "0201990710025"
	require.NoError(t, svc.CreateClient(context.Background(), c2))

	// Try to update c2's email to c1's email
	_, err := svc.UpdateClient(c2.ID, map[string]interface{}{
		"email": "john@example.com",
	}, 0)
	require.Error(t, err)
	// Maps to the 409 sentinel — clean message, no email echoed back.
	assert.ErrorIs(t, err, ErrClientAlreadyExists)
	assert.NotContains(t, err.Error(), "john@example.com")
}

func TestUpdateClient_JMBGImmutable(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)

	c := validClient()
	require.NoError(t, svc.CreateClient(context.Background(), c))

	// Attempt to change JMBG via updates map — should be silently ignored
	updated, err := svc.UpdateClient(c.ID, map[string]interface{}{
		"jmbg": "9999999999999",
	}, 0)
	require.NoError(t, err)
	assert.Equal(t, "0101990710024", updated.JMBG, "JMBG must not change")
}

func TestUpdateClient_InvalidEmail(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)

	c := validClient()
	require.NoError(t, svc.CreateClient(context.Background(), c))

	_, err := svc.UpdateClient(c.ID, map[string]interface{}{
		"email": "invalid",
	}, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "@")
}

func TestUpdateClient_NotFound(t *testing.T) {
	repo := newMockClientRepo()
	svc := NewClientService(repo, nil, nil)

	_, err := svc.UpdateClient(999, map[string]interface{}{
		"first_name": "Ghost",
	}, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------------------------------------------------------------------------
// Client Limit tests
// ---------------------------------------------------------------------------

func TestGetClientLimits_Defaults(t *testing.T) {
	limitRepo := newMockClientLimitRepo()
	svc := NewClientLimitService(limitRepo, nil, nil, nil)

	limits, err := svc.GetClientLimits(42)
	require.NoError(t, err)
	assert.Equal(t, int64(42), limits.ClientID)
	assert.True(t, limits.DailyLimit.Equal(decimal.NewFromInt(100000)), "default daily limit")
	assert.True(t, limits.MonthlyLimit.Equal(decimal.NewFromInt(1000000)), "default monthly limit")
	assert.True(t, limits.TransferLimit.Equal(decimal.NewFromInt(50000)), "default transfer limit")
}

func TestSetClientLimits_PersistedAndRetrievable(t *testing.T) {
	limitRepo := newMockClientLimitRepo()
	empSvc := &mockEmployeeLimitSvc{
		maxClientDaily:   "500000",
		maxClientMonthly: "5000000",
	}
	svc := NewClientLimitService(limitRepo, empSvc, nil, nil)

	limit := model.ClientLimit{
		ClientID:      1,
		DailyLimit:    decimal.NewFromInt(200000),
		MonthlyLimit:  decimal.NewFromInt(2000000),
		TransferLimit: decimal.NewFromInt(100000),
		SetByEmployee: 10,
	}

	result, err := svc.SetClientLimits(context.Background(), limit, 0)
	require.NoError(t, err)
	assert.True(t, result.DailyLimit.Equal(decimal.NewFromInt(200000)))
	assert.True(t, result.MonthlyLimit.Equal(decimal.NewFromInt(2000000)))
	assert.True(t, result.TransferLimit.Equal(decimal.NewFromInt(100000)))

	// Retrieve again to verify persistence
	retrieved, err := svc.GetClientLimits(1)
	require.NoError(t, err)
	assert.True(t, retrieved.DailyLimit.Equal(decimal.NewFromInt(200000)))
	assert.True(t, retrieved.MonthlyLimit.Equal(decimal.NewFromInt(2000000)))
}

func TestSetClientLimits_ExceedsEmployeeDailyLimit(t *testing.T) {
	limitRepo := newMockClientLimitRepo()
	empSvc := &mockEmployeeLimitSvc{
		maxClientDaily:   "100000",
		maxClientMonthly: "5000000",
	}
	svc := NewClientLimitService(limitRepo, empSvc, nil, nil)

	limit := model.ClientLimit{
		ClientID:      1,
		DailyLimit:    decimal.NewFromInt(200000), // exceeds 100000
		MonthlyLimit:  decimal.NewFromInt(2000000),
		TransferLimit: decimal.NewFromInt(50000),
		SetByEmployee: 10,
	}

	_, err := svc.SetClientLimits(context.Background(), limit, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "daily limit")
	assert.Contains(t, err.Error(), "exceeds")
}

func TestSetClientLimits_ExceedsEmployeeMonthlyLimit(t *testing.T) {
	limitRepo := newMockClientLimitRepo()
	empSvc := &mockEmployeeLimitSvc{
		maxClientDaily:   "500000",
		maxClientMonthly: "1000000",
	}
	svc := NewClientLimitService(limitRepo, empSvc, nil, nil)

	limit := model.ClientLimit{
		ClientID:      1,
		DailyLimit:    decimal.NewFromInt(200000),
		MonthlyLimit:  decimal.NewFromInt(2000000), // exceeds 1000000
		TransferLimit: decimal.NewFromInt(50000),
		SetByEmployee: 10,
	}

	_, err := svc.SetClientLimits(context.Background(), limit, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "monthly limit")
	assert.Contains(t, err.Error(), "exceeds")
}

func TestSetClientLimits_NoEmployeeSvc_SkipsCheck(t *testing.T) {
	limitRepo := newMockClientLimitRepo()
	// nil userLimitSvc — should skip employee limit verification
	svc := NewClientLimitService(limitRepo, nil, nil, nil)

	limit := model.ClientLimit{
		ClientID:      1,
		DailyLimit:    decimal.NewFromInt(999999999),
		MonthlyLimit:  decimal.NewFromInt(999999999),
		TransferLimit: decimal.NewFromInt(999999999),
		SetByEmployee: 10,
	}

	result, err := svc.SetClientLimits(context.Background(), limit, 0)
	require.NoError(t, err)
	assert.True(t, result.DailyLimit.Equal(decimal.NewFromInt(999999999)))
}

func TestSetClientLimits_EmployeeSvcError(t *testing.T) {
	limitRepo := newMockClientLimitRepo()
	empSvc := &mockEmployeeLimitSvc{
		err: errors.New("gRPC connection refused"),
	}
	svc := NewClientLimitService(limitRepo, empSvc, nil, nil)

	limit := model.ClientLimit{
		ClientID:      1,
		DailyLimit:    decimal.NewFromInt(100000),
		MonthlyLimit:  decimal.NewFromInt(1000000),
		TransferLimit: decimal.NewFromInt(50000),
		SetByEmployee: 10,
	}

	_, err := svc.SetClientLimits(context.Background(), limit, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmployeeLookupFailed)
}

// ---------------------------------------------------------------------------
// Client-limit publish snapshot tests (SP-5)
// ---------------------------------------------------------------------------

// TestSetClientLimits_PublishedSnapshotCarriesValuesAndVersion asserts that
// SetClientLimits returns a result with non-zero Version and populated limit
// values — the exact data that flows into the PublishClientLimitsUpdated call.
// Uses the mock repo (which now simulates version=1 on first insert and
// version+1 on subsequent upserts) so the Version assertion is non-vacuous.
func TestSetClientLimits_PublishedSnapshotCarriesValuesAndVersion(t *testing.T) {
	limitRepo := newMockClientLimitRepo()
	svc := NewClientLimitService(limitRepo, nil, nil, nil) // nil producer, nil employee svc

	limit := model.ClientLimit{
		ClientID:      5,
		DailyLimit:    decimal.NewFromInt(50000),
		MonthlyLimit:  decimal.NewFromInt(500000),
		TransferLimit: decimal.NewFromInt(20000),
		SetByEmployee: 7,
	}
	result, err := svc.SetClientLimits(context.Background(), limit, 7)
	require.NoError(t, err)

	// The result is the value returned by GetByClientID after Upsert —
	// exactly what is used to populate the published ClientLimitsUpdatedMessage.
	assert.Equal(t, int64(1), result.Version,
		"first upsert must yield Version=1 (non-vacuous: mock simulates DB default)")
	assert.Equal(t, "50000.0000", result.DailyLimit.StringFixed(4))
	assert.Equal(t, "500000.0000", result.MonthlyLimit.StringFixed(4))
	assert.Equal(t, "20000.0000", result.TransferLimit.StringFixed(4))

	// Second upsert must yield Version=2.
	limit.DailyLimit = decimal.NewFromInt(60000)
	result2, err := svc.SetClientLimits(context.Background(), limit, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(2), result2.Version,
		"second upsert must yield Version=2 (monotonic increment)")
	assert.Equal(t, "60000.0000", result2.DailyLimit.StringFixed(4))
}

// ---------------------------------------------------------------------------
// Kafka publish payload tests
// ---------------------------------------------------------------------------

func TestCreateClient_PublishesJMBGAndVersion(t *testing.T) {
	repo := newMockClientRepo()
	prod := &mockClientProducer{}
	svc := NewClientService(repo, prod, nil)

	c := validClient()
	// The in-memory mock does not apply GORM's `default:1`, so we set Version
	// explicitly to a non-zero value. This makes the assertion below
	// non-vacuous: if Version were dropped from the publish payload the test
	// would fail (0 != 7), proving the field is actually wired.
	c.Version = 7
	require.NoError(t, svc.CreateClient(context.Background(), c))

	require.NotNil(t, prod.lastCreated, "ClientCreatedMessage must be published")
	assert.Equal(t, c.JMBG, prod.lastCreated.JMBG, "JMBG must be in published message")
	assert.Equal(t, int64(7), prod.lastCreated.Version, "Version must be in published message")
}

func TestUpdateClient_PublishesJMBGAndVersion(t *testing.T) {
	repo := newMockClientRepo()
	prod := &mockClientProducer{}
	svc := NewClientService(repo, prod, nil)

	c := validClient()
	// Seed a non-zero Version so the assertion below is non-vacuous: if Version
	// were dropped from the publish payload the test would fail (0 != 9).
	c.Version = 9
	require.NoError(t, svc.CreateClient(context.Background(), c))

	updated, err := svc.UpdateClient(c.ID, map[string]interface{}{"first_name": "Jane"}, 0)
	require.NoError(t, err)

	require.NotNil(t, prod.lastUpdated, "ClientCreatedMessage (update) must be published")
	assert.Equal(t, updated.JMBG, prod.lastUpdated.JMBG, "JMBG must be in updated message")
	assert.Equal(t, int64(9), prod.lastUpdated.Version, "Version must be in updated message")
}

// ---------------------------------------------------------------------------
// Real-DB version sequence test (guards replica ordering invariant)
// ---------------------------------------------------------------------------

// TestClient_VersionSequence_CreateThenUpdate_RealDB pins the load-bearing
// invariant that SP-1 replica consumers depend on: a freshly-created client
// row must carry Version=1 (driven by the GORM column default), and the first
// update must carry Version=2 (driven by the BeforeUpdate hook increment).
//
// This test intentionally wires a real gorm.DB (in-memory SQLite) + a real
// ClientRepository so the model's `default:1` tag and BeforeUpdate hook
// actually fire — mock-repo tests cannot catch regressions here because they
// bypass both mechanisms.
func TestClient_VersionSequence_CreateThenUpdate_RealDB(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Client{}))

	repo := repository.NewClientRepository(db)
	prod := &mockClientProducer{}
	svc := NewClientService(repo, prod, nil)

	c := validClient()
	// Do NOT set c.Version — let the DB column default drive it to 1.
	c.Version = 0
	require.NoError(t, svc.CreateClient(context.Background(), c))

	require.NotNil(t, prod.lastCreated, "ClientCreatedMessage must be published on create")
	assert.Equal(t, int64(1), prod.lastCreated.Version,
		"create must publish Version=1 (model column default:1 must fire on real DB)")

	_, err = svc.UpdateClient(c.ID, map[string]interface{}{"first_name": "Jane"}, 0)
	require.NoError(t, err)

	require.NotNil(t, prod.lastUpdated, "ClientCreatedMessage must be published on update")
	assert.Equal(t, int64(2), prod.lastUpdated.Version,
		"first update must publish Version=2 (BeforeUpdate hook must increment version on real DB)")
}
