# Mobile Notification Delivery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend notification-service with mobile inbox (DB + Kafka consumer + gRPC polling), add WebSocket real-time push and mobile-specific middleware to api-gateway.

**Architecture:** Notification-service gets a PostgreSQL database for MobileInboxItem storage. A new Kafka consumer handles `verification.challenge-created` events, routing to SMTP (email method) or mobile inbox (mobile methods). API gateway gets MobileAuthMiddleware (device-bound JWT validation), RequireDeviceSignature middleware (HMAC verification), and a WebSocket endpoint for real-time mobile push.

**Tech Stack:** Go 1.26, gRPC/Protobuf, GORM/PostgreSQL, gorilla/websocket, Kafka (segmentio/kafka-go)

**Depends on:**
- Plan 7 (verification-service) — Kafka topic `verification.challenge-created` and message format
- Plan 8 (mobile-device-auth) — device-bound JWT claims (device_type, device_id), ValidateDeviceSignature RPC

---

## File Structure

### New files to create

```
notification-service/
├── internal/
│   ├── model/
│   │   └── mobile_inbox_item.go           # MobileInboxItem entity
│   ├── repository/
│   │   └── mobile_inbox_repository.go     # Mobile inbox CRUD
│   ├── service/
│   │   └── inbox_cleanup.go              # Background expired item cleanup
│   └── consumer/
│       └── verification_consumer.go      # Kafka consumer for challenge events

api-gateway/
├── internal/
│   ├── middleware/
│   │   └── mobile_auth.go               # MobileAuthMiddleware + RequireDeviceSignature
│   └── handler/
│       └── websocket_handler.go          # WebSocket endpoint for mobile push
```

### Files to modify

```
notification-service/internal/config/config.go         # Add DB config
notification-service/internal/handler/grpc_handler.go   # Add mobile inbox RPCs
notification-service/cmd/main.go                        # Wire DB, repos, consumers, cleanup
contract/proto/notification/notification.proto           # Add mobile inbox RPCs
docker-compose.yml                                      # Add notification_db
```

---

## Task 1: Add database support to notification-service config

**Files:**
- Modify: `notification-service/internal/config/config.go`

- [ ] **Step 1: Add DB fields to Config struct**

```go
package config

import (
	"fmt"
	"os"
)

type Config struct {
	GRPCAddr     string
	KafkaBrokers string
	SMTPHost     string
	SMTPPort     string
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string
	// New: database for mobile inbox
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
}

func Load() *Config {
	return &Config{
		GRPCAddr:     getEnv("NOTIFICATION_GRPC_ADDR", ":50053"),
		KafkaBrokers: getEnv("KAFKA_BROKERS", "localhost:9092"),
		SMTPHost:     getEnv("SMTP_HOST", "smtp.gmail.com"),
		SMTPPort:     getEnv("SMTP_PORT", "587"),
		SMTPUser:     getEnv("SMTP_USER", ""),
		SMTPPassword: getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:     getEnv("SMTP_FROM", ""),
		DBHost:       getEnv("NOTIFICATION_DB_HOST", "localhost"),
		DBPort:       getEnv("NOTIFICATION_DB_PORT", "5441"),
		DBUser:       getEnv("NOTIFICATION_DB_USER", "postgres"),
		DBPassword:   getEnv("NOTIFICATION_DB_PASSWORD", "postgres"),
		DBName:       getEnv("NOTIFICATION_DB_NAME", "notification_db"),
	}
}

func (c *Config) DSN() string {
	sslmode := getEnv("NOTIFICATION_DB_SSLMODE", "disable")
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, sslmode)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

- [ ] **Step 2: Commit**

```bash
git add notification-service/internal/config/config.go
git commit -m "feat(notification-service): add database config for mobile inbox"
```

---

## Task 2: Create MobileInboxItem model

**Files:**
- Create: `notification-service/internal/model/mobile_inbox_item.go`

- [ ] **Step 1: Write the model**

```go
package model

import (
	"time"

	"gorm.io/datatypes"
)

