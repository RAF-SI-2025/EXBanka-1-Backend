package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/repository"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// --- helpers ---

func setupMobileTestDB(t *testing.T) *gorm.DB {
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
		&model.MobileDevice{},
		&model.MobileActivationCode{},
		&model.RefreshToken{},
	))
	return db
}

func seedActiveAccount(t *testing.T, db *gorm.DB, email, principalType string, principalID int64) *model.Account {
	t.Helper()
	acct := &model.Account{
		Email:         email,
		PasswordHash:  "hash",
		Status:        model.AccountStatusActive,
		PrincipalType: principalType,
		PrincipalID:   principalID,
	}
	require.NoError(t, db.Create(acct).Error)
	return acct
}

func seedActiveDevice(t *testing.T, db *gorm.DB, userID int64, deviceID, secret string) *model.MobileDevice {
	t.Helper()
	now := time.Now()
	d := &model.MobileDevice{
		UserID:       userID,
		SystemType:   "client",
		DeviceID:     deviceID,
		DeviceSecret: secret,
		DeviceName:   "Test Phone",
		Status:       "active",
		ActivatedAt:  &now,
		LastSeenAt:   now,
	}
	require.NoError(t, db.Create(d).Error)
	return d
}

// ============================================================
// 1. Pure helper tests
// ============================================================

func TestGenerateActivationCode_Format(t *testing.T) {
	for i := 0; i < 20; i++ {
		code := generateActivationCode()
		assert.Len(t, code, 6, "activation code must be exactly 6 characters")
		_, err := strconv.Atoi(code)
		assert.NoError(t, err, "activation code must be numeric")
	}
}

func TestGenerateActivationCode_Uniqueness(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		code := generateActivationCode()
		seen[code] = true
	}
	// With 1M possible codes and 100 draws, collisions are astronomically unlikely.
	assert.Greater(t, len(seen), 90, "activation codes should be mostly unique across 100 draws")
}

func TestGenerateDeviceID_UUIDFormat(t *testing.T) {
	id := generateDeviceID()
	parts := strings.Split(id, "-")
	assert.Len(t, parts, 5, "device ID should be UUID-like with 5 dash-separated parts")
	assert.Equal(t, 36, len(id), "device ID should be 36 characters (UUID length)")
}

func TestGenerateDeviceSecret_Length(t *testing.T) {
	secret := generateDeviceSecret()
	assert.Len(t, secret, 64, "device secret should be 64 hex chars (32 bytes)")
	_, err := hex.DecodeString(secret)
	assert.NoError(t, err, "device secret should be valid hex")
}

func TestAbs64(t *testing.T) {
	assert.Equal(t, int64(5), abs64(5))
	assert.Equal(t, int64(5), abs64(-5))
	assert.Equal(t, int64(0), abs64(0))
}

// ============================================================
// 2. DeactivateDevice tests (DB-backed)
// ============================================================

func TestDeactivateDevice_Success(t *testing.T) {
	db := setupMobileTestDB(t)
	acct := seedActiveAccount(t, db, "user@test.com", "client", 100)

	deviceID := generateDeviceID()
	secret := generateDeviceSecret()
	seedActiveDevice(t, db, acct.PrincipalID, deviceID, secret)

	deviceRepo := repository.NewMobileDeviceRepository(db)
	activationRepo := repository.NewMobileActivationRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	jwtSvc := NewJWTService("test-secret-256bit", 15*time.Minute)

	svc := NewMobileDeviceService(
		deviceRepo, activationRepo, accountRepo, tokenRepo,
		jwtSvc, nil, 24*time.Hour, 10*time.Minute, "http://localhost:3000",
	)

	err := svc.DeactivateDevice(acct.PrincipalID, deviceID)
	assert.NoError(t, err)

	// Verify device is now deactivated
	var device model.MobileDevice
	require.NoError(t, db.Where("device_id = ?", deviceID).First(&device).Error)
	assert.Equal(t, "deactivated", device.Status)
	assert.NotNil(t, device.DeactivatedAt)
}

