package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/repository"
)

// ----------------------------------------------------------------------------
// SigningKeys / Keys exposure (TokenService.SigningKeys + JWTService.Keys)
// ----------------------------------------------------------------------------

func TestTokenService_SigningKeys_ReturnsPublicJWKS(t *testing.T) {
	f := newAuthFlowFixture(t)

	keys, err := f.svc.SigningKeys()
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, "ES256", keys[0].Alg)
	assert.True(t, keys[0].Primary)
	assert.NotEmpty(t, keys[0].Kid)
	// The exposed PEM must parse back to a P-256 public key.
	block, _ := pem.Decode([]byte(keys[0].PEM))
	require.NotNil(t, block)
	_, err = x509.ParsePKIXPublicKey(block.Bytes)
	require.NoError(t, err)

	// JWTService.Keys() exposes the underlying KeyManager whose Current key kid
	// matches the published JWKS kid.
	km := f.jwtSvc.Keys()
	require.NotNil(t, km)
	assert.Equal(t, km.Current().Kid, keys[0].Kid)
}

// ----------------------------------------------------------------------------
// LoadSigningKeyFromPEM — remaining branches (SEC1, rejections, auto-kid)
// ----------------------------------------------------------------------------

func TestLoadSigningKeyFromPEM_SEC1_AutoKid(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalECPrivateKey(priv) // SEC1 "EC PRIVATE KEY"
	require.NoError(t, err)
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))

	// Empty kid → a random one is assigned.
	key, err := LoadSigningKeyFromPEM("", pemStr)
	require.NoError(t, err)
	assert.NotEmpty(t, key.Kid, "an empty kid must be auto-assigned")
	assert.Equal(t, priv.D, key.Private.D)
}

func TestLoadSigningKeyFromPEM_RejectsNonP256_SEC1(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalECPrivateKey(priv)
	require.NoError(t, err)
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))

	_, err = LoadSigningKeyFromPEM("k", pemStr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "P-256")
}

func TestLoadSigningKeyFromPEM_RejectsNonECDSA_PKCS8(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	require.NoError(t, err)
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	_, err = LoadSigningKeyFromPEM("k", pemStr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an ECDSA key")
}

func TestLoadSigningKeyFromPEM_RejectsBadSEC1Bytes(t *testing.T) {
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte("garbage")}))
	_, err := LoadSigningKeyFromPEM("k", pemStr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse EC private key")
}

func TestLoadSigningKeyFromPEM_RejectsBadPKCS8Bytes(t *testing.T) {
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("garbage")}))
	_, err := LoadSigningKeyFromPEM("k", pemStr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse PKCS#8 private key")
}

// ----------------------------------------------------------------------------
// ActivateDevice — code-state branches (used / max-attempts / last-attempt)
// ----------------------------------------------------------------------------

func TestActivateDevice_MaxAttemptsCap(t *testing.T) {
	db := setupMobileTestDB(t)
	seedActiveAccount(t, db, "u@test.com", "client", 100)
	svc, _ := newMobileSvcWithStubs(t, db)

	require.NoError(t, db.Create(&model.MobileActivationCode{
		Email: "u@test.com", Code: "123456", Attempts: 3,
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}).Error)

	_, _, _, _, err := svc.ActivateDevice(context.Background(), "u@test.com", "123456", "iPhone")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrActivationCodeMaxAttempts)
}

func TestActivateDevice_WrongCode_LastAttemptExhausts(t *testing.T) {
	db := setupMobileTestDB(t)
	seedActiveAccount(t, db, "u@test.com", "client", 100)
	svc, _ := newMobileSvcWithStubs(t, db)

	// Attempts=2: after the in-tx increment, remaining (2-2) <= 0, so a wrong
	// code collapses to the max-attempts sentinel rather than "invalid code".
	require.NoError(t, db.Create(&model.MobileActivationCode{
		Email: "u@test.com", Code: "123456", Attempts: 2,
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}).Error)

	_, _, _, _, err := svc.ActivateDevice(context.Background(), "u@test.com", "999999", "iPhone")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrActivationCodeMaxAttempts)
}

