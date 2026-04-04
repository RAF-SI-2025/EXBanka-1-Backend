# Auth & Per-Device Sessions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bind refresh tokens to sessions with device info, enable per-device revocation, and add session listing and login history endpoints.

**Architecture:** Integrate the existing but unused ActiveSession model into all auth flows. Link RefreshToken -> ActiveSession via FK. Forward IP/UserAgent from gateway via gRPC metadata. New RPCs for session management.

**Tech Stack:** Go, GORM, PostgreSQL, gRPC, Protocol Buffers

---

### Task 1: Extend models with new fields

**Files:**
- Edit: `auth-service/internal/model/token.go`
- Edit: `auth-service/internal/model/active_session.go`
- Edit: `auth-service/internal/model/login_attempt.go`

Add session linkage to RefreshToken, device tracking to ActiveSession, and richer metadata to LoginAttempt. GORM AutoMigrate will handle schema changes automatically.

- [ ] **Step 1: Add SessionID, IPAddress, UserAgent to RefreshToken**

```go
// auth-service/internal/model/token.go
type RefreshToken struct {
	ID         int64     `gorm:"primaryKey;autoIncrement"`
	AccountID  int64     `gorm:"not null;index"`
	Token      string    `gorm:"uniqueIndex;not null"`
	ExpiresAt  time.Time `gorm:"not null"`
	Revoked    bool      `gorm:"default:false"`
	SystemType string    `gorm:"size:20;not null;default:'employee'"`
	SessionID  *int64    `gorm:"index"`     // FK to ActiveSession
	IPAddress  string    `gorm:"size:45"`   // IP from gateway metadata
	UserAgent  string    `gorm:"size:512"`  // User-Agent from gateway metadata
	CreatedAt  time.Time
}
```

- [ ] **Step 2: Add DeviceID, SystemType to ActiveSession**

```go
// auth-service/internal/model/active_session.go
type ActiveSession struct {
	ID           int64      `gorm:"primaryKey;autoIncrement"`
	UserID       int64      `gorm:"not null;index:idx_session_user"`
	UserRole     string     `gorm:"size:30;not null"`
	IPAddress    string     `gorm:"size:45"`
	UserAgent    string     `gorm:"size:512"`
	DeviceID     string     `gorm:"size:36;index:idx_session_device"` // mobile device UUID or empty
	SystemType   string     `gorm:"size:20;not null;default:'employee'"`
	LastActiveAt time.Time  `gorm:"not null"`
	CreatedAt    time.Time  `gorm:"not null"`
	RevokedAt    *time.Time
}
```

- [ ] **Step 3: Add UserAgent, DeviceType to LoginAttempt**

```go
// auth-service/internal/model/login_attempt.go
type LoginAttempt struct {
	ID         int64     `gorm:"primaryKey;autoIncrement"`
	Email      string    `gorm:"not null;index:idx_login_attempt_email"`
	IPAddress  string    `gorm:"size:45"`
	UserAgent  string    `gorm:"size:512"`
	DeviceType string    `gorm:"size:20"` // "browser", "mobile", "api"
	Success    bool      `gorm:"not null;default:false"`
	CreatedAt  time.Time `gorm:"not null;index:idx_login_attempt_created"`
}
```

- [ ] **Step 4: Verify AutoMigrate in cmd/main.go already includes all three models**

`auth-service/cmd/main.go` already calls `db.AutoMigrate(&model.ActiveSession{}, &model.RefreshToken{}, &model.LoginAttempt{}, ...)`. GORM will add the new columns on next startup. No changes needed to `cmd/main.go` for this step.

**Test command:** `cd auth-service && go build ./...`

- [ ] **Commit:** `feat(auth): add session/device fields to RefreshToken, ActiveSession, LoginAttempt models`

---

### Task 2: Create ActiveSession repository

**Files:**
- Create: `auth-service/internal/repository/session_repository.go`

This follows the same pattern as `token_repository.go` and `login_attempt_repository.go`. All methods operate on `model.ActiveSession`.

- [ ] **Step 1: Implement SessionRepository**

```go
// auth-service/internal/repository/session_repository.go
package repository

import (
	"time"

	"github.com/exbanka/auth-service/internal/model"
	"gorm.io/gorm"
)

type SessionRepository struct {
	db *gorm.DB
}

func NewSessionRepository(db *gorm.DB) *SessionRepository {
	return &SessionRepository{db: db}
}

// Create inserts a new active session record.
func (r *SessionRepository) Create(s *model.ActiveSession) error {
	return r.db.Create(s).Error
}

// GetByID returns a session by its primary key.
func (r *SessionRepository) GetByID(id int64) (*model.ActiveSession, error) {
	var s model.ActiveSession
	if err := r.db.First(&s, id).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

// ListByUser returns all non-revoked sessions for a user, ordered by most recent first.
func (r *SessionRepository) ListByUser(userID int64) ([]model.ActiveSession, error) {
	var sessions []model.ActiveSession
	err := r.db.Where("user_id = ? AND revoked_at IS NULL", userID).
		Order("last_active_at DESC").
		Find(&sessions).Error
	return sessions, err
}

// ListAllByUser returns all sessions (including revoked) for a user, ordered by creation time descending.
func (r *SessionRepository) ListAllByUser(userID int64) ([]model.ActiveSession, error) {
	var sessions []model.ActiveSession
	err := r.db.Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&sessions).Error
	return sessions, err
}

// UpdateLastActive updates the LastActiveAt timestamp for a session.
func (r *SessionRepository) UpdateLastActive(sessionID int64) error {
	return r.db.Model(&model.ActiveSession{}).
		Where("id = ?", sessionID).
		Update("last_active_at", time.Now()).Error
}

// Revoke sets the RevokedAt timestamp on a session.
func (r *SessionRepository) Revoke(sessionID int64) error {
	now := time.Now()
	return r.db.Model(&model.ActiveSession{}).
		Where("id = ? AND revoked_at IS NULL", sessionID).
		Update("revoked_at", &now).Error
}

// RevokeAllForUser revokes all active sessions for a given user.
func (r *SessionRepository) RevokeAllForUser(userID int64) error {
	now := time.Now()
	return r.db.Model(&model.ActiveSession{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", &now).Error
}

// RevokeAllExcept revokes all active sessions for a user except the given session ID.
func (r *SessionRepository) RevokeAllExcept(userID int64, keepSessionID int64) error {
	now := time.Now()
	return r.db.Model(&model.ActiveSession{}).
		Where("user_id = ? AND id != ? AND revoked_at IS NULL", userID, keepSessionID).
		Update("revoked_at", &now).Error
}

// RevokeByDeviceID revokes all sessions linked to a specific mobile device.
func (r *SessionRepository) RevokeByDeviceID(deviceID string) error {
	now := time.Now()
	return r.db.Model(&model.ActiveSession{}).
		Where("device_id = ? AND revoked_at IS NULL", deviceID).
		Update("revoked_at", &now).Error
}

// CountActiveByUser returns the number of non-revoked sessions for a user.
func (r *SessionRepository) CountActiveByUser(userID int64) (int64, error) {
	var count int64
	err := r.db.Model(&model.ActiveSession{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Count(&count).Error
	return count, err
}
```

