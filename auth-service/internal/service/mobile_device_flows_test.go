package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/repository"
)

// newMobileSvcWithStubs builds a MobileDeviceService backed by SQLite + a
// fake producer. Returns the service, db, and producer for assertion.
func newMobileSvcWithStubs(t *testing.T, db *gorm.DB) (*MobileDeviceService, *fakeProducer) {
	t.Helper()
	deviceRepo := repository.NewMobileDeviceRepository(db)
	activationRepo := repository.NewMobileActivationRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	jwtSvc := NewJWTService("test-secret-256bit", 15*time.Minute)
	producer := &fakeProducer{}
	svc := newMobileDeviceServiceForTest(
		deviceRepo, activationRepo, accountRepo, tokenRepo,
		jwtSvc, producer, 24*time.Hour, 10*time.Minute, "http://localhost:3000",
	)
	return svc, producer
}

// ----------------------------------------------------------------------------
// RequestActivation tests
// ----------------------------------------------------------------------------

func TestRequestActivation_Success(t *testing.T) {
	db := setupMobileTestDB(t)
	seedActiveAccount(t, db, "user@test.com", "client", 100)
	svc, prod := newMobileSvcWithStubs(t, db)

	require.NoError(t, svc.RequestActivation(context.Background(), "user@test.com"))

	// Activation code persisted
	var count int64
	require.NoError(t, db.Model(&model.MobileActivationCode{}).Where("email = ?", "user@test.com").Count(&count).Error)
	assert.Equal(t, int64(1), count)

	// Email send was attempted
	assert.Equal(t, 1, prod.emailCount(), "should publish a SendEmail message")
}

func TestRequestActivation_AccountNotFound(t *testing.T) {
	db := setupMobileTestDB(t)
	svc, _ := newMobileSvcWithStubs(t, db)

	err := svc.RequestActivation(context.Background(), "ghost@test.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account not found")
}

func TestRequestActivation_AccountNotActive(t *testing.T) {
	db := setupMobileTestDB(t)
	acct := &model.Account{
		Email: "pend@test.com", PasswordHash: "h",
		Status: "pending", PrincipalType: "client", PrincipalID: 1,
	}
	require.NoError(t, db.Create(acct).Error)
	svc, _ := newMobileSvcWithStubs(t, db)

	err := svc.RequestActivation(context.Background(), "pend@test.com")
	require.Error(t, err)
	// Pending accounts collapse to ErrAccountDisabled (status != "active").
	assert.ErrorIs(t, err, ErrAccountDisabled)
}

// ----------------------------------------------------------------------------
// ActivateDevice tests
// ----------------------------------------------------------------------------

func TestActivateDevice_Success(t *testing.T) {
	db := setupMobileTestDB(t)
	acct := seedActiveAccount(t, db, "user@test.com", "client", 100)
	svc, prod := newMobileSvcWithStubs(t, db)

	// Pre-seed activation code
	code := &model.MobileActivationCode{
		Email: "user@test.com", Code: "123456",
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}
	require.NoError(t, db.Create(code).Error)

	access, refresh, deviceID, deviceSecret, err := svc.ActivateDevice(context.Background(), "user@test.com", "123456", "iPhone-15")
	require.NoError(t, err)
	assert.NotEmpty(t, access)
	assert.NotEmpty(t, refresh)
	assert.NotEmpty(t, deviceID)
	assert.NotEmpty(t, deviceSecret)

	// New active device
	var device model.MobileDevice
	require.NoError(t, db.Where("user_id = ? AND status = ?", acct.PrincipalID, "active").First(&device).Error)
	assert.Equal(t, deviceID, device.DeviceID)
	assert.Equal(t, "iPhone-15", device.DeviceName)

	// Code is now used
	var got model.MobileActivationCode
	require.NoError(t, db.First(&got, code.ID).Error)
	assert.True(t, got.Used)

	// Event published (TopicAuthMobileDeviceActivated)
	assert.Equal(t, 1, prod.eventCount())
}

