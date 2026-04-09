# Remove device_id from Notification/Verification Polling Flow

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove `device_id` from the notification service's mobile inbox pipeline, strip email delivery from the verification service entirely, clean up the verification `GetPendingChallenge` RPC, and expose a mobile ACK endpoint — so both browser-created and mobile-created challenges are uniformly visible and ack-able via user_id alone.

**Architecture:** `device_id` is threaded through notification inbox messages but ignored at query time (`GetPendingByUser`). `MarkDelivered` still filters by device_id, silently rejecting browser-created items (stored with `device_id=""`). The fix removes `device_id` from the notification layer entirely. Email delivery is also removed from verification: there is no fallback email, no `email` method, and no `email` field in the challenge proto — code_pull is the only method. The `scanKafkaForVerificationCode` integration test helper is replaced by using the bypass code `"111111"` directly. `MobileAuthMiddleware` remains as-is.

**Tech Stack:** Go, gRPC/protobuf (`make proto`/`make swagger`), GORM (PostgreSQL), Kafka

---

## File Structure

| File | Change |
|------|--------|
| `contract/proto/notification/notification.proto` | Remove `device_id` from `GetPendingMobileRequest` and `AckMobileRequest` |
| `contract/notificationpb/notification.pb.go` | Auto-regenerated |
| `contract/proto/verification/verification.proto` | Remove `device_id` from `GetPendingChallengeRequest`; remove `email` from `CreateChallengeRequest` |
| `contract/verificationpb/verification.pb.go` | Auto-regenerated |
| `contract/kafka/messages.go` | Remove `DeviceID` from `VerificationChallengeCreatedMessage` and `MobilePushMessage` |
| `notification-service/internal/model/mobile_inbox_item.go` | Remove `DeviceID` field |
| `notification-service/internal/repository/mobile_inbox_repository.go` | Remove `GetPendingByUserAndDevice`; remove `deviceID` param from `MarkDelivered` |
| `notification-service/internal/repository/mobile_inbox_repository_test.go` | Remove device-specific tests; update all tests |
| `notification-service/internal/consumer/verification_consumer.go` | Remove `DeviceID` from `MobileInboxItem` and `MobilePushMessage` |
| `notification-service/internal/handler/grpc_handler.go` | Remove `DeviceId` from `AckMobileItem` call |
| `verification-service/internal/repository/verification_challenge_repository.go` | Remove `deviceID` param from `GetPendingByUser` |
| `verification-service/internal/repository/verification_challenge_repository_test.go` | Update `GetPendingByUser` calls; remove empty-device test |
| `verification-service/internal/service/verification_service.go` | Remove email method, email delivery block, `email` param; remove `deviceID` param from `GetPendingChallenge`; update `SubmitCode` guard; update `buildChallengeData` |
| `verification-service/internal/service/verification_service_test.go` | Update `GetPendingChallenge` tests; fix `TestValidMethods_EmailEnabled` |
| `verification-service/internal/handler/grpc_handler.go` | Remove `email`/`device_id` from `CreateChallenge` call; remove `device_id` from `GetPendingChallenge` call |
| `verification-service/internal/kafka/producer.go` | Remove `PublishSendEmail` method |
| `api-gateway/internal/handler/verification_handler.go` | Remove `emailStr`/`DeviceId`/`Email` from `CreateVerification`; remove `DeviceId` from `GetPendingVerifications`; add `AckVerification` handler |
| `api-gateway/internal/router/router.go` | Add `POST /:id/ack` to `mobileVerify` group |
| `test-app/workflows/helpers_test.go` | Remove `email` param from helpers; replace `scanKafkaForVerificationCode` with bypass code |
| `test-app/workflows/verification_test.go` | Rename email-method test to rejected; remove email tests; update invalid-method test |
| `test-app/workflows/wf_verification_retry_test.go` | Remove `email` arg from `createChallengeOnly` |
| `test-app/workflows/wf_client_stock_banking_test.go` | Remove `email` arg from `createAndExecutePayment` |
| `test-app/workflows/wf_cross_currency_test.go` | Remove `email` arg from `createAndExecuteTransfer` |
| `test-app/workflows/wf_full_day_test.go` | Remove `email` arg from `createAndExecutePayment` |
| `test-app/workflows/wf_multicurrency_test.go` | Remove `email` arg from `createAndExecuteTransfer` |
| `test-app/workflows/wf_onboarding_test.go` | Remove `email` arg from `createAndExecutePayment` |
| `docs/api/REST_API.md` | Add ACK endpoint; update verification methods table; remove email references |

---

## Task 1: Update notification proto — remove device_id

**Files:**
- Modify: `contract/proto/notification/notification.proto`
- Modify (generated): `contract/notificationpb/notification.pb.go`

- [ ] **Step 1: Edit the proto**

Replace `contract/proto/notification/notification.proto`:

