package service

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/exbanka/auth-service/internal/cache"
	kafkaprod "github.com/exbanka/auth-service/internal/kafka"
	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/repository"
	kafkamsg "github.com/exbanka/contract/kafka"
	userpb "github.com/exbanka/contract/userpb"
)

// eventProducer abstracts the Kafka producer methods used by AuthService.
// The concrete *kafkaprod.Producer satisfies this interface; tests can
// supply an in-process fake to avoid requiring a live broker.
type eventProducer interface {
	SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error
	Publish(ctx context.Context, topic string, msg any) error
}

// AuthService is the composition root. It embeds the three DI-separable
// concern services (so the gRPC handler can depend on one type whose method set
// is their union) and owns Login + 2FA — the cross-concern orchestration that
// coordinates the account, session, and token repos directly.
//
// Each concern service (TokenService / SessionService / AccountService) is
// independently constructable and unit-testable in isolation; this struct just
// wires them together for the handler.
type AuthService struct {
	*TokenService
	*SessionService
	*AccountService

	// Login + 2FA orchestration deps (held directly so the embedded services'
	// like-named fields are never ambiguous for these methods).
	loginAttemptRepo *repository.LoginAttemptRepository
	accountRepo      *repository.AccountRepository
	sessionRepo      *repository.SessionRepository
	tokenRepo        *repository.TokenRepository
	jwtService       *JWTService
	userClient       userpb.UserServiceClient
	producer         eventProducer
	refreshExp       time.Duration
	totpRepo         *repository.TOTPRepository
	totpSvc          *TOTPService
}

func NewAuthService(
	tokenRepo *repository.TokenRepository,
	sessionRepo *repository.SessionRepository,
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
	return assembleAuthService(tokenRepo, sessionRepo, loginAttemptRepo, totpRepo, totpSvc,
		jwtService, accountRepo, userClient, producer, cache,
		refreshExp, mobileRefreshExp, frontendBaseURL, pepper)
}

// newAuthServiceForTest constructs an AuthService with a pluggable event
// producer. Used by package tests to swap in an in-process fake.
func newAuthServiceForTest(
	tokenRepo *repository.TokenRepository,
	sessionRepo *repository.SessionRepository,
	loginAttemptRepo *repository.LoginAttemptRepository,
	totpRepo *repository.TOTPRepository,
	totpSvc *TOTPService,
	jwtService *JWTService,
	accountRepo *repository.AccountRepository,
	userClient userpb.UserServiceClient,
	producer eventProducer,
	cache *cache.RedisCache,
	refreshExp time.Duration,
	mobileRefreshExp time.Duration,
	frontendBaseURL string,
	pepper string,
) *AuthService {
	return assembleAuthService(tokenRepo, sessionRepo, loginAttemptRepo, totpRepo, totpSvc,
		jwtService, accountRepo, userClient, producer, cache,
		refreshExp, mobileRefreshExp, frontendBaseURL, pepper)
}

// assembleAuthService builds the three concern services (sharing the injected
// dependencies) and composes them into an AuthService. The session service is
// built before the account service because account password-reset depends on it
// (SessionRevoker). Used by both the production and test constructors.
func assembleAuthService(
	tokenRepo *repository.TokenRepository,
	sessionRepo *repository.SessionRepository,
	loginAttemptRepo *repository.LoginAttemptRepository,
	totpRepo *repository.TOTPRepository,
	totpSvc *TOTPService,
	jwtService *JWTService,
	accountRepo *repository.AccountRepository,
	userClient userpb.UserServiceClient,
	producer eventProducer,
	c *cache.RedisCache,
	refreshExp time.Duration,
	mobileRefreshExp time.Duration,
	frontendBaseURL string,
	pepper string,
) *AuthService {
	tokenSvc := NewTokenService(tokenRepo, sessionRepo, accountRepo, userClient, jwtService, c, refreshExp, mobileRefreshExp)
	sessionSvc := NewSessionService(sessionRepo, tokenRepo, loginAttemptRepo, producer, c, jwtService)
	accountSvc := NewAccountService(accountRepo, tokenRepo, userClient, producer, c, jwtService, sessionSvc, loginAttemptRepo, frontendBaseURL, pepper)
	return &AuthService{
		TokenService:     tokenSvc,
		SessionService:   sessionSvc,
		AccountService:   accountSvc,
		loginAttemptRepo: loginAttemptRepo,
		accountRepo:      accountRepo,
		sessionRepo:      sessionRepo,
		tokenRepo:        tokenRepo,
		jwtService:       jwtService,
		userClient:       userClient,
		producer:         producer,
		refreshExp:       refreshExp,
		totpRepo:         totpRepo,
		totpSvc:          totpSvc,
	}
}

