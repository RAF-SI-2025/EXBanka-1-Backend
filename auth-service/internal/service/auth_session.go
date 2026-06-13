package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/exbanka/auth-service/internal/cache"
	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/repository"
	kafkamsg "github.com/exbanka/contract/kafka"
)

// SessionService owns active sessions and login history: listing, logout, and
// targeted/bulk revocation. Independently constructable; it hard-revokes access
// tokens via the package-level revocation helpers (cache + access-token TTL).
type SessionService struct {
	sessionRepo      *repository.SessionRepository
	tokenRepo        *repository.TokenRepository
	loginAttemptRepo *repository.LoginAttemptRepository
	producer         eventProducer
	cache            *cache.RedisCache
	jwtService       *JWTService
}

// NewSessionService wires the session-management dependencies.
func NewSessionService(
	sessionRepo *repository.SessionRepository,
	tokenRepo *repository.TokenRepository,
	loginAttemptRepo *repository.LoginAttemptRepository,
	producer eventProducer,
	c *cache.RedisCache,
	jwtService *JWTService,
) *SessionService {
	return &SessionService{
		sessionRepo:      sessionRepo,
		tokenRepo:        tokenRepo,
		loginAttemptRepo: loginAttemptRepo,
		producer:         producer,
		cache:            c,
		jwtService:       jwtService,
	}
}

func (s *SessionService) Logout(ctx context.Context, refreshTokenStr string) error {
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
		// Hard-revoke the access token(s) for this session so logout is
		// immediate at the gateway (not delayed until token expiry).
		blacklistSession(ctx, s.cache, s.jwtService.AccessExpiry(), *rt.SessionID)
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

// RevokeAllSessions revokes all sessions and refresh tokens for a principal (by account).
func (s *SessionService) RevokeAllSessions(ctx context.Context, principalType string, accountID int64, userID int64, reason string) error {
	// Revoke all refresh tokens
	if err := s.tokenRepo.RevokeAllForAccount(accountID); err != nil {
		return err
	}
	// Revoke all sessions
	if err := s.sessionRepo.RevokeAllForUser(userID); err != nil {
		return err
	}
	// Hard-revoke every access token for the principal via the per-principal epoch
	// (there is no single session to target here).
	hardRevokeUser(ctx, s.cache, s.jwtService.AccessExpiry(), principalType, userID)
	// Publish event
	_ = s.producer.Publish(ctx, kafkamsg.TopicAuthSessionRevoked, kafkamsg.AuthSessionRevokedMessage{
		SessionID: 0, // 0 indicates all sessions
		UserID:    userID,
		Reason:    reason,
	})
	return nil
}

func (s *SessionService) ListSessions(ctx context.Context, userID int64) ([]model.ActiveSession, error) {
	return s.sessionRepo.ListByUser(userID)
}

// RevokeSession revokes a specific session and all its linked refresh tokens.
func (s *SessionService) RevokeSession(ctx context.Context, sessionID int64, callerUserID int64) error {
	session, err := s.sessionRepo.GetByID(sessionID)
	if err != nil {
		return fmt.Errorf("RevokeSession: lookup session %d: %v: %w", sessionID, err, ErrSessionNotFound)
	}
	// Ensure the caller owns this session
	if session.UserID != callerUserID {
		return fmt.Errorf("RevokeSession: caller %d does not own session %d (owner=%d): %w", callerUserID, sessionID, session.UserID, ErrSessionForbidden)
	}
	if session.RevokedAt != nil {
		return fmt.Errorf("RevokeSession: session %d already revoked at %s: %w", sessionID, session.RevokedAt.Format(time.RFC3339), ErrSessionAlreadyRevoked)
	}

	// Revoke all refresh tokens for this session
	if err := s.tokenRepo.RevokeAllTokensForSession(sessionID); err != nil {
		return fmt.Errorf("failed to revoke session tokens: %w", err)
	}
	// Revoke the session itself
	if err := s.sessionRepo.Revoke(sessionID); err != nil {
		return fmt.Errorf("failed to revoke session: %w", err)
	}
	// Hard-revoke its access token(s) at the gateway immediately.
	blacklistSession(ctx, s.cache, s.jwtService.AccessExpiry(), sessionID)

	_ = s.producer.Publish(ctx, kafkamsg.TopicAuthSessionRevoked, kafkamsg.AuthSessionRevokedMessage{
		SessionID: sessionID,
		UserID:    session.UserID,
		Reason:    "force_revoke",
	})
	return nil
}

// RevokeAllSessionsExceptCurrent revokes all sessions except the one tied to the given refresh token.
func (s *SessionService) RevokeAllSessionsExceptCurrent(ctx context.Context, userID int64, currentRefreshToken string) error {
	rt, err := s.tokenRepo.GetRefreshToken(currentRefreshToken)
	if err != nil {
		return fmt.Errorf("RevokeAllSessionsExceptCurrent: current token lookup: %v: %w", err, ErrSessionNotFound)
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
		blacklistSession(ctx, s.cache, s.jwtService.AccessExpiry(), sess.ID) // hard-revoke each session's access tokens
		_ = s.producer.Publish(ctx, kafkamsg.TopicAuthSessionRevoked, kafkamsg.AuthSessionRevokedMessage{
			SessionID: sess.ID,
			UserID:    userID,
			Reason:    "force_revoke",
		})
	}

	return nil
}

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
func (s *SessionService) GetLoginHistory(ctx context.Context, email string, limit int) ([]LoginHistoryEntry, error) {
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
