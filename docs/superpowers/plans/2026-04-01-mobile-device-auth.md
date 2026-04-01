# Mobile Device Authentication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend auth-service with mobile device management: registration, activation, device-bound JWT tokens, and HMAC request signature validation.

**Architecture:** Adds MobileDevice and MobileActivationCode models to auth-service. New gRPC RPCs for mobile activation, device management, and signature validation. Device-bound JWTs include device_type and device_id claims. Device secret (HMAC key) returned once at activation for request signing.

**Tech Stack:** Go 1.26, gRPC/Protobuf, GORM/PostgreSQL, crypto/hmac, crypto/rand

**Depends on:** None (extends existing auth-service independently)

---

## File Structure

### New files to create

```
auth-service/
├── internal/
│   ├── model/
│   │   └── mobile_device.go              # MobileDevice + MobileActivationCode models
│   ├── repository/
│   │   ├── mobile_device_repository.go   # MobileDevice CRUD
│   │   └── mobile_activation_repository.go # Activation code CRUD
│   └── service/
│       └── mobile_device_service.go      # Mobile device business logic
```

### Files to modify

```
auth-service/internal/service/jwt_service.go      # Add DeviceType + DeviceID to Claims
auth-service/internal/handler/grpc_handler.go      # Add mobile RPC methods
auth-service/internal/config/config.go             # Add mobile expiry config
auth-service/cmd/main.go                           # Wire mobile components
contract/proto/auth/auth.proto                     # Add mobile RPCs + messages
docker-compose.yml                                 # Add mobile env vars
```

---

## Task 1: Define MobileDevice and MobileActivationCode models

**Files:**
- Create: `auth-service/internal/model/mobile_device.go`

- [ ] **Step 1: Write the models**

```go
package model

import (
	"time"

	"gorm.io/gorm"
)

// MobileDevice represents a registered mobile app instance for a user.
// One active device per user, enforced at service level.
type MobileDevice struct {
	ID            int64      `gorm:"primaryKey;autoIncrement"`
	UserID        int64      `gorm:"not null;index"`
	SystemType    string     `gorm:"size:20;not null"` // "client" or "employee"
	DeviceID      string     `gorm:"size:36;uniqueIndex;not null"`
	DeviceSecret  string     `gorm:"size:64;not null" json:"-"`
	DeviceName    string     `gorm:"size:100;not null"`
	Status        string     `gorm:"size:20;not null;default:'pending'"` // "pending", "active", "deactivated"
	ActivatedAt   *time.Time
	DeactivatedAt *time.Time
	LastSeenAt    time.Time
	Version       int64 `gorm:"not null;default:1"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (d *MobileDevice) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", d.Version)
	d.Version++
	return nil
}

// MobileActivationCode is a short-lived 6-digit code sent to email for device activation.
type MobileActivationCode struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	Email     string    `gorm:"not null;index"`
	Code      string    `gorm:"size:6;not null"`
	ExpiresAt time.Time `gorm:"not null"`
	Attempts  int       `gorm:"not null;default:0"`
	Used      bool      `gorm:"default:false"`
	CreatedAt time.Time
}
```

- [ ] **Step 2: Commit**

```bash
git add auth-service/internal/model/mobile_device.go
git commit -m "feat(auth-service): add MobileDevice and MobileActivationCode models"
```

---

## Task 2: Create MobileDeviceRepository

**Files:**
- Create: `auth-service/internal/repository/mobile_device_repository.go`

- [ ] **Step 1: Write the repository**

```go
package repository

import (
	"time"

	"github.com/exbanka/auth-service/internal/model"
	"gorm.io/gorm"
)

type MobileDeviceRepository struct {
	db *gorm.DB
}

func NewMobileDeviceRepository(db *gorm.DB) *MobileDeviceRepository {
	return &MobileDeviceRepository{db: db}
}

func (r *MobileDeviceRepository) Create(device *model.MobileDevice) error {
	return r.db.Create(device).Error
}

func (r *MobileDeviceRepository) GetByID(id int64) (*model.MobileDevice, error) {
	var device model.MobileDevice
	if err := r.db.First(&device, id).Error; err != nil {
		return nil, err
	}
	return &device, nil
}