// MobileInboxItem stores a pending mobile notification for a specific device.
// Items auto-expire and are cleaned up by a background goroutine.
type MobileInboxItem struct {
	ID          uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID      uint64         `gorm:"not null;index" json:"user_id"`
	DeviceID    string         `gorm:"size:36;not null;index" json:"device_id"`
	ChallengeID uint64         `gorm:"not null" json:"challenge_id"`
	Method      string         `gorm:"size:20;not null" json:"method"` // "code_pull", "qr_scan", "number_match"
	DisplayData datatypes.JSON `gorm:"type:jsonb" json:"display_data"`
	Status      string         `gorm:"size:20;not null;default:'pending'" json:"status"` // "pending", "delivered", "expired"
	ExpiresAt   time.Time      `gorm:"not null;index" json:"expires_at"`
	DeliveredAt *time.Time     `json:"delivered_at"`
	CreatedAt   time.Time      `json:"created_at"`
}
```

- [ ] **Step 2: Commit**

```bash
git add notification-service/internal/model/mobile_inbox_item.go
git commit -m "feat(notification-service): add MobileInboxItem model"
```

---

## Task 3: Create MobileInboxRepository

**Files:**
- Create: `notification-service/internal/repository/mobile_inbox_repository.go`

- [ ] **Step 1: Write the repository**

```go
package repository

import (
	"time"

	"github.com/exbanka/notification-service/internal/model"
	"gorm.io/gorm"
)

type MobileInboxRepository struct {
	db *gorm.DB
}

func NewMobileInboxRepository(db *gorm.DB) *MobileInboxRepository {
	return &MobileInboxRepository{db: db}
}

func (r *MobileInboxRepository) Create(item *model.MobileInboxItem) error {
	return r.db.Create(item).Error
}

