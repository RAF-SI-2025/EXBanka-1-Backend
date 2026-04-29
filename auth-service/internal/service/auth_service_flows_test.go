package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/repository"
	kafkamsg "github.com/exbanka/contract/kafka"
	userpb "github.com/exbanka/contract/userpb"
)

// ----------------------------------------------------------------------------
// In-memory event producer fake
// ----------------------------------------------------------------------------

type fakeProducer struct {
	mu     sync.Mutex
	emails []kafkamsg.SendEmailMessage
	events []struct {
		Topic string
		Msg   any
	}
}

func (f *fakeProducer) SendEmail(_ context.Context, msg kafkamsg.SendEmailMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emails = append(f.emails, msg)
	return nil
}

func (f *fakeProducer) Publish(_ context.Context, topic string, msg any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, struct {
		Topic string
		Msg   any
	}{topic, msg})
	return nil
}

func (f *fakeProducer) emailCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.emails)
}

func (f *fakeProducer) eventCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

// ----------------------------------------------------------------------------
// Stub UserServiceClient
// ----------------------------------------------------------------------------

type stubUserClient struct {
	resp *userpb.EmployeeResponse
	err  error
}

func (s *stubUserClient) CreateEmployee(ctx context.Context, in *userpb.CreateEmployeeRequest, opts ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	return s.resp, s.err
}
func (s *stubUserClient) GetEmployee(ctx context.Context, in *userpb.GetEmployeeRequest, opts ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	return s.resp, s.err
}
func (s *stubUserClient) ListEmployees(ctx context.Context, in *userpb.ListEmployeesRequest, opts ...grpc.CallOption) (*userpb.ListEmployeesResponse, error) {
	return nil, nil
}
func (s *stubUserClient) UpdateEmployee(ctx context.Context, in *userpb.UpdateEmployeeRequest, opts ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	return s.resp, s.err
}
func (s *stubUserClient) ListRoles(ctx context.Context, in *userpb.ListRolesRequest, opts ...grpc.CallOption) (*userpb.ListRolesResponse, error) {
	return nil, nil
}
func (s *stubUserClient) GetRole(ctx context.Context, in *userpb.GetRoleRequest, opts ...grpc.CallOption) (*userpb.RoleResponse, error) {
	return nil, nil
}
func (s *stubUserClient) CreateRole(ctx context.Context, in *userpb.CreateRoleRequest, opts ...grpc.CallOption) (*userpb.RoleResponse, error) {
	return nil, nil
}
func (s *stubUserClient) UpdateRolePermissions(ctx context.Context, in *userpb.UpdateRolePermissionsRequest, opts ...grpc.CallOption) (*userpb.RoleResponse, error) {
	return nil, nil
}
func (s *stubUserClient) ListPermissions(ctx context.Context, in *userpb.ListPermissionsRequest, opts ...grpc.CallOption) (*userpb.ListPermissionsResponse, error) {
	return nil, nil
}
func (s *stubUserClient) SetEmployeeRoles(ctx context.Context, in *userpb.SetEmployeeRolesRequest, opts ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	return s.resp, s.err
}
func (s *stubUserClient) SetEmployeeAdditionalPermissions(ctx context.Context, in *userpb.SetEmployeePermissionsRequest, opts ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	return s.resp, s.err
}
func (s *stubUserClient) ListEmployeeFullNames(ctx context.Context, in *userpb.ListEmployeeFullNamesRequest, opts ...grpc.CallOption) (*userpb.ListEmployeeFullNamesResponse, error) {
	return &userpb.ListEmployeeFullNamesResponse{}, nil
}
func (s *stubUserClient) AssignPermissionToRole(ctx context.Context, in *userpb.AssignPermissionToRoleRequest, opts ...grpc.CallOption) (*userpb.AssignPermissionToRoleResponse, error) {
	return &userpb.AssignPermissionToRoleResponse{}, nil
}
func (s *stubUserClient) RevokePermissionFromRole(ctx context.Context, in *userpb.RevokePermissionFromRoleRequest, opts ...grpc.CallOption) (*userpb.RevokePermissionFromRoleResponse, error) {
	return &userpb.RevokePermissionFromRoleResponse{}, nil
}
func (s *stubUserClient) ListChangelog(ctx context.Context, in *userpb.ListChangelogRequest, opts ...grpc.CallOption) (*userpb.ListChangelogResponse, error) {
	return &userpb.ListChangelogResponse{}, nil
}

// ----------------------------------------------------------------------------
// Test fixtures
// ----------------------------------------------------------------------------

func newAuthFlowDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })

	require.NoError(t, db.AutoMigrate(
		&model.Account{},
		&model.RefreshToken{},
		&model.ActivationToken{},
		&model.PasswordResetToken{},
		&model.LoginAttempt{},
		&model.AccountLock{},
		&model.TOTPSecret{},
		&model.ActiveSession{},
		&model.MobileDevice{},
		&model.MobileActivationCode{},
	))
	return db
}

type authFlowFixture struct {
	db          *gorm.DB
	svc         *AuthService
	tokenRepo   *repository.TokenRepository
	sessionRepo *repository.SessionRepository
	loginRepo   *repository.LoginAttemptRepository
	totpRepo    *repository.TOTPRepository
	accountRepo *repository.AccountRepository
	jwtSvc      *JWTService
	userClient  *stubUserClient
	producer    *fakeProducer
	pepper      string
}