// Setup2FA generates a TOTP secret for a user (pending confirmation).
func (s *AuthService) Setup2FA(ctx context.Context, userID int64, email string) (string, string, error) {
	secret, url, err := s.totpSvc.GenerateSecret(email, "EXBanka")
	if err != nil {
		return "", "", err
	}
	totpRecord := &model.TOTPSecret{
		UserID:  userID,
		Secret:  secret,
		Enabled: false,
	}
	// Delete any existing pending setup
	_ = s.totpRepo.Delete(userID)
	if err := s.totpRepo.Create(totpRecord); err != nil {
		return "", "", err
	}
	return secret, url, nil
}

// Verify2FA confirms the TOTP code and enables 2FA for the user.
func (s *AuthService) Verify2FA(ctx context.Context, userID int64, code string) (bool, error) {
	totpRecord, err := s.totpRepo.GetByUserID(userID)
	if err != nil {
		return false, fmt.Errorf("2FA not set up")
	}
	if !s.totpSvc.ValidateCode(totpRecord.Secret, code) {
		return false, nil
	}
	return true, s.totpRepo.Enable(userID)
}

// Disable2FA removes 2FA for the user after verifying the current code.
func (s *AuthService) Disable2FA(ctx context.Context, userID int64, code string) (bool, error) {
	totpRecord, err := s.totpRepo.GetByUserID(userID)
	if err != nil {
		return false, fmt.Errorf("2FA not set up")
	}
	if !s.totpSvc.ValidateCode(totpRecord.Secret, code) {
		return false, nil
	}
	return true, s.totpRepo.Delete(userID)
}

