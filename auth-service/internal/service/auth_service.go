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
	tokenRepo  *repository.TokenRepository
	jwtService *JWTService
	userClient userpb.UserServiceClient
	producer   *kafkaprod.Producer
	cache      *cache.RedisCache
	refreshExp time.Duration
}

func NewAuthService(
	tokenRepo *repository.TokenRepository,
	jwtService *JWTService,
	userClient userpb.UserServiceClient,
	producer *kafkaprod.Producer,
	cache *cache.RedisCache,
	refreshExp time.Duration,
) *AuthService {
	return &AuthService{
		tokenRepo:  tokenRepo,
		jwtService: jwtService,
		userClient: userClient,
		producer:   producer,
		cache:      cache,
		refreshExp: refreshExp,
	}
}

func (s *AuthService) Login(ctx context.Context, email, password string) (string, string, error) {
	resp, err := s.userClient.ValidateCredentials(ctx, &userpb.ValidateCredentialsRequest{
		Email:    email,
		Password: password,
	})
	if err != nil || !resp.Valid {
		return "", "", errors.New("invalid credentials")
	}

	accessToken, err := s.jwtService.GenerateAccessToken(resp.UserId, resp.Email, resp.Role, resp.Permissions)
	if err != nil {
		return "", "", err
	}

	refreshToken, err := generateToken()
	if err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	if err := s.tokenRepo.CreateRefreshToken(&model.RefreshToken{
		UserID:    resp.UserId,
		Token:     refreshToken,
		ExpiresAt: time.Now().Add(s.refreshExp),
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
			return &cached, nil
		}
	}

	claims, err := s.jwtService.ValidateToken(tokenString)
	if err != nil {
		return nil, err
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

	userResp, err := s.userClient.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: rt.UserID})
	if err != nil {
		return "", "", errors.New("user not found")
	}

	accessToken, err := s.jwtService.GenerateAccessToken(
		userResp.Id, userResp.Email, userResp.Role, userResp.Permissions,
	)
	if err != nil {
		return "", "", err
	}

	newRefreshToken, err := generateToken()
	if err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	if err := s.tokenRepo.CreateRefreshToken(&model.RefreshToken{
		UserID:    rt.UserID,
		Token:     newRefreshToken,
		ExpiresAt: time.Now().Add(s.refreshExp),
	}); err != nil {
		return "", "", err
	}

	return accessToken, newRefreshToken, nil
}

func (s *AuthService) Logout(ctx context.Context, refreshTokenStr string) error {
	return s.tokenRepo.RevokeRefreshToken(refreshTokenStr)
}

func (s *AuthService) CreateActivationToken(ctx context.Context, userID int64, email, firstName string) error {
	token, err := generateToken()
	if err != nil {
		return err
	}
	if err := s.tokenRepo.CreateActivationToken(&model.ActivationToken{
		UserID:    userID,
		Token:     token,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		return err
	}

	return s.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
		To:        email,
		EmailType: kafkamsg.EmailTypeActivation,
		Data: map[string]string{
			"token":      token,
			"first_name": firstName,
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
		Data:      map[string]string{"token": token},
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

	_, err = s.userClient.SetPassword(ctx, &userpb.SetPasswordRequest{
		UserId:       at.UserID,
		PasswordHash: string(hash),
	})
	if err != nil {
		return fmt.Errorf("failed to set password: %w", err)
	}

	if err := s.tokenRepo.MarkActivationUsed(tokenStr); err != nil {
		log.Printf("warn: failed to mark activation token used (token may be replayable): %v", err)
	}

	user, _ := s.userClient.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: at.UserID})
	if user != nil {
		_ = s.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
			To:        user.Email,
			EmailType: kafkamsg.EmailTypeConfirmation,
			Data:      map[string]string{"first_name": user.FirstName},
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
