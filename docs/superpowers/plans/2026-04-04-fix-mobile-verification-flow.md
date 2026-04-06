# Fix Mobile Verification Flow

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the mobile app able to pull and verify ANY pending verification challenge for its user, regardless of whether the challenge was created from a browser or the mobile app.

**Architecture:** Two clean changes:
1. **verification-service**: Always publish `verification.challenge-created` Kafka event AND send email (both, not either/or). Remove the device_id-based delivery branching. Also fix `GetPendingChallenge` to look up by `user_id` only so the mobile app can find challenges with empty device_id.
2. **notification-service**: Route delivery based on whether the user has an active mobile device (look up from auth-service). Change pending-items query to match by `user_id` only.

**Tech Stack:** Go, gRPC, Kafka, GORM, PostgreSQL

---

## Root Cause Analysis

**The user's flow:**
1. Browser creates transaction → `POST /api/v1/payments` (client JWT, no `device_id`)
2. Browser creates verification → `POST /api/v1/verifications` (same JWT, `device_id=""`)
3. Verification-service sees `device_id==""` → sends email ONLY, does NOT publish `verification.challenge-created` Kafka event
4. Notification-service never creates a `MobileInboxItem`
5. Mobile app polls `GET /api/v1/mobile/verifications/pending` → nothing to show

**Three bugs:**
- **Bug A** (verification-service line 117): `sendViaEmail` gate means the `challenge-created` Kafka event is NEVER published for email-path challenges. The mobile app has no way to discover them.
- **Bug B** (verification-service repo line 61): `GetPendingByUser` filters `WHERE device_id = ?` — challenges from browser have `device_id=""`, so the mobile app (with a real device_id) never matches.
- **Bug C** (notification-service repo line 24): `GetPendingByUserAndDevice` also filters by exact device_id, same problem as Bug B.

**Design flaw:** `CreateChallenge` shouldn't decide delivery routing. It should just create the challenge and publish the event. Delivery is a notification concern.

---

## Task 1: Fix `GetPendingByUser` to Match by User Only (verification-service)

The repo method currently filters by `user_id AND device_id`. Change it to filter by `user_id` only, so the mobile app can discover challenges created from the browser.

**Files:**
- Modify: `verification-service/internal/repository/verification_challenge_repository.go`
- Modify: `verification-service/internal/repository/verification_challenge_repository_test.go`

### Steps

- [ ] **1.1** In `verification-service/internal/repository/verification_challenge_repository.go`, replace `GetPendingByUser`:

```go
// GetPendingByUser returns the most recent pending, non-expired challenge for a user.
// Matches by user_id only — the mobile app must be able to find challenges created
// from the browser (which have empty device_id).
func (r *VerificationChallengeRepository) GetPendingByUser(userID uint64, deviceID string) (*model.VerificationChallenge, error) {
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

Note: `deviceID` parameter is kept in the signature to avoid breaking callers, but is no longer used in the query.

- [ ] **1.2** Update the test `TestGetPendingByUser` in `verification_challenge_repository_test.go` to verify it finds challenges regardless of device_id. Add a new test:

```go
func TestGetPendingByUser_FindsChallengeWithEmptyDeviceID(t *testing.T) {
	repo, _ := setupTestRepo(t)

	future := time.Now().Add(10 * time.Minute)
	// Challenge created from browser — no device_id
	browserChallenge := newChallenge(500, "", "pending", future)
	require.NoError(t, repo.Create(browserChallenge))

	// Mobile app queries with its device_id — should still find browser challenge
	got, err := repo.GetPendingByUser(500, "mobile-device-xyz")
	require.NoError(t, err)
	assert.Equal(t, browserChallenge.ID, got.ID)
}
```

- [ ] **1.3** Run tests:
```bash
cd verification-service && go test ./internal/repository/ -run TestGetPendingByUser -v
```

- [ ] **1.4** Commit: `fix(verification): match pending challenges by user_id only, not device_id`

---

## Task 2: Always Publish `challenge-created` Kafka Event (verification-service)

Remove the email-vs-mobile branching in `CreateChallenge`. Always send the email (when method warrants it) AND always publish the Kafka event. The notification-service will handle mobile delivery.

**Files:**
- Modify: `verification-service/internal/service/verification_service.go`

### Steps

- [ ] **2.1** In `verification-service/internal/service/verification_service.go`, replace the delivery logic in `CreateChallenge` (lines 116-154, the `sendViaEmail` block through the end of the mobile else block) with:

```go
	// --- Delivery ---
	// 1. Send email when: method is "email", or code_pull created from browser (no device).
	if method == "email" || (method == "code_pull" && deviceID == "") {
		if email != "" {
			if err := s.producer.PublishSendEmail(ctx, kafkamsg.SendEmailMessage{
				To:        email,
				EmailType: kafkamsg.EmailTypeVerificationCode,
				Data: map[string]string{
					"code":         code,
					"challenge_id": fmt.Sprintf("%d", vc.ID),
					"expires_in":   "5 minutes",
				},
			}); err != nil {
				log.Printf("warn: failed to publish send-email for verification %d: %v", vc.ID, err)
			}
		}
	}

	// 2. Always publish challenge-created event so notification-service can create
	//    a mobile inbox item. This makes the challenge visible to the mobile app
	//    regardless of how it was created.
	displayData, err := buildDisplayData(method, code, challengeData)
	if err != nil {
		log.Printf("warn: failed to build display data for verification %d: %v", vc.ID, err)
	} else {
		displayDataJSON, _ := json.Marshal(displayData)
		if err := s.producer.PublishChallengeCreated(ctx, kafkamsg.VerificationChallengeCreatedMessage{
			ChallengeID:     vc.ID,
			UserID:          userID,
			DeviceID:        deviceID, // may be empty — notification-service resolves it
			Method:          method,
			DisplayData:     string(displayDataJSON),
			DeliveryChannel: "mobile",
			ExpiresAt:       vc.ExpiresAt.UTC().Format(time.RFC3339),
		}); err != nil {
			log.Printf("warn: failed to publish challenge-created for verification %d: %v", vc.ID, err)
		}
	}
```

- [ ] **2.2** Build:
```bash
cd verification-service && go build ./...
```

- [ ] **2.3** Run tests:
```bash
cd verification-service && go test ./internal/service/ -v
```

- [ ] **2.4** Commit: `fix(verification): always publish challenge-created event for mobile discovery`

---

## Task 3: Resolve Device ID in Notification Consumer

When the `verification.challenge-created` event arrives with an empty `device_id` (browser-created challenge), the notification consumer needs to look up the user's active mobile device from auth-service to create the inbox item with the right device_id.

**Files:**
- Modify: `notification-service/internal/consumer/verification_consumer.go`
- Modify: `notification-service/cmd/main.go`
- Modify: `notification-service/internal/config/config.go` (add `AUTH_GRPC_ADDR` if not present)

### Steps

- [ ] **3.1** Check if notification-service already has `AUTH_GRPC_ADDR` config. If not, add it to `notification-service/internal/config/config.go`:

Add field:
```go
	AuthGRPCAddr string
```

Add in `Load()`:
```go
	AuthGRPCAddr: getEnv("AUTH_GRPC_ADDR", "localhost:50051"),
```

- [ ] **3.2** Add auth-service client to `VerificationConsumer`. In `notification-service/internal/consumer/verification_consumer.go`, update the struct and constructor:

```go
type VerificationConsumer struct {
	reader     *kafkago.Reader
	sender     *sender.EmailSender
	producer   *kafkaprod.Producer
	inboxRepo  *repository.MobileInboxRepository
	authClient authpb.AuthServiceClient
}