```protobuf
syntax = "proto3";

package notification;

option go_package = "github.com/exbanka/contract/notificationpb;notificationpb";

service NotificationService {
  rpc SendEmail(SendEmailRequest) returns (SendEmailResponse);
  rpc GetDeliveryStatus(GetDeliveryStatusRequest) returns (GetDeliveryStatusResponse);
  rpc GetPendingMobileItems(GetPendingMobileRequest) returns (PendingMobileResponse);
  rpc AckMobileItem(AckMobileRequest) returns (AckMobileResponse);
}

message SendEmailRequest {
  string to = 1;
  string email_type = 2;
  map<string, string> data = 3;
}

message SendEmailResponse {
  bool success = 1;
  string message = 2;
}

message GetDeliveryStatusRequest {
  string message_id = 1;
}

message GetDeliveryStatusResponse {
  string message_id = 1;
  string status = 2;
  string delivered_at = 3;
}

message GetPendingMobileRequest {
  uint64 user_id = 1;
}

message MobileInboxEntry {
  uint64 id = 1;
  uint64 challenge_id = 2;
  string method = 3;
  string display_data = 4;
  string expires_at = 5;
}

message PendingMobileResponse {
  repeated MobileInboxEntry items = 1;
}

message AckMobileRequest {
  uint64 id = 1;
}

message AckMobileResponse {
  bool success = 1;
}
```

- [ ] **Step 2: Regenerate**

```bash
make proto
```

Expected: exits 0, `contract/notificationpb/notification.pb.go` updated.

- [ ] **Step 3: Verify contract compiles**

```bash
cd contract && go build ./...
```

Expected: exits 0.

- [ ] **Step 4: Commit**

```bash
git add contract/proto/notification/notification.proto contract/notificationpb/notification.pb.go
git commit -m "feat: remove device_id from notification proto GetPendingMobileRequest and AckMobileRequest"
```

---

## Task 2: Update Kafka contract messages

**Files:**
- Modify: `contract/kafka/messages.go`

- [ ] **Step 1: Remove DeviceID from both structs**

In `contract/kafka/messages.go`, update:

```go
// VerificationChallengeCreatedMessage is published when a new verification challenge is created.
// notification-service consumes this to store a mobile inbox item for the user.
type VerificationChallengeCreatedMessage struct {
	ChallengeID     uint64 `json:"challenge_id"`
	UserID          uint64 `json:"user_id"`
	Method          string `json:"method"`           // "code_pull", "qr_scan", "number_match"
	DisplayData     string `json:"display_data"`     // JSON string — what the mobile app needs to show
	DeliveryChannel string `json:"delivery_channel"` // "mobile"
	ExpiresAt       string `json:"expires_at"`       // RFC3339
}
```

```go
// MobilePushMessage is published by notification-service when a mobile inbox item is stored.
// api-gateway consumes this to push via WebSocket to connected mobile devices.
type MobilePushMessage struct {
	UserID  uint64 `json:"user_id"`
	Type    string `json:"type"`    // "verification_challenge"
	Payload string `json:"payload"` // JSON string
}
```

- [ ] **Step 2: Build contract**

```bash
cd contract && go build ./...
```

Expected: compilation errors in all callers of `DeviceID` — these surface every site to fix.

- [ ] **Step 3: Commit**

```bash
git add contract/kafka/messages.go
git commit -m "feat: remove DeviceID from VerificationChallengeCreatedMessage and MobilePushMessage"
```

---

## Task 3: Update MobileInboxItem model and repository

**Files:**
- Modify: `notification-service/internal/model/mobile_inbox_item.go`
- Modify: `notification-service/internal/repository/mobile_inbox_repository.go`
- Modify: `notification-service/internal/repository/mobile_inbox_repository_test.go`

- [ ] **Step 1: Write failing tests first**

Replace `notification-service/internal/repository/mobile_inbox_repository_test.go`:

```go
package repository

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/notification-service/internal/model"
)

func newInboxTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.MobileInboxItem{}))
	return db
}

func seedInboxItem(t *testing.T, db *gorm.DB, opts model.MobileInboxItem) *model.MobileInboxItem {
	t.Helper()
	if opts.Method == "" {
		opts.Method = "code_pull"
	}
	if opts.Status == "" {
		opts.Status = "pending"
	}
	if opts.ExpiresAt.IsZero() {
		opts.ExpiresAt = time.Now().Add(5 * time.Minute)
	}
	require.NoError(t, db.Create(&opts).Error)
	return &opts
}

func TestGetPendingByUser_ReturnsPendingItems(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, ChallengeID: 1, Method: "code_pull"})
	seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, ChallengeID: 2, Method: "qr_scan"})

	items, err := repo.GetPendingByUser(10)
	require.NoError(t, err)
	assert.Len(t, items, 2)
}

func TestGetPendingByUser_ExcludesOtherUsers(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, ChallengeID: 100})
	seedInboxItem(t, db, model.MobileInboxItem{UserID: 99, ChallengeID: 101})

	items, err := repo.GetPendingByUser(10)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, uint64(100), items[0].ChallengeID)
}

func TestGetPendingByUser_ExcludesExpiredItems(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	seedInboxItem(t, db, model.MobileInboxItem{
		UserID:    10,
		ExpiresAt: time.Now().Add(-1 * time.Minute),
	})

	items, err := repo.GetPendingByUser(10)
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestGetPendingByUser_ExcludesDeliveredItems(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, Status: "delivered"})

	items, err := repo.GetPendingByUser(10)
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestMarkDelivered_MarksItemAsDelivered(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	item := seedInboxItem(t, db, model.MobileInboxItem{UserID: 10})

	err := repo.MarkDelivered(item.ID)
	require.NoError(t, err)

	var updated model.MobileInboxItem
	require.NoError(t, db.First(&updated, item.ID).Error)
	assert.Equal(t, "delivered", updated.Status)
	assert.NotNil(t, updated.DeliveredAt)
}

func TestMarkDelivered_ReturnsErrorForUnknownItem(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	err := repo.MarkDelivered(9999)
	assert.Error(t, err)
}

func TestMarkDelivered_ReturnsErrorForAlreadyDelivered(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	item := seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, Status: "delivered"})

	err := repo.MarkDelivered(item.ID)
	assert.Error(t, err)
}

func TestDeleteExpired_RemovesExpiredItems(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, ExpiresAt: time.Now().Add(-2 * time.Minute)})
	seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, ExpiresAt: time.Now().Add(-1 * time.Minute)})
	seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, ExpiresAt: time.Now().Add(10 * time.Minute)})

	deleted, err := repo.DeleteExpired()
	require.NoError(t, err)
	assert.EqualValues(t, 2, deleted)

	var remaining []model.MobileInboxItem
	require.NoError(t, db.Find(&remaining).Error)
	assert.Len(t, remaining, 1)
}

func TestDeleteExpired_NothingToDelete(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	seedInboxItem(t, db, model.MobileInboxItem{UserID: 10, ExpiresAt: time.Now().Add(10 * time.Minute)})

	deleted, err := repo.DeleteExpired()
	require.NoError(t, err)
	assert.EqualValues(t, 0, deleted)
}
```