func TestDeactivateDevice_WrongUser(t *testing.T) {
	db := setupMobileTestDB(t)
	seedActiveAccount(t, db, "user@test.com", "client", 100)

	deviceID := generateDeviceID()
	seedActiveDevice(t, db, 100, deviceID, generateDeviceSecret())

	deviceRepo := repository.NewMobileDeviceRepository(db)
	activationRepo := repository.NewMobileActivationRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	jwtSvc := NewJWTService("test-secret-256bit", 15*time.Minute)

	svc := NewMobileDeviceService(
		deviceRepo, activationRepo, accountRepo, tokenRepo,
		jwtSvc, nil, 24*time.Hour, 10*time.Minute, "http://localhost:3000",
	)

	// Try to deactivate as a different user
	err := svc.DeactivateDevice(999, deviceID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not belong to user")
}

func TestDeactivateDevice_AlreadyDeactivated(t *testing.T) {
	db := setupMobileTestDB(t)
	seedActiveAccount(t, db, "user@test.com", "client", 100)

	deviceID := generateDeviceID()
	now := time.Now()
	d := &model.MobileDevice{
		UserID:        100,
		SystemType:    "client",
		DeviceID:      deviceID,
		DeviceSecret:  generateDeviceSecret(),
		DeviceName:    "Old Phone",
		Status:        "deactivated",
		DeactivatedAt: &now,
		LastSeenAt:    now,
	}
	require.NoError(t, db.Create(d).Error)

	deviceRepo := repository.NewMobileDeviceRepository(db)
	activationRepo := repository.NewMobileActivationRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	jwtSvc := NewJWTService("test-secret-256bit", 15*time.Minute)

	svc := NewMobileDeviceService(
		deviceRepo, activationRepo, accountRepo, tokenRepo,
		jwtSvc, nil, 24*time.Hour, 10*time.Minute, "http://localhost:3000",
	)

	err := svc.DeactivateDevice(100, deviceID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already deactivated")
}

func TestDeactivateDevice_NotFound(t *testing.T) {
	db := setupMobileTestDB(t)

	deviceRepo := repository.NewMobileDeviceRepository(db)
	activationRepo := repository.NewMobileActivationRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	jwtSvc := NewJWTService("test-secret-256bit", 15*time.Minute)

	svc := NewMobileDeviceService(
		deviceRepo, activationRepo, accountRepo, tokenRepo,
		jwtSvc, nil, 24*time.Hour, 10*time.Minute, "http://localhost:3000",
	)

	err := svc.DeactivateDevice(100, "nonexistent-device-id")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "device not found")
}

// ============================================================
// 3. GetDeviceInfo tests (DB-backed)
// ============================================================

func TestGetDeviceInfo_ActiveDevice(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceID := generateDeviceID()
	seedActiveDevice(t, db, 42, deviceID, generateDeviceSecret())

	deviceRepo := repository.NewMobileDeviceRepository(db)
	activationRepo := repository.NewMobileActivationRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	jwtSvc := NewJWTService("test-secret-256bit", 15*time.Minute)

	svc := NewMobileDeviceService(
		deviceRepo, activationRepo, accountRepo, tokenRepo,
		jwtSvc, nil, 24*time.Hour, 10*time.Minute, "http://localhost:3000",
	)

	device, err := svc.GetDeviceInfo(42)
	assert.NoError(t, err)
	assert.Equal(t, deviceID, device.DeviceID)
	assert.Equal(t, "active", device.Status)
}

func TestGetDeviceInfo_NoActiveDevice(t *testing.T) {
	db := setupMobileTestDB(t)

	deviceRepo := repository.NewMobileDeviceRepository(db)
	activationRepo := repository.NewMobileActivationRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	jwtSvc := NewJWTService("test-secret-256bit", 15*time.Minute)

	svc := NewMobileDeviceService(
		deviceRepo, activationRepo, accountRepo, tokenRepo,
		jwtSvc, nil, 24*time.Hour, 10*time.Minute, "http://localhost:3000",
	)

	_, err := svc.GetDeviceInfo(999)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no active device found")
}

// ============================================================
// 4. ValidateDeviceSignature tests (DB-backed)
// ============================================================

func TestValidateDeviceSignature_Valid(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceID := generateDeviceID()
	secret := generateDeviceSecret()
	seedActiveDevice(t, db, 42, deviceID, secret)

	deviceRepo := repository.NewMobileDeviceRepository(db)
	activationRepo := repository.NewMobileActivationRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	jwtSvc := NewJWTService("test-secret-256bit", 15*time.Minute)

	svc := NewMobileDeviceService(
		deviceRepo, activationRepo, accountRepo, tokenRepo,
		jwtSvc, nil, 24*time.Hour, 10*time.Minute, "http://localhost:3000",
	)

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	method := "GET"
	path := "/api/me/accounts"
	bodySHA := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" // SHA256 of empty body

	payload := ts + ":" + method + ":" + path + ":" + bodySHA
	secretBytes, err := hex.DecodeString(secret)
	require.NoError(t, err)
	mac := hmac.New(sha256.New, secretBytes)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))

	valid, err := svc.ValidateDeviceSignature(deviceID, ts, method, path, bodySHA, sig)
	assert.NoError(t, err)
	assert.True(t, valid)
}

