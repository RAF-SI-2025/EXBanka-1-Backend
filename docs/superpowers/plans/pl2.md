# Plan 10: Mobile Biometric Verification Bypass

**Date:** 2026-04-04
**Design spec:** `docs/superpowers/specs/2026-04-04-blueprints-biometrics-hierarchy-design.md` (Section 2)
**Scope:** Add `BiometricsEnabled` to `MobileDevice`, auth-service gRPC methods, verification-service biometric verify, gateway endpoints, tests

---

## Task 1: Add BiometricsEnabled to MobileDevice Model

**Files:**
- `auth-service/internal/model/mobile_device.go`

### Steps

- [ ] **1.1** Add `BiometricsEnabled` field to `MobileDevice` struct

In `auth-service/internal/model/mobile_device.go`, add the field after `LastSeenAt`:

```go
type MobileDevice struct {
	ID                int64      `gorm:"primaryKey;autoIncrement"`
	UserID            int64      `gorm:"not null;index"`
	SystemType        string     `gorm:"size:20;not null"`
	DeviceID          string     `gorm:"size:36;uniqueIndex;not null"`
	DeviceSecret      string     `gorm:"size:64;not null" json:"-"`
	DeviceName        string     `gorm:"size:100;not null"`
	Status            string     `gorm:"size:20;not null;default:'pending'"`
	BiometricsEnabled bool       `gorm:"default:false"`
	ActivatedAt       *time.Time
	DeactivatedAt     *time.Time
	LastSeenAt        time.Time
	Version           int64 `gorm:"not null;default:1"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
```

GORM auto-migrates on startup (`auth-service/cmd/main.go` line 39-52 already includes `&model.MobileDevice{}`), so no manual migration is needed. The new column `biometrics_enabled` will be added with `DEFAULT false`.

- [ ] **1.2** Verify build compiles

```bash
cd auth-service && go build ./...
```

---

## Task 2: Auth-Service Biometrics Methods

**Files:**
- `auth-service/internal/service/mobile_device_service.go`

### Steps

- [ ] **2.1** Add `SetBiometricsEnabled` method

Append to `auth-service/internal/service/mobile_device_service.go` before the `--- Helpers ---` section:

```go
// SetBiometricsEnabled enables or disables biometric verification for the user's active device.
// Validates ownership (device belongs to userID) and that the device is active.
func (s *MobileDeviceService) SetBiometricsEnabled(userID int64, deviceID string, enabled bool) error {
	device, err := s.deviceRepo.GetByDeviceID(deviceID)
	if err != nil {
		return errors.New("device not found")
	}
	if device.UserID != userID {
		return errors.New("device does not belong to user")
	}
	if device.Status != "active" {
		return errors.New("device is not active")
	}

	device.BiometricsEnabled = enabled
	if err := s.deviceRepo.Update(device); err != nil {
		return fmt.Errorf("failed to update biometrics setting: %w", err)
	}
	return nil
}
```

- [ ] **2.2** Add `GetBiometricsEnabled` method

```go
// GetBiometricsEnabled returns the biometrics enabled status for the user's active device.
func (s *MobileDeviceService) GetBiometricsEnabled(userID int64, deviceID string) (bool, error) {
	device, err := s.deviceRepo.GetByDeviceID(deviceID)
	if err != nil {
		return false, errors.New("device not found")
	}
	if device.UserID != userID {
		return false, errors.New("device does not belong to user")
	}
	if device.Status != "active" {
		return false, errors.New("device is not active")
	}
	return device.BiometricsEnabled, nil
}
```

- [ ] **2.3** Add `CheckBiometricsEnabled` method

This method is called by verification-service via gRPC. It only checks the flag on the device -- ownership is verified at the gateway level by `RequireDeviceSignature` middleware.

```go
// CheckBiometricsEnabled checks if biometrics is enabled for a device by device ID.
// Used by verification-service internally; ownership is verified at the gateway level.
func (s *MobileDeviceService) CheckBiometricsEnabled(deviceID string) (bool, error) {
	device, err := s.deviceRepo.GetByDeviceID(deviceID)
	if err != nil {
		return false, errors.New("device not found")
	}
	if device.Status != "active" {
		return false, errors.New("device is not active")
	}
	return device.BiometricsEnabled, nil
}
```

- [ ] **2.4** Verify build compiles

```bash
cd auth-service && go build ./...
```

---

## Task 3: Auth-Service Proto + gRPC Handler

**Files:**
- `contract/proto/auth/auth.proto`
- `auth-service/internal/handler/grpc_handler.go`

### Steps

- [ ] **3.1** Add biometrics RPCs and messages to `contract/proto/auth/auth.proto`

Add the three new RPCs to the `AuthService` service block, after `GetDeviceInfo`:

```protobuf
  // Biometrics management
  rpc SetBiometricsEnabled(SetBiometricsRequest) returns (SetBiometricsResponse);
  rpc GetBiometricsEnabled(GetBiometricsRequest) returns (GetBiometricsResponse);
  rpc CheckBiometricsEnabled(CheckBiometricsRequest) returns (CheckBiometricsResponse);
