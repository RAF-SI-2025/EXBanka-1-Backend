package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kafkaprod "github.com/exbanka/auth-service/internal/kafka"
	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/repository"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// stubProducer returns a Producer whose writes silently fail (no real broker).
func stubProducer() *kafkaprod.Producer {
	return kafkaprod.NewProducer("localhost:1")
}

func setupAccountStatusTestDB(t *testing.T) *gorm.DB {
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
	))
	return db
}

func buildAuthServiceForStatusTests(t *testing.T, db *gorm.DB) *AuthService {
	t.Helper()
	tokenRepo := repository.NewTokenRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
	loginRepo := repository.NewLoginAttemptRepository(db)
	totpRepo := repository.NewTOTPRepository(db)
	totpSvc := NewTOTPService()
	jwtSvc := NewJWTService("test-secret-key-256bit-min", 15*time.Minute)
	accountRepo := repository.NewAccountRepository(db)

	return NewAuthService(
		tokenRepo, sessionRepo, loginRepo, totpRepo, totpSvc, jwtSvc,
		accountRepo, nil, stubProducer(), nil,
		168*time.Hour, 720*time.Hour,
		"http://localhost:3000", "test-pepper",
	)
}

// ============================================================
// Account Status Tests
// ============================================================

func TestSetAccountStatus_DisableAccount(t *testing.T) {
	db := setupAccountStatusTestDB(t)
	svc := buildAuthServiceForStatusTests(t, db)

	// Seed an active account
	acct := &model.Account{
		Email:         "emp@test.com",
		PasswordHash:  "hash",
		Status:        model.AccountStatusActive,
		PrincipalType: model.PrincipalTypeEmployee,
		PrincipalID:   1,
	}
	require.NoError(t, db.Create(acct).Error)

	// Disable it
	err := svc.SetAccountStatus(context.Background(), model.PrincipalTypeEmployee, 1, false)
	assert.NoError(t, err)

	// Verify persisted status
	var updated model.Account
	require.NoError(t, db.Where("principal_id = ? AND principal_type = ?", 1, model.PrincipalTypeEmployee).First(&updated).Error)
	assert.Equal(t, model.AccountStatusDisabled, updated.Status)
}

func TestSetAccountStatus_EnableAccount(t *testing.T) {
	db := setupAccountStatusTestDB(t)
	svc := buildAuthServiceForStatusTests(t, db)

	// Seed a disabled account
	acct := &model.Account{
		Email:         "emp@test.com",
		PasswordHash:  "hash",
		Status:        model.AccountStatusDisabled,
		PrincipalType: model.PrincipalTypeEmployee,
		PrincipalID:   2,
	}
	require.NoError(t, db.Create(acct).Error)

	// Enable it
	err := svc.SetAccountStatus(context.Background(), model.PrincipalTypeEmployee, 2, true)
	assert.NoError(t, err)

	// Verify persisted status
	var updated model.Account
	require.NoError(t, db.Where("principal_id = ? AND principal_type = ?", 2, model.PrincipalTypeEmployee).First(&updated).Error)
	assert.Equal(t, model.AccountStatusActive, updated.Status)
}

func TestSetAccountStatus_DisableRevokesTokens(t *testing.T) {
	db := setupAccountStatusTestDB(t)
	svc := buildAuthServiceForStatusTests(t, db)

	acct := &model.Account{
		Email:         "emp@test.com",
		PasswordHash:  "hash",
		Status:        model.AccountStatusActive,
		PrincipalType: model.PrincipalTypeEmployee,
		PrincipalID:   3,
	}
	require.NoError(t, db.Create(acct).Error)

	// Create a refresh token for this account
	rt := &model.RefreshToken{
		AccountID:  acct.ID,
		Token:      "refresh-token-abc",
		ExpiresAt:  time.Now().Add(168 * time.Hour),
		SystemType: "employee",
	}
	require.NoError(t, db.Create(rt).Error)

	// Disable
	err := svc.SetAccountStatus(context.Background(), model.PrincipalTypeEmployee, 3, false)
	assert.NoError(t, err)

	// Verify refresh token is revoked
	var updatedRT model.RefreshToken
	require.NoError(t, db.Where("token = ?", "refresh-token-abc").First(&updatedRT).Error)
	assert.True(t, updatedRT.Revoked, "refresh tokens should be revoked after account disabled")
}

func TestGetAccountStatus_Active(t *testing.T) {
	db := setupAccountStatusTestDB(t)
	svc := buildAuthServiceForStatusTests(t, db)

	acct := &model.Account{
		Email:         "client@test.com",
		PasswordHash:  "hash",
		Status:        model.AccountStatusActive,
		PrincipalType: model.PrincipalTypeClient,
		PrincipalID:   10,
	}
	require.NoError(t, db.Create(acct).Error)

	status, active, err := svc.GetAccountStatus(context.Background(), model.PrincipalTypeClient, 10)
	assert.NoError(t, err)
	assert.Equal(t, model.AccountStatusActive, status)
	assert.True(t, active)
}

