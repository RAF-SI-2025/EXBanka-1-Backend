# Transaction Migration & API Surface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate transaction-service from inline verification to verification-service, add all mobile/verification API routes to gateway, seed new permissions, update all documentation, and create mobile app integration guide.

**Architecture:** Transaction-service replaces its inline verification code logic with gRPC calls to verification-service. API gateway gets new handler files for mobile auth, mobile verification, and verification management. New permissions (verification.skip, verification.manage) are seeded via user-service role system. Documentation is updated across REST_API.md, Specification.md, CLAUDE.md, and a new mobile integration guide.

**Tech Stack:** Go 1.26, gRPC/Protobuf, Gin (HTTP), Markdown

**Depends on:**
- Plan 7 (verification-service) — gRPC client for CreateChallenge/GetChallengeStatus/SubmitCode
- Plan 8 (mobile-device-auth) — mobile auth proto RPCs, ValidateDeviceSignature
- Plan 9 (notification-delivery) — mobile inbox proto RPCs, MobileAuthMiddleware, RequireDeviceSignature, WebSocket handler

---

## File Structure

### New files to create

```
api-gateway/
├── internal/
│   ├── grpc/
│   │   └── verification_client.go        # Verification-service gRPC client
│   ├── handler/
│   │   ├── mobile_auth_handler.go        # Mobile auth gateway handlers
│   │   └── verification_handler.go       # Verification gateway handlers
docs/
└── mobile/
    └── MOBILE_APP_INTEGRATION.md         # Mobile team integration guide
```

### Files to modify

```
api-gateway/internal/config/config.go              # Add VerificationGRPCAddr
api-gateway/internal/router/router.go               # Register all new routes
api-gateway/cmd/main.go                             # Wire new clients and handlers
transaction-service/internal/config/config.go       # Add VerificationGRPCAddr
transaction-service/internal/handler/grpc_handler.go # Replace inline verification with gRPC calls
transaction-service/cmd/main.go                     # Wire verification client
user-service/internal/service/role_service.go       # Add new permissions
docs/api/REST_API.md                                # Document new endpoints
Specification.md                                    # Add verification-service, models, topics
CLAUDE.md                                           # Add verification-service section
docker-compose.yml                                  # Final wiring
```

### Files to delete

```
transaction-service/internal/model/verification_code.go
transaction-service/internal/repository/verification_code_repository.go
transaction-service/internal/service/verification_service.go
transaction-service/internal/service/verification_service_test.go
```

---

## Task 1: Create verification gRPC client in gateway

**Files:**
- Create: `api-gateway/internal/grpc/verification_client.go`

- [ ] **Step 1: Write the client**

```go
package grpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	verificationpb "github.com/exbanka/contract/verificationpb"
)

func NewVerificationClient(addr string) (verificationpb.VerificationGRPCServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return verificationpb.NewVerificationGRPCServiceClient(conn), conn, nil
}
```

- [ ] **Step 2: Commit**

```bash
git add api-gateway/internal/grpc/verification_client.go
git commit -m "feat(api-gateway): add verification-service gRPC client"
```

---

## Task 2: Create mobile auth gateway handler

**Files:**
- Create: `api-gateway/internal/handler/mobile_auth_handler.go`

- [ ] **Step 1: Write the handler**

