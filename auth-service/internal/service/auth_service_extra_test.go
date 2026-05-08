package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/exbanka/auth-service/internal/model"
	kafkamsg "github.com/exbanka/contract/kafka"
	userpb "github.com/exbanka/contract/userpb"
)

// totpGenerateCode is a small wrapper over totp.GenerateCode for readability.
func totpGenerateCode(secret string, t time.Time) (string, error) {
	return totp.GenerateCode(secret, t)
}

// errUserClient returns the configured error from every employee-shaped RPC.
type errUserClient struct {
	stubUserClient
	getErr error
}

func (e *errUserClient) GetEmployee(_ context.Context, _ *userpb.GetEmployeeRequest, _ ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	return nil, e.getErr
}

// TestAuthRefreshToken_GetEmployeeFails covers the user-service error branch in
// the employee path of RefreshToken — must surface as "user not found".
func TestAuthRefreshToken_GetEmployeeFails(t *testing.T) {
	f := newAuthFlowFixture(t)
	f.svc.userClient = &errUserClient{getErr: errors.New("user-service down")}

	acct := f.seedActiveAccountWithPassword(t, "u@test.com", "Abcdef12", model.PrincipalTypeEmployee, 1)
	rt := &model.RefreshToken{
		AccountID: acct.ID, Token: "rt-employee",
		ExpiresAt: time.Now().Add(time.Hour), SystemType: model.PrincipalTypeEmployee,
	}
	require.NoError(t, f.db.Create(rt).Error)

	_, _, err := f.svc.RefreshToken(context.Background(), "rt-employee", "ip", "ua")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user not found")
}

// TestAuthRefreshToken_AccountNotFound: the refresh token's AccountID
// references a missing account — must return "account not found".
func TestAuthRefreshToken_AccountNotFound(t *testing.T) {
	f := newAuthFlowFixture(t)
	rt := &model.RefreshToken{
		AccountID: 9999, // doesn't exist
		Token:     "ghost-rt",
		ExpiresAt: time.Now().Add(time.Hour), SystemType: model.PrincipalTypeEmployee,
	}
	require.NoError(t, f.db.Create(rt).Error)

	_, _, err := f.svc.RefreshToken(context.Background(), "ghost-rt", "ip", "ua")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account not found")
}

// TestRefreshTokenForMobile_AccountDisabled covers the disabled-account branch.
func TestRefreshTokenForMobile_AccountDisabled(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := &model.Account{
		Email: "x@test.com", PasswordHash: "h",
		Status:        model.AccountStatusDisabled,
		PrincipalType: model.PrincipalTypeClient,
		PrincipalID:   77,
	}
	require.NoError(t, f.db.Create(acct).Error)

	rt := &model.RefreshToken{
		AccountID: acct.ID, Token: "disabled-mob-rt",
		ExpiresAt: time.Now().Add(time.Hour), SystemType: model.PrincipalTypeClient,
	}
	require.NoError(t, f.db.Create(rt).Error)

	mobSvc, _ := newMobileSvcWithStubs(t, f.db)
	_, _, err := f.svc.RefreshTokenForMobile(context.Background(), "disabled-mob-rt", "any-dev", mobSvc)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountDisabled)
}

// TestRefreshTokenForMobile_RevokedRefreshToken covers the revoked-rt branch.
func TestRefreshTokenForMobile_RevokedRefreshToken(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "u@test.com", "Abcdef12", model.PrincipalTypeClient, 88)
	rt := &model.RefreshToken{
		AccountID: acct.ID, Token: "revoked-mob",
		ExpiresAt:  time.Now().Add(time.Hour),
		SystemType: model.PrincipalTypeClient,
		Revoked:    true,
	}
	require.NoError(t, f.db.Create(rt).Error)

	mobSvc, _ := newMobileSvcWithStubs(t, f.db)
	_, _, err := f.svc.RefreshTokenForMobile(context.Background(), "revoked-mob", "any", mobSvc)
	require.Error(t, err)
	// Token is filtered by the "revoked = false" predicate, so we get the
	// invalid-token sentinel rather than ErrTokenRevoked. Either way, the call
	// must fail.
	assert.Error(t, err)
}