**Test command:** `cd auth-service && go build ./...`

- [ ] **Commit:** `feat(auth): add SessionRepository for ActiveSession CRUD`

---

### Task 3: Add gRPC metadata extraction helper

**Files:**
- Create: `auth-service/internal/handler/metadata.go`

This helper extracts IP address and User-Agent forwarded from the API gateway via gRPC metadata.

- [ ] **Step 1: Implement metadata extraction**

```go
// auth-service/internal/handler/metadata.go
package handler

import (
	"context"
	"strings"

	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

// requestMeta holds client info extracted from gRPC metadata.
type requestMeta struct {
	IPAddress string
	UserAgent string
}

// extractRequestMeta pulls x-forwarded-for and x-user-agent from gRPC metadata.
// Falls back to the peer address if no forwarded IP is present.
func extractRequestMeta(ctx context.Context) requestMeta {
	rm := requestMeta{}

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("x-forwarded-for"); len(vals) > 0 {
			// Take first IP in case of comma-separated chain
			rm.IPAddress = strings.TrimSpace(strings.Split(vals[0], ",")[0])
		}
		if vals := md.Get("x-user-agent"); len(vals) > 0 {
			rm.UserAgent = vals[0]
		}
	}

	// Fallback to gRPC peer address if no forwarded IP
	if rm.IPAddress == "" {
		if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
			addr := p.Addr.String()
			// Strip port
			if idx := strings.LastIndex(addr, ":"); idx != -1 {
				rm.IPAddress = addr[:idx]
			} else {
				rm.IPAddress = addr
			}
		}
	}

	return rm
}

// detectDeviceType infers device type from User-Agent string.
func detectDeviceType(userAgent string) string {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "mobile") || strings.Contains(ua, "android") || strings.Contains(ua, "iphone"):
		return "mobile"
	case strings.Contains(ua, "postman") || strings.Contains(ua, "curl") || strings.Contains(ua, "httpie"):
		return "api"
	default:
		return "browser"
	}
}
```

**Test command:** `cd auth-service && go build ./...`

- [ ] **Commit:** `feat(auth): add gRPC metadata extraction for IP/UserAgent`

---

### Task 4: Add session Kafka events to contract

**Files:**
- Edit: `contract/kafka/messages.go`

Add topic constants and message structs for session events.

- [ ] **Step 1: Add session event types**

Append to the existing auth topic constants block in `contract/kafka/messages.go`:

```go
// In the existing auth topics block:
const (
	TopicAuthAccountStatusChanged  = "auth.account-status-changed"
	TopicAuthDeadLetter            = "auth.dead-letter"
	TopicAuthMobileDeviceActivated = "auth.mobile-device-activated"
	TopicAuthSessionCreated        = "auth.session-created"
	TopicAuthSessionRevoked        = "auth.session-revoked"
)

// AuthSessionCreatedMessage is published when a new login session is created.
type AuthSessionCreatedMessage struct {
	SessionID  int64  `json:"session_id"`
	UserID     int64  `json:"user_id"`
	SystemType string `json:"system_type"`
	IPAddress  string `json:"ip_address"`
	UserAgent  string `json:"user_agent"`
	DeviceType string `json:"device_type"`
}

// AuthSessionRevokedMessage is published when a session is revoked (logout/force-revoke).
type AuthSessionRevokedMessage struct {
	SessionID int64  `json:"session_id"`
	UserID    int64  `json:"user_id"`
	Reason    string `json:"reason"` // "logout", "force_revoke", "password_reset", "device_deactivation"
}
```

**Test command:** `cd contract && go build ./...`

- [ ] **Commit:** `feat(contract): add session created/revoked Kafka event types`

---

### Task 5: Integrate sessions into Login flow

**Files:**
- Edit: `auth-service/internal/service/auth_service.go`

The Login method must:
1. Accept IP/UserAgent parameters (passed from handler via metadata).
2. Create an ActiveSession record after successful authentication.
3. Link the new RefreshToken to the session via SessionID.
4. Record IP/UserAgent on both the session and the login attempt.
5. Publish a `TopicAuthSessionCreated` Kafka event after success.

- [ ] **Step 1: Add sessionRepo field to AuthService and update constructor**

```go
// auth-service/internal/service/auth_service.go

type AuthService struct {
	tokenRepo        *repository.TokenRepository
	sessionRepo      *repository.SessionRepository  // NEW
	loginAttemptRepo *repository.LoginAttemptRepository
	totpRepo         *repository.TOTPRepository
	totpSvc          *TOTPService
	jwtService       *JWTService
	accountRepo      *repository.AccountRepository
	userClient       userpb.UserServiceClient
	producer         *kafkaprod.Producer
	cache            *cache.RedisCache
	refreshExp       time.Duration
	mobileRefreshExp time.Duration
	frontendBaseURL  string
	pepper           string
}

func NewAuthService(
	tokenRepo *repository.TokenRepository,
	sessionRepo *repository.SessionRepository,  // NEW
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
	return &AuthService{
		tokenRepo:        tokenRepo,
		sessionRepo:      sessionRepo,
		loginAttemptRepo: loginAttemptRepo,
		totpRepo:         totpRepo,
		totpSvc:          totpSvc,
		jwtService:       jwtService,
		accountRepo:      accountRepo,
		userClient:       userClient,
		producer:         producer,
		cache:            cache,
		refreshExp:       refreshExp,
		mobileRefreshExp: mobileRefreshExp,
		frontendBaseURL:  frontendBaseURL,
		pepper:           pepper,
	}
}
```

- [ ] **Step 2: Change Login signature to accept IP/UserAgent, create session, link refresh token**

Change the Login method signature from:
```go
func (s *AuthService) Login(ctx context.Context, email, password string) (string, string, error) {
```
to:
```go
func (s *AuthService) Login(ctx context.Context, email, password, ipAddress, userAgent string) (string, string, error) {
```

After the existing `RecordAttempt` call (line 165), add the IP and UserAgent:
```go
_ = s.loginAttemptRepo.RecordAttempt(email, ipAddress, true)
```

After generating the access token (around line 188), replace the refresh token creation block (lines 190-201) with:

```go
	refreshToken, err := generateToken()
	if err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}

	// Determine user role label for session
	userRole := account.PrincipalType
	if account.PrincipalType == model.PrincipalTypeEmployee && len(loginRoles) > 0 {
		userRole = loginRoles[0]
	}

	// Create session
	session := &model.ActiveSession{
		UserID:       account.PrincipalID,
		UserRole:     userRole,
		IPAddress:    ipAddress,
		UserAgent:    userAgent,
		SystemType:   account.PrincipalType,
		LastActiveAt: time.Now(),
		CreatedAt:    time.Now(),
	}
	if err := s.sessionRepo.Create(session); err != nil {
		log.Printf("warn: failed to create session: %v", err)
		// Non-fatal: proceed without session tracking
	}

	rt := &model.RefreshToken{
		AccountID:  account.ID,
		Token:      refreshToken,
		ExpiresAt:  time.Now().Add(s.refreshExp),
		SystemType: account.PrincipalType,
		IPAddress:  ipAddress,
		UserAgent:  userAgent,
	}
	if session.ID != 0 {
		rt.SessionID = &session.ID
	}
	if err := s.tokenRepo.CreateRefreshToken(rt); err != nil {
		return "", "", err
	}

	// Publish session created event
	if session.ID != 0 {
		_ = s.producer.Publish(ctx, kafkamsg.TopicAuthSessionCreated, kafkamsg.AuthSessionCreatedMessage{
			SessionID:  session.ID,
			UserID:     account.PrincipalID,
			SystemType: account.PrincipalType,
			IPAddress:  ipAddress,
			UserAgent:  userAgent,
			DeviceType: detectDeviceType(userAgent),
		})
	}

	return accessToken, refreshToken, nil
```