```

Add the message definitions at the end of the file (before the closing, or after `LoginHistoryResponse`):

```protobuf
// --- Biometrics messages ---

message SetBiometricsRequest {
  int64 user_id = 1;
  string device_id = 2;
  bool enabled = 3;
}

message SetBiometricsResponse {
  bool success = 1;
}

message GetBiometricsRequest {
  int64 user_id = 1;
  string device_id = 2;
}

message GetBiometricsResponse {
  bool enabled = 1;
}

message CheckBiometricsRequest {
  string device_id = 1;
}

message CheckBiometricsResponse {
  bool enabled = 1;
}
```

- [ ] **3.2** Regenerate protobuf Go files

```bash
make proto
```

- [ ] **3.3** Add handler methods to `auth-service/internal/handler/grpc_handler.go`

Add after the `GetDeviceInfo` method (around line 243), before the `--- Session management RPC methods ---` comment:

```go
// --- Biometrics RPC methods ---

func (h *AuthGRPCHandler) SetBiometricsEnabled(ctx context.Context, req *pb.SetBiometricsRequest) (*pb.SetBiometricsResponse, error) {
	if err := h.mobileSvc.SetBiometricsEnabled(req.UserId, req.DeviceId, req.Enabled); err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.SetBiometricsResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) GetBiometricsEnabled(ctx context.Context, req *pb.GetBiometricsRequest) (*pb.GetBiometricsResponse, error) {
	enabled, err := h.mobileSvc.GetBiometricsEnabled(req.UserId, req.DeviceId)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.GetBiometricsResponse{Enabled: enabled}, nil
}

func (h *AuthGRPCHandler) CheckBiometricsEnabled(ctx context.Context, req *pb.CheckBiometricsRequest) (*pb.CheckBiometricsResponse, error) {
	enabled, err := h.mobileSvc.CheckBiometricsEnabled(req.DeviceId)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.CheckBiometricsResponse{Enabled: enabled}, nil
}
```

- [ ] **3.4** Verify build compiles

```bash
cd auth-service && go build ./...
```

---

## Task 4: Verification-Service Biometric Verify Method

**Files:**
- `verification-service/internal/config/config.go`
- `verification-service/internal/service/verification_service.go`
- `verification-service/cmd/main.go`

### Steps

- [ ] **4.1** Add `AUTH_GRPC_ADDR` to verification-service config

In `verification-service/internal/config/config.go`, add the field and load it:

```go
type Config struct {
	DBHost       string
	DBPort       string
	DBUser       string
	DBPassword   string
	DBName       string
	DBSslmode    string
	GRPCAddr     string
	KafkaBrokers string

	ChallengeExpiry time.Duration
	MaxAttempts     int
	MetricsPort     string
	AuthGRPCAddr    string  // NEW
}
```

In the `Load()` function, add:

```go
	AuthGRPCAddr:    getEnv("AUTH_GRPC_ADDR", "localhost:50051"),
```

- [ ] **4.2** Add auth-service gRPC client to verification-service `cmd/main.go`

Add imports:

```go
	"google.golang.org/grpc/credentials/insecure"

	authpb "github.com/exbanka/contract/authpb"
```

After the producer creation (line 47-48) and before the repository creation (line 51), add the auth-service client connection:

```go
	// Connect to auth-service for biometrics checks
	authConn, err := grpc.NewClient(cfg.AuthGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to auth service: %v", err)
	}
	defer authConn.Close()
	authClient := authpb.NewAuthServiceClient(authConn)
```

- [ ] **4.3** Update `VerificationService` struct to hold auth-service client

In `verification-service/internal/service/verification_service.go`, add the auth client field:

```go
type VerificationService struct {
	repo            *repository.VerificationChallengeRepository
	producer        *kafkaprod.Producer
	db              *gorm.DB
	authClient      authpb.AuthServiceClient  // NEW
	challengeExpiry time.Duration
	maxAttempts     int
}
```

Update the constructor:

```go
func NewVerificationService(
	repo *repository.VerificationChallengeRepository,
	producer *kafkaprod.Producer,
	db *gorm.DB,
	authClient authpb.AuthServiceClient,  // NEW parameter
	challengeExpiry time.Duration,
	maxAttempts int,
) *VerificationService {
	return &VerificationService{
		repo:            repo,
		producer:        producer,
		db:              db,
		authClient:      authClient,  // NEW
		challengeExpiry: challengeExpiry,
		maxAttempts:     maxAttempts,
	}
}
```

Add import for authpb at the top of the file:

```go
	authpb "github.com/exbanka/contract/authpb"