func TestActivateDevice_WrongCode(t *testing.T) {
	db := setupMobileTestDB(t)
	seedActiveAccount(t, db, "user@test.com", "client", 100)
	svc, _ := newMobileSvcWithStubs(t, db)

	require.NoError(t, db.Create(&model.MobileActivationCode{
		Email: "user@test.com", Code: "123456",
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}).Error)

	_, _, _, _, err := svc.ActivateDevice(context.Background(), "user@test.com", "999999", "iPhone")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid activation code")
}

func TestActivateDevice_ExpiredCode(t *testing.T) {
	db := setupMobileTestDB(t)
	seedActiveAccount(t, db, "user@test.com", "client", 100)
	svc, _ := newMobileSvcWithStubs(t, db)

	require.NoError(t, db.Create(&model.MobileActivationCode{
		Email: "user@test.com", Code: "123456",
		ExpiresAt: time.Now().Add(-time.Minute),
	}).Error)

	_, _, _, _, err := svc.ActivateDevice(context.Background(), "user@test.com", "123456", "iPhone")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestActivateDevice_AccountNotFound(t *testing.T) {
	db := setupMobileTestDB(t)
	svc, _ := newMobileSvcWithStubs(t, db)
	_, _, _, _, err := svc.ActivateDevice(context.Background(), "ghost@test.com", "123456", "iPhone")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account not found")
}

func TestActivateDevice_NoCode(t *testing.T) {
	db := setupMobileTestDB(t)
	seedActiveAccount(t, db, "user@test.com", "client", 100)
	svc, _ := newMobileSvcWithStubs(t, db)

	_, _, _, _, err := svc.ActivateDevice(context.Background(), "user@test.com", "123456", "iPhone")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "activation code not found")
}

// ----------------------------------------------------------------------------
// Biometrics tests
// ----------------------------------------------------------------------------

func TestSetBiometricsEnabled_Success(t *testing.T) {
	db := setupMobileTestDB(t)
	acct := seedActiveAccount(t, db, "u@test.com", "client", 100)
	deviceID := generateDeviceID()
	seedActiveDevice(t, db, acct.PrincipalID, deviceID, generateDeviceSecret())
	svc, _ := newMobileSvcWithStubs(t, db)

	require.NoError(t, svc.SetBiometricsEnabled(acct.PrincipalID, deviceID, true))

	var d model.MobileDevice
	require.NoError(t, db.Where("device_id = ?", deviceID).First(&d).Error)
	assert.True(t, d.BiometricsEnabled)
}

func TestSetBiometricsEnabled_DeviceNotFound(t *testing.T) {
	db := setupMobileTestDB(t)
	svc, _ := newMobileSvcWithStubs(t, db)
	err := svc.SetBiometricsEnabled(1, "missing-device", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "device not found")
}

func TestSetBiometricsEnabled_WrongUser(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceID := generateDeviceID()
	seedActiveDevice(t, db, 100, deviceID, generateDeviceSecret())
	svc, _ := newMobileSvcWithStubs(t, db)
	err := svc.SetBiometricsEnabled(999, deviceID, true)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDeviceMismatch)
}

func TestSetBiometricsEnabled_InactiveDevice(t *testing.T) {
	db := setupMobileTestDB(t)
	now := time.Now()
	d := &model.MobileDevice{
		UserID: 100, SystemType: "client", DeviceID: "inact-1",
		DeviceSecret: generateDeviceSecret(), DeviceName: "X",
		Status: "deactivated", DeactivatedAt: &now, LastSeenAt: now,
	}
	require.NoError(t, db.Create(d).Error)
	svc, _ := newMobileSvcWithStubs(t, db)
	err := svc.SetBiometricsEnabled(100, "inact-1", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "device is not active")
}

func TestGetBiometricsEnabled_Success(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceID := generateDeviceID()
	d := &model.MobileDevice{
		UserID: 100, SystemType: "client", DeviceID: deviceID,
		DeviceSecret: generateDeviceSecret(), DeviceName: "X",
		Status: "active", LastSeenAt: time.Now(), BiometricsEnabled: true,
	}
	require.NoError(t, db.Create(d).Error)
	svc, _ := newMobileSvcWithStubs(t, db)

	enabled, err := svc.GetBiometricsEnabled(100, deviceID)
	require.NoError(t, err)
	assert.True(t, enabled)
}

