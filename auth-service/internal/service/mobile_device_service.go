package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"gorm.io/gorm"

	kafkaprod "github.com/exbanka/auth-service/internal/kafka"
	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/repository"
	kafkamsg "github.com/exbanka/contract/kafka"
)

type MobileDeviceService struct {
	deviceRepo          *repository.MobileDeviceRepository
	activationRepo      *repository.MobileActivationRepository
	accountRepo         *repository.AccountRepository
	tokenRepo           *repository.TokenRepository
	jwtService          *JWTService
	producer            *kafkaprod.Producer
	mobileRefreshExp    time.Duration
	mobileActivationExp time.Duration
	frontendBaseURL     string
}

func NewMobileDeviceService(
	deviceRepo *repository.MobileDeviceRepository,
	activationRepo *repository.MobileActivationRepository,
	accountRepo *repository.AccountRepository,
	tokenRepo *repository.TokenRepository,
	jwtService *JWTService,
	producer *kafkaprod.Producer,
	mobileRefreshExp time.Duration,
	mobileActivationExp time.Duration,
	frontendBaseURL string,
) *MobileDeviceService {
	return &MobileDeviceService{
		deviceRepo:          deviceRepo,
		activationRepo:      activationRepo,
		accountRepo:         accountRepo,
		tokenRepo:           tokenRepo,
		jwtService:          jwtService,
		producer:            producer,
		mobileRefreshExp:    mobileRefreshExp,
		mobileActivationExp: mobileActivationExp,
		frontendBaseURL:     frontendBaseURL,
	}
}

// RequestActivation sends a 6-digit activation code to the user's email.
func (s *MobileDeviceService) RequestActivation(ctx context.Context, email string) error {
	account, err := s.accountRepo.GetByEmail(email)
	if err != nil {
		return errors.New("account not found")
	}
	if account.Status != "active" {
		return errors.New("account is not active")
	}

	code := generateActivationCode()
	activationCode := &model.MobileActivationCode{
		Email:     email,
		Code:      code,
		ExpiresAt: time.Now().Add(s.mobileActivationExp),
	}
	if err := s.activationRepo.Create(activationCode); err != nil {
		return fmt.Errorf("failed to create activation code: %w", err)
	}

	_ = s.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
		To:        email,
		EmailType: kafkamsg.EmailTypeMobileActivation,
		Data: map[string]string{
			"code":       code,
			"expires_in": fmt.Sprintf("%d minutes", int(s.mobileActivationExp.Minutes())),
		},
	})

	return nil
}

// ActivateDevice validates the activation code and creates a device-bound token pair.
// The entire activation code validation + device creation is wrapped in a transaction
// with SELECT FOR UPDATE to prevent race conditions on concurrent activation attempts.
func (s *MobileDeviceService) ActivateDevice(ctx context.Context, email, code, deviceName string) (accessToken, refreshToken, deviceID, deviceSecret string, err error) {
	// Look up account first (read-only, no lock needed)
	account, err := s.accountRepo.GetByEmail(email)
	if err != nil {
		return "", "", "", "", errors.New("account not found")
	}

	// Generate device credentials outside the transaction
	deviceID = generateDeviceID()
	deviceSecret = generateDeviceSecret()

	// Wrap code validation + device creation in a single transaction
	db := s.activationRepo.DB()
	err = db.Transaction(func(tx *gorm.DB) error {
		// SELECT FOR UPDATE on activation code — prevents concurrent validation
		activationCode, err := s.activationRepo.GetLatestByEmailForUpdate(tx, email)
		if err != nil {
			return errors.New("activation code not found")
		}
		if activationCode.Used {
			return errors.New("activation code already used")
		}
		if time.Now().After(activationCode.ExpiresAt) {
			return errors.New("activation code expired")
		}
		if activationCode.Attempts >= 3 {
			return errors.New("max attempts exceeded for activation code")
		}

		// Increment attempts atomically within the transaction
		if err := s.activationRepo.IncrementAttemptsInTx(tx, activationCode.ID); err != nil {
			return fmt.Errorf("failed to increment attempts: %w", err)
		}

		if activationCode.Code != code {
			remaining := 2 - activationCode.Attempts
			if remaining <= 0 {
				return errors.New("max attempts exceeded for activation code")
			}
			return fmt.Errorf("invalid activation code, %d attempts remaining", remaining)
		}

		// Mark code as used within the same transaction
		if err := s.activationRepo.MarkUsedInTx(tx, activationCode.ID); err != nil {
			return fmt.Errorf("failed to mark code used: %w", err)
		}

		// Deactivate existing devices atomically within the transaction
		if err := s.deviceRepo.DeactivateAllForUserInTx(tx, account.PrincipalID); err != nil {
			return fmt.Errorf("failed to deactivate existing devices: %w", err)
		}

		// Create new device within the same transaction
		now := time.Now()
		device := &model.MobileDevice{
			UserID:       account.PrincipalID,
			SystemType:   account.PrincipalType,
			DeviceID:     deviceID,
			DeviceSecret: deviceSecret,
			DeviceName:   deviceName,
			Status:       "active",
			ActivatedAt:  &now,
			LastSeenAt:   now,
		}
		if err := s.deviceRepo.CreateInTx(tx, device); err != nil {
			return fmt.Errorf("failed to create device: %w", err)
		}

		return nil
	})
	if err != nil {
		return "", "", "", "", err
	}

	// Token generation happens after the DB transaction commits successfully
	roles := []string{account.PrincipalType}
	permissions := []string{}

	accessToken, err = s.jwtService.GenerateMobileAccessToken(
		account.PrincipalID, email, roles, permissions,
		account.PrincipalType, "mobile", deviceID,
	)
	if err != nil {
		return "", "", "", "", fmt.Errorf("failed to generate access token: %w", err)
	}

	refreshTokenStr, err := generateToken()
	if err != nil {
		return "", "", "", "", fmt.Errorf("failed to generate refresh token: %w", err)
	}
	rt := &model.RefreshToken{
		AccountID:  account.ID,
		Token:      refreshTokenStr,
		ExpiresAt:  time.Now().Add(s.mobileRefreshExp),
		SystemType: account.PrincipalType,
	}
	if err := s.tokenRepo.CreateRefreshToken(rt); err != nil {
		return "", "", "", "", fmt.Errorf("failed to create refresh token: %w", err)
	}

	// Publish event AFTER transaction commits
	_ = s.producer.Publish(ctx, kafkamsg.TopicAuthMobileDeviceActivated, map[string]interface{}{
		"user_id":     account.PrincipalID,
		"device_id":   deviceID,
		"device_name": deviceName,
		"system_type": account.PrincipalType,
		"timestamp":   time.Now().Unix(),
	})

	return accessToken, refreshTokenStr, deviceID, deviceSecret, nil
}

