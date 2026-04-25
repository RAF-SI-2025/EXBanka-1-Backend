package handler

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/service"
	pb "github.com/exbanka/contract/authpb"
)

// ----------------------------------------------------------------------------
// Test stubs: hand-written implementations of authServiceFacade and
// mobileDeviceFacade. Each method is a function field so individual tests can
// set only the methods they exercise. This avoids any sqlite/Kafka dependency.
// ----------------------------------------------------------------------------

type stubAuthService struct {
	loginFn                          func(ctx context.Context, email, password, ip, ua string) (string, string, error)
	validateTokenFn                  func(token string) (*service.Claims, error)
	refreshTokenFn                   func(ctx context.Context, rt, ip, ua string) (string, string, error)
	requestPasswordResetFn           func(ctx context.Context, email string) error
	resetPasswordFn                  func(ctx context.Context, tok, np, cp string) error
	activateAccountFn                func(ctx context.Context, tok, p, cp string) error
	logoutFn                         func(ctx context.Context, rt string) error
	setAccountStatusFn               func(ctx context.Context, pt string, pid int64, active bool) error
	getAccountStatusFn               func(ctx context.Context, pt string, pid int64) (string, bool, error)
	resendActivationEmailFn          func(ctx context.Context, email string) error
	createAccountAndActivationFn     func(ctx context.Context, pid int64, email, fn, pt string) error
	getAccountStatusBatchFn          func(ctx context.Context, pt string, pids []int64) (map[int64]model.Account, error)
	refreshTokenForMobileFn          func(ctx context.Context, rt, dev string, m service.MobileDeviceLookup) (string, string, error)
	listSessionsFn                   func(ctx context.Context, userID int64) ([]model.ActiveSession, error)
	revokeSessionFn                  func(ctx context.Context, sessionID, callerID int64) error
	revokeAllSessionsExceptCurrentFn func(ctx context.Context, userID int64, currentRT string) error
	getLoginHistoryFn                func(ctx context.Context, email string, limit int) ([]service.LoginHistoryEntry, error)
}

func (s *stubAuthService) Login(ctx context.Context, email, password, ip, ua string) (string, string, error) {
	return s.loginFn(ctx, email, password, ip, ua)
}
func (s *stubAuthService) ValidateToken(token string) (*service.Claims, error) {
	return s.validateTokenFn(token)
}
func (s *stubAuthService) RefreshToken(ctx context.Context, rt, ip, ua string) (string, string, error) {
	return s.refreshTokenFn(ctx, rt, ip, ua)
}
func (s *stubAuthService) RequestPasswordReset(ctx context.Context, email string) error {
	return s.requestPasswordResetFn(ctx, email)
}
func (s *stubAuthService) ResetPassword(ctx context.Context, tok, np, cp string) error {
	return s.resetPasswordFn(ctx, tok, np, cp)
}
func (s *stubAuthService) ActivateAccount(ctx context.Context, tok, p, cp string) error {
	return s.activateAccountFn(ctx, tok, p, cp)
}
func (s *stubAuthService) Logout(ctx context.Context, rt string) error {
	return s.logoutFn(ctx, rt)
}
func (s *stubAuthService) SetAccountStatus(ctx context.Context, pt string, pid int64, active bool) error {
	return s.setAccountStatusFn(ctx, pt, pid, active)
}
func (s *stubAuthService) GetAccountStatus(ctx context.Context, pt string, pid int64) (string, bool, error) {
	return s.getAccountStatusFn(ctx, pt, pid)
}
func (s *stubAuthService) ResendActivationEmail(ctx context.Context, email string) error {
	return s.resendActivationEmailFn(ctx, email)
}
func (s *stubAuthService) CreateAccountAndActivationToken(ctx context.Context, pid int64, email, fn, pt string) error {
	return s.createAccountAndActivationFn(ctx, pid, email, fn, pt)
}
func (s *stubAuthService) GetAccountStatusBatch(ctx context.Context, pt string, pids []int64) (map[int64]model.Account, error) {
	return s.getAccountStatusBatchFn(ctx, pt, pids)
}
func (s *stubAuthService) RefreshTokenForMobile(ctx context.Context, rt, dev string, m service.MobileDeviceLookup) (string, string, error) {
	return s.refreshTokenForMobileFn(ctx, rt, dev, m)
}
func (s *stubAuthService) ListSessions(ctx context.Context, userID int64) ([]model.ActiveSession, error) {
	return s.listSessionsFn(ctx, userID)
}
func (s *stubAuthService) RevokeSession(ctx context.Context, sessionID, callerID int64) error {
	return s.revokeSessionFn(ctx, sessionID, callerID)
}
func (s *stubAuthService) RevokeAllSessionsExceptCurrent(ctx context.Context, userID int64, currentRT string) error {
	return s.revokeAllSessionsExceptCurrentFn(ctx, userID, currentRT)
}
func (s *stubAuthService) GetLoginHistory(ctx context.Context, email string, limit int) ([]service.LoginHistoryEntry, error) {
	return s.getLoginHistoryFn(ctx, email, limit)
}