Note: `loginRoles` is only in scope for the `PrincipalTypeEmployee` branch. Use a package-level variable before the switch to capture the roles for both paths:

```go
	var loginRoles []string
	// ... switch block sets loginRoles for employee or defaults to []string{"client"} ...
```

- [ ] **Step 3: Also pass IP/UserAgent to failed login attempt recordings**

Update all `RecordFailureAndCheckLock` calls in Login to also pass the IP. The existing code passes `""` for IP in the failure branches. Update to pass `ipAddress`. For example:

```go
locked, remaining, _ := s.loginAttemptRepo.RecordFailureAndCheckLock(email, maxFailedAttempts, lockoutWindow, lockoutDuration)
```

No change is needed here since `RecordFailureAndCheckLock` creates `LoginAttempt` internally with an empty IP. We need to add an `ipAddress` parameter to `RecordFailureAndCheckLock` and `RecordAttempt` (see Task 5, Step 4).

- [ ] **Step 4: Update RecordAttempt and RecordFailureAndCheckLock to accept UserAgent and DeviceType**

In `auth-service/internal/repository/login_attempt_repository.go`:

Change `RecordAttempt` signature:
```go
func (r *LoginAttemptRepository) RecordAttempt(email, ip, userAgent, deviceType string, success bool) error {
	return r.db.Create(&model.LoginAttempt{
		Email:      email,
		IPAddress:  ip,
		UserAgent:  userAgent,
		DeviceType: deviceType,
		Success:    success,
		CreatedAt:  time.Now(),
	}).Error
}
```

Change `RecordFailureAndCheckLock` to accept `userAgent` and `deviceType`:
```go
func (r *LoginAttemptRepository) RecordFailureAndCheckLock(email, ip, userAgent, deviceType string, maxAttempts int, window, lockDuration time.Duration) (locked bool, remaining int, err error) {
```

And update the `LoginAttempt` creation inside the transaction:
```go
if e := tx.Create(&model.LoginAttempt{
	Email:      email,
	IPAddress:  ip,
	UserAgent:  userAgent,
	DeviceType: deviceType,
	Success:    false,
	CreatedAt:  time.Now(),
}).Error; e != nil {
	return e
}
```

- [ ] **Step 5: Update all callers of RecordAttempt and RecordFailureAndCheckLock in auth_service.go**

Every call to `RecordFailureAndCheckLock` in `Login` currently looks like:
```go
locked, remaining, _ := s.loginAttemptRepo.RecordFailureAndCheckLock(email, maxFailedAttempts, lockoutWindow, lockoutDuration)
```

Change each to:
```go
deviceType := detectDeviceType(userAgent)
locked, remaining, _ := s.loginAttemptRepo.RecordFailureAndCheckLock(email, ipAddress, userAgent, deviceType, maxFailedAttempts, lockoutWindow, lockoutDuration)
```

Add a `detectDeviceType` helper in `auth_service.go` (or import from handler package if exported):
```go
func detectDeviceType(userAgent string) string {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "mobile") || strings.Contains(ua, "android") || strings.Contains(ua, "iphone"):
		return "mobile"
	case strings.Contains(ua, "postman") || strings.Contains(ua, "curl") || strings.Contains(ua, "httpie"):
		return "api"
	default:
		return "browser"
	}
}
```

And update the `RecordAttempt` success call:
```go
_ = s.loginAttemptRepo.RecordAttempt(email, ipAddress, userAgent, detectDeviceType(userAgent), true)
```

- [ ] **Step 6: Wire sessionRepo into cmd/main.go**

In `auth-service/cmd/main.go`, after `accountRepo` initialization (around line 74):
```go
sessionRepo := repository.NewSessionRepository(db)
```

Update `NewAuthService` call to include `sessionRepo`:
```go
authService := service.NewAuthService(tokenRepo, sessionRepo, loginAttemptRepo, totpRepo, totpSvc, jwtService, accountRepo, userClient, producer, redisCache, cfg.RefreshExpiry, cfg.MobileRefreshExpiry, cfg.FrontendBaseURL, cfg.PasswordPepper)
```

- [ ] **Step 7: Add new Kafka topics to EnsureTopics**

In `auth-service/cmd/main.go`, add the new session topics:
```go
kafkaprod.EnsureTopics(cfg.KafkaBrokers,
	"user.employee-created",
	"client.created",
	"notification.send-email",
	kafkamsg.TopicAuthAccountStatusChanged,
	kafkamsg.TopicAuthDeadLetter,
	kafkamsg.TopicAuthMobileDeviceActivated,
	kafkamsg.TopicAuthSessionCreated,   // NEW
	kafkamsg.TopicAuthSessionRevoked,   // NEW
)
```

**Test command:** `cd auth-service && go build ./...`

- [ ] **Commit:** `feat(auth): create session on login, link to refresh token, publish events`

---

### Task 6: Integrate sessions into RefreshToken flow

**Files:**
- Edit: `auth-service/internal/service/auth_service.go`

When refreshing a token, the new refresh token should inherit the session from the old one, and the session's `LastActiveAt` should be updated.

- [ ] **Step 1: Update RefreshToken method**

Change the `RefreshToken` method signature from:
```go
func (s *AuthService) RefreshToken(ctx context.Context, refreshTokenStr string) (string, string, error) {
```
to:
```go
func (s *AuthService) RefreshToken(ctx context.Context, refreshTokenStr, ipAddress, userAgent string) (string, string, error) {
```

After revoking the old token (line 279) and before generating the new refresh token, add:

```go
	// Update session activity
	if rt.SessionID != nil {
		_ = s.sessionRepo.UpdateLastActive(*rt.SessionID)
	}
```

When creating the new refresh token (replace lines 316-323):

```go
	newRefreshToken, err := generateToken()
	if err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	newRT := &model.RefreshToken{
		AccountID:  acct.ID,
		Token:      newRefreshToken,
		ExpiresAt:  time.Now().Add(s.refreshExp),
		SystemType: systemType,
		SessionID:  rt.SessionID, // Inherit session from old token
		IPAddress:  ipAddress,
		UserAgent:  userAgent,
	}
	if err := s.tokenRepo.CreateRefreshToken(newRT); err != nil {
		return "", "", err
	}
```

- [ ] **Step 2: Update RefreshTokenForMobile similarly**