```go
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	authpb "github.com/exbanka/contract/authpb"
)

type MobileAuthHandler struct {
	authClient authpb.AuthServiceClient
}

func NewMobileAuthHandler(authClient authpb.AuthServiceClient) *MobileAuthHandler {
	return &MobileAuthHandler{authClient: authClient}
}

type requestActivationReq struct {
	Email string `json:"email" binding:"required,email"`
}

// @Summary Request mobile app activation code
// @Tags mobile-auth
// @Accept json
// @Produce json
// @Param body body requestActivationReq true "Email"
// @Success 200 {object} gin.H
// @Router /api/mobile/auth/request-activation [post]
func (h *MobileAuthHandler) RequestActivation(c *gin.Context) {
	var req requestActivationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	resp, err := h.authClient.RequestMobileActivation(c.Request.Context(), &authpb.MobileActivationRequest{
		Email: req.Email,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": resp.Success, "message": resp.Message})
}

type activateDeviceReq struct {
	Email      string `json:"email" binding:"required,email"`
	Code       string `json:"code" binding:"required"`
	DeviceName string `json:"device_name" binding:"required"`
}

// @Summary Activate mobile device with code
// @Tags mobile-auth
// @Accept json
// @Produce json
// @Param body body activateDeviceReq true "Activation data"
// @Success 200 {object} gin.H
// @Failure 400 {object} gin.H
// @Router /api/mobile/auth/activate [post]
func (h *MobileAuthHandler) ActivateDevice(c *gin.Context) {
	var req activateDeviceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	if len(req.Code) != 6 {
		apiError(c, http.StatusBadRequest, ErrValidation, "code must be 6 digits")
		return
	}

	resp, err := h.authClient.ActivateMobileDevice(c.Request.Context(), &authpb.ActivateMobileDeviceRequest{
		Email:      req.Email,
		Code:       req.Code,
		DeviceName: req.DeviceName,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"access_token":  resp.AccessToken,
		"refresh_token": resp.RefreshToken,
		"device_id":     resp.DeviceId,
		"device_secret": resp.DeviceSecret,
	})
}

type refreshMobileReq struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// @Summary Refresh mobile token
// @Tags mobile-auth
// @Accept json
// @Produce json
// @Param body body refreshMobileReq true "Refresh token"
// @Success 200 {object} gin.H
// @Router /api/mobile/auth/refresh [post]
func (h *MobileAuthHandler) RefreshMobileToken(c *gin.Context) {
	var req refreshMobileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	deviceID := c.GetHeader("X-Device-ID")
	if deviceID == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "X-Device-ID header required")
		return
	}

	resp, err := h.authClient.RefreshMobileToken(c.Request.Context(), &authpb.RefreshMobileTokenRequest{
		RefreshToken: req.RefreshToken,
		DeviceId:     deviceID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"access_token":  resp.AccessToken,
		"refresh_token": resp.RefreshToken,
	})
}

// @Summary Get current device info
// @Tags mobile-device
// @Produce json
// @Security BearerAuth
// @Success 200 {object} gin.H
// @Router /api/mobile/device [get]
func (h *MobileAuthHandler) GetDeviceInfo(c *gin.Context) {
	userID := c.GetInt64("user_id")
	resp, err := h.authClient.GetDeviceInfo(c.Request.Context(), &authpb.GetDeviceInfoRequest{
		UserId: userID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"device_id":    resp.DeviceId,
		"device_name":  resp.DeviceName,
		"status":       resp.Status,
		"activated_at": resp.ActivatedAt,
		"last_seen_at": resp.LastSeenAt,
	})
}

// @Summary Deactivate current device
// @Tags mobile-device
// @Produce json
// @Security BearerAuth
// @Success 200 {object} gin.H
// @Router /api/mobile/device/deactivate [post]
func (h *MobileAuthHandler) DeactivateDevice(c *gin.Context) {
	userID := c.GetInt64("user_id")
	deviceID, _ := c.Get("device_id")
	resp, err := h.authClient.DeactivateDevice(c.Request.Context(), &authpb.DeactivateDeviceRequest{
		UserId:   userID,
		DeviceId: deviceID.(string),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": resp.Success})
}

type transferDeviceReq struct {
	Email string `json:"email" binding:"required,email"`
}

// @Summary Transfer device (deactivate current + send new activation code)
// @Tags mobile-device
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} gin.H
// @Router /api/mobile/device/transfer [post]
func (h *MobileAuthHandler) TransferDevice(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req transferDeviceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	resp, err := h.authClient.TransferDevice(c.Request.Context(), &authpb.TransferDeviceRequest{
		UserId: userID,
		Email:  req.Email,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": resp.Success, "message": resp.Message})
}
```

- [ ] **Step 2: Commit**

```bash
git add api-gateway/internal/handler/mobile_auth_handler.go
git commit -m "feat(api-gateway): add mobile auth gateway handler"
```

---

## Task 3: Create verification gateway handler

**Files:**
- Create: `api-gateway/internal/handler/verification_handler.go`

- [ ] **Step 1: Write the handler**

