package handler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	kafkaprod "github.com/exbanka/auth-service/internal/kafka"
	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/repository"
	"github.com/exbanka/auth-service/internal/service"
	pb "github.com/exbanka/contract/authpb"
)

// ----------------------------------------------------------------------------
// Test fixture: minimal handler wired to real services + SQLite + nil deps.
// ----------------------------------------------------------------------------

func newHandlerDB(t *testing.T) *gorm.DB {
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

type handlerFixture struct {
	db        *gorm.DB
	handler   *AuthGRPCHandler
	authSvc   *service.AuthService
	mobileSvc *service.MobileDeviceService
	pepper    string
}

func newHandlerFixture(t *testing.T) *handlerFixture {
	t.Helper()
	db := newHandlerDB(t)
	tokenRepo := repository.NewTokenRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
	loginRepo := repository.NewLoginAttemptRepository(db)
	totpRepo := repository.NewTOTPRepository(db)
	totpSvc := service.NewTOTPService()
	jwtSvc := service.NewJWTService("test-secret-256bit-min-len-please", 15*time.Minute)
	accountRepo := repository.NewAccountRepository(db)
	deviceRepo := repository.NewMobileDeviceRepository(db)
	activationRepo := repository.NewMobileActivationRepository(db)

	// stubProducer creates a Producer pointing at a non-listening port. All
	// publish calls fail; that is acceptable here because handlers operate
	// on read paths or DB-only mutations for the handlers we test.
	prod := kafkaprod.NewProducer("localhost:1")

	authSvc := service.NewAuthService(
		tokenRepo, sessionRepo, loginRepo, totpRepo, totpSvc, jwtSvc,
		accountRepo, nil, prod, nil,
		168*time.Hour, 720*time.Hour,
		"http://localhost:3000", "test-pepper",
	)
	mobileSvc := service.NewMobileDeviceService(
		deviceRepo, activationRepo, accountRepo, tokenRepo,
		jwtSvc, prod, 24*time.Hour, 10*time.Minute, "http://localhost:3000",
	)

	return &handlerFixture{
		db:        db,
		handler:   NewAuthGRPCHandler(authSvc, mobileSvc),
		authSvc:   authSvc,
		mobileSvc: mobileSvc,
		pepper:    "test-pepper",
	}
}

// ----------------------------------------------------------------------------
// mapServiceError tests
// ----------------------------------------------------------------------------

func TestMapServiceError(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want codes.Code
	}{
		{"not found", "user not found", codes.NotFound},
		{"invalid", "invalid input", codes.InvalidArgument},
		{"must be", "must be 8 characters", codes.InvalidArgument},
		{"already exists", "email already exists", codes.AlreadyExists},
		{"revoked", "token has been revoked", codes.Unauthenticated},
		{"expired", "token expired", codes.DeadlineExceeded},
		{"locked", "account locked", codes.ResourceExhausted},
		{"failed attempts", "too many failed attempts", codes.ResourceExhausted},
		{"permission", "permission denied", codes.PermissionDenied},
		{"do not match", "passwords do not match", codes.InvalidArgument},
		{"default", "totally unknown error", codes.Internal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, mapServiceError(fmt.Errorf("%s", tc.msg)))
		})
	}
}

// ----------------------------------------------------------------------------
// ValidateToken (no DB)
// ----------------------------------------------------------------------------

func TestHandler_ValidateToken_Invalid(t *testing.T) {
	f := newHandlerFixture(t)
	resp, err := f.handler.ValidateToken(context.Background(), &pb.ValidateTokenRequest{Token: "garbage"})
	require.NoError(t, err)
	assert.False(t, resp.Valid)
}

func TestHandler_ValidateToken_Valid(t *testing.T) {
	f := newHandlerFixture(t)
	jwtSvc := service.NewJWTService("test-secret-256bit-min-len-please", 15*time.Minute)
	tok, err := jwtSvc.GenerateAccessToken(7, "x@test.com", []string{"client"}, nil, "client", service.TokenProfile{AccountActive: true})
	require.NoError(t, err)

	resp, err := f.handler.ValidateToken(context.Background(), &pb.ValidateTokenRequest{Token: tok})
	require.NoError(t, err)
	assert.True(t, resp.Valid)
	assert.Equal(t, int64(7), resp.UserId)
	assert.Equal(t, "client", resp.SystemType)
	assert.Equal(t, "client", resp.Role, "legacy Role should reflect first role")
}

// ----------------------------------------------------------------------------
// Login handler
// ----------------------------------------------------------------------------