func (r *MobileInboxRepository) GetPendingByUserAndDevice(userID uint64, deviceID string) ([]model.MobileInboxItem, error) {
	var items []model.MobileInboxItem
	if err := r.db.Where("user_id = ? AND device_id = ? AND status = ? AND expires_at > ?",
		userID, deviceID, "pending", time.Now()).
		Order("created_at DESC").
		Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (r *MobileInboxRepository) MarkDelivered(id uint64, deviceID string) error {
	now := time.Now()
	result := r.db.Model(&model.MobileInboxItem{}).
		Where("id = ? AND device_id = ? AND status = ?", id, deviceID, "pending").
		Updates(map[string]interface{}{
			"status":       "delivered",
			"delivered_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *MobileInboxRepository) DeleteExpired() (int64, error) {
	result := r.db.Where("expires_at < ?", time.Now()).Delete(&model.MobileInboxItem{})
	return result.RowsAffected, result.Error
}
```

- [ ] **Step 2: Commit**

```bash
git add notification-service/internal/repository/mobile_inbox_repository.go
git commit -m "feat(notification-service): add MobileInboxRepository"
```

---

## Task 4: Create verification consumer

**Files:**
- Create: `notification-service/internal/consumer/verification_consumer.go`

This consumer listens for `verification.challenge-created` events. If the delivery channel is email, it sends via SMTP (reusing existing sender). If mobile, it stores in the inbox and publishes to `notification.mobile-push`.

- [ ] **Step 1: Write the consumer**

```go
package consumer

import (
	"context"
	"encoding/json"
	"log"

	kafkago "github.com/segmentio/kafka-go"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/notification-service/internal/model"
	kafkaprod "github.com/exbanka/notification-service/internal/kafka"
	"github.com/exbanka/notification-service/internal/repository"
	"github.com/exbanka/notification-service/internal/sender"
	"gorm.io/datatypes"
)

type VerificationConsumer struct {
	reader   *kafkago.Reader
	sender   *sender.EmailSender
	producer *kafkaprod.Producer
	inboxRepo *repository.MobileInboxRepository
}

func NewVerificationConsumer(brokers string, emailSender *sender.EmailSender, producer *kafkaprod.Producer, inboxRepo *repository.MobileInboxRepository) *VerificationConsumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  []string{brokers},
		Topic:    kafkamsg.TopicVerificationChallengeCreated,
		GroupID:  "notification-service",
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &VerificationConsumer{
		reader:    reader,
		sender:    emailSender,
		producer:  producer,
		inboxRepo: inboxRepo,
	}
}

func (c *VerificationConsumer) Start(ctx context.Context) {
	go func() {
		for {
			msg, err := c.reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("verification consumer: read error: %v", err)
				continue
			}
			c.handleMessage(ctx, msg.Value)
		}
	}()
	log.Println("verification consumer started")
}

func (c *VerificationConsumer) handleMessage(ctx context.Context, data []byte) {
	var event kafkamsg.VerificationChallengeCreatedMessage
	if err := json.Unmarshal(data, &event); err != nil {
		log.Printf("verification consumer: unmarshal error: %v", err)
		return
	}

	switch event.DeliveryChannel {
	case "email":
		c.handleEmailDelivery(ctx, event)
	case "mobile":
		c.handleMobileDelivery(ctx, event)
	default:
		log.Printf("verification consumer: unknown delivery channel: %s", event.DeliveryChannel)
	}
}

func (c *VerificationConsumer) handleEmailDelivery(ctx context.Context, event kafkamsg.VerificationChallengeCreatedMessage) {
	// Reuse existing email sender for verification codes
	subject, body := sender.BuildEmail(kafkamsg.EmailTypeTransactionVerify, map[string]string{
		"verification_code": event.Code,
		"expires_in":        "5 minutes",
	})
	if err := c.sender.Send(event.Email, subject, body); err != nil {
		log.Printf("verification consumer: email send error: %v", err)
	}
}

func (c *VerificationConsumer) handleMobileDelivery(ctx context.Context, event kafkamsg.VerificationChallengeCreatedMessage) {
	// Store in mobile inbox
	displayDataJSON, _ := json.Marshal(event.DisplayData)
	item := &model.MobileInboxItem{
		UserID:      event.UserID,
		DeviceID:    event.DeviceID,
		ChallengeID: event.ChallengeID,
		Method:      event.Method,
		DisplayData: datatypes.JSON(displayDataJSON),
		ExpiresAt:   event.ExpiresAt,
	}
	if err := c.inboxRepo.Create(item); err != nil {
		log.Printf("verification consumer: inbox create error: %v", err)
		return
	}

	// Publish to mobile-push topic for WebSocket delivery
	pushMsg := kafkamsg.MobilePushMessage{
		UserID:   event.UserID,
		DeviceID: event.DeviceID,
		Type:     "verification_challenge",
		Payload: map[string]interface{}{
			"challenge_id": event.ChallengeID,
			"method":       event.Method,
			"display_data": event.DisplayData,
			"expires_at":   event.ExpiresAt,
		},
	}
	if err := c.producer.Publish(ctx, kafkamsg.TopicMobilePush, pushMsg); err != nil {
		log.Printf("verification consumer: mobile push publish error: %v", err)
	}
}

func (c *VerificationConsumer) Close() error {
	return c.reader.Close()
}
```

- [ ] **Step 2: Add Kafka message types to contract**

In `contract/kafka/messages.go`, add these types and topics (if not already added by Plan 7):

```go
const (
	TopicVerificationChallengeCreated  = "verification.challenge-created"
	TopicVerificationChallengeVerified = "verification.challenge-verified"
	TopicVerificationChallengeFailed   = "verification.challenge-failed"
	TopicMobilePush                    = "notification.mobile-push"
)

type VerificationChallengeCreatedMessage struct {
	ChallengeID     uint64                 `json:"challenge_id"`
	UserID          uint64                 `json:"user_id"`
	DeviceID        string                 `json:"device_id"`
	Email           string                 `json:"email"`
	Method          string                 `json:"method"`
	Code            string                 `json:"code"`
	DisplayData     map[string]interface{} `json:"display_data"`
	DeliveryChannel string                 `json:"delivery_channel"` // "email" or "mobile"
	ExpiresAt       time.Time              `json:"expires_at"`
}

type MobilePushMessage struct {
	UserID   uint64                 `json:"user_id"`
	DeviceID string                 `json:"device_id"`
	Type     string                 `json:"type"` // "verification_challenge"
	Payload  map[string]interface{} `json:"payload"`
}
```

- [ ] **Step 3: Commit**

```bash
git add notification-service/internal/consumer/verification_consumer.go contract/kafka/messages.go
git commit -m "feat(notification-service): add verification consumer with email/mobile routing"
```

---

## Task 5: Extend notification.proto with mobile inbox RPCs

**Files:**
- Modify: `contract/proto/notification/notification.proto`

- [ ] **Step 1: Add mobile inbox RPCs**

Add to the `service NotificationService` block:

```protobuf
  rpc GetPendingMobileItems(GetPendingMobileRequest) returns (PendingMobileResponse);
  rpc AckMobileItem(AckMobileRequest) returns (AckMobileResponse);
```

- [ ] **Step 2: Add message types**

```protobuf
message GetPendingMobileRequest {
  uint64 user_id = 1;
  string device_id = 2;
}

message MobileInboxEntry {
  uint64 id = 1;
  uint64 challenge_id = 2;
  string method = 3;
  string display_data = 4;  // JSON string
  string expires_at = 5;
}

message PendingMobileResponse {
  repeated MobileInboxEntry items = 1;
}

message AckMobileRequest {
  uint64 id = 1;
  string device_id = 2;
}

message AckMobileResponse {
  bool success = 1;
}
```

- [ ] **Step 3: Regenerate proto**

```bash
make proto
```

- [ ] **Step 4: Commit**

```bash
git add contract/proto/notification/notification.proto contract/notificationpb/
git commit -m "feat(contract): add mobile inbox RPCs to notification.proto"
```

---

## Task 6: Extend notification gRPC handler

**Files:**
- Modify: `notification-service/internal/handler/grpc_handler.go`

- [ ] **Step 1: Add inbox repo to handler and implement mobile RPCs**

Add `inboxRepo` field to the handler struct. Add constructor parameter. Then implement:

```go
func (h *NotificationHandler) GetPendingMobileItems(ctx context.Context, req *pb.GetPendingMobileRequest) (*pb.PendingMobileResponse, error) {
	items, err := h.inboxRepo.GetPendingByUserAndDevice(req.UserId, req.DeviceId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch pending items: %v", err)
	}

	entries := make([]*pb.MobileInboxEntry, len(items))
	for i, item := range items {
		entries[i] = &pb.MobileInboxEntry{
			Id:          item.ID,
			ChallengeId: item.ChallengeID,
			Method:      item.Method,
			DisplayData: string(item.DisplayData),
			ExpiresAt:   item.ExpiresAt.Format(time.RFC3339),
		}
	}
	return &pb.PendingMobileResponse{Items: entries}, nil
}

func (h *NotificationHandler) AckMobileItem(ctx context.Context, req *pb.AckMobileRequest) (*pb.AckMobileResponse, error) {
	if err := h.inboxRepo.MarkDelivered(req.Id, req.DeviceId); err != nil {
		return nil, status.Errorf(codes.NotFound, "item not found or already delivered")
	}
	return &pb.AckMobileResponse{Success: true}, nil
}
```

- [ ] **Step 2: Commit**

```bash
git add notification-service/internal/handler/grpc_handler.go
git commit -m "feat(notification-service): implement mobile inbox gRPC handlers"
```

---

## Task 7: Background inbox cleanup

**Files:**
- Create: `notification-service/internal/service/inbox_cleanup.go`

- [ ] **Step 1: Write the cleanup service**

```go
package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/notification-service/internal/repository"
)