- [ ] **Step 2: Run — expect compilation failure**

```bash
cd notification-service && go test ./internal/repository/... 2>&1 | head -20
```

Expected: errors referencing `DeviceID`, `GetPendingByUserAndDevice`, `MarkDelivered` signature.

- [ ] **Step 3: Update MobileInboxItem model**

Replace `notification-service/internal/model/mobile_inbox_item.go`:

```go
package model

import (
	"time"

	"gorm.io/datatypes"
)

// MobileInboxItem stores a pending verification notification for a user.
// Both browser-initiated and mobile-initiated challenges land here, queryable
// by user_id alone — no device filtering.
type MobileInboxItem struct {
	ID          uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID      uint64         `gorm:"not null;index" json:"user_id"`
	ChallengeID uint64         `gorm:"not null" json:"challenge_id"`
	Method      string         `gorm:"size:20;not null" json:"method"`
	DisplayData datatypes.JSON `gorm:"type:jsonb" json:"display_data"`
	Status      string         `gorm:"size:20;not null;default:'pending'" json:"status"`
	ExpiresAt   time.Time      `gorm:"not null;index" json:"expires_at"`
	DeliveredAt *time.Time     `json:"delivered_at"`
	CreatedAt   time.Time      `json:"created_at"`
}
```

**DB migration note:** GORM AutoMigrate does not drop columns. On existing databases, run once: `db.Exec("ALTER TABLE mobile_inbox_items DROP COLUMN IF EXISTS device_id")`.

- [ ] **Step 4: Update MobileInboxRepository**

Replace `notification-service/internal/repository/mobile_inbox_repository.go`:

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