func newAuthFlowFixture(t *testing.T) *authFlowFixture {
	t.Helper()
	db := newAuthFlowDB(t)
	tokenRepo := repository.NewTokenRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
	loginRepo := repository.NewLoginAttemptRepository(db)
	totpRepo := repository.NewTOTPRepository(db)
	totpSvc := NewTOTPService()
	jwtSvc := NewJWTService("test-secret-256bit-min-len-please", 15*time.Minute)
	accountRepo := repository.NewAccountRepository(db)
	userClient := &stubUserClient{
		resp: &userpb.EmployeeResponse{
			Id:          1,
			Email:       "emp@test.com",
			FirstName:   "Test",
			LastName:    "Employee",
			Roles:       []string{"EmployeeAdmin"},
			Permissions: []string{"users.manage"},
		},
	}
	producer := &fakeProducer{}

	svc := newAuthServiceForTest(
		tokenRepo, sessionRepo, loginRepo, totpRepo, totpSvc, jwtSvc,
		accountRepo, userClient, producer, nil,
		168*time.Hour, 720*time.Hour,
		"http://localhost:3000", "test-pepper",
	)
	return &authFlowFixture{
		db: db, svc: svc,
		tokenRepo: tokenRepo, sessionRepo: sessionRepo, loginRepo: loginRepo,
		totpRepo: totpRepo, accountRepo: accountRepo, jwtSvc: jwtSvc,
		userClient: userClient, producer: producer, pepper: "test-pepper",
	}
}

// seedActiveAccountWithPassword creates an active Account with a bcrypt'd password using the fixture pepper.
func (f *authFlowFixture) seedActiveAccountWithPassword(t *testing.T, email, password, principalType string, principalID int64) *model.Account {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(PepperPassword(f.pepper, password)), bcrypt.MinCost)
	require.NoError(t, err)
	acct := &model.Account{
		Email:         email,
		PasswordHash:  string(hash),
		Status:        model.AccountStatusActive,
		PrincipalType: principalType,
		PrincipalID:   principalID,
	}
	require.NoError(t, f.db.Create(acct).Error)
	return acct
}

// ----------------------------------------------------------------------------
// Login tests
// ----------------------------------------------------------------------------

func TestAuthLogin_EmployeeSuccess(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "emp@test.com", "Abcdef12", model.PrincipalTypeEmployee, 1)

	access, refresh, err := f.svc.Login(context.Background(), "emp@test.com", "Abcdef12", "1.2.3.4", "Mozilla/5.0")
	require.NoError(t, err)
	assert.NotEmpty(t, access)
	assert.NotEmpty(t, refresh)

	// JWT claims should reflect employee principal_type
	claims, err := f.jwtSvc.ValidateToken(access)
	require.NoError(t, err)
	assert.Equal(t, "employee", claims.PrincipalType)
	assert.Equal(t, int64(1), claims.PrincipalID)

	// Refresh token persisted
	var rt model.RefreshToken
	require.NoError(t, f.db.Where("token = ?", refresh).First(&rt).Error)
	assert.Equal(t, acct.ID, rt.AccountID)
	assert.False(t, rt.Revoked)
	assert.Equal(t, model.PrincipalTypeEmployee, rt.SystemType)

	// A session was created
	var sess model.ActiveSession
	require.NoError(t, f.db.Where("user_id = ?", acct.PrincipalID).First(&sess).Error)
	assert.Equal(t, "1.2.3.4", sess.IPAddress)
}

func TestAuthLogin_ClientSuccess(t *testing.T) {
	f := newAuthFlowFixture(t)
	f.seedActiveAccountWithPassword(t, "client@test.com", "Abcdef12", model.PrincipalTypeClient, 99)

	access, refresh, err := f.svc.Login(context.Background(), "client@test.com", "Abcdef12", "10.0.0.1", "Android/11")
	require.NoError(t, err)
	assert.NotEmpty(t, access)
	assert.NotEmpty(t, refresh)

	claims, err := f.jwtSvc.ValidateToken(access)
	require.NoError(t, err)
	assert.Equal(t, "client", claims.PrincipalType)
	assert.Equal(t, []string{"client"}, claims.Roles)
}

