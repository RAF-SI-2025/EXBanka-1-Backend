package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"

	"golang.org/x/crypto/bcrypt"

	kafkamsg "github.com/exbanka/contract/kafka"
	userpb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/auth-service/internal/cache"
	kafkaprod "github.com/exbanka/auth-service/internal/kafka"
	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/repository"
)

type AuthService struct {
	tokenRepo        *repository.TokenRepository
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
func (s *AuthService) Login(ctx context.Context, email, password string) (string, string, error) {
	const maxFailedAttempts = 5
	const lockoutWindow = 15 * time.Minute
	const lockoutDuration = 30 * time.Minute

	// Check if account is locked
	lock, err := s.loginAttemptRepo.GetActiveLock(email)
	if err != nil {
		return "", "", fmt.Errorf("failed to check account lock: %w", err)
	}
	if lock != nil {
		remaining := time.Until(lock.ExpiresAt).Minutes()
		return "", "", fmt.Errorf("account locked due to too many failed attempts, try again in %.0f minutes", remaining)
	}

	// Look up account by email
	account, err := s.accountRepo.GetByEmail(email)
	if err != nil {
		AuthLoginTotal.WithLabelValues("failure", "unknown").Inc()
		locked, remaining, _ := s.loginAttemptRepo.RecordFailureAndCheckLock(email, maxFailedAttempts, lockoutWindow, lockoutDuration)
		if locked {
			return "", "", fmt.Errorf("account locked after %d failed attempts, try again in 30 minutes", maxFailedAttempts)
		}
		return "", "", fmt.Errorf("no account found with email %s (%d attempts remaining before lockout)", email, remaining)
	}

	// Check account status
	if account.Status == model.AccountStatusPending {
		AuthLoginTotal.WithLabelValues("failure", "unknown").Inc()
		locked, remaining, _ := s.loginAttemptRepo.RecordFailureAndCheckLock(email, maxFailedAttempts, lockoutWindow, lockoutDuration)
		if locked {
			return "", "", fmt.Errorf("account locked after %d failed attempts, try again in 30 minutes", maxFailedAttempts)
		}
		return "", "", fmt.Errorf("account not yet activated (%d attempts remaining before lockout)", remaining)
	}
	if account.Status != model.AccountStatusActive {
		AuthLoginTotal.WithLabelValues("failure", "unknown").Inc()
		locked, remaining, _ := s.loginAttemptRepo.RecordFailureAndCheckLock(email, maxFailedAttempts, lockoutWindow, lockoutDuration)
		if locked {
			return "", "", fmt.Errorf("account locked after %d failed attempts, try again in 30 minutes", maxFailedAttempts)
		}
		return "", "", fmt.Errorf("account is disabled (%d attempts remaining before lockout)", remaining)
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(PepperPassword(s.pepper, password))); err != nil {
		AuthLoginTotal.WithLabelValues("failure", "unknown").Inc()
		locked, remaining, _ := s.loginAttemptRepo.RecordFailureAndCheckLock(email, maxFailedAttempts, lockoutWindow, lockoutDuration)
		if locked {
			return "", "", fmt.Errorf("account locked after %d failed attempts, try again in 30 minutes", maxFailedAttempts)
		}
		return "", "", fmt.Errorf("incorrect password for %s (%d attempts remaining before lockout)", email, remaining)
	}

	_ = s.loginAttemptRepo.RecordAttempt(email, "", true)

	var accessToken string
	systemType := "employee"
	if account.PrincipalType != model.PrincipalTypeEmployee {
		systemType = "client"
	}

	switch account.PrincipalType {
	case model.PrincipalTypeEmployee:
		userResp, err := s.userClient.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: account.PrincipalID})
		if err != nil {
			return "", "", fmt.Errorf("failed to fetch employee data: %w", err)
		}
		loginRoles := userResp.Roles
		if len(loginRoles) == 0 && userResp.Role != "" {
			loginRoles = []string{userResp.Role}
		}
		accessToken, err = s.jwtService.GenerateAccessToken(account.PrincipalID, account.Email, loginRoles, userResp.Permissions, "employee")
		if err != nil {
			return "", "", err
		}
	default: // client
		accessToken, err = s.jwtService.GenerateAccessToken(account.PrincipalID, account.Email, []string{"client"}, nil, "client")
		if err != nil {
			return "", "", err
		}
	}

	refreshToken, err := generateToken()
	if err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	if err := s.tokenRepo.CreateRefreshToken(&model.RefreshToken{
		AccountID:  account.ID,
		Token:      refreshToken,
		ExpiresAt:  time.Now().Add(s.refreshExp),
		SystemType: account.PrincipalType,
	}); err != nil {
		return "", "", err
	}

	AuthLoginTotal.WithLabelValues("success", systemType).Inc()
	AuthTokensIssuedTotal.WithLabelValues("access").Inc()
	AuthTokensIssuedTotal.WithLabelValues("refresh").Inc()

	return accessToken, refreshToken, nil
}