```go
package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	notificationpb "github.com/exbanka/contract/notificationpb"
	verificationpb "github.com/exbanka/contract/verificationpb"
)

type VerificationHandler struct {
	verificationClient verificationpb.VerificationGRPCServiceClient
	notificationClient notificationpb.NotificationServiceClient
}

func NewVerificationHandler(
	vc verificationpb.VerificationGRPCServiceClient,
	nc notificationpb.NotificationServiceClient,
) *VerificationHandler {
	return &VerificationHandler{verificationClient: vc, notificationClient: nc}
}

type createVerificationReq struct {
	SourceService string `json:"source_service" binding:"required"`
	SourceID      uint64 `json:"source_id" binding:"required"`
	Method        string `json:"method"`
}

// @Summary Create verification challenge
// @Tags verifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} gin.H
// @Router /api/verifications [post]
func (h *VerificationHandler) CreateVerification(c *gin.Context) {
	var req createVerificationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	if req.Method == "" {
		req.Method = "code_pull"
	}
	method, err := oneOf("method", req.Method, "code_pull", "qr_scan", "number_match", "email")
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	userID := c.GetInt64("user_id")
	deviceID, _ := c.Get("device_id")
	deviceIDStr := ""
	if deviceID != nil {
		deviceIDStr = deviceID.(string)
	}

	resp, err := h.verificationClient.CreateChallenge(c.Request.Context(), &verificationpb.CreateChallengeRequest{
		UserId:        uint64(userID),
		SourceService: req.SourceService,
		SourceId:      req.SourceID,
		Method:        method,
		DeviceId:      deviceIDStr,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"challenge_id":   resp.ChallengeId,
		"challenge_data": resp.ChallengeData,
		"expires_at":     resp.ExpiresAt,
	})
}

// @Summary Get verification challenge status
// @Tags verifications
// @Produce json
// @Security BearerAuth
// @Param id path int true "Challenge ID"
// @Success 200 {object} gin.H
// @Router /api/verifications/{id}/status [get]
func (h *VerificationHandler) GetVerificationStatus(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid challenge id")
		return
	}

	resp, err := h.verificationClient.GetChallengeStatus(c.Request.Context(), &verificationpb.GetChallengeStatusRequest{
		ChallengeId: id,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":      resp.Status,
		"method":      resp.Method,
		"verified_at": resp.VerifiedAt,
		"expires_at":  resp.ExpiresAt,
	})
}

type submitCodeReq struct {
	Code string `json:"code" binding:"required"`
}

// @Summary Submit verification code (browser, code_pull method)
// @Tags verifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Challenge ID"
// @Success 200 {object} gin.H
// @Router /api/verifications/{id}/code [post]
func (h *VerificationHandler) SubmitVerificationCode(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid challenge id")
		return
	}

	var req submitCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	resp, err := h.verificationClient.SubmitCode(c.Request.Context(), &verificationpb.SubmitCodeRequest{
		ChallengeId: id,
		Code:        req.Code,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":            resp.Success,
		"remaining_attempts": resp.RemainingAttempts,
	})
}

// @Summary Get pending mobile verifications (mobile polling)
// @Tags mobile-verifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} gin.H
// @Router /api/mobile/verifications/pending [get]
func (h *VerificationHandler) GetPendingVerifications(c *gin.Context) {
	userID := c.GetInt64("user_id")
	deviceID, _ := c.Get("device_id")

	resp, err := h.notificationClient.GetPendingMobileItems(c.Request.Context(), &notificationpb.GetPendingMobileRequest{
		UserId:   uint64(userID),
		DeviceId: deviceID.(string),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	items := make([]gin.H, len(resp.Items))
	for i, item := range resp.Items {
		items[i] = gin.H{
			"id":           item.Id,
			"challenge_id": item.ChallengeId,
			"method":       item.Method,
			"display_data": item.DisplayData,
			"expires_at":   item.ExpiresAt,
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type submitMobileVerificationReq struct {
	Response string `json:"response" binding:"required"`
}

// @Summary Submit mobile verification response
// @Tags mobile-verifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param challenge_id path int true "Challenge ID"
// @Success 200 {object} gin.H
// @Router /api/mobile/verifications/{challenge_id}/submit [post]
func (h *VerificationHandler) SubmitMobileVerification(c *gin.Context) {
	challengeID, err := strconv.ParseUint(c.Param("challenge_id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid challenge id")
		return
	}

	var req submitMobileVerificationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	deviceID, _ := c.Get("device_id")
	resp, err := h.verificationClient.SubmitVerification(c.Request.Context(), &verificationpb.SubmitVerificationRequest{
		ChallengeId: challengeID,
		DeviceId:    deviceID.(string),
		Response:    req.Response,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":            resp.Success,
		"remaining_attempts": resp.RemainingAttempts,
	})
}

// @Summary QR code verification (mobile scans QR)
// @Tags mobile-verifications
// @Produce json
// @Security BearerAuth
// @Param challenge_id path int true "Challenge ID"
// @Param token query string true "QR verification token"
// @Success 200 {object} gin.H
// @Router /api/verify/{challenge_id} [post]
func (h *VerificationHandler) VerifyQR(c *gin.Context) {
	challengeID, err := strconv.ParseUint(c.Param("challenge_id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid challenge id")
		return
	}

	token := c.Query("token")
	if token == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "token query parameter required")
		return
	}

	deviceID, _ := c.Get("device_id")
	resp, err := h.verificationClient.SubmitVerification(c.Request.Context(), &verificationpb.SubmitVerificationRequest{
		ChallengeId: challengeID,
		DeviceId:    deviceID.(string),
		Response:    token,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": resp.Success})
}
```