In the `RefreshTokenForMobile` method, inherit `SessionID` from the old token when creating the new one:

```go
	newRT := &model.RefreshToken{
		AccountID:  acct.ID,
		Token:      newRefreshStr,
		ExpiresAt:  time.Now().Add(s.mobileRefreshExp),
		SystemType: systemType,
		SessionID:  rt.SessionID, // Inherit session
	}
```

And update session activity:
```go
	if rt.SessionID != nil {
		_ = s.sessionRepo.UpdateLastActive(*rt.SessionID)
	}
```

**Test command:** `cd auth-service && go build ./...`

- [ ] **Commit:** `feat(auth): link refreshed tokens to existing sessions, update activity`

---

### Task 7: Integrate sessions into Logout flow

**Files:**
- Edit: `auth-service/internal/service/auth_service.go`
- Edit: `auth-service/internal/repository/token_repository.go`

Logout should revoke the session associated with the refresh token and publish a session revoked event.

- [ ] **Step 1: Update Logout method to revoke associated session**

Change the `Logout` method:

```go
func (s *AuthService) Logout(ctx context.Context, refreshTokenStr string) error {
	// Look up the refresh token before revoking to get session info
	rt, err := s.tokenRepo.GetRefreshTokenIncludingRevoked(refreshTokenStr)
	if err != nil {
		// Token not found — just revoke anyway
		return s.tokenRepo.RevokeRefreshToken(refreshTokenStr)
	}

	if err := s.tokenRepo.RevokeRefreshToken(refreshTokenStr); err != nil {
		return err
	}

	// Revoke associated session
	if rt.SessionID != nil {
		if err := s.sessionRepo.Revoke(*rt.SessionID); err != nil {
			log.Printf("warn: failed to revoke session %d on logout: %v", *rt.SessionID, err)
		}
		// Look up session to get UserID for event
		session, sErr := s.sessionRepo.GetByID(*rt.SessionID)
		if sErr == nil {
			_ = s.producer.Publish(ctx, kafkamsg.TopicAuthSessionRevoked, kafkamsg.AuthSessionRevokedMessage{
				SessionID: session.ID,
				UserID:    session.UserID,
				Reason:    "logout",
			})
		}
	}

	return nil
}
```

- [ ] **Step 2: Add GetRefreshTokenIncludingRevoked to TokenRepository**

```go
// auth-service/internal/repository/token_repository.go

// GetRefreshTokenIncludingRevoked returns a refresh token regardless of revoked status.
// Used by logout to find the session association.
func (r *TokenRepository) GetRefreshTokenIncludingRevoked(token string) (*model.RefreshToken, error) {
	var t model.RefreshToken
	err := r.db.Where("token = ?", token).First(&t).Error
	return &t, err
}
```

- [ ] **Step 3: Update RevokeAllForAccount to also revoke sessions**

Add a new method to revoke all sessions for an account (used by password reset, account deactivation):

```go
// auth-service/internal/repository/token_repository.go

// RevokeAllTokensForSession revokes all non-revoked refresh tokens linked to a session.
func (r *TokenRepository) RevokeAllTokensForSession(sessionID int64) error {
	return r.db.Model(&model.RefreshToken{}).
		Where("session_id = ? AND revoked = false", sessionID).
		Update("revoked", true).Error
}
```

Also add to AuthService a method that revokes all sessions + tokens for a user:

```go
// auth-service/internal/service/auth_service.go

// RevokeAllSessions revokes all sessions and refresh tokens for a user (by account).
func (s *AuthService) RevokeAllSessions(ctx context.Context, accountID int64, userID int64, reason string) error {
	// Revoke all refresh tokens
	if err := s.tokenRepo.RevokeAllForAccount(accountID); err != nil {
		return err
	}
	// Revoke all sessions
	if err := s.sessionRepo.RevokeAllForUser(userID); err != nil {
		return err
	}
	// Publish event
	_ = s.producer.Publish(ctx, kafkamsg.TopicAuthSessionRevoked, kafkamsg.AuthSessionRevokedMessage{
		SessionID: 0, // 0 indicates all sessions
		UserID:    userID,
		Reason:    reason,
	})
	return nil
}
```

- [ ] **Step 4: Update ResetPassword to use RevokeAllSessions**

In `auth_service.go` method `ResetPassword`, replace:
```go
if err := s.tokenRepo.RevokeAllForAccount(prt.AccountID); err != nil {
	log.Printf("warn: failed to revoke all sessions after password reset: %v", err)
}
```
with:
```go
// Resolve the user ID from the account
var acct model.Account
if acctErr := s.accountRepo.GetByID(prt.AccountID, &acct); acctErr == nil {
	if err := s.RevokeAllSessions(ctx, prt.AccountID, acct.PrincipalID, "password_reset"); err != nil {
		log.Printf("warn: failed to revoke all sessions after password reset: %v", err)
	}
} else {
	// Fallback: at least revoke tokens
	if err := s.tokenRepo.RevokeAllForAccount(prt.AccountID); err != nil {
		log.Printf("warn: failed to revoke all tokens after password reset: %v", err)
	}
}
```

**Test command:** `cd auth-service && go build ./...`

- [ ] **Commit:** `feat(auth): revoke session on logout/password-reset, publish events`

---

### Task 8: Add per-device revocation and session listing methods

**Files:**
- Edit: `auth-service/internal/service/auth_service.go`

Add new service methods that will be called by the new gRPC handlers.

- [ ] **Step 1: Add ListSessions method**

```go
// ListSessions returns all active sessions for a user.
func (s *AuthService) ListSessions(ctx context.Context, userID int64) ([]model.ActiveSession, error) {
	return s.sessionRepo.ListByUser(userID)
}
```

- [ ] **Step 2: Add RevokeSession method**

```go
// RevokeSession revokes a specific session and all its linked refresh tokens.
func (s *AuthService) RevokeSession(ctx context.Context, sessionID int64, callerUserID int64) error {
	session, err := s.sessionRepo.GetByID(sessionID)
	if err != nil {
		return fmt.Errorf("session not found")
	}
	// Ensure the caller owns this session
	if session.UserID != callerUserID {
		return fmt.Errorf("permission denied: session belongs to another user")
	}
	if session.RevokedAt != nil {
		return fmt.Errorf("session already revoked")
	}

	// Revoke all refresh tokens for this session
	if err := s.tokenRepo.RevokeAllTokensForSession(sessionID); err != nil {
		return fmt.Errorf("failed to revoke session tokens: %w", err)
	}
	// Revoke the session itself
	if err := s.sessionRepo.Revoke(sessionID); err != nil {
		return fmt.Errorf("failed to revoke session: %w", err)
	}

	_ = s.producer.Publish(ctx, kafkamsg.TopicAuthSessionRevoked, kafkamsg.AuthSessionRevokedMessage{
		SessionID: sessionID,
		UserID:    session.UserID,
		Reason:    "force_revoke",
	})
	return nil
}
```

- [ ] **Step 3: Add RevokeAllSessionsExceptCurrent method**