// GetPendingByUser returns all pending, non-expired inbox items for a user.
// Browser-initiated and mobile-initiated challenges are returned equally.
func (r *MobileInboxRepository) GetPendingByUser(userID uint64) ([]model.MobileInboxItem, error) {
	var items []model.MobileInboxItem
	if err := r.db.Where("user_id = ? AND status = ? AND expires_at > ?",
		userID, "pending", time.Now()).
		Order("created_at DESC").
		Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (r *MobileInboxRepository) MarkDelivered(id uint64) error {
	now := time.Now()
	result := r.db.Model(&model.MobileInboxItem{}).
		Where("id = ? AND status = ?", id, "pending").
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

- [ ] **Step 5: Run tests — expect pass**

```bash
cd notification-service && go test ./internal/repository/... -v 2>&1 | tail -20
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add notification-service/internal/model/mobile_inbox_item.go \
        notification-service/internal/repository/mobile_inbox_repository.go \
        notification-service/internal/repository/mobile_inbox_repository_test.go
git commit -m "feat: remove device_id from MobileInboxItem model and repository"
```

---

## Task 4: Update notification service consumer and gRPC handler

**Files:**
- Modify: `notification-service/internal/consumer/verification_consumer.go`
- Modify: `notification-service/internal/handler/grpc_handler.go`

- [ ] **Step 1: Update verification_consumer.go — handleMobileDelivery**

Replace the `handleMobileDelivery` function:

```go
func (c *VerificationConsumer) handleMobileDelivery(ctx context.Context, event kafkamsg.VerificationChallengeCreatedMessage) {
	expiresAt, err := time.Parse(time.RFC3339, event.ExpiresAt)
	if err != nil {
		log.Printf("verification consumer: invalid expires_at: %v", err)
		return
	}

	item := &model.MobileInboxItem{
		UserID:      event.UserID,
		ChallengeID: event.ChallengeID,
		Method:      event.Method,
		DisplayData: datatypes.JSON(event.DisplayData),
		ExpiresAt:   expiresAt,
	}
	if err := c.inboxRepo.Create(item); err != nil {
		log.Printf("verification consumer: inbox create error: %v", err)
		return
	}

	payloadJSON, _ := json.Marshal(map[string]interface{}{
		"challenge_id": event.ChallengeID,
		"method":       event.Method,
		"display_data": event.DisplayData,
		"expires_at":   event.ExpiresAt,
	})
	pushMsg := kafkamsg.MobilePushMessage{
		UserID:  event.UserID,
		Type:    "verification_challenge",
		Payload: string(payloadJSON),
	}
	if err := c.producer.Publish(ctx, kafkamsg.TopicMobilePush, pushMsg); err != nil {
		log.Printf("verification consumer: mobile push publish error: %v", err)
	} else {
		svc.NotificationMobilePushTotal.Inc()
	}
}
```

- [ ] **Step 2: Update grpc_handler.go — AckMobileItem**

Update `AckMobileItem` to remove the device_id argument:

```go
func (h *GRPCHandler) AckMobileItem(ctx context.Context, req *notifpb.AckMobileRequest) (*notifpb.AckMobileResponse, error) {
	if err := h.inboxRepo.MarkDelivered(req.Id); err != nil {
		return nil, status.Errorf(codes.NotFound, "item not found or already delivered")
	}
	return &notifpb.AckMobileResponse{Success: true}, nil
}
```

`GetPendingMobileItems` already uses `GetPendingByUser(req.UserId)` — verify the field `req.DeviceId` is gone from the proto (Task 1) and the call is clean.

- [ ] **Step 3: Build**

```bash
cd notification-service && go build ./...
```

Expected: exits 0.

- [ ] **Step 4: Commit**

```bash
git add notification-service/internal/consumer/verification_consumer.go \
        notification-service/internal/handler/grpc_handler.go
git commit -m "feat: remove device_id from notification consumer and gRPC handler"
```

---

## Task 5: Update verification proto — remove device_id from GetPendingChallenge and email from CreateChallenge

**Files:**
- Modify: `contract/proto/verification/verification.proto`
- Modify (generated): `contract/verificationpb/verification.pb.go`

- [ ] **Step 1: Edit the proto**

In `contract/proto/verification/verification.proto`, make these two changes:

```protobuf
message CreateChallengeRequest {
  uint64 user_id        = 1;
  string source_service = 2; // "transaction", "payment", "transfer"
  uint64 source_id      = 3;
  string method         = 4; // "code_pull"
  string device_id      = 5; // UUID of mobile device, empty for browser sessions
}
```

```protobuf
message GetPendingChallengeRequest {
  uint64 user_id = 1;
}
```

- [ ] **Step 2: Regenerate**

```bash
make proto
```

Expected: exits 0, `contract/verificationpb/verification.pb.go` updated with no `Email` in `CreateChallengeRequest` and no `DeviceId` in `GetPendingChallengeRequest`.

- [ ] **Step 3: Build contract**

```bash
cd contract && go build ./...
```

Expected: compilation errors surfacing every caller of the removed fields — this is expected. The following tasks fix them.

- [ ] **Step 4: Commit proto only**

```bash
git add contract/proto/verification/verification.proto contract/verificationpb/verification.pb.go
git commit -m "feat: remove email from CreateChallengeRequest and device_id from GetPendingChallengeRequest proto"
```

---

## Task 6: Update verification service — remove email delivery and clean up GetPendingChallenge

**Files:**
- Modify: `verification-service/internal/service/verification_service.go`
- Modify: `verification-service/internal/kafka/producer.go`
- Modify: `verification-service/internal/handler/grpc_handler.go`
- Modify: `verification-service/internal/repository/verification_challenge_repository.go`
- Modify: `verification-service/internal/repository/verification_challenge_repository_test.go`
- Modify: `verification-service/internal/service/verification_service_test.go`

- [ ] **Step 1: Write failing unit tests**

In `verification-service/internal/service/verification_service_test.go`, update the two affected tests and add a new validMethods assertion:

Replace `TestValidMethods_EmailEnabled`:
```go
func TestValidMethods_EmailDisabled(t *testing.T) {
	assert.False(t, validMethods["email"])
}
```

Replace `TestGetPendingChallenge_ReturnsForUserAndDevice`:
```go
func TestGetPendingChallenge_ReturnsPendingForUser(t *testing.T) {
	svc, db := setupTestVerificationService(t)

	created := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        20,
		SourceService: "payment",
		SourceID:      600,
		Method:        "code_pull",
		Code:          "112233",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		Attempts:      0,
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	vc, err := svc.GetPendingChallenge(20)
	require.NoError(t, err)
	assert.Equal(t, created.ID, vc.ID)
	assert.Equal(t, "pending", vc.Status)
}
```

Replace `TestGetPendingChallenge_FoundRegardlessOfDevice`:
```go
func TestGetPendingChallenge_NotFoundWhenNoPending(t *testing.T) {
	svc, _ := setupTestVerificationService(t)

	_, err := svc.GetPendingChallenge(99999)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run tests — expect failure**

```bash
cd verification-service && go test ./internal/service/... 2>&1 | head -20
```

Expected: compilation errors from removed proto fields and changed signatures.

- [ ] **Step 3: Update verification_challenge_repository.go**

In `verification-service/internal/repository/verification_challenge_repository.go`, replace `GetPendingByUser`:

```go
// GetPendingByUser returns the most recent pending, non-expired challenge for a user.
func (r *VerificationChallengeRepository) GetPendingByUser(userID uint64) (*model.VerificationChallenge, error) {
	var vc model.VerificationChallenge
	err := r.db.Where("user_id = ? AND status = ? AND expires_at > ?",
		userID, "pending", time.Now()).
		Order("created_at DESC").
		First(&vc).Error
	if err != nil {
		return nil, err
	}
	return &vc, nil
}
```

- [ ] **Step 4: Update repository tests**

In `verification-service/internal/repository/verification_challenge_repository_test.go`:

Replace `TestGetPendingByUser`:
```go
func TestGetPendingByUser(t *testing.T) {
	repo, _ := setupTestRepo(t)

	future := time.Now().Add(10 * time.Minute)
	verified := newChallenge(200, "dev-1", "verified", future)
	require.NoError(t, repo.Create(verified))

	pending := newChallenge(200, "dev-1", "pending", future)
	require.NoError(t, repo.Create(pending))

	got, err := repo.GetPendingByUser(200)
	require.NoError(t, err)
	assert.Equal(t, pending.ID, got.ID)
	assert.Equal(t, "pending", got.Status)
}
```

Replace `TestGetPendingByUser_ExpiredNotReturned`:
```go
func TestGetPendingByUser_ExpiredNotReturned(t *testing.T) {
	repo, _ := setupTestRepo(t)

	past := time.Now().Add(-1 * time.Minute)
	expired := newChallenge(300, "dev-2", "pending", past)
	require.NoError(t, repo.Create(expired))

	_, err := repo.GetPendingByUser(300)
	require.Error(t, err)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
```

Remove `TestGetPendingByUser_FindsChallengeWithEmptyDeviceID` entirely (no longer relevant).

Note: `newChallenge(userID, deviceID, status, expiresAt)` helper retains its `deviceID` param — `VerificationChallenge.DeviceID` still exists in the model (used for challenge binding in `SubmitVerification`).

- [ ] **Step 5: Update verification_service.go**

Make the following changes in `verification-service/internal/service/verification_service.go`:

**a) Update validMethods — remove email:**
```go
var validMethods = map[string]bool{
	"code_pull": true,
	// "qr_scan" and "number_match" are not yet fully implemented — re-enable when ready
}
```

**b) Update CreateChallenge signature — remove email param:**
```go
func (s *VerificationService) CreateChallenge(ctx context.Context, userID uint64, sourceService string, sourceID uint64, method string, deviceID string) (*model.VerificationChallenge, error) {
```

**c) Update error message in CreateChallenge:**
```go
return nil, fmt.Errorf("invalid verification method: %s; must be one of: code_pull", method)
```

**d) Remove the entire email delivery block from CreateChallenge** (the block that begins with `// 1. Send email when:...`). After removal the delivery section should be:
```go
// Publish challenge-created event so notification-service can create a mobile inbox item.
// This makes the challenge visible to the mobile app regardless of session origin.
displayData, err := buildDisplayData(method, code, challengeData)
if err != nil {
    log.Printf("warn: failed to build display data for verification %d: %v", vc.ID, err)
} else {
    displayDataJSON, _ := json.Marshal(displayData)
    if err := s.producer.PublishChallengeCreated(ctx, kafkamsg.VerificationChallengeCreatedMessage{
        ChallengeID:     vc.ID,
        UserID:          userID,
        Method:          method,
        DisplayData:     string(displayDataJSON),
        DeliveryChannel: "mobile",
        ExpiresAt:       vc.ExpiresAt.UTC().Format(time.RFC3339),
    }); err != nil {
        log.Printf("warn: failed to publish challenge-created for verification %d: %v", vc.ID, err)
    }
}
```

**e) Update SubmitCode guard:**
```go
if vc.Method != "code_pull" {
    return fmt.Errorf("code submission is only allowed for code_pull method; this challenge uses %s", vc.Method)
}
```

**f) Update SubmitCode comment:**
```go
// SubmitCode handles browser-submitted 6-digit codes (code_pull method).
```

**g) Remove email case from buildChallengeData** (the `case "email": return map[string]interface{}{}, nil` line).

**h) Update GetPendingChallenge signature:**
```go
func (s *VerificationService) GetPendingChallenge(userID uint64) (*model.VerificationChallenge, error) {
	return s.repo.GetPendingByUser(userID)
}
```