- [ ] **Step 2: Commit**

```bash
git add api-gateway/internal/handler/verification_handler.go
git commit -m "feat(api-gateway): add verification gateway handler with browser + mobile + QR endpoints"
```

---

## Task 4: Update gateway config, main.go, and router

**Files:**
- Modify: `api-gateway/internal/config/config.go`
- Modify: `api-gateway/cmd/main.go`
- Modify: `api-gateway/internal/router/router.go`

- [ ] **Step 1: Add VerificationGRPCAddr to config**

```go
VerificationGRPCAddr string  // Default: "localhost:50060"
```

In `Load()`: `VerificationGRPCAddr: getEnv("VERIFICATION_GRPC_ADDR", "localhost:50060")`

- [ ] **Step 2: Update main.go — create verification client**

After existing client creation:

```go
verificationClient, verificationConn, err := grpcclients.NewVerificationClient(cfg.VerificationGRPCAddr)
if err != nil {
	log.Fatalf("failed to connect to verification service: %v", err)
}
defer verificationConn.Close()
```

Create WebSocket handler and start Kafka consumer:

```go
wsHandler := handler.NewWebSocketHandler(authClient)
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
wsHandler.StartKafkaConsumer(ctx, cfg.KafkaBrokers)
```

Update the `router.Setup()` call to pass the new clients:

```go
r := router.Setup(
	authClient,
	// ... existing clients ...
	verificationClient,
	wsHandler,
)
```

- [ ] **Step 3: Update router.go — register all new routes**

Update the `Setup` function signature to accept new parameters. Add route groups:

```go
// --- Mobile Auth (public, no auth required) ---
mobileAuth := api.Group("/mobile/auth")
{
	mobileAuthHandler := handler.NewMobileAuthHandler(authClient)
	mobileAuth.POST("/request-activation", mobileAuthHandler.RequestActivation)
	mobileAuth.POST("/activate", mobileAuthHandler.ActivateDevice)
	mobileAuth.POST("/refresh", mobileAuthHandler.RefreshMobileToken)
}

// --- Mobile Device Management (MobileAuthMiddleware) ---
mobileDevice := api.Group("/mobile/device")
mobileDevice.Use(middleware.MobileAuthMiddleware(authClient))
{
	mobileAuthHandler := handler.NewMobileAuthHandler(authClient)
	mobileDevice.GET("", mobileAuthHandler.GetDeviceInfo)
	mobileDevice.POST("/deactivate", mobileAuthHandler.DeactivateDevice)
	mobileDevice.POST("/transfer", mobileAuthHandler.TransferDevice)
}

// --- Mobile Verifications (MobileAuthMiddleware + RequireDeviceSignature) ---
mobileVerify := api.Group("/mobile/verifications")
mobileVerify.Use(middleware.MobileAuthMiddleware(authClient))
mobileVerify.Use(middleware.RequireDeviceSignature(authClient))
{
	verifyHandler := handler.NewVerificationHandler(verificationClient, notificationClient)
	mobileVerify.GET("/pending", verifyHandler.GetPendingVerifications)
	mobileVerify.POST("/:challenge_id/submit", verifyHandler.SubmitMobileVerification)
}

// --- QR Verification (MobileAuthMiddleware + RequireDeviceSignature) ---
qrVerify := api.Group("/verify")
qrVerify.Use(middleware.MobileAuthMiddleware(authClient))
qrVerify.Use(middleware.RequireDeviceSignature(authClient))
{
	verifyHandler := handler.NewVerificationHandler(verificationClient, notificationClient)
	qrVerify.POST("/:challenge_id", verifyHandler.VerifyQR)
}

// --- WebSocket (handled specially) ---
api.GET("/ws/mobile", wsHandler.HandleConnect)

// --- Browser-facing Verifications (AnyAuthMiddleware) ---
verifications := api.Group("/verifications")
verifications.Use(middleware.AnyAuthMiddleware(authClient))
{
	verifyHandler := handler.NewVerificationHandler(verificationClient, notificationClient)
	verifications.POST("", verifyHandler.CreateVerification)
	verifications.GET("/:id/status", verifyHandler.GetVerificationStatus)
	verifications.POST("/:id/code", verifyHandler.SubmitVerificationCode)
}

// --- Verification Settings (AuthMiddleware + RequirePermission) ---
verificationSettings := protected.Group("/verifications")
verificationSettings.Use(middleware.RequirePermission("verification.manage"))
{
	// Settings endpoints — TODO: implement in verification-service if needed
	// For now, the permission seeding is the configurable part
}
```

- [ ] **Step 4: Verify build**

```bash
cd api-gateway && go build ./cmd
```

- [ ] **Step 5: Commit**

```bash
git add api-gateway/internal/config/config.go api-gateway/cmd/main.go api-gateway/internal/router/router.go
git commit -m "feat(api-gateway): wire verification client, WebSocket, and all mobile/verification routes"
```

---

## Task 5: Seed new permissions in user-service

**Files:**
- Modify: `user-service/internal/service/role_service.go`

- [ ] **Step 1: Add verification permissions to DefaultRolePermissions**

Add `"verification.skip"` and `"verification.manage"` to `EmployeeSupervisor` and `EmployeeAdmin` permission lists:

In the `EmployeeSupervisor` array, add:
```go
"verification.skip", "verification.manage",
```

In the `EmployeeAdmin` array, add:
```go
"verification.skip", "verification.manage",
```

- [ ] **Step 2: Add to AllPermissions list**

Find the `AllPermissions` slice and add:

```go
{Code: "verification.skip", Desc: "Skip mobile verification for transactions", Category: "verification"},
{Code: "verification.manage", Desc: "Manage verification settings per role", Category: "verification"},
```

- [ ] **Step 3: Verify build**

```bash
cd user-service && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add user-service/internal/service/role_service.go
git commit -m "feat(user-service): seed verification.skip and verification.manage permissions"
```

---

## Task 6: Migrate transaction-service to use verification-service

**Files:**
- Modify: `transaction-service/internal/config/config.go`
- Modify: `transaction-service/internal/handler/grpc_handler.go`
- Modify: `transaction-service/cmd/main.go`
- Delete: `transaction-service/internal/model/verification_code.go`
- Delete: `transaction-service/internal/repository/verification_code_repository.go`
- Delete: `transaction-service/internal/service/verification_service.go`
- Delete: `transaction-service/internal/service/verification_service_test.go`

- [ ] **Step 1: Add VerificationGRPCAddr to transaction-service config**

```go
VerificationGRPCAddr string
```

In `Load()`: `VerificationGRPCAddr: getEnv("VERIFICATION_GRPC_ADDR", "localhost:50060")`

- [ ] **Step 2: Update transaction handler — replace inline verification with gRPC calls**

In `grpc_handler.go`, replace the verification code generation in `CreatePayment` and `CreateTransfer` with:

```go
// Instead of generating and storing a verification code inline:
// OLD: code, _ := h.verificationSvc.CreateVerificationCode(...)
// NEW: Just create the payment in pending_verification status.
// The gateway or browser will call verification-service to create the challenge.
```

In `ExecutePayment` and `ExecuteTransfer`, replace the inline validation with a call to verification-service:

```go
// Instead of validating inline:
// OLD: valid, remaining, err := h.verificationSvc.ValidateVerificationCode(...)
// NEW: Check challenge status via verification-service
verifyResp, err := h.verificationClient.GetChallengeStatus(ctx, &verificationpb.GetChallengeStatusRequest{
	ChallengeId: req.ChallengeId,
})
if err != nil || verifyResp.Status != "verified" {
	return nil, status.Errorf(codes.FailedPrecondition, "verification not completed")
}
```

Add `verificationClient` field to the handler struct and update the constructor.

- [ ] **Step 3: Update transaction main.go — wire verification client**

```go
verificationConn, err := grpc.NewClient(cfg.VerificationGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
if err != nil {
	log.Fatalf("failed to connect to verification service: %v", err)
}
defer verificationConn.Close()
verificationClient := verificationpb.NewVerificationGRPCServiceClient(verificationConn)
```

Pass `verificationClient` to the handler constructor. Remove `verificationSvc` parameter.

- [ ] **Step 4: Remove VerificationCode from AutoMigrate**

Remove `&model.VerificationCode{}` from the `db.AutoMigrate(...)` call.

- [ ] **Step 5: Delete old verification files**

```bash
rm transaction-service/internal/model/verification_code.go
rm transaction-service/internal/repository/verification_code_repository.go
rm transaction-service/internal/service/verification_service.go
rm transaction-service/internal/service/verification_service_test.go
```

- [ ] **Step 6: Verify build**

```bash
cd transaction-service && go build ./cmd
```

- [ ] **Step 7: Commit**

```bash
git add -A transaction-service/
git commit -m "feat(transaction-service): migrate from inline verification to verification-service gRPC"
```

---

## Task 7: Create mobile app integration guide

**Files:**
- Create: `docs/mobile/MOBILE_APP_INTEGRATION.md`

- [ ] **Step 1: Write the integration guide**

This file should contain the complete guide for the mobile team, covering:

1. **Authentication Flow** — step-by-step activation with request/response examples
2. **Token Management** — refresh flow, expiry handling, deactivation detection
3. **Secure Storage** — device_secret in iOS Keychain / Android Keystore
4. **Request Signing** — exact HMAC formula: `HMAC-SHA256(device_secret, timestamp + ":" + method + ":" + path + ":" + sha256(body))`, which headers to send, code examples
5. **Verification Flows** — for each method:
   - code_pull: poll → display code → user types in browser (or biometric auto-submit)
   - qr_scan: scan QR → extract URL → sign request → POST
   - number_match: poll → display 5 options → user picks → submit
6. **WebSocket** — connect URL, auth params, message format, reconnection strategy
7. **Polling Fallback** — intervals (2s foreground, 30s background), endpoint
8. **Biometric UX** — for code_pull: get code → prompt biometric → auto-submit code
9. **Error Codes** — all error responses with handling guidance
10. **Full Examples** — curl-equivalent request/response for every endpoint

Write this as a comprehensive Markdown document (aim for 300-500 lines).

- [ ] **Step 2: Commit**

```bash
git add docs/mobile/MOBILE_APP_INTEGRATION.md
git commit -m "docs: add mobile app integration guide for mobile team"
```

---

## Task 8: Update REST_API.md

**Files:**
- Modify: `docs/api/REST_API.md`

- [ ] **Step 1: Add new endpoint sections**

Add documentation for all new endpoints following the existing format:
- Mobile Auth: `/api/mobile/auth/request-activation`, `/api/mobile/auth/activate`, `/api/mobile/auth/refresh`
- Mobile Device: `/api/mobile/device`, `/api/mobile/device/deactivate`, `/api/mobile/device/transfer`
- Mobile Verifications: `/api/mobile/verifications/pending`, `/api/mobile/verifications/:id/submit`
- QR Verification: `/api/verify/:challenge_id`
- Browser Verifications: `/api/verifications`, `/api/verifications/:id/status`, `/api/verifications/:id/code`
- Verification Settings: `/api/verifications/settings`
- WebSocket: `/ws/mobile`

- [ ] **Step 2: Update payment/transfer sections**

Add `method` optional field to payment and transfer creation request bodies. Document the new verification flow.

- [ ] **Step 3: Commit**

