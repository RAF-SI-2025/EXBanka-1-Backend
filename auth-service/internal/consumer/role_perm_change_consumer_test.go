package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type spyRevokeCache struct {
	calls []struct {
		userID int64
		atUnix int64
		ttl    time.Duration
	}
}

func (s *spyRevokeCache) SetUserRevokedAt(_ context.Context, userID int64, atUnix int64, ttl time.Duration) error {
	s.calls = append(s.calls, struct {
		userID int64
		atUnix int64
		ttl    time.Duration
	}{userID, atUnix, ttl})
	return nil
}

type spyTokenRepo struct {
	revoked []int64
}

func (s *spyTokenRepo) RevokeAllForPrincipal(_ string, principalID int64) error {
	s.revoked = append(s.revoked, principalID)
	return nil
}

func TestRolePermChangeHandler_SetsEpochAndRevokesRefresh(t *testing.T) {
	cache := &spyRevokeCache{}
	tokens := &spyTokenRepo{}
	h := NewRolePermChangeHandler(cache, tokens, 15*time.Minute)

	msg := kafkamsg.RolePermissionsChangedMessage{
		RoleID:              7,
		RoleName:            "EmployeeAgent",
		AffectedEmployeeIDs: []int64{10, 11, 12},
		ChangedAt:           1700000000,
		Source:              "update_role_permissions",
	}
	raw, err := json.Marshal(msg)
	require.NoError(t, err)

	require.NoError(t, h.Handle(context.Background(), raw))
	require.Len(t, cache.calls, 3, "expected 3 cache calls")
	for i, want := range []int64{10, 11, 12} {
		assert.Equal(t, want, cache.calls[i].userID, "call[%d].userID", i)
		assert.Equal(t, int64(1700000000), cache.calls[i].atUnix, "call[%d].atUnix", i)
		assert.Equal(t, 15*time.Minute, cache.calls[i].ttl, "call[%d].ttl", i)
	}
	assert.Equal(t, []int64{10, 11, 12}, tokens.revoked)
}

func TestRolePermChangeHandler_BadJSONReturnsError(t *testing.T) {
	h := NewRolePermChangeHandler(&spyRevokeCache{}, &spyTokenRepo{}, time.Minute)
	err := h.Handle(context.Background(), []byte("not-json"))
	require.Error(t, err, "expected json error")
}

func TestRolePermChangeHandler_EmptyAffectedListIsNoop(t *testing.T) {
	cache := &spyRevokeCache{}
	tokens := &spyTokenRepo{}
	h := NewRolePermChangeHandler(cache, tokens, time.Minute)

	msg := kafkamsg.RolePermissionsChangedMessage{
		RoleID:              7,
		RoleName:            "EmptyRole",
		AffectedEmployeeIDs: []int64{},
		ChangedAt:           1700000000,
	}
	raw, err := json.Marshal(msg)
	require.NoError(t, err)
	require.NoError(t, h.Handle(context.Background(), raw))
	assert.Empty(t, cache.calls)
	assert.Empty(t, tokens.revoked)
}

// errRevokeCache always returns an error from SetUserRevokedAt.
type errRevokeCache struct{}

func (e *errRevokeCache) SetUserRevokedAt(_ context.Context, _ int64, _ int64, _ time.Duration) error {
	return fmt.Errorf("redis unavailable")
}

// errTokenRepo always returns an error from RevokeAllForPrincipal.
type errTokenRepo struct {
	revoked []int64
}

func (e *errTokenRepo) RevokeAllForPrincipal(_ string, principalID int64) error {
	e.revoked = append(e.revoked, principalID)
	return fmt.Errorf("db unavailable")
}

func TestRolePermChangeHandler_CacheError_ContinuesProcessing(t *testing.T) {
	// When SetUserRevokedAt fails, the handler logs the error but still calls
	// RevokeAllForPrincipal and returns nil (best-effort semantics).
	cache := &errRevokeCache{}
	tokens := &spyTokenRepo{}
	h := NewRolePermChangeHandler(cache, tokens, time.Minute)

	msg := kafkamsg.RolePermissionsChangedMessage{
		RoleID:              3,
		RoleName:            "EmployeeBasic",
		AffectedEmployeeIDs: []int64{20, 21},
		ChangedAt:           1700001000,
	}
	raw, err := json.Marshal(msg)
	require.NoError(t, err)

	// Handler must not return an error even though the cache is unavailable.
	require.NoError(t, h.Handle(context.Background(), raw))
	// Token revocation must still have been attempted for all affected employees.
	assert.Equal(t, []int64{20, 21}, tokens.revoked)
}

func TestRolePermChangeHandler_TokenRevokeError_ContinuesProcessing(t *testing.T) {
	// When RevokeAllForPrincipal fails, the handler logs the error but still
	// processes all affected employees and returns nil (best-effort semantics).
	cache := &spyRevokeCache{}
	tokens := &errTokenRepo{}
	h := NewRolePermChangeHandler(cache, tokens, time.Minute)

	msg := kafkamsg.RolePermissionsChangedMessage{
		RoleID:              5,
		RoleName:            "EmployeeAgent",
		AffectedEmployeeIDs: []int64{30, 31, 32},
		ChangedAt:           1700002000,
	}
	raw, err := json.Marshal(msg)
	require.NoError(t, err)

	require.NoError(t, h.Handle(context.Background(), raw))
	// Cache must have been called for each employee despite the token revocation failure.
	require.Len(t, cache.calls, 3)
	for i, want := range []int64{30, 31, 32} {
		assert.Equal(t, want, cache.calls[i].userID)
		assert.Equal(t, int64(1700002000), cache.calls[i].atUnix)
	}
}

func TestRolePermChangeHandler_BothErrors_ContinuesProcessing(t *testing.T) {
	// Both cache and token revocation fail — handler must return nil.
	cache := &errRevokeCache{}
	tokens := &errTokenRepo{}
	h := NewRolePermChangeHandler(cache, tokens, time.Minute)

	msg := kafkamsg.RolePermissionsChangedMessage{
		RoleID:              9,
		RoleName:            "EmployeeSupervisor",
		AffectedEmployeeIDs: []int64{40},
		ChangedAt:           1700003000,
	}
	raw, err := json.Marshal(msg)
	require.NoError(t, err)

	require.NoError(t, h.Handle(context.Background(), raw),
		"handler must not propagate best-effort errors")
}