// ----------------------------------------------------------------------------
// ValidateDeviceSignature — parse/decode error branches
// ----------------------------------------------------------------------------

func TestValidateDeviceSignature_NonNumericTimestamp(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceID := generateDeviceID()
	seedActiveDevice(t, db, 1, deviceID, generateDeviceSecret())
	svc, _ := newMobileSvcWithStubs(t, db)

	_, err := svc.ValidateDeviceSignature(deviceID, "not-a-number", "GET", "/", "abc", "def")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSignature)
}

func TestValidateDeviceSignature_NonHexDeviceSecret(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceID := generateDeviceID()
	// Device secret is not valid hex → hex.DecodeString fails inside the validator.
	seedActiveDevice(t, db, 1, deviceID, "not-hex-secret-zzzz")
	svc, _ := newMobileSvcWithStubs(t, db)

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	_, err := svc.ValidateDeviceSignature(deviceID, ts, "GET", "/", "abc", "def0")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSignature)
}

func TestValidateDeviceSignature_NonHexSignature(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceID := generateDeviceID()
	seedActiveDevice(t, db, 1, deviceID, generateDeviceSecret())
	svc, _ := newMobileSvcWithStubs(t, db)

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	// Valid device secret (hex) but the presented signature is not hex.
	_, err := svc.ValidateDeviceSignature(deviceID, ts, "GET", "/", "abc", "zz-not-hex")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSignature)
}

// TestValidateDeviceSignature_HMACMismatch covers the negative HMAC compare with
// a well-formed (hex) but wrong signature.
func TestValidateDeviceSignature_HMACMismatch_HexSig(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceID := generateDeviceID()
	seedActiveDevice(t, db, 1, deviceID, generateDeviceSecret())
	svc, _ := newMobileSvcWithStubs(t, db)

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	wrong := hex.EncodeToString(sha256.New().Sum(nil)) // 32-byte hex, wrong value
	_, err := svc.ValidateDeviceSignature(deviceID, ts, "GET", "/", "abc", wrong)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSignature)
}

// ----------------------------------------------------------------------------
// GetBiometricsEnabled — inactive device branch
// ----------------------------------------------------------------------------

func TestGetBiometricsEnabled_InactiveDevice(t *testing.T) {
	db := setupMobileTestDB(t)
	now := time.Now()
	require.NoError(t, db.Create(&model.MobileDevice{
		UserID: 100, SystemType: "client", DeviceID: "inact-bio",
		DeviceSecret: generateDeviceSecret(), DeviceName: "X",
		Status: "deactivated", DeactivatedAt: &now, LastSeenAt: now,
	}).Error)
	svc, _ := newMobileSvcWithStubs(t, db)

	_, err := svc.GetBiometricsEnabled(100, "inact-bio")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDeviceInactive)
}

// ----------------------------------------------------------------------------
// Login — brute-force lockout (wrong-password path) + employee role fallback
// ----------------------------------------------------------------------------

func TestAuthLogin_EmployeeRoleFallback_FromLegacyRoleField(t *testing.T) {
	f := newAuthFlowFixture(t)
	f.seedActiveAccountWithPassword(t, "legacy@test.com", "Abcdef12", model.PrincipalTypeEmployee, 1)
	// Employee response has no Roles slice but a legacy singular Role — Login must
	// fall back to [Role].
	f.userClient.resp.Roles = nil
	f.userClient.resp.Role = "EmployeeSupervisor"
	f.userClient.resp.Permissions = nil

	access, _, err := f.svc.Login(context.Background(), "legacy@test.com", "Abcdef12", "1.2.3.4", "Mozilla/5.0")
	require.NoError(t, err)
	claims, err := f.jwtSvc.ValidateToken(access)
	require.NoError(t, err)
	assert.Equal(t, []string{"EmployeeSupervisor"}, claims.Roles)
}