```

- [ ] **4.4** Update service construction in `cmd/main.go`

Change the service creation line (currently line 54) to pass the auth client:

```go
	svc := service.NewVerificationService(repo, producer, db, authClient, cfg.ChallengeExpiry, cfg.MaxAttempts)
```

- [ ] **4.5** Add `VerifyByBiometric` method

Add to `verification-service/internal/service/verification_service.go`, after `SubmitCode`:

```go
// VerifyByBiometric verifies a challenge using device biometrics.
// The device signature has already been validated at the gateway level.
// This method checks that biometrics is enabled on the device (via auth-service),
// then marks the challenge as verified with verified_by=biometric for audit.
func (s *VerificationService) VerifyByBiometric(ctx context.Context, challengeID uint64, userID uint64, deviceID string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		vc, err := s.repo.GetByIDForUpdate(tx, challengeID)
		if err != nil {
			return fmt.Errorf("challenge not found: %w", err)
		}

		// Validate challenge state
		if err := s.validateChallengeState(vc); err != nil {
			return err
		}

		// Validate ownership
		if vc.UserID != userID {
			return fmt.Errorf("challenge does not belong to this user")
		}

		// Check biometrics enabled via auth-service
		resp, err := s.authClient.CheckBiometricsEnabled(ctx, &authpb.CheckBiometricsRequest{
			DeviceId: deviceID,
		})
		if err != nil {
			return fmt.Errorf("failed to check biometrics status: %w", err)
		}
		if !resp.Enabled {
			return fmt.Errorf("biometrics not enabled for this device")
		}

		// Mark verified with biometric audit trail
		now := time.Now()
		vc.Status = "verified"
		vc.VerifiedAt = &now

		// Bind device if not already bound
		if vc.DeviceID == "" {
			vc.DeviceID = deviceID
		}

		// Set verified_by in challenge data for audit
		var challengeData map[string]interface{}
		if err := json.Unmarshal(vc.ChallengeData, &challengeData); err != nil {
			challengeData = map[string]interface{}{}
		}
		challengeData["verified_by"] = "biometric"
		updatedData, _ := json.Marshal(challengeData)
		vc.ChallengeData = datatypes.JSON(updatedData)

		result := tx.Save(vc)
		if err := shared.CheckRowsAffected(result); err != nil {
			return err
		}

		VerificationAttemptsTotal.WithLabelValues("biometric_success").Inc()

		// Publish verified event
		if pubErr := s.producer.PublishChallengeVerified(ctx, kafkamsg.VerificationChallengeVerifiedMessage{
			ChallengeID:   vc.ID,
			UserID:        vc.UserID,
			SourceService: vc.SourceService,
			SourceID:      vc.SourceID,
			Method:        vc.Method,
			VerifiedAt:    vc.VerifiedAt.UTC().Format(time.RFC3339),
		}); pubErr != nil {
			log.Printf("warn: failed to publish challenge-verified for %d: %v", vc.ID, pubErr)
		}

		return nil
	})
}
```

Note: This requires adding `"encoding/json"` to imports if not already present (it already is), and `datatypes` is already imported. The `authpb` import was added in step 4.3.

- [ ] **4.6** Verify build compiles

```bash
cd verification-service && go build ./...
```

---

## Task 5: Verification-Service Proto + gRPC Handler

**Files:**
- `contract/proto/verification/verification.proto`
- `verification-service/internal/handler/grpc_handler.go`

### Steps

- [ ] **5.1** Add `VerifyByBiometric` RPC to `contract/proto/verification/verification.proto`

Add the new RPC to the `VerificationGRPCService` service block, after `SubmitCode`:

```protobuf
  // VerifyByBiometric verifies a challenge using device biometrics (device signature is the proof).
  rpc VerifyByBiometric(VerifyByBiometricRequest) returns (VerifyByBiometricResponse);
```

Add the message definitions at the end of the file:

```protobuf
message VerifyByBiometricRequest {
  uint64 challenge_id = 1;
  uint64 user_id      = 2;
  string device_id    = 3;
}