func TestAuthLogin_WrongPassword(t *testing.T) {
	f := newAuthFlowFixture(t)
	f.seedActiveAccountWithPassword(t, "emp@test.com", "Abcdef12", model.PrincipalTypeEmployee, 1)

	_, _, err := f.svc.Login(context.Background(), "emp@test.com", "WrongPass99", "1.2.3.4", "")
	require.Error(t, err)
	// Wrong-password collapses to ErrInvalidCredentials (codes.Unauthenticated).
	assert.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestAuthLogin_UnknownEmail(t *testing.T) {
	f := newAuthFlowFixture(t)
	_, _, err := f.svc.Login(context.Background(), "ghost@test.com", "Abcdef12", "1.2.3.4", "")
	require.Error(t, err)
	// Email-not-found collapses to ErrInvalidCredentials to prevent enumeration.
	assert.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestAuthLogin_PendingAccount(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := &model.Account{
		Email:         "pending@test.com",
		PasswordHash:  "hash",
		Status:        model.AccountStatusPending,
		PrincipalType: model.PrincipalTypeEmployee,
		PrincipalID:   1,
	}
	require.NoError(t, f.db.Create(acct).Error)

	_, _, err := f.svc.Login(context.Background(), "pending@test.com", "Abcdef12", "1.2.3.4", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountPending)
}

func TestAuthLogin_DisabledAccount(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := &model.Account{
		Email:         "disabled@test.com",
		PasswordHash:  "hash",
		Status:        model.AccountStatusDisabled,
		PrincipalType: model.PrincipalTypeEmployee,
		PrincipalID:   1,
	}
	require.NoError(t, f.db.Create(acct).Error)

	_, _, err := f.svc.Login(context.Background(), "disabled@test.com", "Abcdef12", "1.2.3.4", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountDisabled)
}

func TestAuthLogin_AccountLocked(t *testing.T) {
	f := newAuthFlowFixture(t)
	f.seedActiveAccountWithPassword(t, "emp@test.com", "Abcdef12", model.PrincipalTypeEmployee, 1)

	// Pre-seed a lock
	require.NoError(t, f.db.Create(&model.AccountLock{
		Email:     "emp@test.com",
		Reason:    "too_many_failed_attempts",
		LockedAt:  time.Now(),
		ExpiresAt: time.Now().Add(30 * time.Minute),
	}).Error)

	_, _, err := f.svc.Login(context.Background(), "emp@test.com", "Abcdef12", "1.2.3.4", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountLocked)
}

// ----------------------------------------------------------------------------
// ValidateToken tests
// ----------------------------------------------------------------------------

func TestValidateToken_ValidJWT_NoCache(t *testing.T) {
	f := newAuthFlowFixture(t)
	token, err := f.jwtSvc.GenerateAccessToken(1, "u@test.com", []string{"client"}, nil, "client", TokenProfile{AccountActive: true})
	require.NoError(t, err)

	claims, err := f.svc.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, int64(1), claims.PrincipalID)
	assert.Equal(t, "client", claims.PrincipalType)
}

func TestValidateToken_InvalidJWT(t *testing.T) {
	f := newAuthFlowFixture(t)
	_, err := f.svc.ValidateToken("garbage.not.a.token")
	require.Error(t, err)
}

// ----------------------------------------------------------------------------
// RefreshToken tests
// ----------------------------------------------------------------------------

func TestAuthRefreshToken_Success(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "emp@test.com", "Abcdef12", model.PrincipalTypeEmployee, 1)

	// Seed a session and refresh token
	sess := &model.ActiveSession{
		UserID: acct.PrincipalID, UserRole: "EmployeeAdmin",
		SystemType:   model.PrincipalTypeEmployee,
		LastActiveAt: time.Now(), CreatedAt: time.Now(),
	}
	require.NoError(t, f.db.Create(sess).Error)

	rt := &model.RefreshToken{
		AccountID: acct.ID, Token: "old-refresh-abc",
		ExpiresAt:  time.Now().Add(24 * time.Hour),
		SystemType: model.PrincipalTypeEmployee,
		SessionID:  &sess.ID,
	}
	require.NoError(t, f.db.Create(rt).Error)

	access, newRefresh, err := f.svc.RefreshToken(context.Background(), "old-refresh-abc", "1.2.3.4", "ua")
	require.NoError(t, err)
	assert.NotEmpty(t, access)
	assert.NotEmpty(t, newRefresh)
	assert.NotEqual(t, "old-refresh-abc", newRefresh, "rotation must produce a new token")

	// Old token should now be revoked
	var oldRT model.RefreshToken
	require.NoError(t, f.db.Where("token = ?", "old-refresh-abc").First(&oldRT).Error)
	assert.True(t, oldRT.Revoked)

	// New token should exist
	var newRT model.RefreshToken
	require.NoError(t, f.db.Where("token = ?", newRefresh).First(&newRT).Error)
	assert.Equal(t, acct.ID, newRT.AccountID)
}

func TestAuthRefreshToken_NotFound(t *testing.T) {
	f := newAuthFlowFixture(t)
	_, _, err := f.svc.RefreshToken(context.Background(), "missing-token", "ip", "ua")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refresh token has been revoked")
}