func TestHandler_Login_BadCredentials(t *testing.T) {
	f := newHandlerFixture(t)
	_, err := f.handler.Login(context.Background(), &pb.LoginRequest{Email: "ghost@test.com", Password: "Abcdef12"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
}

// ----------------------------------------------------------------------------
// RefreshToken handler
// ----------------------------------------------------------------------------

func TestHandler_RefreshToken_Invalid(t *testing.T) {
	f := newHandlerFixture(t)
	_, err := f.handler.RefreshToken(context.Background(), &pb.RefreshTokenRequest{RefreshToken: "missing"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
}

// ----------------------------------------------------------------------------
// RequestPasswordReset (always succeeds publicly)
// ----------------------------------------------------------------------------

func TestHandler_RequestPasswordReset_Success(t *testing.T) {
	f := newHandlerFixture(t)
	resp, err := f.handler.RequestPasswordReset(context.Background(), &pb.PasswordResetRequest{Email: "ghost@test.com"})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

// ----------------------------------------------------------------------------
// ResetPassword handler
// ----------------------------------------------------------------------------

func TestHandler_ResetPassword_PasswordMismatch(t *testing.T) {
	f := newHandlerFixture(t)
	_, err := f.handler.ResetPassword(context.Background(), &pb.ResetPasswordRequest{
		Token: "tok", NewPassword: "Abcdef12", ConfirmPassword: "Different12",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_ResetPassword_TokenNotFound(t *testing.T) {
	f := newHandlerFixture(t)
	_, err := f.handler.ResetPassword(context.Background(), &pb.ResetPasswordRequest{
		Token: "missing-tok", NewPassword: "Abcdef12", ConfirmPassword: "Abcdef12",
	})
	require.Error(t, err)
}

// ----------------------------------------------------------------------------
// ActivateAccount handler
// ----------------------------------------------------------------------------

func TestHandler_ActivateAccount_PasswordMismatch(t *testing.T) {
	f := newHandlerFixture(t)
	_, err := f.handler.ActivateAccount(context.Background(), &pb.ActivateAccountRequest{
		Token: "tok", Password: "Abcdef12", ConfirmPassword: "OtherP12",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ----------------------------------------------------------------------------
// Logout handler
// ----------------------------------------------------------------------------

func TestHandler_Logout_UnknownToken_Success(t *testing.T) {
	f := newHandlerFixture(t)
	resp, err := f.handler.Logout(context.Background(), &pb.LogoutRequest{RefreshToken: "missing-tok"})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

// ----------------------------------------------------------------------------
// SetAccountStatus handler — disabling a non-existent account fails
// ----------------------------------------------------------------------------

func TestHandler_SetAccountStatus_AccountNotFound(t *testing.T) {
	f := newHandlerFixture(t)
	_, err := f.handler.SetAccountStatus(context.Background(), &pb.SetAccountStatusRequest{
		PrincipalType: model.PrincipalTypeEmployee, PrincipalId: 9999, Active: false,
	})
	require.Error(t, err)
}

func TestHandler_SetAccountStatus_EnableExisting(t *testing.T) {
	f := newHandlerFixture(t)
	acct := &model.Account{
		Email: "u@test.com", PasswordHash: "h",
		Status: model.AccountStatusDisabled, PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 5,
	}
	require.NoError(t, f.db.Create(acct).Error)

	_, err := f.handler.SetAccountStatus(context.Background(), &pb.SetAccountStatusRequest{
		PrincipalType: model.PrincipalTypeEmployee, PrincipalId: 5, Active: true,
	})
	require.NoError(t, err)

	var got model.Account
	require.NoError(t, f.db.First(&got, acct.ID).Error)
	assert.Equal(t, model.AccountStatusActive, got.Status)
}

// ----------------------------------------------------------------------------
// GetAccountStatus
// ----------------------------------------------------------------------------

func TestHandler_GetAccountStatus_Found(t *testing.T) {
	f := newHandlerFixture(t)
	require.NoError(t, f.db.Create(&model.Account{
		Email: "ok@test.com", PasswordHash: "h",
		Status: model.AccountStatusActive, PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 8,
	}).Error)

	resp, err := f.handler.GetAccountStatus(context.Background(), &pb.GetAccountStatusRequest{
		PrincipalType: model.PrincipalTypeEmployee, PrincipalId: 8,
	})
	require.NoError(t, err)
	assert.Equal(t, model.AccountStatusActive, resp.Status)
	assert.True(t, resp.Active)
}

func TestHandler_GetAccountStatus_NotFound(t *testing.T) {
	f := newHandlerFixture(t)
	_, err := f.handler.GetAccountStatus(context.Background(), &pb.GetAccountStatusRequest{
		PrincipalType: model.PrincipalTypeEmployee, PrincipalId: 999,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ----------------------------------------------------------------------------
// GetAccountStatusBatch
// ----------------------------------------------------------------------------

func TestHandler_GetAccountStatusBatch(t *testing.T) {
	f := newHandlerFixture(t)
	require.NoError(t, f.db.Create(&model.Account{
		Email: "a@test.com", PasswordHash: "h",
		Status: model.AccountStatusActive, PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 1,
	}).Error)
	require.NoError(t, f.db.Create(&model.Account{
		Email: "b@test.com", PasswordHash: "h",
		Status: model.AccountStatusDisabled, PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 2,
	}).Error)

	resp, err := f.handler.GetAccountStatusBatch(context.Background(), &pb.GetAccountStatusBatchRequest{
		PrincipalType: model.PrincipalTypeEmployee, PrincipalIds: []int64{1, 2},
	})
	require.NoError(t, err)
	assert.Len(t, resp.Entries, 2)

	byID := map[int64]*pb.AccountStatusEntry{}
	for _, e := range resp.Entries {
		byID[e.PrincipalId] = e
	}
	assert.True(t, byID[1].Active)
	assert.False(t, byID[2].Active)
}

// ----------------------------------------------------------------------------
// ResendActivationEmail (always succeeds publicly to avoid enumeration)
// ----------------------------------------------------------------------------

func TestHandler_ResendActivationEmail_Success(t *testing.T) {
	f := newHandlerFixture(t)
	resp, err := f.handler.ResendActivationEmail(context.Background(), &pb.ResendActivationEmailRequest{Email: "ghost@test.com"})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

// ----------------------------------------------------------------------------
// RequestMobileActivation (always succeeds publicly)
// ----------------------------------------------------------------------------

func TestHandler_RequestMobileActivation_Success(t *testing.T) {
	f := newHandlerFixture(t)
	resp, err := f.handler.RequestMobileActivation(context.Background(), &pb.MobileActivationRequest{Email: "ghost@test.com"})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

// ----------------------------------------------------------------------------
// ActivateMobileDevice handler (no DB code → InvalidArgument or NotFound)
// ----------------------------------------------------------------------------

func TestHandler_ActivateMobileDevice_AccountNotFound(t *testing.T) {
	f := newHandlerFixture(t)
	_, err := f.handler.ActivateMobileDevice(context.Background(), &pb.ActivateMobileDeviceRequest{
		Email: "ghost@test.com", Code: "123456", DeviceName: "X",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ----------------------------------------------------------------------------
// DeactivateDevice handler
// ----------------------------------------------------------------------------

func TestHandler_DeactivateDevice_DeviceNotFound(t *testing.T) {
	f := newHandlerFixture(t)
	_, err := f.handler.DeactivateDevice(context.Background(), &pb.DeactivateDeviceRequest{
		UserId: 1, DeviceId: "missing",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ----------------------------------------------------------------------------
// ValidateDeviceSignature handler (always returns valid=false on error)
// ----------------------------------------------------------------------------

func TestHandler_ValidateDeviceSignature_DeviceNotFound(t *testing.T) {
	f := newHandlerFixture(t)
	resp, err := f.handler.ValidateDeviceSignature(context.Background(), &pb.ValidateDeviceSignatureRequest{
		DeviceId: "ghost", Timestamp: "12345", Method: "GET", Path: "/", BodySha256: "abc", Signature: "def",
	})
	require.NoError(t, err)
	assert.False(t, resp.Valid)
}

// ----------------------------------------------------------------------------
// GetDeviceInfo handler
// ----------------------------------------------------------------------------

func TestHandler_GetDeviceInfo_NotFound(t *testing.T) {
	f := newHandlerFixture(t)
	_, err := f.handler.GetDeviceInfo(context.Background(), &pb.GetDeviceInfoRequest{UserId: 9999})
	require.Error(t, err)
	// Service emits "no active device found" — note the message uses
	// "found" but not "not found", so mapServiceError falls to Internal.
	// We just assert that an error was returned.
}

func TestHandler_GetDeviceInfo_Found(t *testing.T) {
	f := newHandlerFixture(t)
	now := time.Now()
	require.NoError(t, f.db.Create(&model.MobileDevice{
		UserID: 99, SystemType: "client", DeviceID: "dev-99",
		DeviceSecret: "deadbeef", DeviceName: "iPhone",
		Status: "active", ActivatedAt: &now, LastSeenAt: now,
	}).Error)

	resp, err := f.handler.GetDeviceInfo(context.Background(), &pb.GetDeviceInfoRequest{UserId: 99})
	require.NoError(t, err)
	assert.Equal(t, "dev-99", resp.DeviceId)
	assert.Equal(t, "iPhone", resp.DeviceName)
	assert.Equal(t, "active", resp.Status)
}

// ----------------------------------------------------------------------------
// SetBiometricsEnabled handler
// ----------------------------------------------------------------------------

func TestHandler_SetBiometricsEnabled_DeviceNotFound(t *testing.T) {
	f := newHandlerFixture(t)
	_, err := f.handler.SetBiometricsEnabled(context.Background(), &pb.SetBiometricsRequest{
		UserId: 1, DeviceId: "missing", Enabled: true,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestHandler_GetBiometricsEnabled_DeviceNotFound(t *testing.T) {
	f := newHandlerFixture(t)
	_, err := f.handler.GetBiometricsEnabled(context.Background(), &pb.GetBiometricsRequest{
		UserId: 1, DeviceId: "missing",
	})
	require.Error(t, err)
}

func TestHandler_CheckBiometricsEnabled_DeviceNotFound(t *testing.T) {
	f := newHandlerFixture(t)
	_, err := f.handler.CheckBiometricsEnabled(context.Background(), &pb.CheckBiometricsRequest{DeviceId: "missing"})
	require.Error(t, err)
}

// ----------------------------------------------------------------------------
// ListSessions / GetLoginHistory handlers
// ----------------------------------------------------------------------------

func TestHandler_ListSessions_Empty(t *testing.T) {
	f := newHandlerFixture(t)
	resp, err := f.handler.ListSessions(context.Background(), &pb.ListSessionsRequest{UserId: 9999})
	require.NoError(t, err)
	assert.Empty(t, resp.Sessions)
}

func TestHandler_ListSessions_HasOne(t *testing.T) {
	f := newHandlerFixture(t)
	now := time.Now()
	require.NoError(t, f.db.Create(&model.ActiveSession{
		UserID: 1, UserRole: "client", IPAddress: "1.2.3.4",
		SystemType: "client", LastActiveAt: now, CreatedAt: now,
	}).Error)

	resp, err := f.handler.ListSessions(context.Background(), &pb.ListSessionsRequest{UserId: 1})
	require.NoError(t, err)
	require.Len(t, resp.Sessions, 1)
	assert.Equal(t, "1.2.3.4", resp.Sessions[0].IpAddress)
}

func TestHandler_RevokeSession_NotFound(t *testing.T) {
	f := newHandlerFixture(t)
	_, err := f.handler.RevokeSession(context.Background(), &pb.RevokeSessionRequest{
		SessionId: 9999, CallerUserId: 1,
	})
	require.Error(t, err)
}

func TestHandler_RevokeAllSessions_TokenNotFound(t *testing.T) {
	f := newHandlerFixture(t)
	_, err := f.handler.RevokeAllSessions(context.Background(), &pb.RevokeAllSessionsRequest{
		UserId: 1, CurrentRefreshToken: "missing",
	})
	require.Error(t, err)
}

func TestHandler_GetLoginHistory_Empty(t *testing.T) {
	f := newHandlerFixture(t)
	resp, err := f.handler.GetLoginHistory(context.Background(), &pb.LoginHistoryRequest{Email: "u@test.com", Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, resp.Entries)
}

func TestHandler_GetLoginHistory_NonEmpty(t *testing.T) {
	f := newHandlerFixture(t)
	loginRepo := repository.NewLoginAttemptRepository(f.db)
	require.NoError(t, loginRepo.RecordAttempt("u@test.com", "1.1.1.1", "ua", "browser", true))

	resp, err := f.handler.GetLoginHistory(context.Background(), &pb.LoginHistoryRequest{Email: "u@test.com", Limit: 10})
	require.NoError(t, err)
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, "u@test.com", resp.Entries[0].Email)
}

// ----------------------------------------------------------------------------
// CreateAccount handler — needs a working broker so we expect failure here.
// ----------------------------------------------------------------------------

// (CreateAccount is exercised against a real broker in integration tests; it
// invokes producer.SendEmail which fails without a live Kafka, so we don't
// assert the full positive path here.)

// ----------------------------------------------------------------------------
// TransferDevice handler
// ----------------------------------------------------------------------------

func TestHandler_TransferDevice_AccountNotFound(t *testing.T) {
	f := newHandlerFixture(t)
	// TransferDevice swallows the no-account / no-device errors and instead
	// calls RequestActivation, which will return account-not-found.
	_, err := f.handler.TransferDevice(context.Background(), &pb.TransferDeviceRequest{
		UserId: 9999, Email: "ghost@test.com",
	})
	require.Error(t, err)
}