type stubMobileService struct {
	requestActivationFn       func(ctx context.Context, email string) error
	activateDeviceFn          func(ctx context.Context, email, code, name string) (string, string, string, string, error)
	deactivateDeviceFn        func(userID int64, deviceID string) error
	transferDeviceFn          func(ctx context.Context, userID int64, email string) error
	validateDeviceSignatureFn func(deviceID, ts, method, path, sha, sig string) (bool, error)
	getDeviceInfoFn           func(userID int64) (*model.MobileDevice, error)
	setBiometricsEnabledFn    func(userID int64, deviceID string, enabled bool) error
	getBiometricsEnabledFn    func(userID int64, deviceID string) (bool, error)
	checkBiometricsEnabledFn  func(deviceID string) (bool, error)
}

func (s *stubMobileService) RequestActivation(ctx context.Context, email string) error {
	return s.requestActivationFn(ctx, email)
}
func (s *stubMobileService) ActivateDevice(ctx context.Context, email, code, name string) (string, string, string, string, error) {
	return s.activateDeviceFn(ctx, email, code, name)
}
func (s *stubMobileService) DeactivateDevice(userID int64, deviceID string) error {
	return s.deactivateDeviceFn(userID, deviceID)
}
func (s *stubMobileService) TransferDevice(ctx context.Context, userID int64, email string) error {
	return s.transferDeviceFn(ctx, userID, email)
}
func (s *stubMobileService) ValidateDeviceSignature(deviceID, ts, method, path, sha, sig string) (bool, error) {
	return s.validateDeviceSignatureFn(deviceID, ts, method, path, sha, sig)
}
func (s *stubMobileService) GetDeviceInfo(userID int64) (*model.MobileDevice, error) {
	return s.getDeviceInfoFn(userID)
}
func (s *stubMobileService) SetBiometricsEnabled(userID int64, deviceID string, enabled bool) error {
	return s.setBiometricsEnabledFn(userID, deviceID, enabled)
}
func (s *stubMobileService) GetBiometricsEnabled(userID int64, deviceID string) (bool, error) {
	return s.getBiometricsEnabledFn(userID, deviceID)
}
func (s *stubMobileService) CheckBiometricsEnabled(deviceID string) (bool, error) {
	return s.checkBiometricsEnabledFn(deviceID)
}