type InboxCleanupService struct {
	inboxRepo *repository.MobileInboxRepository
}

func NewInboxCleanupService(inboxRepo *repository.MobileInboxRepository) *InboxCleanupService {
	return &InboxCleanupService{inboxRepo: inboxRepo}
}

// StartCleanupCron runs every minute and deletes expired mobile inbox items.
func (s *InboxCleanupService) StartCleanupCron(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				deleted, err := s.inboxRepo.DeleteExpired()
				if err != nil {
					log.Printf("inbox cleanup: error deleting expired items: %v", err)
				} else if deleted > 0 {
					log.Printf("inbox cleanup: deleted %d expired items", deleted)
				}
			case <-ctx.Done():
				log.Println("inbox cleanup: stopped")
				return
			}
		}
	}()
	log.Println("inbox cleanup: started (every 1 minute)")
}
```

- [ ] **Step 2: Commit**

```bash
git add notification-service/internal/service/inbox_cleanup.go
git commit -m "feat(notification-service): add background inbox cleanup service"
```

---

## Task 8: Wire everything in notification-service main.go

**Files:**
- Modify: `notification-service/cmd/main.go`

- [ ] **Step 1: Add DB connection and AutoMigrate**

After config loading, add:

```go
db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
if err != nil {
	log.Fatalf("failed to connect to database: %v", err)
}
if err := db.AutoMigrate(&model.MobileInboxItem{}); err != nil {
	log.Fatalf("failed to migrate: %v", err)
}
```

Add imports for `"gorm.io/driver/postgres"`, `"gorm.io/gorm"`, and the model package.

- [ ] **Step 2: Create inbox repository and inject into handler**

```go
inboxRepo := repository.NewMobileInboxRepository(db)
```

Update the gRPC handler constructor to accept `inboxRepo`.

- [ ] **Step 3: Start verification consumer**

```go
verificationConsumer := consumer.NewVerificationConsumer(cfg.KafkaBrokers, emailSender, producer, inboxRepo)
verificationConsumer.Start(ctx)
defer verificationConsumer.Close()
```

- [ ] **Step 4: Start inbox cleanup**

```go
cleanupSvc := service.NewInboxCleanupService(inboxRepo)
cleanupSvc.StartCleanupCron(ctx)
```

- [ ] **Step 5: Add topics to EnsureTopics**

```go
kafkaprod.EnsureTopics(cfg.KafkaBrokers,
	"notification.send-email",
	"notification.email-sent",
	"verification.challenge-created",
	"notification.mobile-push",
)
```

- [ ] **Step 6: Verify build**

```bash
cd notification-service && go build ./cmd
```

- [ ] **Step 7: Commit**

```bash
git add notification-service/cmd/main.go
git commit -m "feat(notification-service): wire DB, mobile inbox, verification consumer, cleanup into main"
```

---

## Task 9: MobileAuthMiddleware

**Files:**
- Create: `api-gateway/internal/middleware/mobile_auth.go`

- [ ] **Step 1: Write the middleware**

```go
package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	authpb "github.com/exbanka/contract/authpb"
)