- [ ] **Step 6: Remove PublishSendEmail from verification kafka producer**

In `verification-service/internal/kafka/producer.go`, remove the `PublishSendEmail` method entirely:

```go
// Delete this entire method:
// func (p *Producer) PublishSendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
//     return p.publish(ctx, kafkamsg.TopicSendEmail, msg)
// }
```

The file should have only `PublishChallengeCreated`, `PublishChallengeVerified`, and `PublishChallengeFailed`.

- [ ] **Step 7: Update verification gRPC handler**

In `verification-service/internal/handler/grpc_handler.go`:

Update `CreateChallenge` call — remove `req.GetEmail()`:
```go
func (h *VerificationGRPCHandler) CreateChallenge(ctx context.Context, req *pb.CreateChallengeRequest) (*pb.CreateChallengeResponse, error) {
	vc, err := h.svc.CreateChallenge(ctx, req.GetUserId(), req.GetSourceService(), req.GetSourceId(), req.GetMethod(), req.GetDeviceId())
```

Update `GetPendingChallenge` call — remove `req.GetDeviceId()`:
```go
func (h *VerificationGRPCHandler) GetPendingChallenge(ctx context.Context, req *pb.GetPendingChallengeRequest) (*pb.GetPendingChallengeResponse, error) {
	vc, err := h.svc.GetPendingChallenge(req.GetUserId())
```