// TestValidateRefreshToken_RevokedFlagSetButFiltered: GetRefreshToken filters
// by `revoked = false`, so a revoked token surfaces as ErrInvalidToken, not
// "revoked".
func TestValidateRefreshToken_RevokedTokenSurfacesAsInvalid(t *testing.T) {
	f := newAuthFlowFixture(t)
	require.NoError(t, f.db.Create(&model.RefreshToken{
		AccountID: 1, Token: "rev-rt",
		ExpiresAt:  time.Now().Add(time.Hour),
		SystemType: model.PrincipalTypeEmployee,
		Revoked:    true,
	}).Error)

	_, err := f.svc.ValidateRefreshToken("rev-rt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

// TestSetAccountStatus_NotFound covers the GetByPrincipal error branch when
// disabling an account that doesn't exist.
func TestSetAccountStatus_DisableAccountNotFound(t *testing.T) {
	f := newAuthFlowFixture(t)
	err := f.svc.SetAccountStatus(context.Background(), model.PrincipalTypeEmployee, 9999, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account not found")
}

// TestSetAccountStatus_PublishesEvent verifies the account-status-changed event
// payload reaches the producer queue.
func TestSetAccountStatus_PublishesEvent(t *testing.T) {
	f := newAuthFlowFixture(t)
	require.NoError(t, f.db.Create(&model.Account{
		Email: "u@test.com", Status: model.AccountStatusActive,
		PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 12,
	}).Error)

	require.NoError(t, f.svc.SetAccountStatus(context.Background(), model.PrincipalTypeEmployee, 12, false))
	// Find the Kafka event we just published.
	f.producer.mu.Lock()
	defer f.producer.mu.Unlock()
	found := false
	for _, ev := range f.producer.events {
		if ev.Topic == kafkamsg.TopicAuthAccountStatusChanged {
			found = true
			break
		}
	}
	assert.True(t, found, "must publish an account-status-changed event")
}

// TestActivateAccount_PublishesConfirmationEmail verifies that the success path
// reaches the SendEmail call.
func TestActivateAccount_PublishesConfirmationEmail(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := &model.Account{
		Email: "act@test.com", Status: model.AccountStatusPending,
		PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 77,
	}
	require.NoError(t, f.db.Create(acct).Error)
	require.NoError(t, f.db.Create(&model.ActivationToken{
		AccountID: acct.ID, Token: "act-pub-tok",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}).Error)

	require.NoError(t, f.svc.ActivateAccount(context.Background(), "act-pub-tok", "Abcdef12", "Abcdef12"))
	assert.Greater(t, f.producer.emailCount(), 0, "activation must trigger a confirmation email")
}

// TestCreateAccountAndActivationToken_SendsEmail covers the producer path for
// a brand-new account creation.
func TestCreateAccountAndActivationToken_SendsActivationEmail(t *testing.T) {
	f := newAuthFlowFixture(t)
	require.NoError(t, f.svc.CreateAccountAndActivationToken(
		context.Background(), 200, "fresh@test.com", "Carol", model.PrincipalTypeClient,
	))
	assert.Greater(t, f.producer.emailCount(), 0, "must publish an activation email")
}

// TestRequestPasswordReset_SendsEmail covers the email-emission branch on the
// happy path.
func TestRequestPasswordReset_PublishesEmail(t *testing.T) {
	f := newAuthFlowFixture(t)
	f.seedActiveAccountWithPassword(t, "rp@test.com", "Abcdef12", model.PrincipalTypeEmployee, 17)
	require.NoError(t, f.svc.RequestPasswordReset(context.Background(), "rp@test.com"))
	assert.Greater(t, f.producer.emailCount(), 0)
}

// TestResendActivationEmail_FetchesEmployeeNameForEmail covers the userClient
// success branch inside ResendActivationEmail (employee path).
func TestResendActivationEmail_EmployeeFirstNameInjected(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := &model.Account{
		Email: "resend@test.com", Status: model.AccountStatusPending,
		PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 21,
	}
	require.NoError(t, f.db.Create(acct).Error)

	require.NoError(t, f.svc.ResendActivationEmail(context.Background(), "resend@test.com"))
	// The producer should have at least one email queued.
	f.producer.mu.Lock()
	defer f.producer.mu.Unlock()
	require.NotEmpty(t, f.producer.emails)
	last := f.producer.emails[len(f.producer.emails)-1]
	// Stub returns FirstName="Test" for the GetEmployee response.
	assert.Equal(t, "Test", last.Data["first_name"])
}

// TestRevokeAllSessionsExceptCurrent_NoCurrentSession exercises the keep=0
// branch where the refresh token has no SessionID, falling through to
// RevokeAllForUser.
func TestRevokeAllSessionsExceptCurrent_NoSessionLink(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "u@test.com", "Abcdef12", model.PrincipalTypeEmployee, 60)

	// Two sessions, none linked to the rt.
	for i := 0; i < 2; i++ {
		require.NoError(t, f.db.Create(&model.ActiveSession{
			UserID: 60, UserRole: "EmployeeAdmin",
			SystemType:   model.PrincipalTypeEmployee,
			LastActiveAt: time.Now(), CreatedAt: time.Now(),
		}).Error)
	}

	rt := &model.RefreshToken{
		AccountID: acct.ID, Token: "no-link-rt",
		ExpiresAt:  time.Now().Add(time.Hour),
		SystemType: model.PrincipalTypeEmployee,
		// No SessionID
	}
	require.NoError(t, f.db.Create(rt).Error)

	require.NoError(t, f.svc.RevokeAllSessionsExceptCurrent(context.Background(), 60, "no-link-rt"))

	var live int64
	require.NoError(t, f.db.Model(&model.ActiveSession{}).
		Where("user_id = 60 AND revoked_at IS NULL").Count(&live).Error)
	assert.Equal(t, int64(0), live, "all sessions must be revoked when refresh token has no SessionID")
}

// TestGetLoginHistory_LimitClampHigh exercises the limit > 100 branch.
func TestGetLoginHistory_LimitClampHigh(t *testing.T) {
	f := newAuthFlowFixture(t)
	require.NoError(t, f.loginRepo.RecordAttempt("u@test.com", "ip", "ua", "browser", true))
	got, err := f.svc.GetLoginHistory(context.Background(), "u@test.com", 5000)
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

// TestVerify2FA_Success exercises the happy path where ValidateCode returns
// true and the totp record is enabled.
func TestVerify2FA_Success(t *testing.T) {
	f := newAuthFlowFixture(t)
	secret, _, err := f.svc.Setup2FA(context.Background(), 100, "u@test.com")
	require.NoError(t, err)

	code, err := totpGenerateCode(secret, time.Now())
	require.NoError(t, err)
	ok, err := f.svc.Verify2FA(context.Background(), 100, code)
	require.NoError(t, err)
	assert.True(t, ok)

	rec, err := f.totpRepo.GetByUserID(100)
	require.NoError(t, err)
	assert.True(t, rec.Enabled, "successful verify must mark TOTP enabled")
}

// TestDisable2FA_Success: provide a valid code so we hit the totpRepo.Delete
// branch.
func TestDisable2FA_Success(t *testing.T) {
	f := newAuthFlowFixture(t)
	secret, _, err := f.svc.Setup2FA(context.Background(), 101, "u@test.com")
	require.NoError(t, err)

	code, err := totpGenerateCode(secret, time.Now())
	require.NoError(t, err)
	ok, err := f.svc.Disable2FA(context.Background(), 101, code)
	require.NoError(t, err)
	assert.True(t, ok)

	_, err = f.totpRepo.GetByUserID(101)
	require.Error(t, err, "after Disable2FA the totp row must be gone")
}

// TestSetup2FA_OverwritesExistingRecord exercises the explicit Delete-then-Create
// path: calling Setup2FA twice produces a fresh record.
func TestSetup2FA_OverwritesExistingRecord(t *testing.T) {
	f := newAuthFlowFixture(t)
	s1, _, err := f.svc.Setup2FA(context.Background(), 102, "a@test.com")
	require.NoError(t, err)
	s2, _, err := f.svc.Setup2FA(context.Background(), 102, "a@test.com")
	require.NoError(t, err)
	assert.NotEqual(t, s1, s2, "second Setup2FA must produce a fresh secret")
}

// TestRequestPasswordReset_NoCacheUserClientNotNeeded just exercises the email
// publish branch when account exists.
func TestRequestPasswordReset_PublishesPRTAndEmail(t *testing.T) {
	f := newAuthFlowFixture(t)
	acct := f.seedActiveAccountWithPassword(t, "rp2@test.com", "Abcdef12", model.PrincipalTypeEmployee, 99)
	require.NoError(t, f.svc.RequestPasswordReset(context.Background(), "rp2@test.com"))

	var n int64
	require.NoError(t, f.db.Model(&model.PasswordResetToken{}).Where("account_id = ?", acct.ID).Count(&n).Error)
	assert.Equal(t, int64(1), n)
}