// TestAuthLogin_ActiveLockLookupError_FailsClosed covers the fail-closed branch:
// when the lock lookup itself errors (here, by dropping the lock table), Login
// must reject with ErrAccountLocked rather than letting the request through.
func TestAuthLogin_ActiveLockLookupError_FailsClosed(t *testing.T) {
	f := newAuthFlowFixture(t)
	f.seedActiveAccountWithPassword(t, "emp@test.com", "Abcdef12", model.PrincipalTypeEmployee, 1)
	require.NoError(t, f.db.Migrator().DropTable(&model.AccountLock{}))

	_, _, err := f.svc.Login(context.Background(), "emp@test.com", "Abcdef12", "1.2.3.4", "Mozilla/5.0")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountLocked)
}

// ----------------------------------------------------------------------------
// SetAccountStatus — disable revoke-failure propagation (error injection)
// ----------------------------------------------------------------------------

func TestSetAccountStatus_DisableRevokeError(t *testing.T) {
	f := newAuthFlowFixture(t)
	f.seedActiveAccountWithPassword(t, "emp@test.com", "Abcdef12", model.PrincipalTypeEmployee, 1)

	// Dropping the refresh_tokens table makes RevokeAllForAccount fail; the
	// disable path must surface that error (it never silently leaves tokens live).
	require.NoError(t, f.db.Migrator().DropTable(&model.RefreshToken{}))

	err := f.svc.SetAccountStatus(context.Background(), model.PrincipalTypeEmployee, 1, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "revoke")
}