- [ ] **Step 8: Run service tests — expect pass**

```bash
cd verification-service && go test ./... -v 2>&1 | grep -E "^(=== RUN|--- PASS|--- FAIL|FAIL|ok)"
```

Expected: all PASS.

- [ ] **Step 9: Commit**

```bash
git add verification-service/internal/service/verification_service.go \
        verification-service/internal/kafka/producer.go \
        verification-service/internal/handler/grpc_handler.go \
        verification-service/internal/repository/verification_challenge_repository.go \
        verification-service/internal/repository/verification_challenge_repository_test.go \
        verification-service/internal/service/verification_service_test.go
git commit -m "feat: remove email delivery and device_id from verification service"
```

---

## Task 7: Update API gateway verification handler

**Files:**
- Modify: `api-gateway/internal/handler/verification_handler.go`
- Modify: `api-gateway/internal/router/router.go`

- [ ] **Step 1: Update CreateVerification — remove email and fix validation**

In `api-gateway/internal/handler/verification_handler.go`, update `CreateVerification`:

```go
func (h *VerificationHandler) CreateVerification(c *gin.Context) {
	var req createVerificationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	if req.Method == "" {
		req.Method = "code_pull"
	}
	method, err := oneOf("method", req.Method, "code_pull")
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	userID := c.GetInt64("user_id")
	deviceIDStr := c.GetString("device_id")

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
```

- [ ] **Step 2: Update GetPendingVerifications — remove device_id**

```go
// @Summary Get pending mobile verifications (mobile polling)
// @Tags mobile-verifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/mobile/verifications/pending [get]
func (h *VerificationHandler) GetPendingVerifications(c *gin.Context) {
	userID := c.GetInt64("user_id")

	resp, err := h.notificationClient.GetPendingMobileItems(c.Request.Context(), &notificationpb.GetPendingMobileRequest{
		UserId: uint64(userID),
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
```

- [ ] **Step 3: Add AckVerification handler**

Add the following method to `verification_handler.go`:

```go
// @Summary Acknowledge a delivered mobile verification notification
// @Description Marks an inbox item as delivered so it no longer appears in future polls.
// @Tags mobile-verifications
// @Produce json
// @Security BearerAuth
// @Param id path int true "Inbox item ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{} "Invalid item id"
// @Failure 404 {object} map[string]interface{} "Item not found or already delivered"
// @Router /api/mobile/verifications/{id}/ack [post]
func (h *VerificationHandler) AckVerification(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid item id")
		return
	}

	_, err = h.notificationClient.AckMobileItem(c.Request.Context(), &notificationpb.AckMobileRequest{
		Id: id,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
```

- [ ] **Step 4: Register the ACK route in the router**

In `api-gateway/internal/router/router.go`, inside the `mobileVerify` group, add:

```go
// --- Mobile Verifications (MobileAuthMiddleware + RequireDeviceSignature) ---
mobileVerify := api.Group("/mobile/verifications")
mobileVerify.Use(middleware.MobileAuthMiddleware(authClient))
mobileVerify.Use(middleware.RequireDeviceSignature(authClient))
{
    verifyHandler := handler.NewVerificationHandler(verificationClient, notificationClient)
    mobileVerify.GET("/pending", verifyHandler.GetPendingVerifications)
    mobileVerify.POST("/:challenge_id/submit", verifyHandler.SubmitMobileVerification)
    mobileVerify.POST("/:id/ack", verifyHandler.AckVerification)  // new
}
```

- [ ] **Step 5: Build gateway**

```bash
cd api-gateway && go build ./...
```

Expected: exits 0.

- [ ] **Step 6: Regenerate swagger**

```bash
cd api-gateway && swag init -g cmd/main.go --output docs
```

- [ ] **Step 7: Commit**

```bash
git add api-gateway/internal/handler/verification_handler.go \
        api-gateway/internal/router/router.go \
        api-gateway/docs/
git commit -m "feat: add mobile ACK endpoint; remove email from CreateVerification; remove device_id from GetPendingVerifications"
```

---

## Task 8: Update integration test helpers

**Files:**
- Modify: `test-app/workflows/helpers_test.go`
- Modify: `test-app/workflows/verification_test.go`
- Modify: `test-app/workflows/wf_verification_retry_test.go`
- Modify: `test-app/workflows/wf_client_stock_banking_test.go`
- Modify: `test-app/workflows/wf_cross_currency_test.go`
- Modify: `test-app/workflows/wf_full_day_test.go`
- Modify: `test-app/workflows/wf_multicurrency_test.go`
- Modify: `test-app/workflows/wf_onboarding_test.go`

- [ ] **Step 1: Update helpers_test.go**

In `test-app/workflows/helpers_test.go`:

**a) Delete `scanKafkaForVerificationCode`** — the entire function from line ~111 to ~150.

**b) Replace `createChallengeOnly`** — remove `email` param and use bypass code:

```go
// createChallengeOnly creates a verification challenge and returns the challenge ID
// and the universal bypass code. No email scanning needed — code_pull challenges
// are verified with the bypass code "111111".
func createChallengeOnly(t *testing.T, c *client.APIClient, sourceService string, sourceID int) (int, string) {
	t.Helper()
	createResp, err := c.POST("/api/verifications", map[string]interface{}{
		"source_service": sourceService,
		"source_id":      sourceID,
	})
	if err != nil {
		t.Fatalf("createChallengeOnly: POST /api/verifications: %v", err)
	}
	helpers.RequireStatus(t, createResp, 200)
	challengeID := int(helpers.GetNumberField(t, createResp, "challenge_id"))
	return challengeID, "111111"
}
```

**c) Replace `createAndVerifyChallenge`** — remove `email` param:

```go
// createAndVerifyChallenge creates a verification challenge and verifies it
// using the universal bypass code.
func createAndVerifyChallenge(t *testing.T, c *client.APIClient, sourceService string, sourceID int) int {
	t.Helper()
	challengeID, code := createChallengeOnly(t, c, sourceService, sourceID)
	submitVerificationCode(t, c, challengeID, code)
	return challengeID
}
```

**d) Update `createAndExecutePayment`** — remove `email` param:

```go
func createAndExecutePayment(t *testing.T, fromClient *client.APIClient, toAccountNum string, amount float64) int {
	t.Helper()
	createResp, err := fromClient.POST("/api/me/payments", map[string]interface{}{
		"to_account_number": toAccountNum,
		"amount":            amount,
		"payment_purpose":   "test payment",
	})
	if err != nil {
		t.Fatalf("createAndExecutePayment: create: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	paymentID := int(helpers.GetNumberField(t, createResp, "id"))

	challengeID := createAndVerifyChallenge(t, fromClient, "payment", paymentID)

	execResp, err := fromClient.POST(fmt.Sprintf("/api/me/payments/%d/execute", paymentID), map[string]interface{}{
		"verification_code": "111111",
		"challenge_id":      challengeID,
	})
	if err != nil {
		t.Fatalf("createAndExecutePayment: execute: %v", err)
	}
	helpers.RequireStatus(t, execResp, 200)
	return paymentID
}
```

**e) Update `createAndExecuteTransfer`** — remove `email` param:

```go
func createAndExecuteTransfer(t *testing.T, clientC *client.APIClient, fromAccountNum string, toAccountNum string, amount float64) int {
	t.Helper()
	createResp, err := clientC.POST("/api/me/transfers", map[string]interface{}{
		"from_account_number": fromAccountNum,
		"to_account_number":   toAccountNum,
		"amount":              amount,
	})
	if err != nil {
		t.Fatalf("createAndExecuteTransfer: create: %v", err)
	}
	helpers.RequireStatus(t, createResp, 201)
	transferID := int(helpers.GetNumberField(t, createResp, "id"))

	challengeID := createAndVerifyChallenge(t, clientC, "transfer", transferID)

	execResp, err := clientC.POST(fmt.Sprintf("/api/me/transfers/%d/execute", transferID), map[string]interface{}{
		"verification_code": "111111",
		"challenge_id":      challengeID,
	})
	if err != nil {
		t.Fatalf("createAndExecuteTransfer: execute: %v", err)
	}
	helpers.RequireStatus(t, execResp, 200)
	return transferID
}
```

- [ ] **Step 2: Update verification_test.go**

In `test-app/workflows/verification_test.go`:

**a) Remove** `TestVerification_CreateChallengeWithEmailMethod` and `TestVerification_RealEmailCode` and `TestVerification_CodePullFallbackToEmail`.

**b) Replace** `TestVerification_InvalidMethodRejected` to also assert email is rejected:

```go
func TestVerification_InvalidMethodRejected(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	for _, method := range []string{"qr_scan", "number_match", "email"} {
		resp, err := clientC.POST("/api/verifications", map[string]interface{}{
			"source_service": "transfer",
			"source_id":      1,
			"method":         method,
		})
		if err != nil {
			t.Fatalf("error for method %s: %v", method, err)
		}
		if resp.StatusCode != 400 {
			t.Fatalf("expected 400 for unavailable method %s, got %d", method, resp.StatusCode)
		}
	}
}
```

**c) Add** `TestVerification_AckEndpointRequiresMobileAuth`:

```go
// TestVerification_AckEndpointRequiresMobileAuth verifies that the ACK endpoint
// rejects non-mobile tokens with 403.
func TestVerification_AckEndpointRequiresMobileAuth(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)

	// Client tokens are not mobile tokens — should get 403
	resp, err := clientC.POST("/api/mobile/verifications/1/ack", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("expected 403 for non-mobile token on ACK endpoint, got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 3: Update wf_verification_retry_test.go**

Find and update the call to `createChallengeOnly` — remove the email argument:

```go
challengeID, _ := createChallengeOnly(t, senderC, "payment", paymentID)
```

- [ ] **Step 4: Update remaining workflow test files**

In each file, remove the email argument from the listed calls:

`wf_client_stock_banking_test.go`:
```go
paymentID := createAndExecutePayment(t, senderC, receiverAcct, paymentAmount)
```

`wf_cross_currency_test.go`:
```go
transferID := createAndExecuteTransfer(t, clientC, rsdAcct, eurAcct, transferAmount)
```

`wf_full_day_test.go`:
```go
paymentID := createAndExecutePayment(t, clientAC, acctB, paymentAmount)
```

`wf_multicurrency_test.go`:
```go
transferID := createAndExecuteTransfer(t, clientC, rsdAcct, eurAcct, transferAmount)
```

`wf_onboarding_test.go`:
```go
paymentID := createAndExecutePayment(t, senderC, receiverAcct, paymentAmount)
```

- [ ] **Step 5: Build test-app**

```bash
cd test-app && go build ./... 2>&1 | head -20
```

Expected: exits 0 (no compilation errors).

- [ ] **Step 6: Commit**

```bash
git add test-app/workflows/
git commit -m "feat: remove email param from test helpers; replace Kafka scan with bypass code"
```

---

## Task 9: Update REST_API.md

**Files:**
- Modify: `docs/api/REST_API.md`

- [ ] **Step 1: Update Section 22 methods table**

Replace the methods table in Section 22:

```markdown
| Method | Status | Description |
|---|---|---|
| `code_pull` | **Active** (default) | 6-digit code delivered to the client's mobile app; client types it into the browser |
| `email` | **Removed** | Eliminated — all verification is via code_pull |
| `qr_scan` | **Not available** | Planned — selecting this returns 400 |
| `number_match` | **Not available** | Planned — selecting this returns 400 |
```

- [ ] **Step 2: Update POST /api/verifications description**

Update the `method` field row in the request body table:

```markdown
| `method` | string | No | `code_pull` only (default). `email`, `qr_scan`, `number_match` return 400. |
```

Remove the `challenge_data` email explanation from the endpoint.

- [ ] **Step 3: Add POST /api/mobile/verifications/:id/ack section**

After the `GET /api/mobile/verifications/pending` section, add:

```markdown
### POST /api/mobile/verifications/:id/ack

Acknowledge a mobile inbox item, marking it as delivered. Acknowledged items no longer appear in `GET /api/mobile/verifications/pending`.

**Authentication:** MobileAuthMiddleware + RequireDeviceSignature

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Inbox item ID (from the `id` field in the pending items list) |

**Response 200:**
```json
{ "success": true }
```

| Status | Description |
|---|---|
| 200 | Item marked as delivered |
| 400 | Invalid item id |
| 403 | Non-mobile token |
| 404 | Item not found or already delivered |
```

- [ ] **Step 4: Commit**

```bash
git add docs/api/REST_API.md
git commit -m "docs: update verification section — remove email method, add ACK endpoint"
```

---

## Task 10: Run full lint and test suite

- [ ] **Step 1: Lint all affected services**

```bash
make lint 2>&1 | grep -E "^(notification|verification|api-gateway|contract)" | head -30
```

Expected: zero new warnings. Fix any that appear before continuing.

- [ ] **Step 2: Unit tests**

```bash
make test 2>&1 | tail -40
```

Expected: all PASS, zero failures.

- [ ] **Step 3: Spot-check notification service**

```bash
cd notification-service && go test ./... -v -count=1 2>&1 | grep -E "^(=== RUN|--- PASS|--- FAIL|FAIL|ok)"
```

- [ ] **Step 4: Spot-check verification service**

```bash
cd verification-service && go test ./... -v -count=1 2>&1 | grep -E "^(=== RUN|--- PASS|--- FAIL|FAIL|ok)"
```

- [ ] **Step 5: Final commit if lint fixes were needed**

```bash
git add -p
git commit -m "fix: lint warnings from device_id and email removal"
```

---

## Self-Review Checklist

**Notification service device_id removal:**
- [x] `device_id` removed from `GetPendingMobileRequest` and `AckMobileRequest` proto
- [x] `DeviceID` removed from `MobileInboxItem` model
- [x] `GetPendingByUserAndDevice` removed (was dead production code)
- [x] `MarkDelivered` no longer filters by device_id — browser-created items are ack-able
- [x] `VerificationChallengeCreatedMessage.DeviceID` removed from Kafka contract
- [x] `MobilePushMessage.DeviceID` removed from Kafka contract
- [x] Notification consumer creates items without DeviceID
- [x] Repository tests updated

**Email delivery removal:**
- [x] `email` method removed from `validMethods`
- [x] `email` param removed from `CreateChallengeRequest` proto
- [x] `email` param removed from `CreateChallenge` service signature
- [x] Email delivery block removed from `CreateChallenge`
- [x] `PublishSendEmail` removed from verification kafka producer
- [x] `SubmitCode` guard updated to only allow `code_pull`
- [x] `buildChallengeData` email case removed
- [x] Gateway oneOf now only allows `code_pull`
- [x] Gateway no longer reads `email` from context or passes it to gRPC
- [x] `scanKafkaForVerificationCode` removed from test helpers
- [x] Helper signatures updated; all callers updated across 6 workflow test files
- [x] Email-method integration tests removed/renamed

**GetPendingChallenge device_id cleanup:**
- [x] `device_id` removed from `GetPendingChallengeRequest` proto
- [x] `GetPendingByUser` signature in repo updated (no deviceID param)
- [x] `GetPendingChallenge` service method updated (no deviceID param)
- [x] gRPC handler updated
- [x] Repo and service tests updated

**ACK gateway endpoint:**
- [x] `AckVerification` handler added with swagger annotations
- [x] Route `POST /:id/ack` added to `mobileVerify` group
- [x] Swagger regenerated
- [x] REST_API.md updated
- [x] Integration test verifies 403 for non-mobile tokens

**MobileAuthMiddleware remains intact** — mobile-only gate preserved on all `/api/mobile/verifications/*` routes.
