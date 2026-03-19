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
	clientpb "github.com/exbanka/contract/clientpb"
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
	userClient       userpb.UserServiceClient
	clientClient     clientpb.ClientServiceClient
	producer         *kafkaprod.Producer
	cache            *cache.RedisCache
	refreshExp       time.Duration
	frontendBaseURL  string
}

func NewAuthService(
	tokenRepo *repository.TokenRepository,
	loginAttemptRepo *repository.LoginAttemptRepository,
	totpRepo *repository.TOTPRepository,
	totpSvc *TOTPService,
	jwtService *JWTService,
	userClient userpb.UserServiceClient,
	clientClient clientpb.ClientServiceClient,
	producer *kafkaprod.Producer,
	cache *cache.RedisCache,
	refreshExp time.Duration,
	frontendBaseURL string,
) *AuthService {
	return &AuthService{
		tokenRepo:        tokenRepo,
		loginAttemptRepo: loginAttemptRepo,
		totpRepo:         totpRepo,
		totpSvc:          totpSvc,
		jwtService:       jwtService,
		userClient:       userClient,
		clientClient:     clientClient,
		producer:         producer,
		cache:            cache,
		refreshExp:       refreshExp,
		frontendBaseURL:  frontendBaseURL,
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

	// Count recent failures
	failedCount, _ := s.loginAttemptRepo.CountRecentFailedAttempts(email, lockoutWindow)

	resp, err := s.userClient.ValidateCredentials(ctx, &userpb.ValidateCredentialsRequest{
		Email:    email,
		Password: password,
	})
	if err != nil || !resp.Valid {
		_ = s.loginAttemptRepo.RecordAttempt(email, "", false)
		if failedCount+1 >= maxFailedAttempts {
			_ = s.loginAttemptRepo.LockAccount(email, lockoutDuration)
			return "", "", fmt.Errorf("account locked after %d failed attempts, try again in 30 minutes", maxFailedAttempts)
		}
		remaining := int(maxFailedAttempts - (failedCount + 1))
		return "", "", fmt.Errorf("invalid credentials (%d attempts remaining before lockout)", remaining)
	}

	_ = s.loginAttemptRepo.RecordAttempt(email, "", true)

	// Gather roles from response (new multi-role field takes priority, fall back to single role)
	loginRoles := resp.Roles
	if len(loginRoles) == 0 && resp.Role != "" {
		loginRoles = []string{resp.Role}
	}

	accessToken, err := s.jwtService.GenerateAccessToken(resp.UserId, resp.Email, loginRoles, resp.Permissions, "employee")
	if err != nil {
		return "", "", err
	}

	refreshToken, err := generateToken()
	if err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	if err := s.tokenRepo.CreateRefreshToken(&model.RefreshToken{
		UserID:     resp.UserId,
		Token:      refreshToken,
		ExpiresAt:  time.Now().Add(s.refreshExp),
		SystemType: "employee",
	}); err != nil {
		return "", "", err
	}

	return accessToken, refreshToken, nil
}

// ClientLogin authenticates a bank client using the client-service's ValidateCredentials RPC.
// It returns a JWT access token with role="client" and a refresh token.
func (s *AuthService) ClientLogin(ctx context.Context, email, password string) (string, string, error) {
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

	// Count recent failures
	failedCount, _ := s.loginAttemptRepo.CountRecentFailedAttempts(email, lockoutWindow)

	resp, err := s.clientClient.ValidateCredentials(ctx, &clientpb.ValidateClientCredentialsRequest{
		Email:    email,
		Password: password,
	})
	if err != nil {
		_ = s.loginAttemptRepo.RecordAttempt(email, "", false)
		if failedCount+1 >= maxFailedAttempts {
			_ = s.loginAttemptRepo.LockAccount(email, lockoutDuration)
			return "", "", fmt.Errorf("account locked after %d failed attempts, try again in 30 minutes", maxFailedAttempts)
		}
		remaining := int(maxFailedAttempts - (failedCount + 1))
		return "", "", fmt.Errorf("invalid credentials (%d attempts remaining before lockout)", remaining)
	}

	_ = s.loginAttemptRepo.RecordAttempt(email, "", true)

	// Client ID is uint64 in clientpb; cast to int64 for JWT claims.
	accessToken, err := s.jwtService.GenerateAccessToken(int64(resp.Id), resp.Email, []string{"client"}, nil, "client")
	if err != nil {
		return "", "", err
	}

	refreshToken, err := generateToken()
	if err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	if err := s.tokenRepo.CreateRefreshToken(&model.RefreshToken{
		UserID:     int64(resp.Id),
		Token:      refreshToken,
		ExpiresAt:  time.Now().Add(s.refreshExp),
		SystemType: "client",
	}); err != nil {
		return "", "", err
	}

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
					return nil, fmt.Errorf("token has been revoked")
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
			return nil, fmt.Errorf("token has been revoked")
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
		return "", "", errors.New("invalid refresh token")
	}
	if time.Now().After(rt.ExpiresAt) {
		return "", "", errors.New("refresh token expired")
	}

	if err := s.tokenRepo.RevokeRefreshToken(refreshTokenStr); err != nil {
		return "", "", fmt.Errorf("failed to revoke old refresh token: %w", err)
	}

	var accessToken string
	systemType := rt.SystemType
	if systemType == "" {
		systemType = "employee" // backwards compat for existing tokens without system_type
	}

	if systemType == "client" {
		// Client refresh: look up client by ID to get email
		clientResp, err := s.clientClient.GetClient(ctx, &clientpb.GetClientRequest{Id: uint64(rt.UserID)})
		clientEmail := ""
		if err == nil && clientResp != nil {
			clientEmail = clientResp.Email
		}
		accessToken, err = s.jwtService.GenerateAccessToken(rt.UserID, clientEmail, []string{"client"}, nil, "client")
		if err != nil {
			return "", "", err
		}
	} else {
		userResp, err := s.userClient.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: rt.UserID})
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
		UserID:     rt.UserID,
		Token:      newRefreshToken,
		ExpiresAt:  time.Now().Add(s.refreshExp),
		SystemType: systemType,
	}); err != nil {
		return "", "", err
	}

	return accessToken, newRefreshToken, nil
}