func NewVerificationConsumer(brokers string, emailSender *sender.EmailSender, producer *kafkaprod.Producer, inboxRepo *repository.MobileInboxRepository, authClient authpb.AuthServiceClient) *VerificationConsumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    kafkamsg.TopicVerificationChallengeCreated,
		GroupID:  "notification-service",
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &VerificationConsumer{
		reader:     reader,
		sender:     emailSender,
		producer:   producer,
		inboxRepo:  inboxRepo,
		authClient: authClient,
	}
}
```

Add the import:
```go
	authpb "github.com/exbanka/contract/authpb"
```

- [ ] **3.3** Update `handleMobileDelivery` to resolve empty device_id:

```go
func (c *VerificationConsumer) handleMobileDelivery(ctx context.Context, event kafkamsg.VerificationChallengeCreatedMessage) {
	// Resolve device_id if empty (challenge created from browser)
	deviceID := event.DeviceID
	if deviceID == "" && c.authClient != nil {
		devResp, err := c.authClient.GetDeviceInfo(ctx, &authpb.GetDeviceInfoRequest{
			UserId: int64(event.UserID),
		})
		if err == nil && devResp.DeviceId != "" && devResp.Status == "active" {
			deviceID = devResp.DeviceId
		} else {
			log.Printf("verification consumer: no active device for user %d, skipping mobile delivery for challenge %d", event.UserID, event.ChallengeID)
			return
		}
	}

	expiresAt, err := time.Parse(time.RFC3339, event.ExpiresAt)
	if err != nil {
		log.Printf("verification consumer: invalid expires_at: %v", err)
		return
	}

	item := &model.MobileInboxItem{
		UserID:      event.UserID,
		DeviceID:    deviceID,
		ChallengeID: event.ChallengeID,
		Method:      event.Method,
		DisplayData: datatypes.JSON(event.DisplayData),
		ExpiresAt:   expiresAt,
	}
	if err := c.inboxRepo.Create(item); err != nil {
		log.Printf("verification consumer: inbox create error: %v", err)
		return
	}

	// Publish to mobile-push topic for WebSocket delivery
	payloadJSON, _ := json.Marshal(map[string]interface{}{
		"challenge_id": event.ChallengeID,
		"method":       event.Method,
		"display_data": event.DisplayData,
		"expires_at":   event.ExpiresAt,
	})
	pushMsg := kafkamsg.MobilePushMessage{
		UserID:   event.UserID,
		DeviceID: deviceID,
		Type:     "verification_challenge",
		Payload:  string(payloadJSON),
	}
	if err := c.producer.Publish(ctx, kafkamsg.TopicMobilePush, pushMsg); err != nil {
		log.Printf("verification consumer: mobile push publish error: %v", err)
	} else {
		svc.NotificationMobilePushTotal.Inc()
	}
}
```

- [ ] **3.4** Update `notification-service/cmd/main.go` to create the auth client and pass it to the consumer. Add after the existing gRPC client connections:

```go
	// Connect to auth-service for device lookups
	authConn, err := grpc.NewClient(cfg.AuthGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("warn: failed to connect to auth service: %v (device lookup will not work)", err)
	}
	if authConn != nil {
		defer authConn.Close()
	}
	var authClient authpb.AuthServiceClient
	if authConn != nil {
		authClient = authpb.NewAuthServiceClient(authConn)
	}
```

Update the `NewVerificationConsumer` call to pass `authClient`.

Add imports:
```go
	"google.golang.org/grpc/credentials/insecure"
	authpb "github.com/exbanka/contract/authpb"
```

- [ ] **3.5** Update `docker-compose.yml`: add `AUTH_GRPC_ADDR: "auth-service:50051"` to notification-service environment and `auth-service` to its `depends_on`.

- [ ] **3.6** Build:
```bash
cd notification-service && go build ./...
```

- [ ] **3.7** Commit: `feat(notification): resolve device_id from auth-service for browser-created challenges`

---

## Task 4: Query Pending Items by User Only (notification-service)

The `GetPendingMobileItems` gRPC handler queries by `user_id + device_id`. Change to query by `user_id` only so the mobile app sees all pending challenges.

**Files:**
- Modify: `notification-service/internal/repository/mobile_inbox_repository.go`
- Modify: `notification-service/internal/handler/grpc_handler.go`
- Modify: `notification-service/internal/repository/mobile_inbox_repository_test.go`

### Steps

- [ ] **4.1** Add `GetPendingByUser` to `notification-service/internal/repository/mobile_inbox_repository.go`:

```go
// GetPendingByUser returns all pending, non-expired inbox items for a user regardless of device.
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
```

- [ ] **4.2** Update `GetPendingMobileItems` in `notification-service/internal/handler/grpc_handler.go`:

Replace:
```go
	items, err := h.inboxRepo.GetPendingByUserAndDevice(req.UserId, req.DeviceId)