func TestAuthRefreshToken_Expired(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "emp@test.com", "Abcdef12", model.PrincipalTypeEmployee, 1)

	rt := &model.RefreshToken{
		AccountID: acct.ID, Token: "expired-tok",
		ExpiresAt:  time.Now().Add(-time.Hour),
		SystemType: model.PrincipalTypeEmployee,
	}
	require.NoError(t, f.db.Create(rt).Error)

	_, _, err := f.svc.RefreshToken(context.Background(), "expired-tok", "ip", "ua")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestAuthRefreshToken_AccountDisabled(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := &model.Account{
		Email: "x@test.com", PasswordHash: "h",
		Status:        model.AccountStatusDisabled,
		PrincipalType: model.PrincipalTypeEmployee,
		PrincipalID:   1,
	}
	require.NoError(t, f.db.Create(acct).Error)
	rt := &model.RefreshToken{
		AccountID: acct.ID, Token: "tok-disabled",
		ExpiresAt:  time.Now().Add(time.Hour),
		SystemType: model.PrincipalTypeEmployee,
	}
	require.NoError(t, f.db.Create(rt).Error)

	_, _, err := f.svc.RefreshToken(context.Background(), "tok-disabled", "ip", "ua")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

func TestAuthRefreshToken_ClientSystemType(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "client@test.com", "Abcdef12", model.PrincipalTypeClient, 42)

	rt := &model.RefreshToken{
		AccountID: acct.ID, Token: "client-rt",
		ExpiresAt:  time.Now().Add(time.Hour),
		SystemType: model.PrincipalTypeClient,
	}
	require.NoError(t, f.db.Create(rt).Error)

	access, newRefresh, err := f.svc.RefreshToken(context.Background(), "client-rt", "ip", "ua")
	require.NoError(t, err)
	assert.NotEmpty(t, access)
	assert.NotEmpty(t, newRefresh)

	claims, err := f.jwtSvc.ValidateToken(access)
	require.NoError(t, err)
	assert.Equal(t, "client", claims.PrincipalType)
}

// ----------------------------------------------------------------------------
// ValidateRefreshToken tests
// ----------------------------------------------------------------------------

func TestValidateRefreshToken_Valid(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "u@test.com", "Abcdef12", model.PrincipalTypeEmployee, 1)
	rt := &model.RefreshToken{
		AccountID: acct.ID, Token: "valid-rt",
		ExpiresAt: time.Now().Add(time.Hour), SystemType: model.PrincipalTypeEmployee,
	}
	require.NoError(t, f.db.Create(rt).Error)

	got, err := f.svc.ValidateRefreshToken("valid-rt")
	require.NoError(t, err)
	assert.Equal(t, "valid-rt", got.Token)
}