func (r *MobileDeviceRepository) GetByDeviceID(deviceID string) (*model.MobileDevice, error) {
	var device model.MobileDevice
	if err := r.db.Where("device_id = ?", deviceID).First(&device).Error; err != nil {
		return nil, err
	}
	return &device, nil
}

func (r *MobileDeviceRepository) GetActiveByUserID(userID int64) (*model.MobileDevice, error) {
	var device model.MobileDevice
	if err := r.db.Where("user_id = ? AND status = ?", userID, "active").First(&device).Error; err != nil {
		return nil, err
	}
	return &device, nil
}

func (r *MobileDeviceRepository) Update(device *model.MobileDevice) error {
	result := r.db.Save(device)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound // optimistic lock conflict
	}
	return nil
}

func (r *MobileDeviceRepository) DeactivateAllForUser(userID int64) error {
	now := time.Now()
	return r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.MobileDevice{}).
		Where("user_id = ? AND status = ?", userID, "active").
		Updates(map[string]interface{}{
			"status":         "deactivated",
			"deactivated_at": now,
		}).Error
}
```

- [ ] **Step 2: Commit**

```bash
git add auth-service/internal/repository/mobile_device_repository.go
git commit -m "feat(auth-service): add MobileDeviceRepository"
```

---

## Task 3: Create MobileActivationCodeRepository

**Files:**
- Create: `auth-service/internal/repository/mobile_activation_repository.go`

- [ ] **Step 1: Write the repository**

```go
package repository

import (
	"github.com/exbanka/auth-service/internal/model"
	"gorm.io/gorm"
)

type MobileActivationRepository struct {
	db *gorm.DB
}

func NewMobileActivationRepository(db *gorm.DB) *MobileActivationRepository {
	return &MobileActivationRepository{db: db}
}

func (r *MobileActivationRepository) Create(code *model.MobileActivationCode) error {
	return r.db.Create(code).Error
}

func (r *MobileActivationRepository) GetLatestByEmail(email string) (*model.MobileActivationCode, error) {
	var code model.MobileActivationCode
	if err := r.db.Where("email = ? AND used = false", email).
		Order("id DESC").First(&code).Error; err != nil {
		return nil, err
	}
	return &code, nil
}

func (r *MobileActivationRepository) IncrementAttempts(id int64) error {
	return r.db.Model(&model.MobileActivationCode{}).
		Where("id = ?", id).
		Update("attempts", gorm.Expr("attempts + 1")).Error
}