// newHandlerForTest wires stubs into an AuthGRPCHandler. It is unexported on
// purpose: production code must continue to use NewAuthGRPCHandler.
func newHandlerForTest(auth authServiceFacade, mobile mobileDeviceFacade) *AuthGRPCHandler {
	return &AuthGRPCHandler{authService: auth, mobileSvc: mobile}
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
// ValidateToken
// ----------------------------------------------------------------------------

func TestHandler_ValidateToken_Invalid(t *testing.T) {
	auth := &stubAuthService{
		validateTokenFn: func(string) (*service.Claims, error) { return nil, errors.New("invalid token") },
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.ValidateToken(context.Background(), &pb.ValidateTokenRequest{Token: "garbage"})
	require.NoError(t, err)
	assert.False(t, resp.Valid)
}

func TestHandler_ValidateToken_Valid(t *testing.T) {
	auth := &stubAuthService{
		validateTokenFn: func(string) (*service.Claims, error) {
			return &service.Claims{
				UserID:        7,
				Email:         "x@test.com",
				Roles:         []string{"client"},
				SystemType:    "client",
				AccountActive: true,
			}, nil
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.ValidateToken(context.Background(), &pb.ValidateTokenRequest{Token: "tok"})
	require.NoError(t, err)
	assert.True(t, resp.Valid)
	assert.Equal(t, int64(7), resp.UserId)
	assert.Equal(t, "client", resp.SystemType)
	assert.Equal(t, "client", resp.Role, "legacy Role should reflect first role")
}

// ----------------------------------------------------------------------------
// Login
// ----------------------------------------------------------------------------

func TestHandler_Login_BadCredentials(t *testing.T) {
	auth := &stubAuthService{
		loginFn: func(context.Context, string, string, string, string) (string, string, error) {
			return "", "", errors.New("invalid credentials")
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	_, err := h.Login(context.Background(), &pb.LoginRequest{Email: "ghost@test.com", Password: "Abcdef12"})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_Login_Success(t *testing.T) {
	auth := &stubAuthService{
		loginFn: func(_ context.Context, email, password, _, _ string) (string, string, error) {
			assert.Equal(t, "u@test.com", email)
			assert.Equal(t, "Abcdef12", password)
			return "access-tok", "refresh-tok", nil
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.Login(context.Background(), &pb.LoginRequest{Email: "u@test.com", Password: "Abcdef12"})
	require.NoError(t, err)
	assert.Equal(t, "access-tok", resp.AccessToken)
	assert.Equal(t, "refresh-tok", resp.RefreshToken)
}

// ----------------------------------------------------------------------------
// RefreshToken
// ----------------------------------------------------------------------------

func TestHandler_RefreshToken_Invalid(t *testing.T) {
	auth := &stubAuthService{
		refreshTokenFn: func(context.Context, string, string, string) (string, string, error) {
			return "", "", errors.New("invalid refresh token")
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	_, err := h.RefreshToken(context.Background(), &pb.RefreshTokenRequest{RefreshToken: "missing"})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_RefreshToken_Success(t *testing.T) {
	auth := &stubAuthService{
		refreshTokenFn: func(_ context.Context, rt, _, _ string) (string, string, error) {
			assert.Equal(t, "old-refresh", rt)
			return "new-access", "new-refresh", nil
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.RefreshToken(context.Background(), &pb.RefreshTokenRequest{RefreshToken: "old-refresh"})
	require.NoError(t, err)
	assert.Equal(t, "new-access", resp.AccessToken)
	assert.Equal(t, "new-refresh", resp.RefreshToken)
}

// ----------------------------------------------------------------------------
// RequestPasswordReset (always succeeds publicly)
// ----------------------------------------------------------------------------

func TestHandler_RequestPasswordReset_Success(t *testing.T) {
	auth := &stubAuthService{
		requestPasswordResetFn: func(context.Context, string) error { return errors.New("user not found") },
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.RequestPasswordReset(context.Background(), &pb.PasswordResetRequest{Email: "ghost@test.com"})
	require.NoError(t, err, "must not leak email existence")
	assert.True(t, resp.Success)
}

// ----------------------------------------------------------------------------
// ResetPassword
// ----------------------------------------------------------------------------

func TestHandler_ResetPassword_PasswordMismatch(t *testing.T) {
	auth := &stubAuthService{
		resetPasswordFn: func(context.Context, string, string, string) error {
			return errors.New("passwords do not match")
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	_, err := h.ResetPassword(context.Background(), &pb.ResetPasswordRequest{
		Token: "tok", NewPassword: "Abcdef12", ConfirmPassword: "Different12",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_ResetPassword_TokenNotFound(t *testing.T) {
	auth := &stubAuthService{
		resetPasswordFn: func(context.Context, string, string, string) error {
			return errors.New("password reset token not found")
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	_, err := h.ResetPassword(context.Background(), &pb.ResetPasswordRequest{
		Token: "missing-tok", NewPassword: "Abcdef12", ConfirmPassword: "Abcdef12",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandler_ResetPassword_Success(t *testing.T) {
	auth := &stubAuthService{
		resetPasswordFn: func(context.Context, string, string, string) error { return nil },
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.ResetPassword(context.Background(), &pb.ResetPasswordRequest{
		Token: "tok", NewPassword: "Abcdef12", ConfirmPassword: "Abcdef12",
	})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

// ----------------------------------------------------------------------------
// ActivateAccount
// ----------------------------------------------------------------------------

func TestHandler_ActivateAccount_PasswordMismatch(t *testing.T) {
	auth := &stubAuthService{
		activateAccountFn: func(context.Context, string, string, string) error {
			return errors.New("passwords do not match")
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	_, err := h.ActivateAccount(context.Background(), &pb.ActivateAccountRequest{
		Token: "tok", Password: "Abcdef12", ConfirmPassword: "OtherP12",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_ActivateAccount_Success(t *testing.T) {
	auth := &stubAuthService{
		activateAccountFn: func(context.Context, string, string, string) error { return nil },
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.ActivateAccount(context.Background(), &pb.ActivateAccountRequest{
		Token: "tok", Password: "Abcdef12", ConfirmPassword: "Abcdef12",
	})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

// ----------------------------------------------------------------------------
// Logout
// ----------------------------------------------------------------------------

func TestHandler_Logout_Success(t *testing.T) {
	auth := &stubAuthService{
		logoutFn: func(context.Context, string) error { return nil },
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.Logout(context.Background(), &pb.LogoutRequest{RefreshToken: "tok"})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

func TestHandler_Logout_Error(t *testing.T) {
	auth := &stubAuthService{
		logoutFn: func(context.Context, string) error { return errors.New("internal db error") },
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	_, err := h.Logout(context.Background(), &pb.LogoutRequest{RefreshToken: "tok"})
	require.Error(t, err)
}

// ----------------------------------------------------------------------------
// SetAccountStatus
// ----------------------------------------------------------------------------

func TestHandler_SetAccountStatus_AccountNotFound(t *testing.T) {
	auth := &stubAuthService{
		setAccountStatusFn: func(context.Context, string, int64, bool) error {
			return errors.New("account not found")
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	_, err := h.SetAccountStatus(context.Background(), &pb.SetAccountStatusRequest{
		PrincipalType: model.PrincipalTypeEmployee, PrincipalId: 9999, Active: false,
	})
	require.Error(t, err)
}

func TestHandler_SetAccountStatus_EnableExisting(t *testing.T) {
	called := false
	auth := &stubAuthService{
		setAccountStatusFn: func(_ context.Context, pt string, pid int64, active bool) error {
			called = true
			assert.Equal(t, model.PrincipalTypeEmployee, pt)
			assert.Equal(t, int64(5), pid)
			assert.True(t, active)
			return nil
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.SetAccountStatus(context.Background(), &pb.SetAccountStatusRequest{
		PrincipalType: model.PrincipalTypeEmployee, PrincipalId: 5, Active: true,
	})
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.True(t, called)
}

// ----------------------------------------------------------------------------
// GetAccountStatus
// ----------------------------------------------------------------------------

func TestHandler_GetAccountStatus_Found(t *testing.T) {
	auth := &stubAuthService{
		getAccountStatusFn: func(context.Context, string, int64) (string, bool, error) {
			return model.AccountStatusActive, true, nil
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.GetAccountStatus(context.Background(), &pb.GetAccountStatusRequest{
		PrincipalType: model.PrincipalTypeEmployee, PrincipalId: 8,
	})
	require.NoError(t, err)
	assert.Equal(t, model.AccountStatusActive, resp.Status)
	assert.True(t, resp.Active)
}

func TestHandler_GetAccountStatus_NotFound(t *testing.T) {
	auth := &stubAuthService{
		getAccountStatusFn: func(context.Context, string, int64) (string, bool, error) {
			return "", false, errors.New("not found")
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	_, err := h.GetAccountStatus(context.Background(), &pb.GetAccountStatusRequest{
		PrincipalType: model.PrincipalTypeEmployee, PrincipalId: 999,
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// ----------------------------------------------------------------------------
// GetAccountStatusBatch
// ----------------------------------------------------------------------------

func TestHandler_GetAccountStatusBatch(t *testing.T) {
	auth := &stubAuthService{
		getAccountStatusBatchFn: func(_ context.Context, pt string, pids []int64) (map[int64]model.Account, error) {
			assert.Equal(t, model.PrincipalTypeEmployee, pt)
			assert.ElementsMatch(t, []int64{1, 2}, pids)
			return map[int64]model.Account{
				1: {PrincipalID: 1, Status: model.AccountStatusActive},
				2: {PrincipalID: 2, Status: model.AccountStatusDisabled},
			}, nil
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.GetAccountStatusBatch(context.Background(), &pb.GetAccountStatusBatchRequest{
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

func TestHandler_GetAccountStatusBatch_Error(t *testing.T) {
	auth := &stubAuthService{
		getAccountStatusBatchFn: func(context.Context, string, []int64) (map[int64]model.Account, error) {
			return nil, errors.New("db down")
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	_, err := h.GetAccountStatusBatch(context.Background(), &pb.GetAccountStatusBatchRequest{
		PrincipalType: model.PrincipalTypeEmployee, PrincipalIds: []int64{1},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// ----------------------------------------------------------------------------
// ResendActivationEmail (always succeeds publicly)
// ----------------------------------------------------------------------------

func TestHandler_ResendActivationEmail_Success(t *testing.T) {
	auth := &stubAuthService{
		resendActivationEmailFn: func(context.Context, string) error { return errors.New("no such user") },
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.ResendActivationEmail(context.Background(), &pb.ResendActivationEmailRequest{Email: "ghost@test.com"})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

// ----------------------------------------------------------------------------
// CreateAccount
// ----------------------------------------------------------------------------

func TestHandler_CreateAccount_Success(t *testing.T) {
	auth := &stubAuthService{
		createAccountAndActivationFn: func(_ context.Context, pid int64, email, fn, pt string) error {
			assert.Equal(t, int64(11), pid)
			assert.Equal(t, "n@test.com", email)
			assert.Equal(t, "Nina", fn)
			assert.Equal(t, model.PrincipalTypeEmployee, pt)
			return nil
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.CreateAccount(context.Background(), &pb.CreateAccountRequest{
		PrincipalId: 11, Email: "n@test.com", FirstName: "Nina", PrincipalType: model.PrincipalTypeEmployee,
	})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

func TestHandler_CreateAccount_AlreadyExists(t *testing.T) {
	auth := &stubAuthService{
		createAccountAndActivationFn: func(context.Context, int64, string, string, string) error {
			return errors.New("account already exists")
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	_, err := h.CreateAccount(context.Background(), &pb.CreateAccountRequest{
		PrincipalId: 1, Email: "x@test.com", FirstName: "X", PrincipalType: model.PrincipalTypeEmployee,
	})
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

// ----------------------------------------------------------------------------
// RequestMobileActivation (always succeeds publicly)
// ----------------------------------------------------------------------------

func TestHandler_RequestMobileActivation_Success(t *testing.T) {
	mob := &stubMobileService{
		requestActivationFn: func(context.Context, string) error { return errors.New("no such user") },
	}
	h := newHandlerForTest(&stubAuthService{}, mob)

	resp, err := h.RequestMobileActivation(context.Background(), &pb.MobileActivationRequest{Email: "ghost@test.com"})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

// ----------------------------------------------------------------------------
// ActivateMobileDevice
// ----------------------------------------------------------------------------

func TestHandler_ActivateMobileDevice_AccountNotFound(t *testing.T) {
	mob := &stubMobileService{
		activateDeviceFn: func(context.Context, string, string, string) (string, string, string, string, error) {
			return "", "", "", "", errors.New("account not found")
		},
	}
	h := newHandlerForTest(&stubAuthService{}, mob)

	_, err := h.ActivateMobileDevice(context.Background(), &pb.ActivateMobileDeviceRequest{
		Email: "ghost@test.com", Code: "123456", DeviceName: "X",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandler_ActivateMobileDevice_Success(t *testing.T) {
	mob := &stubMobileService{
		activateDeviceFn: func(_ context.Context, email, code, name string) (string, string, string, string, error) {
			assert.Equal(t, "u@test.com", email)
			assert.Equal(t, "123456", code)
			assert.Equal(t, "iPhone", name)
			return "access", "refresh", "dev-1", "secret-x", nil
		},
	}
	h := newHandlerForTest(&stubAuthService{}, mob)

	resp, err := h.ActivateMobileDevice(context.Background(), &pb.ActivateMobileDeviceRequest{
		Email: "u@test.com", Code: "123456", DeviceName: "iPhone",
	})
	require.NoError(t, err)
	assert.Equal(t, "access", resp.AccessToken)
	assert.Equal(t, "refresh", resp.RefreshToken)
	assert.Equal(t, "dev-1", resp.DeviceId)
	assert.Equal(t, "secret-x", resp.DeviceSecret)
}

// ----------------------------------------------------------------------------
// RefreshMobileToken
// ----------------------------------------------------------------------------

func TestHandler_RefreshMobileToken_Success(t *testing.T) {
	auth := &stubAuthService{
		refreshTokenForMobileFn: func(_ context.Context, rt, dev string, _ service.MobileDeviceLookup) (string, string, error) {
			assert.Equal(t, "old-rt", rt)
			assert.Equal(t, "dev-1", dev)
			return "new-access", "new-refresh", nil
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.RefreshMobileToken(context.Background(), &pb.RefreshMobileTokenRequest{
		RefreshToken: "old-rt", DeviceId: "dev-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "new-access", resp.AccessToken)
	assert.Equal(t, "new-refresh", resp.RefreshToken)
}

func TestHandler_RefreshMobileToken_DeviceMismatch(t *testing.T) {
	auth := &stubAuthService{
		refreshTokenForMobileFn: func(context.Context, string, string, service.MobileDeviceLookup) (string, string, error) {
			return "", "", errors.New("device ID mismatch — permission denied")
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	_, err := h.RefreshMobileToken(context.Background(), &pb.RefreshMobileTokenRequest{
		RefreshToken: "rt", DeviceId: "wrong",
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// ----------------------------------------------------------------------------
// DeactivateDevice
// ----------------------------------------------------------------------------

func TestHandler_DeactivateDevice_DeviceNotFound(t *testing.T) {
	mob := &stubMobileService{
		deactivateDeviceFn: func(int64, string) error { return errors.New("device not found") },
	}
	h := newHandlerForTest(&stubAuthService{}, mob)

	_, err := h.DeactivateDevice(context.Background(), &pb.DeactivateDeviceRequest{
		UserId: 1, DeviceId: "missing",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandler_DeactivateDevice_Success(t *testing.T) {
	mob := &stubMobileService{
		deactivateDeviceFn: func(uid int64, did string) error {
			assert.Equal(t, int64(7), uid)
			assert.Equal(t, "dev", did)
			return nil
		},
	}
	h := newHandlerForTest(&stubAuthService{}, mob)

	resp, err := h.DeactivateDevice(context.Background(), &pb.DeactivateDeviceRequest{UserId: 7, DeviceId: "dev"})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

// ----------------------------------------------------------------------------
// TransferDevice
// ----------------------------------------------------------------------------

func TestHandler_TransferDevice_AccountNotFound(t *testing.T) {
	mob := &stubMobileService{
		transferDeviceFn: func(context.Context, int64, string) error { return errors.New("account not found") },
	}
	h := newHandlerForTest(&stubAuthService{}, mob)

	_, err := h.TransferDevice(context.Background(), &pb.TransferDeviceRequest{
		UserId: 9999, Email: "ghost@test.com",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandler_TransferDevice_Success(t *testing.T) {
	mob := &stubMobileService{
		transferDeviceFn: func(context.Context, int64, string) error { return nil },
	}
	h := newHandlerForTest(&stubAuthService{}, mob)

	resp, err := h.TransferDevice(context.Background(), &pb.TransferDeviceRequest{UserId: 1, Email: "u@test.com"})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

// ----------------------------------------------------------------------------
// ValidateDeviceSignature (always returns valid=false on error)
// ----------------------------------------------------------------------------

func TestHandler_ValidateDeviceSignature_DeviceNotFound(t *testing.T) {
	mob := &stubMobileService{
		validateDeviceSignatureFn: func(string, string, string, string, string, string) (bool, error) {
			return false, errors.New("device not found")
		},
	}
	h := newHandlerForTest(&stubAuthService{}, mob)

	resp, err := h.ValidateDeviceSignature(context.Background(), &pb.ValidateDeviceSignatureRequest{
		DeviceId: "ghost", Timestamp: "12345", Method: "GET", Path: "/", BodySha256: "abc", Signature: "def",
	})
	require.NoError(t, err)
	assert.False(t, resp.Valid)
}

func TestHandler_ValidateDeviceSignature_Valid(t *testing.T) {
	mob := &stubMobileService{
		validateDeviceSignatureFn: func(string, string, string, string, string, string) (bool, error) {
			return true, nil
		},
	}
	h := newHandlerForTest(&stubAuthService{}, mob)

	resp, err := h.ValidateDeviceSignature(context.Background(), &pb.ValidateDeviceSignatureRequest{DeviceId: "dev"})
	require.NoError(t, err)
	assert.True(t, resp.Valid)
}

// ----------------------------------------------------------------------------
// GetDeviceInfo
// ----------------------------------------------------------------------------

func TestHandler_GetDeviceInfo_NotFound(t *testing.T) {
	mob := &stubMobileService{
		getDeviceInfoFn: func(int64) (*model.MobileDevice, error) {
			return nil, errors.New("active device not found")
		},
	}
	h := newHandlerForTest(&stubAuthService{}, mob)

	_, err := h.GetDeviceInfo(context.Background(), &pb.GetDeviceInfoRequest{UserId: 9999})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandler_GetDeviceInfo_Found(t *testing.T) {
	now := time.Now()
	mob := &stubMobileService{
		getDeviceInfoFn: func(uid int64) (*model.MobileDevice, error) {
			assert.Equal(t, int64(99), uid)
			return &model.MobileDevice{
				UserID: 99, SystemType: "client", DeviceID: "dev-99",
				DeviceSecret: "deadbeef", DeviceName: "iPhone",
				Status: "active", ActivatedAt: &now, LastSeenAt: now,
			}, nil
		},
	}
	h := newHandlerForTest(&stubAuthService{}, mob)

	resp, err := h.GetDeviceInfo(context.Background(), &pb.GetDeviceInfoRequest{UserId: 99})
	require.NoError(t, err)
	assert.Equal(t, "dev-99", resp.DeviceId)
	assert.Equal(t, "iPhone", resp.DeviceName)
	assert.Equal(t, "active", resp.Status)
	assert.NotEmpty(t, resp.ActivatedAt)
}

// ----------------------------------------------------------------------------
// Biometrics
// ----------------------------------------------------------------------------

func TestHandler_SetBiometricsEnabled_DeviceNotFound(t *testing.T) {
	mob := &stubMobileService{
		setBiometricsEnabledFn: func(int64, string, bool) error { return errors.New("device not found") },
	}
	h := newHandlerForTest(&stubAuthService{}, mob)

	_, err := h.SetBiometricsEnabled(context.Background(), &pb.SetBiometricsRequest{
		UserId: 1, DeviceId: "missing", Enabled: true,
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandler_SetBiometricsEnabled_Success(t *testing.T) {
	mob := &stubMobileService{
		setBiometricsEnabledFn: func(uid int64, did string, en bool) error {
			assert.Equal(t, int64(1), uid)
			assert.Equal(t, "dev", did)
			assert.True(t, en)
			return nil
		},
	}
	h := newHandlerForTest(&stubAuthService{}, mob)

	resp, err := h.SetBiometricsEnabled(context.Background(), &pb.SetBiometricsRequest{
		UserId: 1, DeviceId: "dev", Enabled: true,
	})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

func TestHandler_GetBiometricsEnabled_DeviceNotFound(t *testing.T) {
	mob := &stubMobileService{
		getBiometricsEnabledFn: func(int64, string) (bool, error) {
			return false, errors.New("device not found")
		},
	}
	h := newHandlerForTest(&stubAuthService{}, mob)

	_, err := h.GetBiometricsEnabled(context.Background(), &pb.GetBiometricsRequest{UserId: 1, DeviceId: "missing"})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandler_GetBiometricsEnabled_True(t *testing.T) {
	mob := &stubMobileService{
		getBiometricsEnabledFn: func(int64, string) (bool, error) { return true, nil },
	}
	h := newHandlerForTest(&stubAuthService{}, mob)

	resp, err := h.GetBiometricsEnabled(context.Background(), &pb.GetBiometricsRequest{UserId: 1, DeviceId: "d"})
	require.NoError(t, err)
	assert.True(t, resp.Enabled)
}

func TestHandler_CheckBiometricsEnabled_DeviceNotFound(t *testing.T) {
	mob := &stubMobileService{
		checkBiometricsEnabledFn: func(string) (bool, error) { return false, errors.New("device not found") },
	}
	h := newHandlerForTest(&stubAuthService{}, mob)

	_, err := h.CheckBiometricsEnabled(context.Background(), &pb.CheckBiometricsRequest{DeviceId: "missing"})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// ----------------------------------------------------------------------------
// Sessions / login history
// ----------------------------------------------------------------------------

func TestHandler_ListSessions_Empty(t *testing.T) {
	auth := &stubAuthService{
		listSessionsFn: func(context.Context, int64) ([]model.ActiveSession, error) {
			return nil, nil
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.ListSessions(context.Background(), &pb.ListSessionsRequest{UserId: 9999})
	require.NoError(t, err)
	assert.Empty(t, resp.Sessions)
}

func TestHandler_ListSessions_HasOne(t *testing.T) {
	now := time.Now()
	auth := &stubAuthService{
		listSessionsFn: func(_ context.Context, uid int64) ([]model.ActiveSession, error) {
			assert.Equal(t, int64(1), uid)
			return []model.ActiveSession{
				{UserID: 1, UserRole: "client", IPAddress: "1.2.3.4", SystemType: "client", LastActiveAt: now, CreatedAt: now},
			}, nil
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.ListSessions(context.Background(), &pb.ListSessionsRequest{UserId: 1})
	require.NoError(t, err)
	require.Len(t, resp.Sessions, 1)
	assert.Equal(t, "1.2.3.4", resp.Sessions[0].IpAddress)
}

func TestHandler_ListSessions_Error(t *testing.T) {
	auth := &stubAuthService{
		listSessionsFn: func(context.Context, int64) ([]model.ActiveSession, error) {
			return nil, errors.New("db unreachable")
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	_, err := h.ListSessions(context.Background(), &pb.ListSessionsRequest{UserId: 1})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestHandler_RevokeSession_NotFound(t *testing.T) {
	auth := &stubAuthService{
		revokeSessionFn: func(context.Context, int64, int64) error { return errors.New("session not found") },
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	_, err := h.RevokeSession(context.Background(), &pb.RevokeSessionRequest{
		SessionId: 9999, CallerUserId: 1,
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandler_RevokeSession_Success(t *testing.T) {
	auth := &stubAuthService{
		revokeSessionFn: func(context.Context, int64, int64) error { return nil },
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.RevokeSession(context.Background(), &pb.RevokeSessionRequest{SessionId: 1, CallerUserId: 1})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

func TestHandler_RevokeAllSessions_TokenNotFound(t *testing.T) {
	auth := &stubAuthService{
		revokeAllSessionsExceptCurrentFn: func(context.Context, int64, string) error {
			return errors.New("current refresh token not found")
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	_, err := h.RevokeAllSessions(context.Background(), &pb.RevokeAllSessionsRequest{
		UserId: 1, CurrentRefreshToken: "missing",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandler_RevokeAllSessions_Success(t *testing.T) {
	auth := &stubAuthService{
		revokeAllSessionsExceptCurrentFn: func(context.Context, int64, string) error { return nil },
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.RevokeAllSessions(context.Background(), &pb.RevokeAllSessionsRequest{
		UserId: 1, CurrentRefreshToken: "tok",
	})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

func TestHandler_GetLoginHistory_Empty(t *testing.T) {
	auth := &stubAuthService{
		getLoginHistoryFn: func(context.Context, string, int) ([]service.LoginHistoryEntry, error) {
			return nil, nil
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.GetLoginHistory(context.Background(), &pb.LoginHistoryRequest{Email: "u@test.com", Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, resp.Entries)
}

func TestHandler_GetLoginHistory_NonEmpty(t *testing.T) {
	now := time.Now()
	auth := &stubAuthService{
		getLoginHistoryFn: func(_ context.Context, email string, limit int) ([]service.LoginHistoryEntry, error) {
			assert.Equal(t, "u@test.com", email)
			assert.Equal(t, 10, limit)
			return []service.LoginHistoryEntry{
				{ID: 1, Email: "u@test.com", IPAddress: "1.1.1.1", UserAgent: "ua", DeviceType: "browser", Success: true, CreatedAt: now},
			}, nil
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	resp, err := h.GetLoginHistory(context.Background(), &pb.LoginHistoryRequest{Email: "u@test.com", Limit: 10})
	require.NoError(t, err)
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, "u@test.com", resp.Entries[0].Email)
}

func TestHandler_GetLoginHistory_Error(t *testing.T) {
	auth := &stubAuthService{
		getLoginHistoryFn: func(context.Context, string, int) ([]service.LoginHistoryEntry, error) {
			return nil, errors.New("db down")
		},
	}
	h := newHandlerForTest(auth, &stubMobileService{})

	_, err := h.GetLoginHistory(context.Background(), &pb.LoginHistoryRequest{Email: "u@test.com", Limit: 10})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}