func TestValidateRefreshToken_Invalid(t *testing.T) {
	f := newAuthFlowFixture(t)
	_, err := f.svc.ValidateRefreshToken("missing-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestValidateRefreshToken_Expired(t *testing.T) {
	f := newAuthFlowFixture(t)
	rt := &model.RefreshToken{
		AccountID: 1, Token: "expired-rt",
		ExpiresAt: time.Now().Add(-time.Hour), SystemType: model.PrincipalTypeEmployee,
	}
	require.NoError(t, f.db.Create(rt).Error)

	_, err := f.svc.ValidateRefreshToken("expired-rt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

// ----------------------------------------------------------------------------
// Logout tests
// ----------------------------------------------------------------------------

func TestLogout_Success(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "u@test.com", "Abcdef12", model.PrincipalTypeEmployee, 1)
	sess := &model.ActiveSession{
		UserID: acct.PrincipalID, UserRole: "EmployeeAdmin",
		SystemType:   model.PrincipalTypeEmployee,
		LastActiveAt: time.Now(), CreatedAt: time.Now(),
	}
	require.NoError(t, f.db.Create(sess).Error)

	rt := &model.RefreshToken{
		AccountID: acct.ID, Token: "logout-tok",
		ExpiresAt: time.Now().Add(time.Hour), SystemType: model.PrincipalTypeEmployee,
		SessionID: &sess.ID,
	}
	require.NoError(t, f.db.Create(rt).Error)

	require.NoError(t, f.svc.Logout(context.Background(), "logout-tok"))

	// Token revoked
	var got model.RefreshToken
	require.NoError(t, f.db.Where("token = ?", "logout-tok").First(&got).Error)
	assert.True(t, got.Revoked)

	// Session revoked
	var s2 model.ActiveSession
	require.NoError(t, f.db.First(&s2, sess.ID).Error)
	assert.NotNil(t, s2.RevokedAt)
}

func TestLogout_TokenNotFound_StillSucceeds(t *testing.T) {
	f := newAuthFlowFixture(t)
	// No-op revoke even when token is missing.
	err := f.svc.Logout(context.Background(), "no-such-token")
	assert.NoError(t, err)
}

// ----------------------------------------------------------------------------
// RevokeAllSessions tests
// ----------------------------------------------------------------------------

func TestRevokeAllSessions_RevokesTokensAndSessions(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "u@test.com", "Abcdef12", model.PrincipalTypeEmployee, 7)

	// Two sessions and two refresh tokens
	for i := 0; i < 2; i++ {
		s := &model.ActiveSession{
			UserID: acct.PrincipalID, UserRole: "EmployeeAdmin",
			SystemType: model.PrincipalTypeEmployee, LastActiveAt: time.Now(), CreatedAt: time.Now(),
		}
		require.NoError(t, f.db.Create(s).Error)
	}
	for i := 0; i < 2; i++ {
		rt := &model.RefreshToken{
			AccountID: acct.ID, Token: fmt.Sprintf("tok-%d", i),
			ExpiresAt: time.Now().Add(time.Hour), SystemType: model.PrincipalTypeEmployee,
		}
		require.NoError(t, f.db.Create(rt).Error)
	}

	require.NoError(t, f.svc.RevokeAllSessions(context.Background(), acct.ID, acct.PrincipalID, "test_reason"))

	var revokedTokens int64
	require.NoError(t, f.db.Model(&model.RefreshToken{}).Where("account_id = ? AND revoked = true", acct.ID).Count(&revokedTokens).Error)
	assert.Equal(t, int64(2), revokedTokens)

	var liveSessions int64
	require.NoError(t, f.db.Model(&model.ActiveSession{}).Where("user_id = ? AND revoked_at IS NULL", acct.PrincipalID).Count(&liveSessions).Error)
	assert.Equal(t, int64(0), liveSessions)
}

// ----------------------------------------------------------------------------
// CreateAccountAndActivationToken tests
// ----------------------------------------------------------------------------

func TestCreateAccountAndActivationToken_NewAccount(t *testing.T) {
	f := newAuthFlowFixture(t)

	err := f.svc.CreateAccountAndActivationToken(context.Background(), 100, "new@test.com", "Alice", model.PrincipalTypeEmployee)
	require.NoError(t, err)

	// Account exists
	acct, err := f.accountRepo.GetByEmail("new@test.com")
	require.NoError(t, err)
	assert.Equal(t, model.AccountStatusPending, acct.Status)
	assert.Equal(t, int64(100), acct.PrincipalID)

	// An activation token was created
	var count int64
	require.NoError(t, f.db.Model(&model.ActivationToken{}).Where("account_id = ?", acct.ID).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestCreateAccountAndActivationToken_ExistingAccount(t *testing.T) {
	f := newAuthFlowFixture(t)
	// Pre-seed
	existing := &model.Account{
		Email: "exist@test.com", PasswordHash: "h",
		Status: model.AccountStatusPending, PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 99,
	}
	require.NoError(t, f.db.Create(existing).Error)

	err := f.svc.CreateAccountAndActivationToken(context.Background(), 99, "exist@test.com", "Bob", model.PrincipalTypeEmployee)
	require.NoError(t, err)

	// Still exactly one account
	var n int64
	require.NoError(t, f.db.Model(&model.Account{}).Where("email = ?", "exist@test.com").Count(&n).Error)
	assert.Equal(t, int64(1), n)
}

// ----------------------------------------------------------------------------
// ResendActivationEmail tests
// ----------------------------------------------------------------------------

func TestResendActivationEmail_PendingAccount(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := &model.Account{
		Email: "pending@test.com", PasswordHash: "",
		Status: model.AccountStatusPending, PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 5,
	}
	require.NoError(t, f.db.Create(acct).Error)

	require.NoError(t, f.svc.ResendActivationEmail(context.Background(), "pending@test.com"))

	var count int64
	require.NoError(t, f.db.Model(&model.ActivationToken{}).Where("account_id = ?", acct.ID).Count(&count).Error)
	assert.Equal(t, int64(1), count, "a fresh activation token should be created")
}

func TestResendActivationEmail_AlreadyActive_NoOp(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := &model.Account{
		Email: "active@test.com", PasswordHash: "h",
		Status: model.AccountStatusActive, PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 6,
	}
	require.NoError(t, f.db.Create(acct).Error)

	require.NoError(t, f.svc.ResendActivationEmail(context.Background(), "active@test.com"))

	// No activation token created
	var count int64
	require.NoError(t, f.db.Model(&model.ActivationToken{}).Where("account_id = ?", acct.ID).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestResendActivationEmail_UnknownEmail_NoOp(t *testing.T) {
	f := newAuthFlowFixture(t)
	require.NoError(t, f.svc.ResendActivationEmail(context.Background(), "ghost@test.com"))
}

// ----------------------------------------------------------------------------
// RequestPasswordReset tests
// ----------------------------------------------------------------------------

func TestRequestPasswordReset_KnownEmail(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "u@test.com", "Abcdef12", model.PrincipalTypeEmployee, 8)

	require.NoError(t, f.svc.RequestPasswordReset(context.Background(), "u@test.com"))

	var n int64
	require.NoError(t, f.db.Model(&model.PasswordResetToken{}).Where("account_id = ?", acct.ID).Count(&n).Error)
	assert.Equal(t, int64(1), n)
}

func TestRequestPasswordReset_UnknownEmail_NoOp(t *testing.T) {
	f := newAuthFlowFixture(t)
	require.NoError(t, f.svc.RequestPasswordReset(context.Background(), "ghost@test.com"))
}

// ----------------------------------------------------------------------------
// ResetPassword tests
// ----------------------------------------------------------------------------

func TestResetPassword_Success(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "u@test.com", "OldPass12", model.PrincipalTypeEmployee, 9)

	prt := &model.PasswordResetToken{
		AccountID: acct.ID, Token: "reset-tok",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, f.db.Create(prt).Error)

	err := f.svc.ResetPassword(context.Background(), "reset-tok", "NewPass12", "NewPass12")
	require.NoError(t, err)

	// Token marked used
	var got model.PasswordResetToken
	require.NoError(t, f.db.Where("token = ?", "reset-tok").First(&got).Error)
	assert.True(t, got.Used)

	// New password hash works
	var updated model.Account
	require.NoError(t, f.db.First(&updated, acct.ID).Error)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(updated.PasswordHash), []byte(PepperPassword(f.pepper, "NewPass12"))))
}

func TestResetPassword_MismatchedPasswords(t *testing.T) {
	f := newAuthFlowFixture(t)
	err := f.svc.ResetPassword(context.Background(), "tok", "NewPass12", "OtherPass12")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "do not match")
}