message VerifyByBiometricResponse {
  bool success = 1;
}
```

- [ ] **5.2** Regenerate protobuf Go files

```bash
make proto
```

- [ ] **5.3** Add handler method to `verification-service/internal/handler/grpc_handler.go`

Add after the `SubmitCode` handler method:

```go
func (h *VerificationGRPCHandler) VerifyByBiometric(ctx context.Context, req *pb.VerifyByBiometricRequest) (*pb.VerifyByBiometricResponse, error) {
	err := h.svc.VerifyByBiometric(ctx, req.GetChallengeId(), req.GetUserId(), req.GetDeviceId())
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.VerifyByBiometricResponse{Success: true}, nil
}
```

- [ ] **5.4** Update `mapServiceError` in verification handler for biometrics errors

Add a case for "biometrics not enabled" to map to `PermissionDenied` (which becomes HTTP 403 at the gateway). In `verification-service/internal/handler/grpc_handler.go`, update the `mapServiceError` function's `PermissionDenied` case:

```go
	case strings.Contains(msg, "bound to a different device"),
		strings.Contains(msg, "biometrics not enabled"),
		strings.Contains(msg, "does not belong to this user"):
		return codes.PermissionDenied
```

- [ ] **5.5** Verify build compiles

```bash
cd verification-service && go build ./...
```

---

## Task 6: API Gateway Endpoints

**Files:**
- `api-gateway/internal/handler/verification_handler.go`
- `api-gateway/internal/handler/mobile_auth_handler.go`
- `api-gateway/internal/router/router_v1.go`
- `api-gateway/internal/router/router.go` (v0 routes, if parity needed)

### Steps

- [ ] **6.1** Add `BiometricVerify` handler to `verification_handler.go`

Add to `api-gateway/internal/handler/verification_handler.go`:

```go
// @Summary Verify challenge using device biometrics
// @Description Verifies a pending challenge using the device's biometric authentication.
// @Description No request body needed - the device signature IS the proof of authentication.
// @Description Requires MobileAuthMiddleware + RequireDeviceSignature.
// @Tags mobile-verifications
// @Produce json
// @Security BearerAuth
// @Param challenge_id path int true "Challenge ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{} "Biometrics not enabled"
// @Failure 409 {object} map[string]interface{} "Challenge expired or already verified"
// @Router /api/v1/mobile/verifications/{challenge_id}/biometric [post]
func (h *VerificationHandler) BiometricVerify(c *gin.Context) {
	challengeID, err := strconv.ParseUint(c.Param("challenge_id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid challenge id")
		return
	}

	userID := c.GetInt64("user_id")
	deviceID, _ := c.Get("device_id")

	resp, err := h.verificationClient.VerifyByBiometric(c.Request.Context(), &verificationpb.VerifyByBiometricRequest{
		ChallengeId: challengeID,
		UserId:      uint64(userID),
		DeviceId:    deviceID.(string),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": resp.Success})
}
```

- [ ] **6.2** Add `SetBiometrics` handler to `mobile_auth_handler.go`

Add to `api-gateway/internal/handler/mobile_auth_handler.go`:

```go
type setBiometricsReq struct {
	Enabled bool `json:"enabled"`
}

// @Summary Enable or disable biometric verification for device
// @Tags mobile-device
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body setBiometricsReq true "Biometrics setting"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /api/v1/mobile/device/biometrics [post]
func (h *MobileAuthHandler) SetBiometrics(c *gin.Context) {
	var req setBiometricsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	userID := c.GetInt64("user_id")
	deviceID, _ := c.Get("device_id")

	resp, err := h.authClient.SetBiometricsEnabled(c.Request.Context(), &authpb.SetBiometricsRequest{
		UserId:   userID,
		DeviceId: deviceID.(string),
		Enabled:  req.Enabled,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": resp.Success})
}
```

- [ ] **6.3** Add `GetBiometrics` handler to `mobile_auth_handler.go`

```go
// @Summary Get biometric verification status for device
// @Tags mobile-device
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/mobile/device/biometrics [get]
func (h *MobileAuthHandler) GetBiometrics(c *gin.Context) {
	userID := c.GetInt64("user_id")
	deviceID, _ := c.Get("device_id")

	resp, err := h.authClient.GetBiometricsEnabled(c.Request.Context(), &authpb.GetBiometricsRequest{
		UserId:   userID,
		DeviceId: deviceID.(string),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"enabled": resp.Enabled})
}
```

- [ ] **6.4** Register routes in `api-gateway/internal/router/router_v1.go`

**Biometric verify route** -- add to the `mobileVerify` group (after line 282, the `/:challenge_id/submit` route):

```go
			mobileVerify.POST("/:challenge_id/biometric", verifyHandler.BiometricVerify)
```

**Biometrics toggle routes** -- the `mobileDevice` group (lines 266-273) currently uses `MobileAuthMiddleware` but NOT `RequireDeviceSignature`. The biometrics toggle endpoints need `RequireDeviceSignature` per the design spec. Create a new sub-group or add the routes to the `mobileVerify` group's handler scope.

Best approach: add a new device-settings group with both middlewares. After the `mobileDevice` group (after line 273), add:

```go
		// ── Mobile device settings (MobileAuth + DeviceSignature) ────
		mobileDeviceSettings := v1.Group("/mobile/device")
		mobileDeviceSettings.Use(middleware.MobileAuthMiddleware(authClient))
		mobileDeviceSettings.Use(middleware.RequireDeviceSignature(authClient))
		{
			mobileAuthHandler := handler.NewMobileAuthHandler(authClient)
			mobileDeviceSettings.POST("/biometrics", mobileAuthHandler.SetBiometrics)
			mobileDeviceSettings.GET("/biometrics", mobileAuthHandler.GetBiometrics)
		}
```

This works because Gin matches routes with more specific paths first, and `/mobile/device/biometrics` won't conflict with `/mobile/device` (GET), `/mobile/device/deactivate` (POST), or `/mobile/device/transfer` (POST).

- [ ] **6.5** Verify build compiles

```bash
cd api-gateway && go build ./...
```

- [ ] **6.6** Regenerate Swagger docs

```bash
cd api-gateway && swag init -g cmd/main.go --output docs
```

---

## Task 7: Docker Compose + Config Updates

**Files:**
- `docker-compose.yml`
- `verification-service/go.mod` (may need `go mod tidy` after adding authpb dependency)

### Steps

- [ ] **7.1** Add `AUTH_GRPC_ADDR` to verification-service in `docker-compose.yml`

In the `verification-service` environment block (around line 590-601), add:

```yaml
      AUTH_GRPC_ADDR: "auth-service:50051"
```

- [ ] **7.2** Add `auth-service` to verification-service's `depends_on`

After the existing `depends_on` entries for verification-service (lines 605-609), add:

```yaml
      auth-service:
        condition: service_started
```

The full verification-service block should now look like:

```yaml
  verification-service:
    build:
      context: .
      dockerfile: verification-service/Dockerfile
    environment:
      VERIFICATION_DB_HOST: verification-db
      VERIFICATION_DB_PORT: "5432"
      VERIFICATION_DB_USER: postgres
      VERIFICATION_DB_PASSWORD: postgres
      VERIFICATION_DB_NAME: verificationdb
      VERIFICATION_GRPC_ADDR: ":50061"
      KAFKA_BROKERS: kafka:9092
      VERIFICATION_DB_SSLMODE: disable
      VERIFICATION_CHALLENGE_EXPIRY: "5m"
      VERIFICATION_MAX_ATTEMPTS: "3"
      METRICS_PORT: "9111"
      AUTH_GRPC_ADDR: "auth-service:50051"
    ports:
      - "50061:50061"
      - "9111:9111"
    depends_on:
      verification-db:
        condition: service_healthy
      kafka:
        condition: service_healthy
      auth-service:
        condition: service_started
```

- [ ] **7.3** Run `make tidy` to update go.mod files

```bash
make tidy
```

- [ ] **7.4** Verify full build

```bash
make build
```

---

## Task 8: Unit Tests

**Files:**
- `auth-service/internal/service/mobile_device_service_test.go`
- `verification-service/internal/service/verification_service_test.go`

### Steps

- [ ] **8.1** Add biometrics unit tests to auth-service

Add to `auth-service/internal/service/mobile_device_service_test.go`:

```go
// ============================================================
// Biometrics tests
// ============================================================

func TestSetBiometricsEnabled_Success(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceRepo := repository.NewMobileDeviceRepository(db)

	svc := &MobileDeviceService{deviceRepo: deviceRepo}

	device := seedActiveDevice(t, db, 1, "dev-bio-1", "secret123")

	// Enable biometrics
	err := svc.SetBiometricsEnabled(1, device.DeviceID, true)
	require.NoError(t, err)

	// Verify it's enabled
	var updated model.MobileDevice
	require.NoError(t, db.First(&updated, device.ID).Error)
	assert.True(t, updated.BiometricsEnabled)

	// Disable biometrics
	err = svc.SetBiometricsEnabled(1, device.DeviceID, false)
	require.NoError(t, err)

	require.NoError(t, db.First(&updated, device.ID).Error)
	assert.False(t, updated.BiometricsEnabled)
}

func TestSetBiometricsEnabled_WrongUser(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceRepo := repository.NewMobileDeviceRepository(db)

	svc := &MobileDeviceService{deviceRepo: deviceRepo}

	seedActiveDevice(t, db, 1, "dev-bio-2", "secret123")

	err := svc.SetBiometricsEnabled(999, "dev-bio-2", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not belong to user")
}

func TestSetBiometricsEnabled_InactiveDevice(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceRepo := repository.NewMobileDeviceRepository(db)

	svc := &MobileDeviceService{deviceRepo: deviceRepo}

	now := time.Now()
	device := &model.MobileDevice{
		UserID:       1,
		SystemType:   "client",
		DeviceID:     "dev-bio-3",
		DeviceSecret: "secret123",
		DeviceName:   "Test",
		Status:       "deactivated",
		DeactivatedAt: &now,
		LastSeenAt:   now,
	}
	require.NoError(t, db.Create(device).Error)

	err := svc.SetBiometricsEnabled(1, "dev-bio-3", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not active")
}

func TestGetBiometricsEnabled_Success(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceRepo := repository.NewMobileDeviceRepository(db)

	svc := &MobileDeviceService{deviceRepo: deviceRepo}

	device := seedActiveDevice(t, db, 1, "dev-bio-4", "secret123")

	// Default is false
	enabled, err := svc.GetBiometricsEnabled(1, device.DeviceID)
	require.NoError(t, err)
	assert.False(t, enabled)

	// Set to true and check
	device.BiometricsEnabled = true
	require.NoError(t, db.Save(device).Error)

	enabled, err = svc.GetBiometricsEnabled(1, device.DeviceID)
	require.NoError(t, err)
	assert.True(t, enabled)
}

func TestCheckBiometricsEnabled_Success(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceRepo := repository.NewMobileDeviceRepository(db)

	svc := &MobileDeviceService{deviceRepo: deviceRepo}

	device := seedActiveDevice(t, db, 1, "dev-bio-5", "secret123")
	device.BiometricsEnabled = true
	require.NoError(t, db.Save(device).Error)

	enabled, err := svc.CheckBiometricsEnabled(device.DeviceID)
	require.NoError(t, err)
	assert.True(t, enabled)
}

func TestCheckBiometricsEnabled_DeviceNotFound(t *testing.T) {
	db := setupMobileTestDB(t)
	deviceRepo := repository.NewMobileDeviceRepository(db)

	svc := &MobileDeviceService{deviceRepo: deviceRepo}

	_, err := svc.CheckBiometricsEnabled("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
```

- [ ] **8.2** Add biometric verification tests to verification-service

In `verification-service/internal/service/verification_service_test.go`, the existing tests use `nil` producer and construct the service directly. The new `VerifyByBiometric` method calls `s.authClient.CheckBiometricsEnabled`, so we need a mock auth client.

Add a mock auth client implementation at the top of the test file (after the helper functions):

```go
// mockAuthClient implements authpb.AuthServiceClient for testing.
// Only CheckBiometricsEnabled is used by VerifyByBiometric.
type mockAuthClient struct {
	authpb.AuthServiceClient // embed to satisfy interface for unused methods
	biometricsEnabled bool
	checkErr          error
}

func (m *mockAuthClient) CheckBiometricsEnabled(ctx context.Context, req *authpb.CheckBiometricsRequest, opts ...grpc.CallOption) (*authpb.CheckBiometricsResponse, error) {
	if m.checkErr != nil {
		return nil, m.checkErr
	}
	return &authpb.CheckBiometricsResponse{Enabled: m.biometricsEnabled}, nil
}
```

Add imports:

```go
	authpb "github.com/exbanka/contract/authpb"
	"google.golang.org/grpc"
```

Update `setupTestVerificationService` to accept an optional auth client:

```go
func setupTestVerificationServiceWithAuth(t *testing.T, authClient authpb.AuthServiceClient) (*VerificationService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.VerificationChallenge{}))

	repo := repository.NewVerificationChallengeRepository(db)
	svc := &VerificationService{
		repo:            repo,
		producer:        nil,
		db:              db,
		authClient:      authClient,
		challengeExpiry: 5 * time.Minute,
		maxAttempts:     3,
	}
	return svc, db
}
```

Add test functions:

```go
// ---------------------------------------------------------------------------
// VerifyByBiometric tests
// ---------------------------------------------------------------------------

func TestVerifyByBiometric_Success(t *testing.T) {
	mock := &mockAuthClient{biometricsEnabled: true}
	svc, db := setupTestVerificationServiceWithAuth(t, mock)

	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      100,
		Method:        "code_pull",
		Code:          "123456",
		ChallengeData: datatypes.JSON([]byte(`{}`)),
		Status:        "pending",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	err := svc.VerifyByBiometric(context.Background(), vc.ID, 1, "device-1")
	require.NoError(t, err)

	// Verify the challenge is now verified
	var updated model.VerificationChallenge
	require.NoError(t, db.First(&updated, vc.ID).Error)
	assert.Equal(t, "verified", updated.Status)
	assert.NotNil(t, updated.VerifiedAt)

	// Verify challenge data contains verified_by
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(updated.ChallengeData, &data))
	assert.Equal(t, "biometric", data["verified_by"])
}

func TestVerifyByBiometric_BiometricsDisabled(t *testing.T) {
	mock := &mockAuthClient{biometricsEnabled: false}
	svc, db := setupTestVerificationServiceWithAuth(t, mock)

	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      100,
		Method:        "code_pull",
		Code:          "123456",
		ChallengeData: datatypes.JSON([]byte(`{}`)),
		Status:        "pending",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	err := svc.VerifyByBiometric(context.Background(), vc.ID, 1, "device-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "biometrics not enabled")

	// Challenge should still be pending
	var updated model.VerificationChallenge
	require.NoError(t, db.First(&updated, vc.ID).Error)
	assert.Equal(t, "pending", updated.Status)
}

func TestVerifyByBiometric_WrongUser(t *testing.T) {
	mock := &mockAuthClient{biometricsEnabled: true}
	svc, db := setupTestVerificationServiceWithAuth(t, mock)

	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      100,
		Method:        "code_pull",
		Code:          "123456",
		ChallengeData: datatypes.JSON([]byte(`{}`)),
		Status:        "pending",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	err := svc.VerifyByBiometric(context.Background(), vc.ID, 999, "device-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not belong to this user")
}

func TestVerifyByBiometric_AlreadyVerified(t *testing.T) {
	mock := &mockAuthClient{biometricsEnabled: true}
	svc, db := setupTestVerificationServiceWithAuth(t, mock)

	now := time.Now()
	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      100,
		Method:        "code_pull",
		Code:          "123456",
		ChallengeData: datatypes.JSON([]byte(`{}`)),
		Status:        "verified",
		VerifiedAt:    &now,
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	err := svc.VerifyByBiometric(context.Background(), vc.ID, 1, "device-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already verified")
}

func TestVerifyByBiometric_Expired(t *testing.T) {
	mock := &mockAuthClient{biometricsEnabled: true}
	svc, db := setupTestVerificationServiceWithAuth(t, mock)

	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      100,
		Method:        "code_pull",
		Code:          "123456",
		ChallengeData: datatypes.JSON([]byte(`{}`)),
		Status:        "pending",
		ExpiresAt:     time.Now().Add(-1 * time.Minute),
		Version:       1,
	})

	err := svc.VerifyByBiometric(context.Background(), vc.ID, 1, "device-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestVerifyByBiometric_WorksForAllMethods(t *testing.T) {
	// Biometric verification should work for any challenge type
	methods := []string{"code_pull", "email", "qr_scan", "number_match"}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			mock := &mockAuthClient{biometricsEnabled: true}
			svc, db := setupTestVerificationServiceWithAuth(t, mock)

			vc := seedChallenge(t, db, &model.VerificationChallenge{
				UserID:        1,
				SourceService: "transaction",
				SourceID:      100,
				Method:        method,
				Code:          "123456",
				ChallengeData: datatypes.JSON([]byte(`{}`)),
				Status:        "pending",
				ExpiresAt:     time.Now().Add(5 * time.Minute),
				Version:       1,
			})

			err := svc.VerifyByBiometric(context.Background(), vc.ID, 1, "device-1")
			require.NoError(t, err)

			var updated model.VerificationChallenge
			require.NoError(t, db.First(&updated, vc.ID).Error)
			assert.Equal(t, "verified", updated.Status)
		})
	}
}
```

- [ ] **8.3** Update existing `setupTestVerificationService` to pass nil authClient

The existing helper should still work with `authClient: nil` since it's only used for non-biometric tests:

```go
func setupTestVerificationService(t *testing.T) (*VerificationService, *gorm.DB) {
	return setupTestVerificationServiceWithAuth(t, nil)
}
```

- [ ] **8.4** Run tests

```bash
cd auth-service && go test ./internal/service/ -run TestSetBiometricsEnabled -v
cd auth-service && go test ./internal/service/ -run TestGetBiometricsEnabled -v
cd auth-service && go test ./internal/service/ -run TestCheckBiometricsEnabled -v
cd verification-service && go test ./internal/service/ -run TestVerifyByBiometric -v
```

- [ ] **8.5** Run full test suite

```bash
make test
```

---

## Task 9: Update REST_API_v1.md

**Files:**
- `docs/api/REST_API_v1.md`

### Steps

- [ ] **9.1** Add "Biometric Device Settings" section

Under the Mobile Device Management section, add:

```markdown
### Set Biometrics Enabled

`POST /api/v1/mobile/device/biometrics`

**Authentication:** MobileAuthMiddleware + RequireDeviceSignature

**Request body:**
```json
{"enabled": true}
```

**Response (200):**
```json
{"success": true}
```

**Errors:**
- `400` — Invalid request body
- `403` — Device not found or not active
- `404` — Device not found

### Get Biometrics Status

`GET /api/v1/mobile/device/biometrics`

**Authentication:** MobileAuthMiddleware + RequireDeviceSignature

**Response (200):**
```json
{"enabled": false}
```

**Errors:**
- `403` — Device not found or not active
- `404` — Device not found
```

- [ ] **9.2** Add "Biometric Verification" section

Under the Mobile Verifications section, add:

```markdown
### Verify Challenge by Biometrics

`POST /api/v1/mobile/verifications/:challenge_id/biometric`

**Authentication:** MobileAuthMiddleware + RequireDeviceSignature

**Request body:** None. The device signature in the request headers IS the authentication proof. The mobile app must prompt the user for local biometric auth (FaceID, fingerprint) before making this call.

**Response (200):**
```json
{"success": true}
```

**Errors:**
- `400` — Invalid challenge ID
- `403` — Biometrics not enabled for this device, or challenge does not belong to user
- `409` — Challenge already verified, expired, or failed
- `404` — Challenge not found
```

---

## Task 10: Update Specification.md

**Files:**
- `Specification.md`

### Steps

- [ ] **10.1** Add biometric verification to the verification section
- [ ] **10.2** Add `BiometricsEnabled` field to MobileDevice entity
- [ ] **10.3** Add new API routes to the routes section
- [ ] **10.4** Add new gRPC RPCs to the gRPC section

---

## Commit Sequence

1. **Commit 1** (Tasks 1-3): `feat(auth): add BiometricsEnabled field and gRPC methods for mobile biometrics`
2. **Commit 2** (Tasks 4-5): `feat(verification): add VerifyByBiometric method for biometric verification bypass`
3. **Commit 3** (Task 6): `feat(gateway): add biometric verification and biometrics toggle endpoints`
4. **Commit 4** (Task 7): `chore(infra): add AUTH_GRPC_ADDR to verification-service docker-compose`
5. **Commit 5** (Task 8): `test: add unit tests for biometric verification`
6. **Commit 6** (Tasks 9-10): `docs: add biometric verification to REST_API_v1.md and Specification.md`

---

## Verification Checklist

After all tasks are complete:

- [ ] `make proto` succeeds
- [ ] `make build` succeeds (all services compile)
- [ ] `make tidy` produces no changes
- [ ] `make test` passes (all existing + new tests)
- [ ] `make swagger` produces no diff (swagger already regenerated)
- [ ] `docker-compose.yml` has `AUTH_GRPC_ADDR` in verification-service
- [ ] `Specification.md` updated with new endpoints and model changes
- [ ] `docs/api/REST_API_v1.md` updated with biometric endpoints

---

## Key Design Decisions

1. **No request body for biometric verify** -- the device signature (HMAC-SHA256) validated by `RequireDeviceSignature` middleware IS the proof that the user holds the device with the secret. The local biometric prompt (FaceID/fingerprint) is the app's responsibility; the backend only cares that the device signature is valid.

2. **Auth-service round-trip from verification-service** -- verification-service calls auth-service's `CheckBiometricsEnabled` RPC rather than duplicating the device table. This keeps the `MobileDevice` model authoritative in auth-service. The latency of one gRPC call is acceptable since biometric verify is a one-shot action, not a polling endpoint.

3. **`verified_by: "biometric"` in ChallengeData** -- stored in the existing JSONB `challenge_data` column for audit trail. No schema change needed. Downstream consumers of `verification.challenge-verified` Kafka events see the same event shape; the extra context is in the DB only.

4. **Biometrics toggle requires device signature** -- both `POST /biometrics` and `GET /biometrics` require `RequireDeviceSignature`, so an attacker who steals a JWT but not the device secret cannot enable biometrics. This matches the security model: biometrics operations are device-bound.

5. **Works for ALL challenge types** -- biometric verify bypasses the type-specific validation (code matching, QR token, number selection). It works equally for `code_pull`, `email`, `qr_scan`, and `number_match` challenges. The test `TestVerifyByBiometric_WorksForAllMethods` confirms this.