func (r *MobileActivationRepository) MarkUsed(id int64) error {
	return r.db.Model(&model.MobileActivationCode{}).
		Where("id = ?", id).
		Update("used", true).Error
}
```

- [ ] **Step 2: Commit**

```bash
git add auth-service/internal/repository/mobile_activation_repository.go
git commit -m "feat(auth-service): add MobileActivationCodeRepository"
```

---

## Task 4: Extend JWT Claims with device fields

**Files:**
- Modify: `auth-service/internal/service/jwt_service.go`

- [ ] **Step 1: Add DeviceType and DeviceID to Claims struct**

```go
type Claims struct {
	UserID      int64    `json:"user_id"`
	Email       string   `json:"email"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	SystemType  string   `json:"system_type"`
	DeviceType  string   `json:"device_type,omitempty"` // "mobile" for mobile app tokens, empty for browser
	DeviceID    string   `json:"device_id,omitempty"`   // UUID of registered mobile device
	jwt.RegisteredClaims
}
```

- [ ] **Step 2: Add GenerateMobileAccessToken method**

Add after the existing `GenerateAccessToken` method:

```go
func (s *JWTService) GenerateMobileAccessToken(userID int64, email string, roles []string, permissions []string, systemType, deviceType, deviceID string) (string, error) {
	claims := &Claims{
		UserID:      userID,
		Email:       email,
		Roles:       roles,
		Permissions: permissions,
		SystemType:  systemType,
		DeviceType:  deviceType,
		DeviceID:    deviceID,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        generateJTI(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.accessExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}
```

- [ ] **Step 3: Verify existing GenerateAccessToken still works unchanged**

The existing method produces tokens with `DeviceType=""` and `DeviceID=""`, which are omitted from JSON (`omitempty`). Browser tokens are unaffected.

- [ ] **Step 4: Commit**

```bash
git add auth-service/internal/service/jwt_service.go
git commit -m "feat(auth-service): add device_type and device_id to JWT claims"
```

---

## Task 5: Create MobileDeviceService

**Files:**
- Create: `auth-service/internal/service/mobile_device_service.go`

- [ ] **Step 1: Write the service**

```go
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

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/repository"
	kafkaprod "github.com/exbanka/auth-service/internal/kafka"
)

type MobileDeviceService struct {
	deviceRepo     *repository.MobileDeviceRepository
	activationRepo *repository.MobileActivationRepository
	accountRepo    *repository.AccountRepository
	tokenRepo      *repository.TokenRepository
	jwtService     *JWTService
	producer       *kafkaprod.Producer
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
func (s *MobileDeviceService) ActivateDevice(ctx context.Context, email, code, deviceName string) (accessToken, refreshToken, deviceID, deviceSecret string, err error) {
	// Validate activation code
	activationCode, err := s.activationRepo.GetLatestByEmail(email)
	if err != nil {
		return "", "", "", "", errors.New("activation code not found")
	}
	if activationCode.Used {
		return "", "", "", "", errors.New("activation code already used")
	}
	if time.Now().After(activationCode.ExpiresAt) {
		return "", "", "", "", errors.New("activation code expired")
	}
	if activationCode.Attempts >= 3 {
		return "", "", "", "", errors.New("max attempts exceeded for activation code")
	}

	_ = s.activationRepo.IncrementAttempts(activationCode.ID)

	if activationCode.Code != code {
		remaining := 2 - activationCode.Attempts // already incremented
		if remaining <= 0 {
			return "", "", "", "", errors.New("max attempts exceeded for activation code")
		}
		return "", "", "", "", fmt.Errorf("invalid activation code, %d attempts remaining", remaining)
	}

	_ = s.activationRepo.MarkUsed(activationCode.ID)

	// Look up account
	account, err := s.accountRepo.GetByEmail(email)
	if err != nil {
		return "", "", "", "", errors.New("account not found")
	}

	// Deactivate existing devices for this user
	_ = s.deviceRepo.DeactivateAllForUser(account.PrincipalID)

	// Generate device credentials
	deviceID = generateDeviceID()
	deviceSecret = generateDeviceSecret()
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
	if err := s.deviceRepo.Create(device); err != nil {
		return "", "", "", "", fmt.Errorf("failed to create device: %w", err)
	}

	// Fetch roles/permissions for the token
	roles := []string{account.PrincipalType}
	permissions := []string{}
	// For clients, role is just "client" with no permissions
	// For employees, we'd need to fetch from user-service — but mobile activation
	// doesn't have user-service client. The first token refresh will get full claims.
	// For now, use account-level info.

	accessToken, err = s.jwtService.GenerateMobileAccessToken(
		account.PrincipalID, email, roles, permissions,
		account.PrincipalType, "mobile", deviceID,
	)
	if err != nil {
		return "", "", "", "", fmt.Errorf("failed to generate access token: %w", err)
	}

	// Create refresh token
	refreshTokenStr := generateTokenString()
	rt := &model.RefreshToken{
		AccountID:  account.ID,
		Token:      refreshTokenStr,
		ExpiresAt:  time.Now().Add(s.mobileRefreshExp),
		SystemType: account.PrincipalType,
	}
	if err := s.tokenRepo.CreateRefreshToken(rt); err != nil {
		return "", "", "", "", fmt.Errorf("failed to create refresh token: %w", err)
	}

	// Publish event
	_ = s.producer.Publish(ctx, "auth.mobile-device-activated", map[string]interface{}{
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
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	sigBytes, err := hex.DecodeString(signature)
	if err != nil {
		return false, errors.New("invalid signature format")
	}
	expectedBytes, _ := hex.DecodeString(expectedSig)

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
func (s *MobileDeviceService) UpdateLastSeen(deviceID string) {
	device, err := s.deviceRepo.GetByDeviceID(deviceID)
	if err != nil {
		return
	}
	device.LastSeenAt = time.Now()
	_ = s.deviceRepo.Update(device)
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

func generateTokenString() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
```

- [ ] **Step 2: Commit**

```bash
git add auth-service/internal/service/mobile_device_service.go
git commit -m "feat(auth-service): add MobileDeviceService with activation, signature validation, device management"
```

---

## Task 6: Extend auth.proto with mobile RPCs

**Files:**
- Modify: `contract/proto/auth/auth.proto`

- [ ] **Step 1: Add mobile RPCs to AuthService**

Add these RPCs inside the `service AuthService` block:

```protobuf
  // Mobile device management
  rpc RequestMobileActivation(MobileActivationRequest) returns (MobileActivationResponse);
  rpc ActivateMobileDevice(ActivateMobileDeviceRequest) returns (ActivateMobileDeviceResponse);
  rpc RefreshMobileToken(RefreshMobileTokenRequest) returns (RefreshMobileTokenResponse);
  rpc DeactivateDevice(DeactivateDeviceRequest) returns (DeactivateDeviceResponse);
  rpc TransferDevice(TransferDeviceRequest) returns (TransferDeviceResponse);
  rpc ValidateDeviceSignature(ValidateDeviceSignatureRequest) returns (ValidateDeviceSignatureResponse);
  rpc GetDeviceInfo(GetDeviceInfoRequest) returns (GetDeviceInfoResponse);
```

- [ ] **Step 2: Add device_type and device_id to ValidateTokenResponse**

```protobuf
message ValidateTokenResponse {
  bool valid = 1;
  int64 user_id = 2;
  string email = 3;
  string role = 4;
  repeated string permissions = 5;
  string system_type = 6;
  repeated string roles = 7;
  string device_type = 8;   // "mobile" or empty for browser
  string device_id = 9;     // UUID of mobile device or empty
}
```

- [ ] **Step 3: Add all mobile request/response messages**

Append to the proto file:

```protobuf
// --- Mobile device messages ---

message MobileActivationRequest {
  string email = 1;
}

message MobileActivationResponse {
  bool success = 1;
  string message = 2;
}

message ActivateMobileDeviceRequest {
  string email = 1;
  string code = 2;
  string device_name = 3;
}

message ActivateMobileDeviceResponse {
  string access_token = 1;
  string refresh_token = 2;
  string device_id = 3;
  string device_secret = 4;
}

message RefreshMobileTokenRequest {
  string refresh_token = 1;
  string device_id = 2;
}

message RefreshMobileTokenResponse {
  string access_token = 1;
  string refresh_token = 2;
}

message DeactivateDeviceRequest {
  int64 user_id = 1;
  string device_id = 2;
}

message DeactivateDeviceResponse {
  bool success = 1;
}

message TransferDeviceRequest {
  int64 user_id = 1;
  string email = 2;
}

message TransferDeviceResponse {
  bool success = 1;
  string message = 2;
}

message ValidateDeviceSignatureRequest {
  string device_id = 1;
  string timestamp = 2;
  string method = 3;
  string path = 4;
  string body_sha256 = 5;
  string signature = 6;
}

message ValidateDeviceSignatureResponse {
  bool valid = 1;
}

message GetDeviceInfoRequest {
  int64 user_id = 1;
}

message GetDeviceInfoResponse {
  string device_id = 1;
  string device_name = 2;
  string status = 3;
  string activated_at = 4;
  string last_seen_at = 5;
}
```

- [ ] **Step 4: Regenerate proto**

```bash
make proto
```

- [ ] **Step 5: Verify generated code exists**

```bash
ls contract/authpb/*.go
```

Expected: `auth.pb.go` and `auth_grpc.pb.go` with new methods.

- [ ] **Step 6: Commit**

```bash
git add contract/proto/auth/auth.proto contract/authpb/
git commit -m "feat(contract): add mobile device RPCs and messages to auth.proto"
```

---

## Task 7: Extend gRPC handler with mobile methods

**Files:**
- Modify: `auth-service/internal/handler/grpc_handler.go`

- [ ] **Step 1: Add mobileSvc to handler struct**

Replace the handler struct and constructor:

```go
type AuthGRPCHandler struct {
	pb.UnimplementedAuthServiceServer
	authService *service.AuthService
	mobileSvc   *service.MobileDeviceService
}

func NewAuthGRPCHandler(authService *service.AuthService, mobileSvc *service.MobileDeviceService) *AuthGRPCHandler {
	return &AuthGRPCHandler{authService: authService, mobileSvc: mobileSvc}
}
```

- [ ] **Step 2: Update ValidateToken to return device fields**

Replace the ValidateToken method:

```go
func (h *AuthGRPCHandler) ValidateToken(ctx context.Context, req *pb.ValidateTokenRequest) (*pb.ValidateTokenResponse, error) {
	claims, err := h.authService.ValidateToken(req.Token)
	if err != nil {
		return &pb.ValidateTokenResponse{Valid: false}, nil
	}
	legacyRole := ""
	if len(claims.Roles) > 0 {
		legacyRole = claims.Roles[0]
	}
	return &pb.ValidateTokenResponse{
		Valid:       true,
		UserId:      claims.UserID,
		Email:       claims.Email,
		Role:        legacyRole,
		Roles:       claims.Roles,
		Permissions: claims.Permissions,
		SystemType:  claims.SystemType,
		DeviceType:  claims.DeviceType,
		DeviceId:    claims.DeviceID,
	}, nil
}
```

- [ ] **Step 3: Add mobile RPC methods**

Append to the handler file:

```go
func (h *AuthGRPCHandler) RequestMobileActivation(ctx context.Context, req *pb.MobileActivationRequest) (*pb.MobileActivationResponse, error) {
	if err := h.mobileSvc.RequestActivation(ctx, req.Email); err != nil {
		// Always return success to avoid email enumeration (same pattern as password reset)
		log.Printf("mobile activation request error (suppressed): %v", err)
	}
	return &pb.MobileActivationResponse{
		Success: true,
		Message: "If the email is registered, an activation code has been sent",
	}, nil
}

func (h *AuthGRPCHandler) ActivateMobileDevice(ctx context.Context, req *pb.ActivateMobileDeviceRequest) (*pb.ActivateMobileDeviceResponse, error) {
	access, refresh, deviceID, deviceSecret, err := h.mobileSvc.ActivateDevice(ctx, req.Email, req.Code, req.DeviceName)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), err.Error())
	}
	return &pb.ActivateMobileDeviceResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		DeviceId:     deviceID,
		DeviceSecret: deviceSecret,
	}, nil
}

func (h *AuthGRPCHandler) RefreshMobileToken(ctx context.Context, req *pb.RefreshMobileTokenRequest) (*pb.RefreshMobileTokenResponse, error) {
	// Validate the refresh token
	rt, err := h.authService.ValidateRefreshToken(req.RefreshToken)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid refresh token")
	}

	// Check device is still active
	device, err := h.mobileSvc.GetDeviceInfo(rt.AccountID)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "device deactivated, please re-activate")
	}
	if device.DeviceID != req.DeviceId {
		return nil, status.Errorf(codes.PermissionDenied, "device ID mismatch")
	}

	// Revoke old refresh token and issue new pair
	access, refresh, err := h.authService.RefreshTokenForMobile(ctx, req.RefreshToken, device.DeviceID)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), err.Error())
	}
	return &pb.RefreshMobileTokenResponse{
		AccessToken:  access,
		RefreshToken: refresh,
	}, nil
}

func (h *AuthGRPCHandler) DeactivateDevice(ctx context.Context, req *pb.DeactivateDeviceRequest) (*pb.DeactivateDeviceResponse, error) {
	if err := h.mobileSvc.DeactivateDevice(req.UserId, req.DeviceId); err != nil {
		return nil, status.Errorf(mapServiceError(err), err.Error())
	}
	return &pb.DeactivateDeviceResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) TransferDevice(ctx context.Context, req *pb.TransferDeviceRequest) (*pb.TransferDeviceResponse, error) {
	if err := h.mobileSvc.TransferDevice(ctx, req.UserId, req.Email); err != nil {
		return nil, status.Errorf(mapServiceError(err), err.Error())
	}
	return &pb.TransferDeviceResponse{
		Success: true,
		Message: "Device deactivated. New activation code sent to email.",
	}, nil
}

func (h *AuthGRPCHandler) ValidateDeviceSignature(ctx context.Context, req *pb.ValidateDeviceSignatureRequest) (*pb.ValidateDeviceSignatureResponse, error) {
	valid, err := h.mobileSvc.ValidateDeviceSignature(req.DeviceId, req.Timestamp, req.Method, req.Path, req.BodySha256, req.Signature)
	if err != nil {
		return &pb.ValidateDeviceSignatureResponse{Valid: false}, nil
	}
	return &pb.ValidateDeviceSignatureResponse{Valid: valid}, nil
}

func (h *AuthGRPCHandler) GetDeviceInfo(ctx context.Context, req *pb.GetDeviceInfoRequest) (*pb.GetDeviceInfoResponse, error) {
	device, err := h.mobileSvc.GetDeviceInfo(req.UserId)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), err.Error())
	}
	resp := &pb.GetDeviceInfoResponse{
		DeviceId:   device.DeviceID,
		DeviceName: device.DeviceName,
		Status:     device.Status,
		LastSeenAt: device.LastSeenAt.Format("2006-01-02T15:04:05Z"),
	}
	if device.ActivatedAt != nil {
		resp.ActivatedAt = device.ActivatedAt.Format("2006-01-02T15:04:05Z")
	}
	return resp, nil
}
```

- [ ] **Step 4: Verify build**

```bash
cd auth-service && go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add auth-service/internal/handler/grpc_handler.go
git commit -m "feat(auth-service): add mobile device gRPC handler methods"
```

---

## Task 8: Add RefreshTokenForMobile to AuthService

**Files:**
- Modify: `auth-service/internal/service/auth_service.go`

The handler's `RefreshMobileToken` calls `authService.RefreshTokenForMobile`. We need to add this method that issues a mobile-flavored token pair.

- [ ] **Step 1: Add the method**

Append to `auth_service.go`:

```go
// RefreshTokenForMobile revokes the old refresh token and issues a new mobile token pair.
func (s *AuthService) RefreshTokenForMobile(ctx context.Context, oldRefreshToken, deviceID string) (string, string, error) {
	rt, err := s.tokenRepo.GetRefreshToken(oldRefreshToken)
	if err != nil {
		return "", "", errors.New("invalid refresh token")
	}
	if rt.Revoked {
		return "", "", errors.New("refresh token revoked")
	}
	if time.Now().After(rt.ExpiresAt) {
		return "", "", errors.New("refresh token expired")
	}

	// Revoke old token
	_ = s.tokenRepo.RevokeRefreshToken(oldRefreshToken)

	// Get account
	account, err := s.accountRepo.GetByID(rt.AccountID)
	if err != nil {
		return "", "", errors.New("account not found")
	}
	if account.Status != "active" {
		return "", "", errors.New("account is not active")
	}

	// Fetch roles/permissions
	var roles []string
	var permissions []string
	if account.PrincipalType == "employee" {
		emp, err := s.userClient.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: account.PrincipalID})
		if err == nil {
			roles = emp.Roles
			permissions = emp.Permissions
		}
	} else {
		roles = []string{"client"}
	}

	// Generate new access token with device claims
	access, err := s.jwtService.GenerateMobileAccessToken(
		account.PrincipalID, account.Email, roles, permissions,
		account.PrincipalType, "mobile", deviceID,
	)
	if err != nil {
		return "", "", err
	}

	// Generate new refresh token
	newRefreshStr := generateRefreshTokenString()
	newRT := &model.RefreshToken{
		AccountID:  account.ID,
		Token:      newRefreshStr,
		ExpiresAt:  time.Now().Add(s.mobileRefreshExp),
		SystemType: account.PrincipalType,
	}
	if err := s.tokenRepo.CreateRefreshToken(newRT); err != nil {
		return "", "", err
	}

	return access, newRefreshStr, nil
}
```

- [ ] **Step 2: Add mobileRefreshExp field to AuthService**

Add to the AuthService struct:

```go
mobileRefreshExp time.Duration
```

Update `NewAuthService` to accept and store this parameter. Add it as the last parameter before the existing `pepper` parameter:

```go
func NewAuthService(
	tokenRepo *repository.TokenRepository,
	loginAttemptRepo *repository.LoginAttemptRepository,
	totpRepo *repository.TOTPRepository,
	totpSvc *TOTPService,
	jwtService *JWTService,
	accountRepo *repository.AccountRepository,
	userClient userpb.UserServiceClient,
	producer *kafkaprod.Producer,
	cache *cache.RedisCache,
	refreshExp time.Duration,
	mobileRefreshExp time.Duration,
	frontendBaseURL string,
	pepper string,
) *AuthService {
```

And store `mobileRefreshExp: mobileRefreshExp` in the struct initialization.

- [ ] **Step 3: Add ValidateRefreshToken method (for handler to call)**

```go
// ValidateRefreshToken returns the refresh token record if valid.
func (s *AuthService) ValidateRefreshToken(token string) (*model.RefreshToken, error) {
	rt, err := s.tokenRepo.GetRefreshToken(token)
	if err != nil {
		return nil, errors.New("invalid refresh token")
	}
	if rt.Revoked {
		return nil, errors.New("refresh token revoked")
	}
	if time.Now().After(rt.ExpiresAt) {
		return nil, errors.New("refresh token expired")
	}
	return rt, nil
}
```

- [ ] **Step 4: Add helper if not exists**

```go
func generateRefreshTokenString() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
```

- [ ] **Step 5: Commit**

```bash
git add auth-service/internal/service/auth_service.go
git commit -m "feat(auth-service): add RefreshTokenForMobile and ValidateRefreshToken methods"
```

---

## Task 9: Add Kafka email type for mobile activation

**Files:**
- Modify: `contract/kafka/messages.go`
- Modify: `notification-service/internal/sender/templates.go`

- [ ] **Step 1: Add EmailTypeMobileActivation constant**

In `contract/kafka/messages.go`, add to the email type constants:

```go
EmailTypeMobileActivation EmailType = "MOBILE_ACTIVATION"
```

- [ ] **Step 2: Add email template**

In `notification-service/internal/sender/templates.go`, add a case for `MOBILE_ACTIVATION` in the `BuildEmail` function:

```go
case kafkamsg.EmailTypeMobileActivation:
	code := data["code"]
	expiresIn := data["expires_in"]
	subject = "Your EXBanka Mobile App Activation Code"
	body = fmt.Sprintf(`
		<html><body style="font-family:Arial,sans-serif;max-width:600px;margin:0 auto;">
		<h2 style="color:#1a365d;">Mobile App Activation</h2>
		<p>Use the following code to activate your EXBanka mobile app:</p>
		<div style="background:#f0f4f8;padding:20px;text-align:center;border-radius:8px;margin:20px 0;">
			<span style="font-size:32px;font-weight:bold;letter-spacing:8px;color:#2d3748;">%s</span>
		</div>
		<p>This code expires in <strong>%s</strong>.</p>
		<p>If you did not request this, please ignore this email.</p>
		<p style="color:#718096;font-size:12px;">EXBanka Security Team</p>
		</body></html>
	`, code, expiresIn)
```

- [ ] **Step 3: Commit**

```bash
git add contract/kafka/messages.go notification-service/internal/sender/templates.go
git commit -m "feat: add MOBILE_ACTIVATION email type and template"
```

---

## Task 10: Config additions and main.go wiring

**Files:**
- Modify: `auth-service/internal/config/config.go`
- Modify: `auth-service/cmd/main.go`

- [ ] **Step 1: Add mobile config fields**

In `config.go`, add to the Config struct:

```go
MobileRefreshExpiry    time.Duration
MobileActivationExpiry time.Duration
```

In `Load()`, add:

```go
mobileRefreshExp, _ := time.ParseDuration(getEnv("MOBILE_REFRESH_EXPIRY", "2160h"))
mobileActivationExp, _ := time.ParseDuration(getEnv("MOBILE_ACTIVATION_EXPIRY", "15m"))
```

And set them in the returned Config struct.

- [ ] **Step 2: Update main.go — add models to AutoMigrate**

Add to the AutoMigrate call:

```go
&model.MobileDevice{},
&model.MobileActivationCode{},
```

- [ ] **Step 3: Update main.go — create repos and services**

After existing repository creation, add:

```go
mobileDeviceRepo := repository.NewMobileDeviceRepository(db)
mobileActivationRepo := repository.NewMobileActivationRepository(db)

mobileSvc := service.NewMobileDeviceService(
	mobileDeviceRepo, mobileActivationRepo, accountRepo, tokenRepo,
	jwtService, producer, cfg.MobileRefreshExpiry, cfg.MobileActivationExpiry, cfg.FrontendBaseURL,
)
```

- [ ] **Step 4: Update main.go — pass mobileSvc to handler**

Update the handler construction:

```go
grpcHandler := handler.NewAuthGRPCHandler(authService, mobileSvc)
```

- [ ] **Step 5: Update main.go — update AuthService constructor**

Pass `cfg.MobileRefreshExpiry` as the new parameter to `NewAuthService`:

```go
authService := service.NewAuthService(tokenRepo, loginAttemptRepo, totpRepo, totpSvc, jwtService, accountRepo, userClient, producer, redisCache, cfg.RefreshExpiry, cfg.MobileRefreshExpiry, cfg.FrontendBaseURL, cfg.PasswordPepper)
```

- [ ] **Step 6: Add new topic to EnsureTopics**

```go
kafkaprod.EnsureTopics(cfg.KafkaBrokers,
	"user.employee-created",
	"client.created",
	"notification.send-email",
	kafkamsg.TopicAuthAccountStatusChanged,
	kafkamsg.TopicAuthDeadLetter,
	"auth.mobile-device-activated",
)
```

- [ ] **Step 7: Verify build**

```bash
cd auth-service && go build ./cmd
```

Expected: BUILD SUCCESS.

- [ ] **Step 8: Commit**

```bash
git add auth-service/internal/config/config.go auth-service/cmd/main.go
git commit -m "feat(auth-service): wire mobile device models, repos, service into main"
```

---

## Task 11: Docker-compose updates

**Files:**
- Modify: `docker-compose.yml`

- [ ] **Step 1: Add mobile env vars to auth-service**

In the `auth-service` environment section, add:

```yaml
MOBILE_REFRESH_EXPIRY: "2160h"
MOBILE_ACTIVATION_EXPIRY: "15m"
```

- [ ] **Step 2: Commit**

```bash
git add docker-compose.yml
git commit -m "chore: add mobile auth env vars to docker-compose"
```

---

## Task 12: Verify full build

- [ ] **Step 1: Run go mod tidy**

```bash
cd auth-service && go mod tidy
```

- [ ] **Step 2: Build auth-service**

```bash
cd auth-service && go build ./cmd
```

Expected: BUILD SUCCESS.

- [ ] **Step 3: Full repo build**

```bash
make build
```

Expected: All services build successfully.

- [ ] **Step 4: Commit any dependency changes**

```bash
git add auth-service/go.sum auth-service/go.mod
git commit -m "chore(auth-service): update dependencies after mobile device auth"
```

---

## Design Notes

### Security Model

1. **Device secret never leaves auth-service** — only returned once at activation, only used for HMAC verification server-side
2. **Constant-time comparison** — `hmac.Equal` prevents timing attacks on signature verification
3. **30-second timestamp window** — prevents replay attacks while allowing reasonable clock skew
4. **One device per user** — activating a new device automatically deactivates the old one
5. **Email enumeration protection** — RequestMobileActivation always returns success, matching the existing password reset pattern

### JWT Backward Compatibility

- Existing browser tokens have `DeviceType=""` and `DeviceID=""` (omitted from JSON via `omitempty`)
- `ValidateToken` returns empty strings for these fields on browser tokens
- Middleware (Plan 9) checks `device_type == "mobile"` to enforce mobile-only routes
- All existing middleware works unchanged — it never checks these new fields

### What This Plan Does NOT Cover

- API Gateway routes and handlers (Plan 10)
- MobileAuthMiddleware and RequireDeviceSignature middleware (Plan 9)
- Notification-service mobile inbox (Plan 9)
- Verification-service (Plan 7)
- Transaction-service migration (Plan 10)
