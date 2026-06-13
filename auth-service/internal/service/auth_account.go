package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/exbanka/auth-service/internal/cache"
	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/repository"
	kafkamsg "github.com/exbanka/contract/kafka"
	userpb "github.com/exbanka/contract/userpb"
	"golang.org/x/crypto/bcrypt"
)

// SessionRevoker is the slice of SessionService that AccountService depends on:
// a password reset revokes all of the account's sessions. Declared as an
// interface so AccountService is decoupled from the concrete SessionService
// (injected by the composition root).
type SessionRevoker interface {
	RevokeAllSessions(ctx context.Context, principalType string, accountID, userID int64, reason string) error
}

// accountUnlocker clears an active brute-force lock (and the failed-attempt
// counter) for an email. A password reset must unlock the account per Celina 1.
// Satisfied by *repository.LoginAttemptRepository.
type accountUnlocker interface {
	UnlockAccount(email string) error
}

// AccountService owns account lifecycle: creation/activation, password reset,
// and account status. Independently constructable; it depends on a
// SessionRevoker (to drop sessions on password reset) and the package-level
// revocation helper (to drop access tokens on disable).
type AccountService struct {
	accountRepo     *repository.AccountRepository
	tokenRepo       *repository.TokenRepository
	userClient      userpb.UserServiceClient
	producer        eventProducer
	cache           *cache.RedisCache
	jwtService      *JWTService
	sessions        SessionRevoker
	unlocker        accountUnlocker
	frontendBaseURL string
	pepper          string
}

// NewAccountService wires the account-lifecycle dependencies.
func NewAccountService(
	accountRepo *repository.AccountRepository,
	tokenRepo *repository.TokenRepository,
	userClient userpb.UserServiceClient,
	producer eventProducer,
	c *cache.RedisCache,
	jwtService *JWTService,
	sessions SessionRevoker,
	unlocker accountUnlocker,
	frontendBaseURL, pepper string,
) *AccountService {
	return &AccountService{
		accountRepo:     accountRepo,
		tokenRepo:       tokenRepo,
		userClient:      userClient,
		producer:        producer,
		cache:           c,
		jwtService:      jwtService,
		sessions:        sessions,
		unlocker:        unlocker,
		frontendBaseURL: frontendBaseURL,
		pepper:          pepper,
	}
}