// MobileAuthMiddleware validates that the request comes from a registered mobile device.
// Checks: valid JWT with device_type="mobile", X-Device-ID header matches device_id claim.
func MobileAuthMiddleware(authClient authpb.AuthServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractBearerToken(c)
		if token == "" {
			abortWithError(c, http.StatusUnauthorized, "unauthorized", "missing authorization token")
			return
		}

		resp, err := authClient.ValidateToken(c.Request.Context(), &authpb.ValidateTokenRequest{Token: token})
		if err != nil || !resp.Valid {
			abortWithError(c, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
			return
		}

		// Require mobile device type
		if resp.DeviceType != "mobile" {
			abortWithError(c, http.StatusForbidden, "forbidden", "this endpoint requires a mobile device token")
			return
		}

		// Require X-Device-ID header matching token claim
		headerDeviceID := c.GetHeader("X-Device-ID")
		if headerDeviceID == "" || headerDeviceID != resp.DeviceId {
			abortWithError(c, http.StatusForbidden, "forbidden", "device ID mismatch or missing X-Device-ID header")
			return
		}

		// Set context values (same as AuthMiddleware + device fields)
		c.Set("user_id", resp.UserId)
		c.Set("email", resp.Email)
		c.Set("role", resp.Role)
		c.Set("roles", resp.Roles)
		c.Set("system_type", resp.SystemType)
		c.Set("permissions", resp.Permissions)
		c.Set("device_type", resp.DeviceType)
		c.Set("device_id", resp.DeviceId)

		c.Next()
	}
}

// RequireDeviceSignature validates the HMAC request signature from a mobile device.
// Must be chained AFTER MobileAuthMiddleware (needs device_id from context).
func RequireDeviceSignature(authClient authpb.AuthServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		deviceID, _ := c.Get("device_id")
		deviceIDStr, ok := deviceID.(string)
		if !ok || deviceIDStr == "" {
			abortWithError(c, http.StatusForbidden, "forbidden", "device_id not found in context")
			return
		}

		timestamp := c.GetHeader("X-Device-Timestamp")
		signature := c.GetHeader("X-Device-Signature")
		if timestamp == "" || signature == "" {
			abortWithError(c, http.StatusUnauthorized, "unauthorized", "missing X-Device-Timestamp or X-Device-Signature headers")
			return
		}

		// Read body for SHA256 (need to buffer it for the handler)
		bodyBytes, err := c.GetRawData()
		if err != nil {
			abortWithError(c, http.StatusBadRequest, "validation_error", "failed to read request body")
			return
		}
		// Restore body for downstream handlers
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		bodySHA := sha256Hex(bodyBytes)

		resp, err := authClient.ValidateDeviceSignature(c.Request.Context(), &authpb.ValidateDeviceSignatureRequest{
			DeviceId:   deviceIDStr,
			Timestamp:  timestamp,
			Method:     c.Request.Method,
			Path:       c.Request.URL.Path,
			BodySha256: bodySHA,
			Signature:  signature,
		})
		if err != nil || !resp.Valid {
			abortWithError(c, http.StatusForbidden, "forbidden", "invalid device signature")
			return
		}

		c.Next()
	}
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
```

Add necessary imports: `"bytes"`, `"crypto/sha256"`, `"encoding/hex"`, `"io"`.

- [ ] **Step 2: Commit**

```bash
git add api-gateway/internal/middleware/mobile_auth.go
git commit -m "feat(api-gateway): add MobileAuthMiddleware and RequireDeviceSignature middleware"
```

---

## Task 10: WebSocket handler

**Files:**
- Create: `api-gateway/internal/handler/websocket_handler.go`

- [ ] **Step 1: Write the WebSocket handler**

```go
package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	kafkago "github.com/segmentio/kafka-go"

	authpb "github.com/exbanka/contract/authpb"
	kafkamsg "github.com/exbanka/contract/kafka"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type WebSocketHandler struct {
	authClient  authpb.AuthServiceClient
	connections map[int64]*wsConnection
	mu          sync.RWMutex
}