func TestValidateDeviceSignature_WrongSignature(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceID := generateDeviceID()
	secret := generateDeviceSecret()
	seedActiveDevice(t, db, 42, deviceID, secret)

	deviceRepo := repository.NewMobileDeviceRepository(db)
	activationRepo := repository.NewMobileActivationRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	jwtSvc := NewJWTService("test-secret-256bit", 15*time.Minute)

	svc := NewMobileDeviceService(
		deviceRepo, activationRepo, accountRepo, tokenRepo,
		jwtSvc, nil, 24*time.Hour, 10*time.Minute, "http://localhost:3000",
	)

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	// Use a completely wrong signature
	wrongSig := hex.EncodeToString(make([]byte, 32))

	_, err := svc.ValidateDeviceSignature(deviceID, ts, "GET", "/api/me", "abc123", wrongSig)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "signature mismatch")
}

func TestValidateDeviceSignature_ExpiredTimestamp(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceID := generateDeviceID()
	secret := generateDeviceSecret()
	seedActiveDevice(t, db, 42, deviceID, secret)

	deviceRepo := repository.NewMobileDeviceRepository(db)
	activationRepo := repository.NewMobileActivationRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	jwtSvc := NewJWTService("test-secret-256bit", 15*time.Minute)

	svc := NewMobileDeviceService(
		deviceRepo, activationRepo, accountRepo, tokenRepo,
		jwtSvc, nil, 24*time.Hour, 10*time.Minute, "http://localhost:3000",
	)

	// Timestamp 60 seconds ago (outside the 30-second window)
	ts := strconv.FormatInt(time.Now().Unix()-60, 10)
	sig := hex.EncodeToString(make([]byte, 32))

	_, err := svc.ValidateDeviceSignature(deviceID, ts, "GET", "/api/me", "abc", sig)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timestamp too old")
}

func TestValidateDeviceSignature_DeviceNotFound(t *testing.T) {
	db := setupMobileTestDB(t)

	deviceRepo := repository.NewMobileDeviceRepository(db)
	activationRepo := repository.NewMobileActivationRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	jwtSvc := NewJWTService("test-secret-256bit", 15*time.Minute)

	svc := NewMobileDeviceService(
		deviceRepo, activationRepo, accountRepo, tokenRepo,
		jwtSvc, nil, 24*time.Hour, 10*time.Minute, "http://localhost:3000",
	)

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	_, err := svc.ValidateDeviceSignature("nonexistent", ts, "GET", "/", "abc", "def")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "device not found")
}

func TestValidateDeviceSignature_InactiveDevice(t *testing.T) {
	db := setupMobileTestDB(t)

	now := time.Now()
	d := &model.MobileDevice{
		UserID:        42,
		SystemType:    "client",
		DeviceID:      "inactive-device",
		DeviceSecret:  generateDeviceSecret(),
		DeviceName:    "Old Phone",
		Status:        "deactivated",
		DeactivatedAt: &now,
		LastSeenAt:    now,
	}
	require.NoError(t, db.Create(d).Error)

	deviceRepo := repository.NewMobileDeviceRepository(db)
	activationRepo := repository.NewMobileActivationRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	jwtSvc := NewJWTService("test-secret-256bit", 15*time.Minute)

	svc := NewMobileDeviceService(
		deviceRepo, activationRepo, accountRepo, tokenRepo,
		jwtSvc, nil, 24*time.Hour, 10*time.Minute, "http://localhost:3000",
	)

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	_, err := svc.ValidateDeviceSignature("inactive-device", ts, "GET", "/", "abc", "def")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "device is not active")
}