```
With:
```go
	items, err := h.inboxRepo.GetPendingByUser(req.UserId)
```

- [ ] **4.3** Add test in `notification-service/internal/repository/mobile_inbox_repository_test.go`:

```go
func TestGetPendingByUser_FindsItemsAcrossDevices(t *testing.T) {
	db := newInboxTestDB(t)
	repo := NewMobileInboxRepository(db)

	seedInboxItem(t, db, model.MobileInboxItem{
		UserID:      10,
		DeviceID:    "device-abc",
		ChallengeID: 100,
		Method:      "code_pull",
	})
	seedInboxItem(t, db, model.MobileInboxItem{
		UserID:      10,
		DeviceID:    "device-def",
		ChallengeID: 101,
		Method:      "code_pull",
	})
	// Different user — should NOT appear
	seedInboxItem(t, db, model.MobileInboxItem{
		UserID:      99,
		DeviceID:    "device-abc",
		ChallengeID: 102,
		Method:      "code_pull",
	})

	items, err := repo.GetPendingByUser(10)
	require.NoError(t, err)
	assert.Len(t, items, 2)
}
```

- [ ] **4.4** Run tests:
```bash
cd notification-service && go test ./internal/repository/ -v
cd notification-service && go build ./...
```

- [ ] **4.5** Commit: `fix(notification): query pending inbox items by user_id only`

---

## Task 5: Build + Full Test

### Steps

- [ ] **5.1** Run tidy:
```bash
make tidy
```

- [ ] **5.2** Build all:
```bash
make build
```

- [ ] **5.3** Run full test suite:
```bash
make test
```

- [ ] **5.4** Fix any failures and commit.

---

## Summary of Changes

| File | Change |
|------|--------|
| `verification-service/internal/repository/verification_challenge_repository.go` | `GetPendingByUser` matches by `user_id` only |
| `verification-service/internal/repository/verification_challenge_repository_test.go` | Test for cross-device pending lookup |
| `verification-service/internal/service/verification_service.go` | Always publish `challenge-created` Kafka event; email is additive, not exclusive |
| `notification-service/internal/consumer/verification_consumer.go` | Add auth-service client; resolve empty device_id from auth-service |
| `notification-service/internal/repository/mobile_inbox_repository.go` | Add `GetPendingByUser` (user-only) |
| `notification-service/internal/repository/mobile_inbox_repository_test.go` | Test for user-only query |
| `notification-service/internal/handler/grpc_handler.go` | Use `GetPendingByUser` |
| `notification-service/internal/config/config.go` | Add `AUTH_GRPC_ADDR` |
| `notification-service/cmd/main.go` | Wire auth-service client |
| `docker-compose.yml` | Add `AUTH_GRPC_ADDR` to notification-service |

## Flow After Fix

1. Browser creates payment → `pending_verification`
2. Browser calls `POST /api/v1/verifications` with `device_id=""`
3. Verification-service creates challenge, sends email (code_pull from browser)
4. Verification-service **also** publishes `challenge-created` Kafka event (device_id may be empty)
5. Notification-service consumes event → sees empty device_id → looks up user's active device from auth-service → creates `MobileInboxItem` with resolved device_id
6. Notification-service pushes to WebSocket via `notification.mobile-push`
7. Mobile app polls `GET /api/v1/mobile/verifications/pending` → gets the challenge
8. User sees the code on mobile and can submit it
