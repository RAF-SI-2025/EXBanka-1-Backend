package consumer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
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
	raw, _ := json.Marshal(msg)

	if err := h.Handle(context.Background(), raw); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(cache.calls) != 3 {
		t.Fatalf("expected 3 cache calls, got %d", len(cache.calls))
	}
	for i, want := range []int64{10, 11, 12} {
		if cache.calls[i].userID != want {
			t.Errorf("call[%d].userID = %d, want %d", i, cache.calls[i].userID, want)
		}
		if cache.calls[i].atUnix != 1700000000 {
			t.Errorf("call[%d].atUnix = %d, want 1700000000", i, cache.calls[i].atUnix)
		}
		if cache.calls[i].ttl != 15*time.Minute {
			t.Errorf("call[%d].ttl = %v, want 15m", i, cache.calls[i].ttl)
		}
	}
	if len(tokens.revoked) != 3 || tokens.revoked[0] != 10 || tokens.revoked[1] != 11 || tokens.revoked[2] != 12 {
		t.Errorf("revoked principals: %v", tokens.revoked)
	}
}

func TestRolePermChangeHandler_BadJSONReturnsError(t *testing.T) {
	h := NewRolePermChangeHandler(&spyRevokeCache{}, &spyTokenRepo{}, time.Minute)
	if err := h.Handle(context.Background(), []byte("not-json")); err == nil {
		t.Fatal("expected json error, got nil")
	}
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
	raw, _ := json.Marshal(msg)
	if err := h.Handle(context.Background(), raw); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(cache.calls) != 0 || len(tokens.revoked) != 0 {
		t.Errorf("expected no calls, got cache=%d tokens=%d", len(cache.calls), len(tokens.revoked))
	}
}