```go
// RevokeAllSessionsExceptCurrent revokes all sessions except the one tied to the given refresh token.
func (s *AuthService) RevokeAllSessionsExceptCurrent(ctx context.Context, userID int64, currentRefreshToken string) error {
	rt, err := s.tokenRepo.GetRefreshToken(currentRefreshToken)
	if err != nil {
		return fmt.Errorf("current token not found")
	}

	keepSessionID := int64(0)
	if rt.SessionID != nil {
		keepSessionID = *rt.SessionID
	}

	// Get all sessions for user to publish events
	sessions, _ := s.sessionRepo.ListByUser(userID)

	// Revoke all sessions except current
	if keepSessionID > 0 {
		if err := s.sessionRepo.RevokeAllExcept(userID, keepSessionID); err != nil {
			return err
		}
	} else {
		if err := s.sessionRepo.RevokeAllForUser(userID); err != nil {
			return err
		}
	}

	// Revoke refresh tokens for those sessions (but not the current one)
	for _, sess := range sessions {
		if sess.ID == keepSessionID {
			continue
		}
		_ = s.tokenRepo.RevokeAllTokensForSession(sess.ID)
		_ = s.producer.Publish(ctx, kafkamsg.TopicAuthSessionRevoked, kafkamsg.AuthSessionRevokedMessage{
			SessionID: sess.ID,
			UserID:    userID,
			Reason:    "force_revoke",
		})
	}

	return nil
}
```

- [ ] **Step 4: Add GetLoginHistory method**

```go
// LoginHistoryEntry is a view-model for login history returned to clients.
type LoginHistoryEntry struct {
	ID         int64
	Email      string
	IPAddress  string
	UserAgent  string
	DeviceType string
	Success    bool
	CreatedAt  time.Time
}

// GetLoginHistory returns recent login attempts for a user's email.
func (s *AuthService) GetLoginHistory(ctx context.Context, email string, limit int) ([]LoginHistoryEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	attempts, err := s.loginAttemptRepo.ListRecentByEmail(email, limit)
	if err != nil {
		return nil, err
	}
	entries := make([]LoginHistoryEntry, len(attempts))
	for i, a := range attempts {
		entries[i] = LoginHistoryEntry{
			ID:         a.ID,
			Email:      a.Email,
			IPAddress:  a.IPAddress,
			UserAgent:  a.UserAgent,
			DeviceType: a.DeviceType,
			Success:    a.Success,
			CreatedAt:  a.CreatedAt,
		}
	}
	return entries, nil
}
```

- [ ] **Step 5: Add ListRecentByEmail to LoginAttemptRepository**

```go
// auth-service/internal/repository/login_attempt_repository.go

// ListRecentByEmail returns the most recent login attempts for an email, ordered newest first.
func (r *LoginAttemptRepository) ListRecentByEmail(email string, limit int) ([]model.LoginAttempt, error) {
	var attempts []model.LoginAttempt
	err := r.db.Where("email = ?", email).
		Order("created_at DESC").
		Limit(limit).
		Find(&attempts).Error
	return attempts, err
}
```

**Test command:** `cd auth-service && go build ./...`

- [ ] **Commit:** `feat(auth): add session listing, revocation, and login history service methods`

---

### Task 9: Add gRPC RPCs for session management

**Files:**
- Edit: `contract/proto/auth/auth.proto`
- Run: `make proto`
- Edit: `auth-service/internal/handler/grpc_handler.go`

Add four new RPCs: ListSessions, RevokeSession, RevokeAllSessions, GetLoginHistory.

- [ ] **Step 1: Add new messages and RPCs to auth.proto**

Add to the `service AuthService` block:

```protobuf
  // Session management
  rpc ListSessions(ListSessionsRequest) returns (ListSessionsResponse);
  rpc RevokeSession(RevokeSessionRequest) returns (RevokeSessionResponse);
  rpc RevokeAllSessions(RevokeAllSessionsRequest) returns (RevokeAllSessionsResponse);
  rpc GetLoginHistory(LoginHistoryRequest) returns (LoginHistoryResponse);
```

Add the message definitions at the end of the file:

```protobuf
// --- Session management messages ---

message ListSessionsRequest {
  int64 user_id = 1;
}

message SessionInfo {
  int64 id = 1;
  int64 user_id = 2;
  string user_role = 3;
  string ip_address = 4;
  string user_agent = 5;
  string device_id = 6;
  string system_type = 7;
  string last_active_at = 8;  // RFC3339
  string created_at = 9;      // RFC3339
  bool is_current = 10;
}

message ListSessionsResponse {
  repeated SessionInfo sessions = 1;
}

message RevokeSessionRequest {
  int64 session_id = 1;
  int64 caller_user_id = 2;  // The user requesting revocation (for ownership check)
}

message RevokeSessionResponse {
  bool success = 1;
}

message RevokeAllSessionsRequest {
  int64 user_id = 1;
  string current_refresh_token = 2;  // Keep this session alive
}

message RevokeAllSessionsResponse {
  bool success = 1;
  int32 revoked_count = 2;
}

message LoginHistoryRequest {
  string email = 1;
  int32 limit = 2;  // max 100, default 50
}

message LoginHistoryEntry {
  int64 id = 1;
  string email = 2;
  string ip_address = 3;
  string user_agent = 4;
  string device_type = 5;
  bool success = 6;
  string created_at = 7;  // RFC3339
}

message LoginHistoryResponse {
  repeated LoginHistoryEntry entries = 1;
}
```

- [ ] **Step 2: Regenerate protobuf Go files**

```bash
make proto
```

- [ ] **Step 3: Implement gRPC handlers**

Add to `auth-service/internal/handler/grpc_handler.go`:

```go
func (h *AuthGRPCHandler) ListSessions(ctx context.Context, req *pb.ListSessionsRequest) (*pb.ListSessionsResponse, error) {
	sessions, err := h.authService.ListSessions(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list sessions: %v", err)
	}

	var infos []*pb.SessionInfo
	for _, s := range sessions {
		infos = append(infos, &pb.SessionInfo{
			Id:           s.ID,
			UserId:       s.UserID,
			UserRole:     s.UserRole,
			IpAddress:    s.IPAddress,
			UserAgent:    s.UserAgent,
			DeviceId:     s.DeviceID,
			SystemType:   s.SystemType,
			LastActiveAt: s.LastActiveAt.Format(time.RFC3339),
			CreatedAt:    s.CreatedAt.Format(time.RFC3339),
		})
	}
	return &pb.ListSessionsResponse{Sessions: infos}, nil
}

func (h *AuthGRPCHandler) RevokeSession(ctx context.Context, req *pb.RevokeSessionRequest) (*pb.RevokeSessionResponse, error) {
	if err := h.authService.RevokeSession(ctx, req.SessionId, req.CallerUserId); err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.RevokeSessionResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) RevokeAllSessions(ctx context.Context, req *pb.RevokeAllSessionsRequest) (*pb.RevokeAllSessionsResponse, error) {
	if err := h.authService.RevokeAllSessionsExceptCurrent(ctx, req.UserId, req.CurrentRefreshToken); err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.RevokeAllSessionsResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) GetLoginHistory(ctx context.Context, req *pb.LoginHistoryRequest) (*pb.LoginHistoryResponse, error) {
	entries, err := h.authService.GetLoginHistory(ctx, req.Email, int(req.Limit))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get login history: %v", err)
	}

	var pbEntries []*pb.LoginHistoryEntry
	for _, e := range entries {
		pbEntries = append(pbEntries, &pb.LoginHistoryEntry{
			Id:         e.ID,
			Email:      e.Email,
			IpAddress:  e.IPAddress,
			UserAgent:  e.UserAgent,
			DeviceType: e.DeviceType,
			Success:    e.Success,
			CreatedAt:  e.CreatedAt.Format(time.RFC3339),
		})
	}
	return &pb.LoginHistoryResponse{Entries: pbEntries}, nil
}
```