func (s *AuthService) Logout(ctx context.Context, refreshTokenStr string) error {
	return s.tokenRepo.RevokeRefreshToken(refreshTokenStr)
}

func (s *AuthService) CreateActivationToken(ctx context.Context, userID int64, email, firstName, systemType string) error {
	token, err := generateToken()
	if err != nil {
		return err
	}
	if err := s.tokenRepo.CreateActivationToken(&model.ActivationToken{
		UserID:     userID,
		Token:      token,
		ExpiresAt:  time.Now().Add(24 * time.Hour),
		SystemType: systemType,
	}); err != nil {
		return err
	}

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
	user, err := s.userClient.GetUserByEmail(ctx, &userpb.GetUserByEmailRequest{Email: email})
	if err != nil {
		return nil // Don't reveal if email exists
	}

	token, err := generateToken()
	if err != nil {
		return err
	}
	if err := s.tokenRepo.CreatePasswordResetToken(&model.PasswordResetToken{
		UserID:    user.Id,
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
		return errors.New("passwords do not match")
	}
	if err := validatePassword(newPassword); err != nil {
		return err
	}

	prt, err := s.tokenRepo.GetPasswordResetToken(tokenStr)
	if err != nil {
		return errors.New("invalid or expired token")
	}
	if time.Now().After(prt.ExpiresAt) {
		return errors.New("token expired")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	_, err = s.userClient.SetPassword(ctx, &userpb.SetPasswordRequest{
		UserId:       prt.UserID,
		PasswordHash: string(hash),
	})
	if err != nil {
		return fmt.Errorf("failed to set password: %w", err)
	}

	if err := s.tokenRepo.MarkPasswordResetUsed(tokenStr); err != nil {
		log.Printf("warn: failed to mark password reset token used (token may be replayable): %v", err)
	}
	if err := s.tokenRepo.RevokeAllForUser(prt.UserID); err != nil {
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

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	switch at.SystemType {
	case "client":
		_, err = s.clientClient.SetPassword(ctx, &clientpb.SetClientPasswordRequest{
			UserId:       uint64(at.UserID),
			PasswordHash: string(hash),
		})
	default:
		_, err = s.userClient.SetPassword(ctx, &userpb.SetPasswordRequest{
			UserId:       at.UserID,
			PasswordHash: string(hash),
		})
	}
	if err != nil {
		return fmt.Errorf("failed to set password: %w", err)
	}

	if err := s.tokenRepo.MarkActivationUsed(tokenStr); err != nil {
		log.Printf("warn: failed to mark activation token used (token may be replayable): %v", err)
	}

	// Send confirmation email
	var email, firstName string
	switch at.SystemType {
	case "client":
		resp, _ := s.clientClient.GetClient(ctx, &clientpb.GetClientRequest{Id: uint64(at.UserID)})
		if resp != nil {
			email = resp.Email
			firstName = resp.FirstName
		}
	default:
		user, _ := s.userClient.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: at.UserID})
		if user != nil {
			email = user.Email
			firstName = user.FirstName
		}
	}
	if email != "" {
		_ = s.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
			To:        email,
			EmailType: kafkamsg.EmailTypeConfirmation,
			Data:      map[string]string{"first_name": firstName},
		})
	}

	return nil
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