func TestGetAccountStatus_Disabled(t *testing.T) {
	db := setupAccountStatusTestDB(t)
	svc := buildAuthServiceForStatusTests(t, db)

	acct := &model.Account{
		Email:         "client@test.com",
		PasswordHash:  "hash",
		Status:        model.AccountStatusDisabled,
		PrincipalType: model.PrincipalTypeClient,
		PrincipalID:   11,
	}
	require.NoError(t, db.Create(acct).Error)

	status, active, err := svc.GetAccountStatus(context.Background(), model.PrincipalTypeClient, 11)
	assert.NoError(t, err)
	assert.Equal(t, model.AccountStatusDisabled, status)
	assert.False(t, active)
}

func TestGetAccountStatus_NotFound(t *testing.T) {
	db := setupAccountStatusTestDB(t)
	svc := buildAuthServiceForStatusTests(t, db)

	_, _, err := svc.GetAccountStatus(context.Background(), model.PrincipalTypeClient, 9999)
	assert.Error(t, err)
}

func TestGetAccountStatusBatch(t *testing.T) {
	db := setupAccountStatusTestDB(t)
	svc := buildAuthServiceForStatusTests(t, db)

	// Seed multiple accounts
	for i, status := range []string{model.AccountStatusActive, model.AccountStatusDisabled, model.AccountStatusPending} {
		acct := &model.Account{
			Email:         fmt.Sprintf("user%d@test.com", i),
			PasswordHash:  "hash",
			Status:        status,
			PrincipalType: model.PrincipalTypeEmployee,
			PrincipalID:   int64(100 + i),
		}
		require.NoError(t, db.Create(acct).Error)
	}

	result, err := svc.GetAccountStatusBatch(context.Background(), model.PrincipalTypeEmployee, []int64{100, 101, 102})
	assert.NoError(t, err)
	assert.Len(t, result, 3)
	assert.Equal(t, model.AccountStatusActive, result[100].Status)
	assert.Equal(t, model.AccountStatusDisabled, result[101].Status)
	assert.Equal(t, model.AccountStatusPending, result[102].Status)
}

// ============================================================
// Token Claims Tests (system_type in JWT)
// ============================================================

func TestEmployeeLogin_SystemTypeEmployee(t *testing.T) {
	jwtSvc := NewJWTService("test-secret-key-256bit-min", 15*time.Minute)

	token, err := jwtSvc.GenerateAccessToken(1, "emp@test.com", []string{"EmployeeAdmin"}, []string{"users.manage"}, "employee", TokenProfile{AccountActive: true})
	require.NoError(t, err)

	claims, err := jwtSvc.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "employee", claims.SystemType)
	assert.Equal(t, int64(1), claims.UserID)
	assert.Equal(t, "emp@test.com", claims.Email)
	assert.Equal(t, []string{"EmployeeAdmin"}, claims.Roles)
	assert.Equal(t, []string{"users.manage"}, claims.Permissions)
}

func TestClientLogin_SystemTypeClient(t *testing.T) {
	jwtSvc := NewJWTService("test-secret-key-256bit-min", 15*time.Minute)

	token, err := jwtSvc.GenerateAccessToken(42, "client@test.com", []string{"client"}, nil, "client", TokenProfile{AccountActive: true})
	require.NoError(t, err)

	claims, err := jwtSvc.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "client", claims.SystemType)
	assert.Equal(t, int64(42), claims.UserID)
	assert.Equal(t, "client@test.com", claims.Email)
	assert.Equal(t, []string{"client"}, claims.Roles)
	assert.Empty(t, claims.Permissions)
}

func TestMobileAccessToken_ContainsDeviceClaims(t *testing.T) {
	jwtSvc := NewJWTService("test-secret-key-256bit-min", 15*time.Minute)

	deviceID := "abc-123-def-456"
	token, err := jwtSvc.GenerateMobileAccessToken(
		10, "mobile@test.com", []string{"client"}, nil,
		"client", MobileProfile{
			TokenProfile:  TokenProfile{AccountActive: true},
			DeviceType:    "mobile",
			DeviceID:      deviceID,
		},
	)
	require.NoError(t, err)

	claims, err := jwtSvc.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "client", claims.SystemType)
	assert.Equal(t, "mobile", claims.DeviceType)
	assert.Equal(t, deviceID, claims.DeviceID)
}

func TestAccessToken_HasJTI(t *testing.T) {
	jwtSvc := NewJWTService("test-secret-key-256bit-min", 15*time.Minute)

	token, err := jwtSvc.GenerateAccessToken(1, "user@test.com", []string{"client"}, nil, "client", TokenProfile{AccountActive: true})
	require.NoError(t, err)

	claims, err := jwtSvc.ValidateToken(token)
	require.NoError(t, err)
	assert.NotEmpty(t, claims.ID, "access tokens must have a JTI for revocation support")
}

func TestAccessToken_UniqueJTIs(t *testing.T) {
	jwtSvc := NewJWTService("test-secret-key-256bit-min", 15*time.Minute)

	token1, err := jwtSvc.GenerateAccessToken(1, "user@test.com", []string{"client"}, nil, "client", TokenProfile{AccountActive: true})
	require.NoError(t, err)
	token2, err := jwtSvc.GenerateAccessToken(1, "user@test.com", []string{"client"}, nil, "client", TokenProfile{AccountActive: true})
	require.NoError(t, err)

	claims1, err := jwtSvc.ValidateToken(token1)
	require.NoError(t, err)
	claims2, err := jwtSvc.ValidateToken(token2)
	require.NoError(t, err)

	assert.NotEqual(t, claims1.ID, claims2.ID, "each token must have a unique JTI")
}