- [ ] **Step 4: Update Login handler to extract and pass metadata**

In `grpc_handler.go`, update the `Login` handler to extract IP/UserAgent from gRPC metadata and pass to the service:

```go
func (h *AuthGRPCHandler) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	meta := extractRequestMeta(ctx)
	access, refresh, err := h.authService.Login(ctx, req.Email, req.Password, meta.IPAddress, meta.UserAgent)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid credentials")
	}
	return &pb.LoginResponse{
		AccessToken:  access,
		RefreshToken: refresh,
	}, nil
}
```

- [ ] **Step 5: Update RefreshToken handler similarly**

```go
func (h *AuthGRPCHandler) RefreshToken(ctx context.Context, req *pb.RefreshTokenRequest) (*pb.RefreshTokenResponse, error) {
	meta := extractRequestMeta(ctx)
	access, refresh, err := h.authService.RefreshToken(ctx, req.RefreshToken, meta.IPAddress, meta.UserAgent)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid refresh token")
	}
	return &pb.RefreshTokenResponse{
		AccessToken:  access,
		RefreshToken: refresh,
	}, nil
}
```

**Test command:** `cd auth-service && go build ./...`

- [ ] **Commit:** `feat(auth): add ListSessions, RevokeSession, RevokeAllSessions, GetLoginHistory gRPC RPCs`

---

### Task 10: Gateway metadata forwarding

**Files:**
- Edit: `api-gateway/internal/handler/auth_handler.go`

The API gateway must forward the client's IP and User-Agent as gRPC metadata on login and refresh calls so the auth-service can record them.

- [ ] **Step 1: Add metadata forwarding to Login handler**

```go
// api-gateway/internal/handler/auth_handler.go

import (
	"google.golang.org/grpc/metadata"
)

func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	// Forward client IP and User-Agent to auth-service via gRPC metadata
	md := metadata.Pairs(
		"x-forwarded-for", c.ClientIP(),
		"x-user-agent", c.Request.UserAgent(),
	)
	ctx := metadata.NewOutgoingContext(c.Request.Context(), md)

	resp, err := h.authClient.Login(ctx, &authpb.LoginRequest{
		Email:    req.Email,
		Password: req.Password,
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
```

- [ ] **Step 2: Add metadata forwarding to RefreshToken handler**

```go
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	// Forward client IP and User-Agent to auth-service via gRPC metadata
	md := metadata.Pairs(
		"x-forwarded-for", c.ClientIP(),
		"x-user-agent", c.Request.UserAgent(),
	)
	ctx := metadata.NewOutgoingContext(c.Request.Context(), md)

	resp, err := h.authClient.RefreshToken(ctx, &authpb.RefreshTokenRequest{
		RefreshToken: req.RefreshToken,
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
```

- [ ] **Step 3: Also forward metadata on Logout**

```go
func (h *AuthHandler) Logout(c *gin.Context) {
	var req logoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	md := metadata.Pairs(
		"x-forwarded-for", c.ClientIP(),
		"x-user-agent", c.Request.UserAgent(),
	)
	ctx := metadata.NewOutgoingContext(c.Request.Context(), md)

	h.authClient.Logout(ctx, &authpb.LogoutRequest{
		RefreshToken: req.RefreshToken,
	})
	c.JSON(http.StatusOK, gin.H{"message": "logged out successfully"})
}
```

**Test command:** `cd api-gateway && go build ./...`

- [ ] **Commit:** `feat(gateway): forward IP and User-Agent as gRPC metadata on auth calls`

---

### Task 11: Gateway handler stubs for session management endpoints

**Files:**
- Create: `api-gateway/internal/handler/session_handler.go`

These handlers expose the session management and login history RPCs via REST. They will be wired into the router in Plan 6 (API Versioning & Router), but the handler code needs to exist now. Temporary route wiring is also provided for immediate use.

- [ ] **Step 1: Create session handler**