```bash
git add docs/api/REST_API.md
git commit -m "docs: add mobile verification endpoints to REST_API.md"
```

---

## Task 9: Update Specification.md and CLAUDE.md

**Files:**
- Modify: `Specification.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Update Specification.md**

Add:
- verification-service to service list (port 50060, DB port 5440)
- VerificationChallenge entity description
- MobileDevice entity description
- MobileInboxItem entity description
- New Kafka topics (verification.challenge-created, .verified, .failed, notification.mobile-push)
- New permissions (verification.skip, verification.manage)
- Updated transaction verification flow description

- [ ] **Step 2: Update CLAUDE.md**

Add:
- verification-service to repository layout (gRPC port 50060)
- notification-service DB port 5441 to env table
- VERIFICATION_GRPC_ADDR, MOBILE_REFRESH_EXPIRY, MOBILE_ACTIVATION_EXPIRY to env table
- Update architecture diagram: verification-service
- Add verification.skip and verification.manage to permission notes
- Add `verification_method` to enum values list: `code_pull`, `qr_scan`, `number_match`, `email`

- [ ] **Step 3: Commit**

```bash
git add Specification.md CLAUDE.md
git commit -m "docs: update Specification.md and CLAUDE.md with verification-service and mobile auth"
```

---

## Task 10: Docker-compose final updates

**Files:**
- Modify: `docker-compose.yml`

- [ ] **Step 1: Add verification-service and its DB**

```yaml
  verification_db:
    image: postgres:16
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: postgres
      POSTGRES_DB: verification_db
    ports:
      - "5440:5432"
    volumes:
      - verification_db_data:/var/lib/postgresql/data

  verification-service:
    build: ./verification-service
    environment:
      VERIFICATION_DB_HOST: verification_db
      VERIFICATION_DB_PORT: "5432"
      VERIFICATION_DB_USER: postgres
      VERIFICATION_DB_PASSWORD: postgres
      VERIFICATION_DB_NAME: verification_db
      VERIFICATION_GRPC_ADDR: ":50060"
      KAFKA_BROKERS: kafka:9092
    ports:
      - "50060:50060"
    depends_on:
      - verification_db
      - kafka
```

- [ ] **Step 2: Add VERIFICATION_GRPC_ADDR to api-gateway and transaction-service**

```yaml
  api-gateway:
    environment:
      VERIFICATION_GRPC_ADDR: "verification-service:50060"
      # ... existing vars

  transaction-service:
    environment:
      VERIFICATION_GRPC_ADDR: "verification-service:50060"
      # ... existing vars
```

- [ ] **Step 3: Add volumes**

```yaml
volumes:
  verification_db_data:
  notification_db_data:
```

- [ ] **Step 4: Commit**

```bash
git add docker-compose.yml
git commit -m "chore: add verification-service and final wiring to docker-compose"
```

---

## Task 11: Verify full build

- [ ] **Step 1: Tidy all modules**

```bash
make tidy
```

- [ ] **Step 2: Build all services**

```bash
make build
```

Expected: All services build successfully.

- [ ] **Step 3: Commit any dependency changes**

```bash
git add -A
git diff --cached --stat
git commit -m "chore: final dependency updates after mobile verification integration"
```

---

## Design Notes

### Migration Strategy

The transaction-service migration is designed to be non-breaking:
1. Payment/transfer creation still returns `pending_verification` status
2. The Execute endpoints now check verification-service instead of inline codes
3. The gateway handles verification challenge creation (browser calls `/api/verifications`)
4. Email fallback preserves existing UX for users without mobile devices

### Permission Model

- `verification.skip` — assigned to EmployeeSupervisor and EmployeeAdmin by default
- `verification.manage` — assigned to EmployeeSupervisor and EmployeeAdmin by default
- Clients NEVER get `verification.skip` (must always verify)
- The gateway checks this permission before creating a challenge

### What This Plan Covers (Complete List)

1. Gateway verification gRPC client
2. Gateway mobile auth handler (6 endpoints)
3. Gateway verification handler (7 endpoints including QR)
4. Gateway config + main.go + router wiring
5. Permission seeding in user-service
6. Transaction-service migration to verification-service
7. Mobile app integration guide
8. REST_API.md updates
9. Specification.md + CLAUDE.md updates
10. Docker-compose final wiring