func (s *AuthService) ValidateToken(tokenString string) (*Claims, error) {
	cacheKey := "token:" + hashToken(tokenString)

	// Try cache first
	if s.cache != nil {
		var cached Claims
		if err := s.cache.Get(context.Background(), cacheKey, &cached); err == nil {
			// Check if token has been blacklisted by JTI
			if cached.ID != "" {
				blacklisted, _ := s.cache.Exists(context.Background(), "blacklist:"+cached.ID)
				if blacklisted {
					return nil, fmt.Errorf("access token has been revoked; please log in again")
				}
			}
			return &cached, nil
		}
	}

	claims, err := s.jwtService.ValidateToken(tokenString)
	if err != nil {
		return nil, err
	}

	// Check blacklist by JTI
	if claims.ID != "" && s.cache != nil {
		blacklisted, _ := s.cache.Exists(context.Background(), "blacklist:"+claims.ID)
		if blacklisted {
			return nil, fmt.Errorf("access token has been revoked; please log in again")
		}
	}

	// Cache with TTL = remaining token lifetime
	if s.cache != nil && claims.ExpiresAt != nil {
		ttl := time.Until(claims.ExpiresAt.Time)
		if ttl > 0 {
			_ = s.cache.Set(context.Background(), cacheKey, claims, ttl)
		}
	}

	return claims, nil
}

// RevokeAccessToken adds a JWT JTI to the Redis blacklist until it would naturally expire.
func (s *AuthService) RevokeAccessToken(ctx context.Context, jti string, remainingTTL time.Duration) error {
	if s.cache == nil {
		return nil // gracefully skip if no Redis
	}
	return s.cache.Set(ctx, "blacklist:"+jti, "revoked", remainingTTL)
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (s *AuthService) RefreshToken(ctx context.Context, refreshTokenStr string) (string, string, error) {
	rt, err := s.tokenRepo.GetRefreshToken(refreshTokenStr)
	if err != nil {
		return "", "", errors.New("refresh token has been revoked")
	}
	if time.Now().After(rt.ExpiresAt) {
		return "", "", errors.New("refresh token expired; please log in again")
	}

	// Look up account by AccountID
	var acct model.Account
	if err := s.accountRepo.GetByID(rt.AccountID, &acct); err != nil {
		return "", "", errors.New("account not found")
	}
	if acct.Status != model.AccountStatusActive {
		return "", "", errors.New("account is disabled")
	}

	if err := s.tokenRepo.RevokeRefreshToken(refreshTokenStr); err != nil {
		return "", "", fmt.Errorf("failed to revoke old refresh token: %w", err)
	}

	systemType := rt.SystemType
	if systemType == "" {
		systemType = "employee" // backwards compat for existing tokens without system_type
	}

	var accessToken string

	if systemType == "client" {
		accessToken, err = s.jwtService.GenerateAccessToken(acct.PrincipalID, acct.Email, []string{"client"}, nil, "client")
		if err != nil {
			return "", "", err
		}
	} else {
		userResp, err := s.userClient.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: acct.PrincipalID})
		if err != nil {
			return "", "", errors.New("user not found")
		}
		refreshRoles := userResp.Roles
		if len(refreshRoles) == 0 && userResp.Role != "" {
			refreshRoles = []string{userResp.Role}
		}
		accessToken, err = s.jwtService.GenerateAccessToken(
			userResp.Id, userResp.Email, refreshRoles, userResp.Permissions, "employee",
		)
		if err != nil {
			return "", "", err
		}
	}

	newRefreshToken, err := generateToken()
	if err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	if err := s.tokenRepo.CreateRefreshToken(&model.RefreshToken{
		AccountID:  acct.ID,
		Token:      newRefreshToken,
		ExpiresAt:  time.Now().Add(s.refreshExp),
		SystemType: systemType,
	}); err != nil {
		return "", "", err
	}

	AuthTokensIssuedTotal.WithLabelValues("access").Inc()
	AuthTokensIssuedTotal.WithLabelValues("refresh").Inc()

	return accessToken, newRefreshToken, nil
}

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