func TestGetBiometricsEnabled_DeviceNotFound(t *testing.T) {
	db := setupMobileTestDB(t)
	svc, _ := newMobileSvcWithStubs(t, db)
	_, err := svc.GetBiometricsEnabled(1, "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "device not found")
}

func TestGetBiometricsEnabled_WrongUser(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceID := generateDeviceID()
	seedActiveDevice(t, db, 100, deviceID, generateDeviceSecret())
	svc, _ := newMobileSvcWithStubs(t, db)
	_, err := svc.GetBiometricsEnabled(999, deviceID)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDeviceMismatch)
}

func TestCheckBiometricsEnabled_Success(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceID := generateDeviceID()
	d := &model.MobileDevice{
		UserID: 100, SystemType: "client", DeviceID: deviceID,
		DeviceSecret: generateDeviceSecret(), DeviceName: "X",
		Status: "active", LastSeenAt: time.Now(), BiometricsEnabled: true,
	}
	require.NoError(t, db.Create(d).Error)
	svc, _ := newMobileSvcWithStubs(t, db)

	enabled, err := svc.CheckBiometricsEnabled(deviceID)
	require.NoError(t, err)
	assert.True(t, enabled)
}

func TestCheckBiometricsEnabled_DeviceNotFound(t *testing.T) {
	db := setupMobileTestDB(t)
	svc, _ := newMobileSvcWithStubs(t, db)
	_, err := svc.CheckBiometricsEnabled("nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "device not found")
}

func TestCheckBiometricsEnabled_InactiveDevice(t *testing.T) {
	db := setupMobileTestDB(t)
	now := time.Now()
	require.NoError(t, db.Create(&model.MobileDevice{
		UserID: 100, SystemType: "client", DeviceID: "inact-2",
		DeviceSecret: generateDeviceSecret(), DeviceName: "X",
		Status: "deactivated", DeactivatedAt: &now, LastSeenAt: now,
	}).Error)
	svc, _ := newMobileSvcWithStubs(t, db)

	_, err := svc.CheckBiometricsEnabled("inact-2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "device is not active")
}

// ----------------------------------------------------------------------------
// TransferDevice tests
// ----------------------------------------------------------------------------

func TestTransferDevice_Success(t *testing.T) {
	db := setupMobileTestDB(t)
	acct := seedActiveAccount(t, db, "u@test.com", "client", 100)
	deviceID := generateDeviceID()
	seedActiveDevice(t, db, acct.PrincipalID, deviceID, generateDeviceSecret())

	svc, _ := newMobileSvcWithStubs(t, db)

	require.NoError(t, svc.TransferDevice(context.Background(), acct.PrincipalID, "u@test.com"))

	// Old device deactivated
	var d model.MobileDevice
	require.NoError(t, db.Where("device_id = ?", deviceID).First(&d).Error)
	assert.Equal(t, "deactivated", d.Status)

	// New activation code created
	var count int64
	require.NoError(t, db.Model(&model.MobileActivationCode{}).Where("email = ?", "u@test.com").Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

// ----------------------------------------------------------------------------
// UpdateLastSeen
// ----------------------------------------------------------------------------

func TestUpdateLastSeen_DoesNotPanicOnUnknown(t *testing.T) {
	db := setupMobileTestDB(t)
	svc, _ := newMobileSvcWithStubs(t, db)
	// Should be a no-op (errors swallowed) for unknown device.
	svc.UpdateLastSeen("missing-device")
}

func TestUpdateLastSeen_ActiveDevice(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceID := generateDeviceID()
	d := seedActiveDevice(t, db, 1, deviceID, generateDeviceSecret())
	earlier := time.Now().Add(-time.Hour)
	require.NoError(t, db.Model(&model.MobileDevice{}).Where("device_id = ?", deviceID).Update("last_seen_at", earlier).Error)

	svc, _ := newMobileSvcWithStubs(t, db)
	svc.UpdateLastSeen(deviceID)

	var got model.MobileDevice
	require.NoError(t, db.First(&got, d.ID).Error)
	assert.True(t, got.LastSeenAt.After(earlier), "LastSeenAt should advance")
}