func (s *AccountService) CreateAccountAndActivationToken(ctx context.Context, principalID int64, email, firstName, principalType string) error {
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
			return fmt.Errorf("CreateAccountAndActivationToken: create account: %v: %w", err, ErrAccountCreationFailed)
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

// ResendActivationEmail re-sends the activation email for a pending account.
// If the account is already active, it returns nil (no-op).
func (s *AccountService) ResendActivationEmail(ctx context.Context, email string) error {
	account, err := s.accountRepo.GetByEmail(email)
	if err != nil {
		return nil // don't reveal if email exists
	}

	if account.Status != model.AccountStatusPending {
		return nil // already activated or disabled — no-op
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

	var firstName string
	if account.PrincipalType == model.PrincipalTypeEmployee {
		user, uErr := s.userClient.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: account.PrincipalID})
		if uErr == nil && user != nil {
			firstName = user.FirstName
		}
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

func (s *AccountService) RequestPasswordReset(ctx context.Context, email string) error {
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

func (s *AccountService) ResetPassword(ctx context.Context, tokenStr, newPassword, confirmPassword string) error {
	if newPassword != confirmPassword {
		return fmt.Errorf("ResetPassword: password and confirmation do not match: %w", ErrPasswordsDoNotMatch)
	}
	if err := validatePassword(newPassword); err != nil {
		return fmt.Errorf("ResetPassword: %v: %w", err, ErrPasswordValidation)
	}

	prt, err := s.tokenRepo.GetPasswordResetToken(tokenStr)
	if err != nil {
		return fmt.Errorf("ResetPassword: lookup token: %v: %w", err, ErrInvalidToken)
	}
	if time.Now().After(prt.ExpiresAt) {
		return fmt.Errorf("ResetPassword: token expired at %s: %w", prt.ExpiresAt.Format(time.RFC3339), ErrTokenExpired)
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

	// Resolve the user ID from the account
	var acct model.Account
	if acctErr := s.accountRepo.GetByID(prt.AccountID, &acct); acctErr == nil {
		// Celina 1: a password reset unlocks a brute-force-locked account and
		// resets its failed-attempt counter.
		if s.unlocker != nil {
			if err := s.unlocker.UnlockAccount(acct.Email); err != nil {
				log.Printf("warn: failed to unlock account after password reset: %v", err)
			}
		}
		if err := s.sessions.RevokeAllSessions(ctx, acct.PrincipalType, prt.AccountID, acct.PrincipalID, "password_reset"); err != nil {
			log.Printf("warn: failed to revoke all sessions after password reset: %v", err)
		}
		// General notification (no email)
		_ = s.producer.Publish(ctx, kafkamsg.TopicGeneralNotification, kafkamsg.GeneralNotificationMessage{
			UserID:  uint64(acct.PrincipalID),
			Type:    "password_changed",
			Title:   "Password Changed",
			Message: "Your password was successfully changed. If you did not make this change, contact support immediately.",
		})
	} else {
		// Fallback: at least revoke tokens
		if err := s.tokenRepo.RevokeAllForAccount(prt.AccountID); err != nil {
			log.Printf("warn: failed to revoke all tokens after password reset: %v", err)
		}
	}

	return nil
}

func (s *AccountService) ActivateAccount(ctx context.Context, tokenStr, password, confirmPassword string) error {
	if password != confirmPassword {
		return fmt.Errorf("ActivateAccount: passwords do not match: %w", ErrPasswordsDoNotMatch)
	}
	if err := validatePassword(password); err != nil {
		return fmt.Errorf("ActivateAccount: %v: %w", err, ErrPasswordValidation)
	}

	at, err := s.tokenRepo.GetActivationToken(tokenStr)
	if err != nil {
		return fmt.Errorf("ActivateAccount: lookup token: %v: %w", err, ErrInvalidToken)
	}
	if time.Now().After(at.ExpiresAt) {
		return fmt.Errorf("ActivateAccount: token expired at %s: %w", at.ExpiresAt.Format(time.RFC3339), ErrTokenExpired)
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
func (s *AccountService) SetAccountStatus(ctx context.Context, principalType string, principalID int64, active bool) error {
	status := model.AccountStatusActive
	if !active {
		status = model.AccountStatusDisabled
	}

	if !active {
		// Get account so we can revoke its tokens
		acct, err := s.accountRepo.GetByPrincipal(principalType, principalID)
		if err != nil {
			return fmt.Errorf("set account status: %w", ErrAccountNotFound)
		}
		if revokeErr := s.tokenRepo.RevokeAllForAccount(acct.ID); revokeErr != nil {
			return fmt.Errorf("account disabled but failed to revoke sessions: %w", revokeErr)
		}
		// Closes the 2.2 gap: a disabled account's still-valid access token kept
		// working ≤15 min. Bump the epoch so it is rejected immediately.
		hardRevokeUser(ctx, s.cache, s.jwtService.AccessExpiry(), principalType, principalID)
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
func (s *AccountService) GetAccountStatus(ctx context.Context, principalType string, principalID int64) (string, bool, error) {
	acct, err := s.accountRepo.GetByPrincipal(principalType, principalID)
	if err != nil {
		return "", false, fmt.Errorf("get account status: %w", ErrAccountNotFound)
	}
	return acct.Status, acct.Status == model.AccountStatusActive, nil
}

// GetAccountStatusBatch returns a map of principalID → Account for batch status lookups.
func (s *AccountService) GetAccountStatusBatch(ctx context.Context, principalType string, principalIDs []int64) (map[int64]model.Account, error) {
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

// detectDeviceType infers device type from User-Agent string.
// DetectDeviceType infers a coarse device class ("mobile" | "api" | "browser")
// from a User-Agent string. Exported so the handler layer can reuse it instead
// of keeping a duplicate copy.
func DetectDeviceType(userAgent string) string {
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

// ListSessions returns all active sessions for a user.