// RefreshTokenForMobile validates the refresh token, verifies the device is active and matches,
// revokes the old token, and issues a new mobile token pair.
func (s *AuthService) RefreshTokenForMobile(ctx context.Context, oldRefreshToken, deviceID string, mobileSvc *MobileDeviceService) (string, string, error) {
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

	// Get account to resolve PrincipalID (the actual user ID used in MobileDevice)
	var acct model.Account
	if err := s.accountRepo.GetByID(rt.AccountID, &acct); err != nil {
		return "", "", errors.New("account not found")
	}
	if acct.Status != model.AccountStatusActive {
		return "", "", errors.New("account is not active")
	}

	// Verify device is active and matches the provided deviceID
	device, err := mobileSvc.GetDeviceInfo(acct.PrincipalID)
	if err != nil {
		return "", "", errors.New("device not found or deactivated, please re-activate")
	}
	if device.DeviceID != deviceID {
		return "", "", errors.New("device ID mismatch — permission denied")
	}

	// Revoke old token
	_ = s.tokenRepo.RevokeRefreshToken(oldRefreshToken)

	// Fetch roles/permissions
	var roles []string
	var permissions []string
	systemType := rt.SystemType
	if systemType == "" {
		systemType = "employee"
	}

	if systemType == "employee" {
		emp, err := s.userClient.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: acct.PrincipalID})
		if err == nil {
			roles = emp.Roles
			permissions = emp.Permissions
		}
	} else {
		roles = []string{"client"}
	}

	// Generate new access token with device claims
	access, err := s.jwtService.GenerateMobileAccessToken(
		acct.PrincipalID, acct.Email, roles, permissions,
		systemType, "mobile", deviceID,
	)
	if err != nil {
		return "", "", err
	}

	// Generate new refresh token
	newRefreshStr, err := generateToken()
	if err != nil {
		return "", "", err
	}
	newRT := &model.RefreshToken{
		AccountID:  acct.ID,
		Token:      newRefreshStr,
		ExpiresAt:  time.Now().Add(s.mobileRefreshExp),
		SystemType: systemType,
	}
	if err := s.tokenRepo.CreateRefreshToken(newRT); err != nil {
		return "", "", err
	}

	return access, newRefreshStr, nil
}

func (s *AuthService) Logout(ctx context.Context, refreshTokenStr string) error {
	return s.tokenRepo.RevokeRefreshToken(refreshTokenStr)
}

// CreateAccountAndActivationToken creates an Account (if not already present) and sends an activation email.
func (s *AuthService) CreateAccountAndActivationToken(ctx context.Context, principalID int64, email, firstName, principalType string) error {
	// Idempotent: check if account already exists
	account, err := s.accountRepo.GetByEmail(email)
	if err != nil {
		// Account does not exist — create it
		account = &model.Account{
			Email:         email,
			Status:        model.AccountStatusPending,
			PrincipalType: principalType,
			PrincipalID:   principalID,
		}
		if err := s.accountRepo.Create(account); err != nil {
			return fmt.Errorf("failed to create account: %w", err)
		}
	}

	token, err := generateToken()
	if err != nil {
		return err
	}
	if err := s.tokenRepo.CreateActivationToken(&model.ActivationToken{
		AccountID: account.ID,
		Token:     token,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		return err
	}

	AuthTokensIssuedTotal.WithLabelValues("activation").Inc()

	return s.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
		To:        email,
		EmailType: kafkamsg.EmailTypeActivation,
		Data: map[string]string{
			"token":      token,
			"first_name": firstName,
			"link":       s.frontendBaseURL + "/activate?token=" + token,
		},
	})
}

func (s *AuthService) RequestPasswordReset(ctx context.Context, email string) error {
	AuthPasswordResetTotal.Inc()

	account, err := s.accountRepo.GetByEmail(email)
	if err != nil {
		return nil // Don't reveal if email exists
	}

	token, err := generateToken()
	if err != nil {
		return err
	}
	if err := s.tokenRepo.CreatePasswordResetToken(&model.PasswordResetToken{
		AccountID: account.ID,
		Token:     token,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}); err != nil {
		return err
	}

	return s.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
		To:        email,
		EmailType: kafkamsg.EmailTypePasswordReset,
		Data: map[string]string{
			"link": s.frontendBaseURL + "/reset-password?token=" + token,
		},
	})
}