type wsConnection struct {
	conn     *websocket.Conn
	deviceID string
	lastPong time.Time
}

func NewWebSocketHandler(authClient authpb.AuthServiceClient) *WebSocketHandler {
	return &WebSocketHandler{
		authClient:  authClient,
		connections: make(map[int64]*wsConnection),
	}
}

// HandleConnect upgrades HTTP to WebSocket, validates mobile JWT + device_id.
func (h *WebSocketHandler) HandleConnect(c *gin.Context) {
	// Extract and validate token
	token := c.Query("token")
	if token == "" {
		token = extractBearerToken(c)
	}
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
		return
	}

	resp, err := h.authClient.ValidateToken(c.Request.Context(), &authpb.ValidateTokenRequest{Token: token})
	if err != nil || !resp.Valid {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	if resp.DeviceType != "mobile" {
		c.JSON(http.StatusForbidden, gin.H{"error": "mobile token required"})
		return
	}

	deviceID := c.GetHeader("X-Device-ID")
	if deviceID == "" {
		deviceID = c.Query("device_id")
	}
	if deviceID != resp.DeviceId {
		c.JSON(http.StatusForbidden, gin.H{"error": "device ID mismatch"})
		return
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("websocket upgrade error: %v", err)
		return
	}

	userID := resp.UserId

	// Replace existing connection for this user
	h.mu.Lock()
	if existing, ok := h.connections[userID]; ok {
		existing.conn.Close()
	}
	h.connections[userID] = &wsConnection{
		conn:     conn,
		deviceID: deviceID,
		lastPong: time.Now(),
	}
	h.mu.Unlock()

	log.Printf("websocket: user %d connected (device %s)", userID, deviceID)

	// Handle pong responses
	conn.SetPongHandler(func(string) error {
		h.mu.Lock()
		if ws, ok := h.connections[userID]; ok {
			ws.lastPong = time.Now()
		}
		h.mu.Unlock()
		return nil
	})

	// Start ping loop
	go h.pingLoop(userID, conn)

	// Read loop (to detect disconnection)
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}

	// Cleanup on disconnect
	h.mu.Lock()
	delete(h.connections, userID)
	h.mu.Unlock()
	conn.Close()
	log.Printf("websocket: user %d disconnected", userID)
}

func (h *WebSocketHandler) pingLoop(userID int64, conn *websocket.Conn) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		h.mu.RLock()
		ws, ok := h.connections[userID]
		h.mu.RUnlock()
		if !ok {
			return
		}
		// Check for dead connection (no pong in 60s)
		if time.Since(ws.lastPong) > 60*time.Second {
			h.mu.Lock()
			delete(h.connections, userID)
			h.mu.Unlock()
			conn.Close()
			log.Printf("websocket: user %d timed out (no pong)", userID)
			return
		}
		if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
			return
		}
	}
}

// PushToUser sends a message to a connected user's WebSocket.
func (h *WebSocketHandler) PushToUser(userID int64, message interface{}) {
	h.mu.RLock()
	ws, ok := h.connections[userID]
	h.mu.RUnlock()
	if !ok {
		return
	}
	data, err := json.Marshal(message)
	if err != nil {
		return
	}
	if err := ws.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("websocket: push to user %d failed: %v", userID, err)
	}
}

