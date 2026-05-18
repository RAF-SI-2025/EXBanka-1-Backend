package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/contract/testutil"
)

func newTokenRepoFixture(t *testing.T) (*TokenRepository, *AccountRepository, int64) {
	t.Helper()
	db := testutil.SetupTestDB(t,
		&model.Account{},
		&model.RefreshToken{},
		&model.ActivationToken{},
		&model.PasswordResetToken{},
	)
	tr := NewTokenRepository(db)
	ar := NewAccountRepository(db)
	a := &model.Account{
		Email: "u@test.com", PasswordHash: "h",
		Status:        model.AccountStatusActive,
		PrincipalType: model.PrincipalTypeEmployee,
		PrincipalID:   42,
	}
	require.NoError(t, ar.Create(a))
	return tr, ar, a.ID
}

func TestTokenRepository_RefreshTokenCRUD(t *testing.T) {
	tr, _, accountID := newTokenRepoFixture(t)

	// Create
	rt := &model.RefreshToken{
		AccountID: accountID, Token: "rt-1",
		ExpiresAt: time.Now().Add(time.Hour),
		Revoked:   false, SystemType: model.PrincipalTypeEmployee,
	}
	require.NoError(t, tr.CreateRefreshToken(rt))
	assert.NotZero(t, rt.ID)

	// Get
	got, err := tr.GetRefreshToken("rt-1")
	require.NoError(t, err)
	assert.Equal(t, accountID, got.AccountID)

	// Revoke
	require.NoError(t, tr.RevokeRefreshToken("rt-1"))
	_, err = tr.GetRefreshToken("rt-1")
	assert.Error(t, err, "revoked token should not be returned")

	// IncludingRevoked still returns it
	gotRev, err := tr.GetRefreshTokenIncludingRevoked("rt-1")
	require.NoError(t, err)
	assert.True(t, gotRev.Revoked)
}

func TestTokenRepository_RevokeAllForAccount(t *testing.T) {
	tr, _, accountID := newTokenRepoFixture(t)

	for i, tok := range []string{"a", "b", "c"} {
		require.NoError(t, tr.CreateRefreshToken(&model.RefreshToken{
			AccountID: accountID, Token: tok,
			ExpiresAt: time.Now().Add(time.Duration(i+1) * time.Hour),
		}))
	}

	require.NoError(t, tr.RevokeAllForAccount(accountID))

	for _, tok := range []string{"a", "b", "c"} {
		_, err := tr.GetRefreshToken(tok)
		assert.Error(t, err, "token %s should be revoked", tok)
	}
}

func TestTokenRepository_RevokeAllTokensForSession(t *testing.T) {
	tr, _, accountID := newTokenRepoFixture(t)

	sid := int64(7)
	other := int64(99)
	require.NoError(t, tr.CreateRefreshToken(&model.RefreshToken{
		AccountID: accountID, Token: "for-session",
		ExpiresAt: time.Now().Add(time.Hour),
		SessionID: &sid,
	}))
	require.NoError(t, tr.CreateRefreshToken(&model.RefreshToken{
		AccountID: accountID, Token: "other-session",
		ExpiresAt: time.Now().Add(time.Hour),
		SessionID: &other,
	}))

	require.NoError(t, tr.RevokeAllTokensForSession(sid))

	_, err := tr.GetRefreshToken("for-session")
	assert.Error(t, err)
	_, err = tr.GetRefreshToken("other-session")
	assert.NoError(t, err, "untouched session must remain")
}

func TestTokenRepository_RevokeAllForPrincipal(t *testing.T) {
	tr, _, accountID := newTokenRepoFixture(t)

	require.NoError(t, tr.CreateRefreshToken(&model.RefreshToken{
		AccountID: accountID, Token: "principal-tok",
		ExpiresAt: time.Now().Add(time.Hour),
	}))

	require.NoError(t, tr.RevokeAllForPrincipal(model.PrincipalTypeEmployee, 42))

	_, err := tr.GetRefreshToken("principal-tok")
	assert.Error(t, err)
}

func TestTokenRepository_ActivationTokenCRUD(t *testing.T) {
	tr, _, accountID := newTokenRepoFixture(t)

	at := &model.ActivationToken{
		AccountID: accountID, Token: "act-1",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, tr.CreateActivationToken(at))

	got, err := tr.GetActivationToken("act-1")
	require.NoError(t, err)
	assert.Equal(t, accountID, got.AccountID)
	assert.False(t, got.Used)

	require.NoError(t, tr.MarkActivationUsed("act-1"))

	_, err = tr.GetActivationToken("act-1")
	assert.Error(t, err, "used token should not be returned")
}

func TestTokenRepository_PasswordResetTokenCRUD(t *testing.T) {
	tr, _, accountID := newTokenRepoFixture(t)

	prt := &model.PasswordResetToken{
		AccountID: accountID, Token: "prt-1",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, tr.CreatePasswordResetToken(prt))

	got, err := tr.GetPasswordResetToken("prt-1")
	require.NoError(t, err)
	assert.Equal(t, accountID, got.AccountID)
	assert.False(t, got.Used)

	require.NoError(t, tr.MarkPasswordResetUsed("prt-1"))
	_, err = tr.GetPasswordResetToken("prt-1")
	assert.Error(t, err, "used token should not be returned")
}