func (s *AuthService) ResetPassword(ctx context.Context, tokenStr, newPassword, confirmPassword string) error {
	if newPassword != confirmPassword {
		return errors.New("password and confirmation do not match")
	}
	if err := validatePassword(newPassword); err != nil {
		return err
	}

	prt, err := s.tokenRepo.GetPasswordResetToken(tokenStr)
	if err != nil {
		return errors.New("invalid or expired password reset token; request a new password reset")
	}
	if time.Now().After(prt.ExpiresAt) {
		return errors.New("password reset token expired; request a new password reset")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(PepperPassword(s.pepper, newPassword)), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	if err := s.accountRepo.SetPassword(prt.AccountID, string(hash)); err != nil {
		return fmt.Errorf("failed to set password: %w", err)
	}

	if err := s.tokenRepo.MarkPasswordResetUsed(tokenStr); err != nil {
		log.Printf("warn: failed to mark password reset token used (token may be replayable): %v", err)
	}
	if err := s.tokenRepo.RevokeAllForAccount(prt.AccountID); err != nil {
		log.Printf("warn: failed to revoke all sessions after password reset: %v", err)
	}

	return nil
}

func (s *AuthService) ActivateAccount(ctx context.Context, tokenStr, password, confirmPassword string) error {
	if password != confirmPassword {
		return errors.New("passwords do not match")
	}
	if err := validatePassword(password); err != nil {
		return err
	}

	at, err := s.tokenRepo.GetActivationToken(tokenStr)
	if err != nil {
		return errors.New("invalid or expired activation token")
	}
	if time.Now().After(at.ExpiresAt) {
		return errors.New("token expired")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(PepperPassword(s.pepper, password)), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	if err := s.accountRepo.SetPasswordAndActivate(at.AccountID, string(hash)); err != nil {
		return fmt.Errorf("failed to activate account: %w", err)
	}

	if err := s.tokenRepo.MarkActivationUsed(tokenStr); err != nil {
		log.Printf("warn: failed to mark activation token used (token may be replayable): %v", err)
	}

	// Send confirmation email
	var acct model.Account
	if err := s.accountRepo.GetByID(at.AccountID, &acct); err != nil {
		return nil // account activated; confirmation email failure is non-fatal
	}

	var firstName string
	if acct.PrincipalType == model.PrincipalTypeEmployee {
		user, err := s.userClient.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: acct.PrincipalID})
		if err == nil && user != nil {
			firstName = user.FirstName
		}
	}

	if acct.Email != "" {
		_ = s.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
			To:        acct.Email,
			EmailType: kafkamsg.EmailTypeConfirmation,
			Data:      map[string]string{"first_name": firstName},
		})
	}

	return nil
}

// SetAccountStatus enables or disables an account identified by principalType + principalID.
func (s *AuthService) SetAccountStatus(ctx context.Context, principalType string, principalID int64, active bool) error {
	status := model.AccountStatusActive
	if !active {
		status = model.AccountStatusDisabled
	}

	if !active {
		// Get account so we can revoke its tokens
		acct, err := s.accountRepo.GetByPrincipal(principalType, principalID)
		if err != nil {
			return fmt.Errorf("account not found: %w", err)
		}
		if revokeErr := s.tokenRepo.RevokeAllForAccount(acct.ID); revokeErr != nil {
			return fmt.Errorf("account disabled but failed to revoke sessions: %w", revokeErr)
		}
	}

	if err := s.accountRepo.SetStatusByPrincipal(principalType, principalID, status); err != nil {
		return err
	}

	if err := s.producer.Publish(ctx, kafkamsg.TopicAuthAccountStatusChanged, kafkamsg.AuthAccountStatusChangedMessage{
		PrincipalType: principalType,
		PrincipalID:   principalID,
		Status:        string(status),
	}); err != nil {
		log.Printf("warn: failed to publish account status changed event for %s/%d: %v", principalType, principalID, err)
	}
	return nil
}

// GetAccountStatus returns the status string and active bool for a given principal.
func (s *AuthService) GetAccountStatus(ctx context.Context, principalType string, principalID int64) (string, bool, error) {
	acct, err := s.accountRepo.GetByPrincipal(principalType, principalID)
	if err != nil {
		return "", false, err
	}
	return acct.Status, acct.Status == model.AccountStatusActive, nil
}

// GetAccountStatusBatch returns a map of principalID → Account for batch status lookups.
func (s *AuthService) GetAccountStatusBatch(ctx context.Context, principalType string, principalIDs []int64) (map[int64]model.Account, error) {
	ptrs, err := s.accountRepo.GetByPrincipals(principalType, principalIDs)
	if err != nil {
		return nil, err
	}
	result := make(map[int64]model.Account, len(ptrs))
	for k, v := range ptrs {
		result[k] = *v
	}
	return result, nil
}

func validatePassword(password string) error {
	if len(password) < 8 || len(password) > 32 {
		return errors.New("password must be 8-32 characters")
	}
	digits := 0
	hasUpper := false
	hasLower := false
	for _, c := range password {
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= 'a' && c <= 'z':
			hasLower = true
		}
	}
	if digits < 2 || !hasUpper || !hasLower {
		return errors.New("password must have at least 2 digits, 1 uppercase and 1 lowercase letter")
	}
	return nil
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand unavailable: %w", err)
	}
	return hex.EncodeToString(b), nil
}
