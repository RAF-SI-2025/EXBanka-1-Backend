package consumer

import (
	"context"
	"encoding/json"
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