// Login authenticates both employees and bank clients using the unified Account table.
//
// Failure modes are mapped to typed sentinels (see errors.go) so the gRPC
// handler can passthrough errors and the wire status reflects the true
// failure (locked, pending, disabled, etc.). Email-not-found and bcrypt
// mismatch deliberately collapse to the same sentinel (ErrInvalidCredentials)
// to prevent email enumeration.
func (s *AuthService) Login(ctx context.Context, email, password, ipAddress, userAgent string) (string, string, error) {
	const maxFailedAttempts = 5
	const lockoutWindow = 15 * time.Minute
	const lockoutDuration = 30 * time.Minute

	deviceType := DetectDeviceType(userAgent)

	// Check if account is locked
	lock, err := s.loginAttemptRepo.GetActiveLock(email)
	if err != nil {
		log.Printf("Login check active lock failed: %v", err)
		return "", "", fmt.Errorf("Login check active lock: %w", ErrAccountLocked)
	}
	if lock != nil {
		return "", "", fmt.Errorf("Login account already locked until %s: %w", lock.ExpiresAt.Format(time.RFC3339), ErrAccountLocked)
	}

	// Look up account by email
	account, err := s.accountRepo.GetByEmail(email)
	if err != nil {
		AuthLoginTotal.WithLabelValues("failure", "unknown").Inc()
		locked, _, _ := s.loginAttemptRepo.RecordFailureAndCheckLock(email, ipAddress, userAgent, deviceType, maxFailedAttempts, lockoutWindow, lockoutDuration)
		if locked {
			return "", "", fmt.Errorf("Login locked after %d failed attempts: %w", maxFailedAttempts, ErrAccountLocked)
		}
		// Email-not-found COLLAPSES to invalid-credentials to prevent enumeration.
		return "", "", fmt.Errorf("Login account not found: %w", ErrInvalidCredentials)
	}

	// Check account status
	if account.Status == model.AccountStatusPending {
		AuthLoginTotal.WithLabelValues("failure", "unknown").Inc()
		locked, _, _ := s.loginAttemptRepo.RecordFailureAndCheckLock(email, ipAddress, userAgent, deviceType, maxFailedAttempts, lockoutWindow, lockoutDuration)
		if locked {
			return "", "", fmt.Errorf("Login locked after %d failed attempts: %w", maxFailedAttempts, ErrAccountLocked)
		}
		return "", "", fmt.Errorf("Login account not yet activated: %w", ErrAccountPending)
	}
	if account.Status != model.AccountStatusActive {
		AuthLoginTotal.WithLabelValues("failure", "unknown").Inc()
		locked, _, _ := s.loginAttemptRepo.RecordFailureAndCheckLock(email, ipAddress, userAgent, deviceType, maxFailedAttempts, lockoutWindow, lockoutDuration)
		if locked {
			return "", "", fmt.Errorf("Login locked after %d failed attempts: %w", maxFailedAttempts, ErrAccountLocked)
		}
		return "", "", fmt.Errorf("Login account disabled: %w", ErrAccountDisabled)
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(PepperPassword(s.pepper, password))); err != nil {
		AuthLoginTotal.WithLabelValues("failure", "unknown").Inc()
		locked, _, _ := s.loginAttemptRepo.RecordFailureAndCheckLock(email, ipAddress, userAgent, deviceType, maxFailedAttempts, lockoutWindow, lockoutDuration)
		if locked {
			return "", "", fmt.Errorf("Login locked after %d failed attempts: %w", maxFailedAttempts, ErrAccountLocked)
		}
		// Wrong-password COLLAPSES to invalid-credentials to prevent enumeration.
		return "", "", fmt.Errorf("Login bcrypt mismatch: %w", ErrInvalidCredentials)
	}

	_ = s.loginAttemptRepo.RecordAttempt(email, ipAddress, userAgent, deviceType, true)

	systemType := "employee"
	if account.PrincipalType != model.PrincipalTypeEmployee {
		systemType = "client"
	}

	// Gather token identity (roles/permissions/profile) WITHOUT signing yet —
	// the access token is signed after the session row exists so it can carry
	// that session's sid (enables targeted per-session revocation).
	var loginRoles []string
	var permissions []string
	var firstName, lastName string
	if account.PrincipalType == model.PrincipalTypeEmployee {
		userResp, gerr := s.userClient.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: account.PrincipalID})
		if gerr != nil {
			log.Printf("Login get employee underlying: %v", gerr)
			return "", "", fmt.Errorf("Login get employee: %w", ErrEmployeeRPCFailed)
		}
		loginRoles = userResp.Roles
		if len(loginRoles) == 0 && userResp.Role != "" {
			loginRoles = []string{userResp.Role}
		}
		permissions = userResp.Permissions
		firstName, lastName = userResp.FirstName, userResp.LastName
	} else {
		loginRoles = []string{"client"}
	}

	refreshToken, err := generateToken()
	if err != nil {
		log.Printf("Login generate refresh token underlying: %v", err)
		return "", "", fmt.Errorf("Login generate refresh token: %w", ErrTokenGenFailed)
	}

	// Determine user role label for session
	userRole := account.PrincipalType
	if account.PrincipalType == model.PrincipalTypeEmployee && len(loginRoles) > 0 {
		userRole = loginRoles[0]
	}

	// Create session FIRST so the access token can embed its sid.
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
		// Non-fatal: proceed without session tracking (token gets no sid).
	}
	sid := ""
	if session.ID != 0 {
		sid = strconv.FormatInt(session.ID, 10)
	}

	accessToken, err := s.jwtService.GenerateAccessToken(account.PrincipalID, account.Email, loginRoles, permissions, systemType, TokenProfile{
		FirstName:     firstName,
		LastName:      lastName,
		AccountActive: account.Status == model.AccountStatusActive,
		Sid:           sid,
	})
	if err != nil {
		log.Printf("Login sign access token underlying: %v", err)
		return "", "", fmt.Errorf("Login sign access token: %w", ErrTokenSignFailed)
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
			SessionID:     session.ID,
			PrincipalType: account.PrincipalType,
			PrincipalID:   account.PrincipalID,
			IPAddress:     ipAddress,
			UserAgent:     userAgent,
			DeviceType:    deviceType,
		})
	}

	AuthLoginTotal.WithLabelValues("success", systemType).Inc()
	AuthTokensIssuedTotal.WithLabelValues("access").Inc()
	AuthTokensIssuedTotal.WithLabelValues("refresh").Inc()

	return accessToken, refreshToken, nil
}