```go
// api-gateway/internal/handler/session_handler.go
package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	authpb "github.com/exbanka/contract/authpb"
)

type SessionHandler struct {
	authClient authpb.AuthServiceClient
}

func NewSessionHandler(authClient authpb.AuthServiceClient) *SessionHandler {
	return &SessionHandler{authClient: authClient}
}

// ListMySessions godoc
// @Summary      List my active sessions
// @Description  Returns all active sessions for the authenticated user
// @Tags         sessions
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "sessions array"
// @Failure      401  {object}  map[string]string  "unauthorized"
// @Router       /api/me/sessions [get]
func (h *SessionHandler) ListMySessions(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int64)
	if !ok {
		apiError(c, http.StatusUnauthorized, ErrUnauthorized, "invalid user context")
		return
	}

	resp, err := h.authClient.ListSessions(c.Request.Context(), &authpb.ListSessionsRequest{
		UserId: uid,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	sessions := make([]gin.H, 0, len(resp.Sessions))
	for _, s := range resp.Sessions {
		sessions = append(sessions, gin.H{
			"id":             s.Id,
			"user_role":      s.UserRole,
			"ip_address":     s.IpAddress,
			"user_agent":     s.UserAgent,
			"device_id":      s.DeviceId,
			"system_type":    s.SystemType,
			"last_active_at": s.LastActiveAt,
			"created_at":     s.CreatedAt,
			"is_current":     s.IsCurrent,
		})
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

type revokeSessionRequest struct {
	SessionID int64 `json:"session_id" binding:"required"`
}

// RevokeSession godoc
// @Summary      Revoke a specific session
// @Description  Revokes a session by ID, logging out the device associated with it
// @Tags         sessions
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  revokeSessionRequest  true  "Session to revoke"
// @Success      200  {object}  map[string]string  "success message"
// @Failure      400  {object}  map[string]string  "validation error"
// @Failure      401  {object}  map[string]string  "unauthorized"
// @Failure      404  {object}  map[string]string  "session not found"
// @Router       /api/me/sessions/revoke [post]
func (h *SessionHandler) RevokeSession(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int64)
	if !ok {
		apiError(c, http.StatusUnauthorized, ErrUnauthorized, "invalid user context")
		return
	}

	var req revokeSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	_, err := h.authClient.RevokeSession(c.Request.Context(), &authpb.RevokeSessionRequest{
		SessionId:    req.SessionID,
		CallerUserId: uid,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "session revoked successfully"})
}

type revokeAllSessionsRequest struct {
	CurrentRefreshToken string `json:"current_refresh_token" binding:"required"`
}

// RevokeAllSessions godoc
// @Summary      Revoke all other sessions
// @Description  Revokes all sessions except the current one (identified by the provided refresh token)
// @Tags         sessions
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  revokeAllSessionsRequest  true  "Current refresh token to keep"
// @Success      200  {object}  map[string]string  "success message"
// @Failure      400  {object}  map[string]string  "validation error"
// @Failure      401  {object}  map[string]string  "unauthorized"
// @Router       /api/me/sessions/revoke-others [post]
func (h *SessionHandler) RevokeAllSessions(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int64)
	if !ok {
		apiError(c, http.StatusUnauthorized, ErrUnauthorized, "invalid user context")
		return
	}

	var req revokeAllSessionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	_, err := h.authClient.RevokeAllSessions(c.Request.Context(), &authpb.RevokeAllSessionsRequest{
		UserId:              uid,
		CurrentRefreshToken: req.CurrentRefreshToken,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "all other sessions revoked successfully"})
}

// GetMyLoginHistory godoc
// @Summary      Get my login history
// @Description  Returns recent login attempts for the authenticated user
// @Tags         sessions
// @Produce      json
// @Security     BearerAuth
// @Param        limit  query  int  false  "Max entries (default 50, max 100)"
// @Success      200  {object}  map[string]interface{}  "entries array"
// @Failure      401  {object}  map[string]string  "unauthorized"
// @Router       /api/me/login-history [get]
func (h *SessionHandler) GetMyLoginHistory(c *gin.Context) {
	email, _ := c.Get("email")
	emailStr, ok := email.(string)
	if !ok || emailStr == "" {
		apiError(c, http.StatusUnauthorized, ErrUnauthorized, "invalid user context")
		return
	}

	limit := int32(50)
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.ParseInt(l, 10, 32); err == nil && parsed > 0 && parsed <= 100 {
			limit = int32(parsed)
		}
	}

	resp, err := h.authClient.GetLoginHistory(c.Request.Context(), &authpb.LoginHistoryRequest{
		Email: emailStr,
		Limit: limit,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	entries := make([]gin.H, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		entries = append(entries, gin.H{
			"id":          e.Id,
			"ip_address":  e.IpAddress,
			"user_agent":  e.UserAgent,
			"device_type": e.DeviceType,
			"success":     e.Success,
			"created_at":  e.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries})
}
```

- [ ] **Step 2: Wire session handler into router**

In `api-gateway/internal/router/router.go`, create the handler and add routes under the `/api/me` group:

After the existing `meHandler` creation (around line 69), add:
```go
sessionHandler := handler.NewSessionHandler(authClient)
```

Inside the `me` group (around line 99), add the session routes:
```go
// Sessions
me.GET("/sessions", sessionHandler.ListMySessions)
me.POST("/sessions/revoke", sessionHandler.RevokeSession)
me.POST("/sessions/revoke-others", sessionHandler.RevokeAllSessions)
me.GET("/login-history", sessionHandler.GetMyLoginHistory)
```

**Test command:** `cd api-gateway && go build ./...`

- [ ] **Commit:** `feat(gateway): add session management and login history REST endpoints`

---

### Task 12: Unit tests

**Files:**
- Create: `auth-service/internal/repository/session_repository_test.go`
- Create: `auth-service/internal/handler/metadata_test.go`
- Edit: `auth-service/internal/service/auth_service_test.go`

- [ ] **Step 1: SessionRepository unit tests**

```go
// auth-service/internal/repository/session_repository_test.go
package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/contract/testutil"
)

func TestSessionRepository_CreateAndGetByID(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ActiveSession{})
	repo := NewSessionRepository(db)

	session := &model.ActiveSession{
		UserID:       1,
		UserRole:     "EmployeeBasic",
		IPAddress:    "192.168.1.1",
		UserAgent:    "Mozilla/5.0",
		SystemType:   "employee",
		LastActiveAt: time.Now(),
		CreatedAt:    time.Now(),
	}
	require.NoError(t, repo.Create(session))
	assert.NotZero(t, session.ID)

	got, err := repo.GetByID(session.ID)
	require.NoError(t, err)
	assert.Equal(t, session.UserID, got.UserID)
	assert.Equal(t, session.IPAddress, got.IPAddress)
}

func TestSessionRepository_ListByUser(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ActiveSession{})
	repo := NewSessionRepository(db)

	now := time.Now()
	// Create 2 active sessions and 1 revoked
	require.NoError(t, repo.Create(&model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}))
	require.NoError(t, repo.Create(&model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now.Add(-time.Hour), CreatedAt: now.Add(-time.Hour)}))
	revokedAt := now
	require.NoError(t, db.Create(&model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now, RevokedAt: &revokedAt}).Error)
	// Different user
	require.NoError(t, repo.Create(&model.ActiveSession{UserID: 2, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}))

	sessions, err := repo.ListByUser(1)
	require.NoError(t, err)
	assert.Len(t, sessions, 2) // Only non-revoked
}

func TestSessionRepository_Revoke(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ActiveSession{})
	repo := NewSessionRepository(db)

	now := time.Now()
	session := &model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}
	require.NoError(t, repo.Create(session))

	require.NoError(t, repo.Revoke(session.ID))

	got, err := repo.GetByID(session.ID)
	require.NoError(t, err)
	assert.NotNil(t, got.RevokedAt)
}

func TestSessionRepository_RevokeAllExcept(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ActiveSession{})
	repo := NewSessionRepository(db)

	now := time.Now()
	s1 := &model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}
	s2 := &model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}
	s3 := &model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}
	require.NoError(t, repo.Create(s1))
	require.NoError(t, repo.Create(s2))
	require.NoError(t, repo.Create(s3))

	require.NoError(t, repo.RevokeAllExcept(1, s2.ID))

	sessions, err := repo.ListByUser(1)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, s2.ID, sessions[0].ID)
}

func TestSessionRepository_UpdateLastActive(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ActiveSession{})
	repo := NewSessionRepository(db)

	old := time.Now().Add(-time.Hour)
	session := &model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: old, CreatedAt: old}
	require.NoError(t, repo.Create(session))

	require.NoError(t, repo.UpdateLastActive(session.ID))

	got, err := repo.GetByID(session.ID)
	require.NoError(t, err)
	assert.True(t, got.LastActiveAt.After(old))
}

func TestSessionRepository_RevokeByDeviceID(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ActiveSession{})
	repo := NewSessionRepository(db)

	now := time.Now()
	s1 := &model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", DeviceID: "device-abc", LastActiveAt: now, CreatedAt: now}
	s2 := &model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", DeviceID: "device-xyz", LastActiveAt: now, CreatedAt: now}
	require.NoError(t, repo.Create(s1))
	require.NoError(t, repo.Create(s2))

	require.NoError(t, repo.RevokeByDeviceID("device-abc"))

	sessions, err := repo.ListByUser(1)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, "device-xyz", sessions[0].DeviceID)
}
```

- [ ] **Step 2: Metadata extraction unit tests**