func TestResetPassword_WeakPassword(t *testing.T) {
	f := newAuthFlowFixture(t)
	err := f.svc.ResetPassword(context.Background(), "tok", "weak", "weak")
	require.Error(t, err)
}

func TestResetPassword_InvalidToken(t *testing.T) {
	f := newAuthFlowFixture(t)
	err := f.svc.ResetPassword(context.Background(), "missing-tok", "Abcdef12", "Abcdef12")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestResetPassword_ExpiredToken(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "u@test.com", "Abcdef12", model.PrincipalTypeEmployee, 1)
	prt := &model.PasswordResetToken{
		AccountID: acct.ID, Token: "expired-prt",
		ExpiresAt: time.Now().Add(-time.Hour),
	}
	require.NoError(t, f.db.Create(prt).Error)

	err := f.svc.ResetPassword(context.Background(), "expired-prt", "NewPass12", "NewPass12")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

// ----------------------------------------------------------------------------
// ActivateAccount tests
// ----------------------------------------------------------------------------

func TestActivateAccount_Success(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := &model.Account{
		Email: "new@test.com", PasswordHash: "",
		Status: model.AccountStatusPending, PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 1,
	}
	require.NoError(t, f.db.Create(acct).Error)

	at := &model.ActivationToken{
		AccountID: acct.ID, Token: "activate-tok",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, f.db.Create(at).Error)

	err := f.svc.ActivateAccount(context.Background(), "activate-tok", "Abcdef12", "Abcdef12")
	require.NoError(t, err)

	var updated model.Account
	require.NoError(t, f.db.First(&updated, acct.ID).Error)
	assert.Equal(t, model.AccountStatusActive, updated.Status)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(updated.PasswordHash), []byte(PepperPassword(f.pepper, "Abcdef12"))))

	// Token marked used
	var actUsed model.ActivationToken
	require.NoError(t, f.db.Where("token = ?", "activate-tok").First(&actUsed).Error)
	assert.True(t, actUsed.Used)
}

func TestActivateAccount_PasswordMismatch(t *testing.T) {
	f := newAuthFlowFixture(t)
	err := f.svc.ActivateAccount(context.Background(), "tok", "Abcdef12", "OtherP12")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "do not match")
}

func TestActivateAccount_WeakPassword(t *testing.T) {
	f := newAuthFlowFixture(t)
	err := f.svc.ActivateAccount(context.Background(), "tok", "weak", "weak")
	require.Error(t, err)
}