func TestSetAccountStatus_DisableUnknownPrincipal(t *testing.T) {
	f := newAuthFlowFixture(t)
	err := f.svc.SetAccountStatus(context.Background(), model.PrincipalTypeEmployee, 4242, false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

// ----------------------------------------------------------------------------
// RevokeAllSessions — DB error propagation (error injection)
// ----------------------------------------------------------------------------

func TestRevokeAllSessions_TokenRevokeError(t *testing.T) {
	f := newAuthFlowFixture(t)
	require.NoError(t, f.db.Migrator().DropTable(&model.RefreshToken{}))

	err := f.svc.RevokeAllSessions(context.Background(), model.PrincipalTypeEmployee, 1, 7, "test")
	require.Error(t, err)
}

func TestRevokeAllSessions_SessionRevokeError(t *testing.T) {
	f := newAuthFlowFixture(t)
	// Keep refresh_tokens (so RevokeAllForAccount succeeds) but drop sessions so
	// RevokeAllForUser fails.
	require.NoError(t, f.db.Migrator().DropTable(&model.ActiveSession{}))

	err := f.svc.RevokeAllSessions(context.Background(), model.PrincipalTypeEmployee, 1, 7, "test")
	require.Error(t, err)
}

// ----------------------------------------------------------------------------
// RevokeAllSessionsExceptCurrent — token carrying no session id (else branch)
// ----------------------------------------------------------------------------

func TestRevokeAllSessionsExceptCurrent_TokenWithoutSession(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "u@test.com", "Abcdef12", model.PrincipalTypeEmployee, 60)

	// Two sessions for the user.
	for i := 0; i < 2; i++ {
		require.NoError(t, f.db.Create(&model.ActiveSession{
			UserID: 60, UserRole: "EmployeeAdmin", SystemType: model.PrincipalTypeEmployee,
			LastActiveAt: time.Now(), CreatedAt: time.Now(),
		}).Error)
	}
	// A refresh token with NO SessionID → keepSessionID stays 0 → the bulk
	// RevokeAllForUser branch runs (no session is preserved).
	require.NoError(t, f.db.Create(&model.RefreshToken{
		AccountID: acct.ID, Token: "sessionless-rt",
		ExpiresAt: time.Now().Add(time.Hour), SystemType: model.PrincipalTypeEmployee,
	}).Error)

	require.NoError(t, f.svc.RevokeAllSessionsExceptCurrent(context.Background(), 60, "sessionless-rt"))

	var live int64
	require.NoError(t, f.db.Model(&model.ActiveSession{}).
		Where("user_id = ? AND revoked_at IS NULL", 60).Count(&live).Error)
	assert.Equal(t, int64(0), live, "with no current session, every session is revoked")
}

// ----------------------------------------------------------------------------
// ResetPassword — best-effort warn branches + account-lookup fallback.
// These exercise AccountService directly with failing collaborators.
// ----------------------------------------------------------------------------

type failingSessionRevoker struct{}

func (failingSessionRevoker) RevokeAllSessions(_ context.Context, _ string, _, _ int64, _ string) error {
	return errors.New("revoke-all boom")
}

type failingUnlocker struct{}

func (failingUnlocker) UnlockAccount(_ string) error { return errors.New("unlock boom") }

func newAccountServiceForResetTest(t *testing.T, db *gorm.DB, sessions SessionRevoker, unlocker accountUnlocker) (*AccountService, *fakeProducer) {
	t.Helper()
	accountRepo := repository.NewAccountRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	jwtSvc := NewJWTService(mustTestKeyManager(), 15*time.Minute)
	producer := &fakeProducer{}
	svc := NewAccountService(
		accountRepo, tokenRepo, nil, producer, nil, jwtSvc,
		sessions, unlocker, "http://localhost:3000", "test-pepper",
	)
	return svc, producer
}

func TestResetPassword_UnlockAndRevokeErrors_StillSucceeds(t *testing.T) {
	db := newAuthFlowDB(t)
	svc, producer := newAccountServiceForResetTest(t, db, failingSessionRevoker{}, failingUnlocker{})

	acct := &model.Account{
		Email: "u@test.com", PasswordHash: "old",
		Status: model.AccountStatusActive, PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 9,
	}
	require.NoError(t, db.Create(acct).Error)
	require.NoError(t, db.Create(&model.PasswordResetToken{
		AccountID: acct.ID, Token: "reset-warn", ExpiresAt: time.Now().Add(time.Hour),
	}).Error)

	// Both the unlock and the session revoke fail, but the reset itself succeeds
	// (best-effort warn semantics) and still rotates the password + emits a
	// password_changed notification.
	require.NoError(t, svc.ResetPassword(context.Background(), "reset-warn", "NewPass12", "NewPass12"))

	var got model.PasswordResetToken
	require.NoError(t, db.Where("token = ?", "reset-warn").First(&got).Error)
	assert.True(t, got.Used)
	assert.GreaterOrEqual(t, producer.eventCount(), 1, "a password_changed notification is published")
}

func TestResetPassword_AccountLookupFails_FallbackRevoke(t *testing.T) {
	db := newAuthFlowDB(t)
	svc, _ := newAccountServiceForResetTest(t, db, failingSessionRevoker{}, failingUnlocker{})

	// Reset token references an account id that does not exist → GetByID fails →
	// the fallback path (RevokeAllForAccount) runs instead of session revocation.
	require.NoError(t, db.Create(&model.PasswordResetToken{
		AccountID: 999999, Token: "orphan-reset", ExpiresAt: time.Now().Add(time.Hour),
	}).Error)

	require.NoError(t, svc.ResetPassword(context.Background(), "orphan-reset", "NewPass12", "NewPass12"))

	var got model.PasswordResetToken
	require.NoError(t, db.Where("token = ?", "orphan-reset").First(&got).Error)
	assert.True(t, got.Used)
}

// ----------------------------------------------------------------------------
// blacklistSession / hardRevokeUser nil-guard fast paths (package helpers)
// ----------------------------------------------------------------------------

func TestHardRevokeUser_NilCacheAndGuards(t *testing.T) {
	// nil cache, zero user id, and empty principal type are all no-ops.
	assert.NotPanics(t, func() {
		hardRevokeUser(context.Background(), nil, time.Minute, "employee", 1)
		hardRevokeUser(context.Background(), nil, time.Minute, "employee", 0)
		hardRevokeUser(context.Background(), nil, time.Minute, "", 1)
	})
}

func TestBlacklistSession_ZeroSessionID_IsNoOp(t *testing.T) {
	assert.NotPanics(t, func() {
		blacklistSession(context.Background(), nil, time.Minute, 0)
	})
}