```go
// auth-service/internal/handler/metadata_test.go
package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
)

func TestExtractRequestMeta_WithMetadata(t *testing.T) {
	md := metadata.Pairs(
		"x-forwarded-for", "10.0.0.1",
		"x-user-agent", "Mozilla/5.0 Test",
	)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	rm := extractRequestMeta(ctx)
	assert.Equal(t, "10.0.0.1", rm.IPAddress)
	assert.Equal(t, "Mozilla/5.0 Test", rm.UserAgent)
}

func TestExtractRequestMeta_CommaChainForwardedFor(t *testing.T) {
	md := metadata.Pairs(
		"x-forwarded-for", "10.0.0.1, 10.0.0.2, 10.0.0.3",
	)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	rm := extractRequestMeta(ctx)
	assert.Equal(t, "10.0.0.1", rm.IPAddress)
}

func TestExtractRequestMeta_EmptyContext(t *testing.T) {
	rm := extractRequestMeta(context.Background())
	assert.Equal(t, "", rm.IPAddress)
	assert.Equal(t, "", rm.UserAgent)
}

func TestDetectDeviceType(t *testing.T) {
	tests := []struct {
		name      string
		userAgent string
		expected  string
	}{
		{"browser", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)", "browser"},
		{"mobile android", "Mozilla/5.0 (Linux; Android 11; Pixel 5)", "mobile"},
		{"mobile iphone", "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0)", "mobile"},
		{"postman", "PostmanRuntime/7.29.0", "api"},
		{"curl", "curl/7.68.0", "api"},
		{"empty", "", "browser"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, detectDeviceType(tt.userAgent))
		})
	}
}
```

- [ ] **Step 3: Add session-related unit tests to auth_service_test.go**

Append to the existing test file:

```go
// auth-service/internal/service/auth_service_test.go

func TestDetectDeviceType_Service(t *testing.T) {
	// Test the service-layer detectDeviceType if it was defined there
	assert.Equal(t, "browser", detectDeviceType("Mozilla/5.0"))
	assert.Equal(t, "mobile", detectDeviceType("Android/11"))
	assert.Equal(t, "api", detectDeviceType("curl/7.0"))
}
```

**Test command:** `cd auth-service && go test ./...`

- [ ] **Commit:** `test(auth): add unit tests for SessionRepository, metadata extraction, device detection`

---

### Task 13: Update Specification.md

**Files:**
- Edit: `Specification.md`

- [ ] **Step 1: Add session management to the specification**

Add the following to the relevant sections of `Specification.md`:

**Section 17 (API Routes):** Add these endpoints:
- `GET /api/me/sessions` — List active sessions (AnyAuthMiddleware)
- `POST /api/me/sessions/revoke` — Revoke a specific session (AnyAuthMiddleware)
- `POST /api/me/sessions/revoke-others` — Revoke all sessions except current (AnyAuthMiddleware)
- `GET /api/me/login-history` — Get login history (AnyAuthMiddleware)

**Section 18 (Entities):** Update:
- `RefreshToken` — add `SessionID`, `IPAddress`, `UserAgent` fields
- `ActiveSession` — add `DeviceID`, `SystemType` fields
- `LoginAttempt` — add `UserAgent`, `DeviceType` fields

**Section 11 (gRPC):** Add to AuthService:
- `ListSessions(ListSessionsRequest) returns (ListSessionsResponse)`
- `RevokeSession(RevokeSessionRequest) returns (RevokeSessionResponse)`
- `RevokeAllSessions(RevokeAllSessionsRequest) returns (RevokeAllSessionsResponse)`
- `GetLoginHistory(LoginHistoryRequest) returns (LoginHistoryResponse)`

**Section 19 (Kafka Topics):** Add:
- `auth.session-created` — published on successful login
- `auth.session-revoked` — published on logout, force-revoke, password reset

- [ ] **Step 2: Update REST API documentation**

Add session management section to `docs/api/REST_API.md` following the existing format.

- [ ] **Commit:** `docs: update Specification.md and REST_API.md with session management`

---

### Task 14: Regenerate Swagger documentation

**Files:**
- Regenerate: `api-gateway/docs/`

- [ ] **Step 1: Regenerate Swagger**

```bash
cd api-gateway && swag init -g cmd/main.go --output docs
```

- [ ] **Commit:** `docs(swagger): regenerate swagger docs with session management endpoints`

---

### Summary of all files changed

**New files:**
- `auth-service/internal/repository/session_repository.go`
- `auth-service/internal/repository/session_repository_test.go`
- `auth-service/internal/handler/metadata.go`
- `auth-service/internal/handler/metadata_test.go`
- `api-gateway/internal/handler/session_handler.go`

**Modified files:**
- `auth-service/internal/model/token.go` — add SessionID, IPAddress, UserAgent
- `auth-service/internal/model/active_session.go` — add DeviceID, SystemType
- `auth-service/internal/model/login_attempt.go` — add UserAgent, DeviceType
- `auth-service/internal/repository/token_repository.go` — add GetRefreshTokenIncludingRevoked, RevokeAllTokensForSession
- `auth-service/internal/repository/login_attempt_repository.go` — update RecordAttempt/RecordFailureAndCheckLock signatures, add ListRecentByEmail
- `auth-service/internal/service/auth_service.go` — add sessionRepo, update Login/RefreshToken/Logout signatures, add ListSessions/RevokeSession/RevokeAllSessionsExceptCurrent/GetLoginHistory
- `auth-service/internal/handler/grpc_handler.go` — update Login/RefreshToken handlers, add ListSessions/RevokeSession/RevokeAllSessions/GetLoginHistory handlers
- `auth-service/internal/service/auth_service_test.go` — add detectDeviceType test
- `auth-service/cmd/main.go` — wire sessionRepo, add Kafka topics
- `contract/proto/auth/auth.proto` — add 4 new RPCs and messages
- `contract/kafka/messages.go` — add session event topics and messages
- `api-gateway/internal/handler/auth_handler.go` — add gRPC metadata forwarding
- `api-gateway/internal/router/router.go` — wire session routes
- `Specification.md` — add session management documentation
- `docs/api/REST_API.md` — add session endpoint documentation
- `api-gateway/docs/` — regenerated swagger

### Verification checklist

After all tasks are complete, verify:

1. `cd auth-service && go build ./...` passes
2. `cd api-gateway && go build ./...` passes
3. `cd contract && go build ./...` passes
4. `cd auth-service && go test ./...` passes
5. `make proto` generates updated Go files from proto changes
6. Login creates an ActiveSession record and links RefreshToken to it
7. Token refresh updates session LastActiveAt and inherits SessionID
8. Logout revokes both the refresh token and the associated session
9. Password reset revokes all sessions and tokens for the account
10. `GET /api/me/sessions` returns active sessions for the logged-in user
11. `POST /api/me/sessions/revoke` revokes a specific session
12. `POST /api/me/sessions/revoke-others` revokes all sessions except the current one
13. `GET /api/me/login-history` returns login attempts for the user
14. Kafka events `auth.session-created` and `auth.session-revoked` are published correctly