// ValidateDeviceSignature verifies an HMAC-SHA256 request signature from a mobile device.
func (s *MobileDeviceService) ValidateDeviceSignature(deviceID, timestamp, method, path, bodySHA256, signature string) (bool, error) {
	device, err := s.deviceRepo.GetByDeviceID(deviceID)
	if err != nil {
		return false, errors.New("device not found")
	}
	if device.Status != "active" {
		return false, errors.New("device is not active")
	}

	// Check timestamp freshness (30 second window)
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false, errors.New("invalid timestamp")
	}
	if abs64(time.Now().Unix()-ts) > 30 {
		return false, errors.New("request timestamp too old")
	}

	// Reconstruct payload and verify HMAC
	payload := timestamp + ":" + method + ":" + path + ":" + bodySHA256
	secretBytes, err := hex.DecodeString(device.DeviceSecret)
	if err != nil {
		return false, errors.New("invalid device secret")
	}

	mac := hmac.New(sha256.New, secretBytes)
	mac.Write([]byte(payload))
	expectedBytes := mac.Sum(nil)

	sigBytes, err := hex.DecodeString(signature)
	if err != nil {
		return false, errors.New("invalid signature format")
	}

	if !hmac.Equal(sigBytes, expectedBytes) {
		return false, errors.New("signature mismatch")
	}

	return true, nil
}

// GetDeviceInfo returns the active device for a user.
func (s *MobileDeviceService) GetDeviceInfo(userID int64) (*model.MobileDevice, error) {
	device, err := s.deviceRepo.GetActiveByUserID(userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("no active device found")
		}
		return nil, err
	}
	return device, nil
}

// DeactivateDevice deactivates a user's device and revokes associated refresh tokens.
func (s *MobileDeviceService) DeactivateDevice(userID int64, deviceID string) error {
	device, err := s.deviceRepo.GetByDeviceID(deviceID)
	if err != nil {
		return errors.New("device not found")
	}
	if device.UserID != userID {
		return errors.New("device does not belong to user")
	}
	if device.Status != "active" {
		return errors.New("device is already deactivated")
	}

	now := time.Now()
	device.Status = "deactivated"
	device.DeactivatedAt = &now
	if err := s.deviceRepo.Update(device); err != nil {
		return fmt.Errorf("failed to deactivate device: %w", err)
	}

	// Revoke all refresh tokens for this account
	account, err := s.accountRepo.GetByPrincipal(device.SystemType, device.UserID)
	if err == nil {
		_ = s.tokenRepo.RevokeAllForAccount(account.ID)
	}

	return nil
}

// TransferDevice deactivates the current device and sends a new activation code.
func (s *MobileDeviceService) TransferDevice(ctx context.Context, userID int64, email string) error {
	// Deactivate all existing devices
	_ = s.deviceRepo.DeactivateAllForUser(userID)

	// Revoke refresh tokens
	account, err := s.accountRepo.GetByEmail(email)
	if err == nil {
		_ = s.tokenRepo.RevokeAllForAccount(account.ID)
	}

	// Send new activation code
	return s.RequestActivation(ctx, email)
}

// UpdateLastSeen updates the device's last seen timestamp. Fire-and-forget.
// Uses direct SQL update to avoid optimistic lock contention on high-frequency calls.
func (s *MobileDeviceService) UpdateLastSeen(deviceID string) {
	_ = s.deviceRepo.UpdateLastSeen(deviceID)
}

// --- Helpers ---

func generateDeviceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	// Format as UUID v4
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func generateDeviceSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func generateActivationCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("%06d", n.Int64())
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