// StartKafkaConsumer listens for mobile-push events and routes to WebSocket connections.
func (h *WebSocketHandler) StartKafkaConsumer(ctx context.Context, brokers string) {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  []string{brokers},
		Topic:    kafkamsg.TopicMobilePush,
		GroupID:  "api-gateway-ws",
		MinBytes: 1,
		MaxBytes: 10e6,
	})

	go func() {
		defer reader.Close()
		for {
			msg, err := reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("websocket kafka consumer: read error: %v", err)
				continue
			}

			var pushMsg kafkamsg.MobilePushMessage
			if err := json.Unmarshal(msg.Value, &pushMsg); err != nil {
				log.Printf("websocket kafka consumer: unmarshal error: %v", err)
				continue
			}

			h.PushToUser(int64(pushMsg.UserID), pushMsg)
		}
	}()
	log.Println("websocket kafka consumer started (topic: notification.mobile-push)")
}
```

- [ ] **Step 2: Add gorilla/websocket dependency**

```bash
cd api-gateway && go get github.com/gorilla/websocket
```

- [ ] **Step 3: Add segmentio/kafka-go dependency (if not already present)**

```bash
cd api-gateway && go get github.com/segmentio/kafka-go
```

- [ ] **Step 4: Commit**

```bash
git add api-gateway/internal/handler/websocket_handler.go api-gateway/go.mod api-gateway/go.sum
git commit -m "feat(api-gateway): add WebSocket handler with Kafka consumer for mobile push"
```

---

## Task 11: Docker-compose updates

**Files:**
- Modify: `docker-compose.yml`

- [ ] **Step 1: Add notification_db service**

```yaml
  notification_db:
    image: postgres:16
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: postgres
      POSTGRES_DB: notification_db
    ports:
      - "5441:5432"
    volumes:
      - notification_db_data:/var/lib/postgresql/data
```

- [ ] **Step 2: Add DB env vars to notification-service**

```yaml
  notification-service:
    environment:
      # ... existing SMTP and Kafka vars
      NOTIFICATION_DB_HOST: notification_db
      NOTIFICATION_DB_PORT: "5432"
      NOTIFICATION_DB_USER: postgres
      NOTIFICATION_DB_PASSWORD: postgres
      NOTIFICATION_DB_NAME: notification_db
      NOTIFICATION_DB_SSLMODE: disable
    depends_on:
      - notification_db
      - kafka
```

- [ ] **Step 3: Add volume**

```yaml
volumes:
  # ... existing volumes
  notification_db_data:
```

- [ ] **Step 4: Commit**

```bash
git add docker-compose.yml
git commit -m "chore: add notification_db to docker-compose"
```

---

## Task 12: Verify full build

- [ ] **Step 1: Tidy all modules**

```bash
cd notification-service && go mod tidy
cd api-gateway && go mod tidy
```

- [ ] **Step 2: Build notification-service**

```bash
cd notification-service && go build ./cmd
```

- [ ] **Step 3: Build api-gateway**

```bash
cd api-gateway && go build ./cmd
```

- [ ] **Step 4: Full repo build**

```bash
make build
```

Expected: All services build successfully.

- [ ] **Step 5: Commit dependencies**

```bash
git add notification-service/go.mod notification-service/go.sum api-gateway/go.mod api-gateway/go.sum
git commit -m "chore: update dependencies after mobile notification delivery implementation"
```

---

## Design Notes

### Mobile Inbox Lifecycle

1. Verification-service publishes `verification.challenge-created` to Kafka
2. Notification-service consumes → routes based on `delivery_channel`
3. If `mobile`: stores `MobileInboxItem` + publishes to `notification.mobile-push`
4. Gateway WebSocket consumer routes push to connected device
5. If no WebSocket: mobile app polls `GetPendingMobileItems` gRPC
6. After display, app calls `AckMobileItem` → marks delivered
7. Background cleanup deletes expired items every minute

### WebSocket Connection Management

- One connection per user (new replaces old gracefully)
- Ping every 30s, dead detection at 60s (no pong)
- Kafka consumer group `api-gateway-ws` ensures messages are delivered once
- If gateway restarts, mobile apps reconnect automatically and fall back to polling

### What This Plan Does NOT Cover

- Gateway route registration (Plan 10)
- Gateway handler files for mobile auth/verification endpoints (Plan 10)
- Transaction-service migration (Plan 10)
- Permission seeding (Plan 10)
- Documentation updates (Plan 10)