func TestActivateAccount_InvalidToken(t *testing.T) {
	f := newAuthFlowFixture(t)
	err := f.svc.ActivateAccount(context.Background(), "missing-tok", "Abcdef12", "Abcdef12")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestActivateAccount_ExpiredToken(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := &model.Account{
		Email: "x@test.com", Status: model.AccountStatusPending,
		PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 1,
	}
	require.NoError(t, f.db.Create(acct).Error)
	at := &model.ActivationToken{
		AccountID: acct.ID, Token: "old-tok",
		ExpiresAt: time.Now().Add(-time.Hour),
	}
	require.NoError(t, f.db.Create(at).Error)

	err := f.svc.ActivateAccount(context.Background(), "old-tok", "Abcdef12", "Abcdef12")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

// ----------------------------------------------------------------------------
// 2FA tests
// ----------------------------------------------------------------------------

func TestSetup2FA_CreatesPendingSecret(t *testing.T) {
	f := newAuthFlowFixture(t)
	secret, url, err := f.svc.Setup2FA(context.Background(), 42, "user@test.com")
	require.NoError(t, err)
	assert.NotEmpty(t, secret)
	assert.Contains(t, url, "user@test.com")

	got, err := f.totpRepo.GetByUserID(42)
	require.NoError(t, err)
	assert.Equal(t, secret, got.Secret)
	assert.False(t, got.Enabled, "must start disabled until verified")
}

func TestVerify2FA_NotSetUp(t *testing.T) {
	f := newAuthFlowFixture(t)
	ok, err := f.svc.Verify2FA(context.Background(), 999, "123456")
	require.Error(t, err)
	assert.False(t, ok)
}

func TestDisable2FA_NotSetUp(t *testing.T) {
	f := newAuthFlowFixture(t)
	ok, err := f.svc.Disable2FA(context.Background(), 999, "123456")
	require.Error(t, err)
	assert.False(t, ok)
}

func TestVerify2FA_InvalidCode(t *testing.T) {
	f := newAuthFlowFixture(t)
	// Setup a TOTP record but don't generate a valid code.
	_, _, err := f.svc.Setup2FA(context.Background(), 42, "user@test.com")
	require.NoError(t, err)
	ok, err := f.svc.Verify2FA(context.Background(), 42, "000000")
	require.NoError(t, err) // ValidateCode returns false, no error
	assert.False(t, ok)
}

func TestDisable2FA_InvalidCode(t *testing.T) {
	f := newAuthFlowFixture(t)
	_, _, err := f.svc.Setup2FA(context.Background(), 42, "user@test.com")
	require.NoError(t, err)
	ok, err := f.svc.Disable2FA(context.Background(), 42, "000000")
	require.NoError(t, err)
	assert.False(t, ok)
}

// ----------------------------------------------------------------------------
// Sessions / RevokeSession tests
// ----------------------------------------------------------------------------

func TestListSessions_ReturnsActive(t *testing.T) {
	f := newAuthFlowFixture(t)
	for i := 0; i < 2; i++ {
		s := &model.ActiveSession{
			UserID: 100, UserRole: "EmployeeAdmin",
			SystemType:   model.PrincipalTypeEmployee,
			LastActiveAt: time.Now(), CreatedAt: time.Now(),
		}
		require.NoError(t, f.db.Create(s).Error)
	}
	got, err := f.svc.ListSessions(context.Background(), 100)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestRevokeSession_Success(t *testing.T) {
	f := newAuthFlowFixture(t)
	s := &model.ActiveSession{
		UserID: 100, UserRole: "EmployeeAdmin",
		SystemType:   model.PrincipalTypeEmployee,
		LastActiveAt: time.Now(), CreatedAt: time.Now(),
	}
	require.NoError(t, f.db.Create(s).Error)

	require.NoError(t, f.svc.RevokeSession(context.Background(), s.ID, 100))

	var updated model.ActiveSession
	require.NoError(t, f.db.First(&updated, s.ID).Error)
	assert.NotNil(t, updated.RevokedAt)
}

func TestRevokeSession_WrongOwner(t *testing.T) {
	f := newAuthFlowFixture(t)
	s := &model.ActiveSession{
		UserID: 100, UserRole: "EmployeeAdmin",
		SystemType:   model.PrincipalTypeEmployee,
		LastActiveAt: time.Now(), CreatedAt: time.Now(),
	}
	require.NoError(t, f.db.Create(s).Error)

	err := f.svc.RevokeSession(context.Background(), s.ID, 999)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionForbidden)
}

func TestRevokeSession_NotFound(t *testing.T) {
	f := newAuthFlowFixture(t)
	err := f.svc.RevokeSession(context.Background(), 9999, 100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestRevokeSession_AlreadyRevoked(t *testing.T) {
	f := newAuthFlowFixture(t)
	now := time.Now()
	s := &model.ActiveSession{
		UserID: 100, UserRole: "EmployeeAdmin",
		SystemType:   model.PrincipalTypeEmployee,
		LastActiveAt: time.Now(), CreatedAt: time.Now(),
		RevokedAt: &now,
	}
	require.NoError(t, f.db.Create(s).Error)

	err := f.svc.RevokeSession(context.Background(), s.ID, 100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already revoked")
}

// ----------------------------------------------------------------------------
// RevokeAllSessionsExceptCurrent tests
// ----------------------------------------------------------------------------

func TestRevokeAllSessionsExceptCurrent_KeepsCurrent(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "u@test.com", "Abcdef12", model.PrincipalTypeEmployee, 50)

	keep := &model.ActiveSession{
		UserID: 50, UserRole: "EmployeeAdmin",
		SystemType:   model.PrincipalTypeEmployee,
		LastActiveAt: time.Now(), CreatedAt: time.Now(),
	}
	require.NoError(t, f.db.Create(keep).Error)

	other := &model.ActiveSession{
		UserID: 50, UserRole: "EmployeeAdmin",
		SystemType:   model.PrincipalTypeEmployee,
		LastActiveAt: time.Now(), CreatedAt: time.Now(),
	}
	require.NoError(t, f.db.Create(other).Error)

	rt := &model.RefreshToken{
		AccountID: acct.ID, Token: "current-rt",
		ExpiresAt:  time.Now().Add(time.Hour),
		SystemType: model.PrincipalTypeEmployee,
		SessionID:  &keep.ID,
	}
	require.NoError(t, f.db.Create(rt).Error)

	require.NoError(t, f.svc.RevokeAllSessionsExceptCurrent(context.Background(), 50, "current-rt"))

	var updatedKeep model.ActiveSession
	require.NoError(t, f.db.First(&updatedKeep, keep.ID).Error)
	assert.Nil(t, updatedKeep.RevokedAt, "current session should not be revoked")

	var updatedOther model.ActiveSession
	require.NoError(t, f.db.First(&updatedOther, other.ID).Error)
	assert.NotNil(t, updatedOther.RevokedAt, "other session should be revoked")
}

func TestRevokeAllSessionsExceptCurrent_TokenNotFound(t *testing.T) {
	f := newAuthFlowFixture(t)
	err := f.svc.RevokeAllSessionsExceptCurrent(context.Background(), 1, "missing-token")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

// ----------------------------------------------------------------------------
// GetLoginHistory tests
// ----------------------------------------------------------------------------

func TestGetLoginHistory_ReturnsRecent(t *testing.T) {
	f := newAuthFlowFixture(t)

	// Seed a few login attempts
	for i := 0; i < 3; i++ {
		require.NoError(t, f.loginRepo.RecordAttempt("u@test.com", "1.1.1.1", "ua", "browser", i%2 == 0))
	}

	got, err := f.svc.GetLoginHistory(context.Background(), "u@test.com", 10)
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

func TestGetLoginHistory_DefaultLimit(t *testing.T) {
	f := newAuthFlowFixture(t)
	// limit out of range should clamp to 50
	require.NoError(t, f.loginRepo.RecordAttempt("u@test.com", "ip", "ua", "browser", true))
	got, err := f.svc.GetLoginHistory(context.Background(), "u@test.com", 0)
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

// ----------------------------------------------------------------------------
// RevokeAccessToken & hashToken tests
// ----------------------------------------------------------------------------

func TestRevokeAccessToken_NoCache_IsNoOp(t *testing.T) {
	f := newAuthFlowFixture(t)
	// Cache is nil; should silently succeed.
	err := f.svc.RevokeAccessToken(context.Background(), "jti-123", time.Minute)
	assert.NoError(t, err)
}

// ----------------------------------------------------------------------------
// RefreshTokenForMobile tests
// ----------------------------------------------------------------------------

func TestRefreshTokenForMobile_Success(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "u@test.com", "Abcdef12", model.PrincipalTypeClient, 50)

	deviceID := generateDeviceID()
	now := time.Now()
	device := &model.MobileDevice{
		UserID: acct.PrincipalID, SystemType: model.PrincipalTypeClient,
		DeviceID: deviceID, DeviceSecret: generateDeviceSecret(),
		DeviceName: "Test", Status: "active",
		ActivatedAt: &now, LastSeenAt: now,
	}
	require.NoError(t, f.db.Create(device).Error)

	rt := &model.RefreshToken{
		AccountID: acct.ID, Token: "mob-rt",
		ExpiresAt: time.Now().Add(time.Hour), SystemType: model.PrincipalTypeClient,
	}
	require.NoError(t, f.db.Create(rt).Error)

	// Build a MobileDeviceService backed by the same DB.
	mobSvc, _ := newMobileSvcWithStubs(t, f.db)

	access, newRefresh, err := f.svc.RefreshTokenForMobile(context.Background(), "mob-rt", deviceID, mobSvc)
	require.NoError(t, err)
	assert.NotEmpty(t, access)
	assert.NotEmpty(t, newRefresh)
	assert.NotEqual(t, "mob-rt", newRefresh)

	// Old token is revoked
	var old model.RefreshToken
	require.NoError(t, f.db.Where("token = ?", "mob-rt").First(&old).Error)
	assert.True(t, old.Revoked)
}

func TestRefreshTokenForMobile_DeviceMismatch(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "u@test.com", "Abcdef12", model.PrincipalTypeClient, 51)

	now := time.Now()
	require.NoError(t, f.db.Create(&model.MobileDevice{
		UserID: acct.PrincipalID, SystemType: model.PrincipalTypeClient,
		DeviceID: "real-device", DeviceSecret: generateDeviceSecret(),
		DeviceName: "X", Status: "active",
		ActivatedAt: &now, LastSeenAt: now,
	}).Error)
	require.NoError(t, f.db.Create(&model.RefreshToken{
		AccountID: acct.ID, Token: "mob-rt-2",
		ExpiresAt: time.Now().Add(time.Hour), SystemType: model.PrincipalTypeClient,
	}).Error)

	mobSvc, _ := newMobileSvcWithStubs(t, f.db)

	_, _, err := f.svc.RefreshTokenForMobile(context.Background(), "mob-rt-2", "wrong-device", mobSvc)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDeviceMismatch)
}

func TestRefreshTokenForMobile_NoDevice(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "u@test.com", "Abcdef12", model.PrincipalTypeClient, 52)
	require.NoError(t, f.db.Create(&model.RefreshToken{
		AccountID: acct.ID, Token: "no-device-rt",
		ExpiresAt: time.Now().Add(time.Hour), SystemType: model.PrincipalTypeClient,
	}).Error)
	mobSvc, _ := newMobileSvcWithStubs(t, f.db)
	_, _, err := f.svc.RefreshTokenForMobile(context.Background(), "no-device-rt", "any", mobSvc)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDeviceNotFound)
}

func TestRefreshTokenForMobile_TokenNotFound(t *testing.T) {
	f := newAuthFlowFixture(t)
	mobSvc, _ := newMobileSvcWithStubs(t, f.db)
	_, _, err := f.svc.RefreshTokenForMobile(context.Background(), "missing-rt", "any-dev", mobSvc)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestRefreshTokenForMobile_Expired(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "u@test.com", "Abcdef12", model.PrincipalTypeClient, 53)
	require.NoError(t, f.db.Create(&model.RefreshToken{
		AccountID: acct.ID, Token: "expired-mob",
		ExpiresAt: time.Now().Add(-time.Hour), SystemType: model.PrincipalTypeClient,
	}).Error)
	mobSvc, _ := newMobileSvcWithStubs(t, f.db)
	_, _, err := f.svc.RefreshTokenForMobile(context.Background(), "expired-mob", "any", mobSvc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestHashToken_DeterministicAndUnique(t *testing.T) {
	a := hashToken("token-a")
	b := hashToken("token-a")
	c := hashToken("token-b")
	assert.Equal(t, a, b)
	assert.NotEqual(t, a, c)
	assert.Len(t, a, 64) // sha256 hex
}
