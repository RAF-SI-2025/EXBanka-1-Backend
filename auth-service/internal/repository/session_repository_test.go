package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/contract/testutil"
)

func TestSessionRepository_CreateAndGetByID(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ActiveSession{})
	repo := NewSessionRepository(db)

	session := &model.ActiveSession{
		UserID:       1,
		UserRole:     "EmployeeBasic",
		IPAddress:    "192.168.1.1",
		UserAgent:    "Mozilla/5.0",
		SystemType:   "employee",
		LastActiveAt: time.Now(),
		CreatedAt:    time.Now(),
	}
	require.NoError(t, repo.Create(session))
	assert.NotZero(t, session.ID)

	got, err := repo.GetByID(session.ID)
	require.NoError(t, err)
	assert.Equal(t, session.UserID, got.UserID)
	assert.Equal(t, session.IPAddress, got.IPAddress)
}

func TestSessionRepository_ListByUser(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ActiveSession{})
	repo := NewSessionRepository(db)

	now := time.Now()
	// Create 2 active sessions and 1 revoked
	require.NoError(t, repo.Create(&model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}))
	require.NoError(t, repo.Create(&model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now.Add(-time.Hour), CreatedAt: now.Add(-time.Hour)}))
	revokedAt := now
	require.NoError(t, db.Create(&model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now, RevokedAt: &revokedAt}).Error)
	// Different user
	require.NoError(t, repo.Create(&model.ActiveSession{UserID: 2, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}))

	sessions, err := repo.ListByUser(1)
	require.NoError(t, err)
	assert.Len(t, sessions, 2) // Only non-revoked
}

func TestSessionRepository_Revoke(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ActiveSession{})
	repo := NewSessionRepository(db)

	now := time.Now()
	session := &model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}
	require.NoError(t, repo.Create(session))

	require.NoError(t, repo.Revoke(session.ID))

	got, err := repo.GetByID(session.ID)
	require.NoError(t, err)
	assert.NotNil(t, got.RevokedAt)
}

func TestSessionRepository_RevokeAllExcept(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ActiveSession{})
	repo := NewSessionRepository(db)

	now := time.Now()
	s1 := &model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}
	s2 := &model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}
	s3 := &model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}
	require.NoError(t, repo.Create(s1))
	require.NoError(t, repo.Create(s2))
	require.NoError(t, repo.Create(s3))

	require.NoError(t, repo.RevokeAllExcept(1, s2.ID))

	sessions, err := repo.ListByUser(1)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, s2.ID, sessions[0].ID)
}

func TestSessionRepository_UpdateLastActive(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ActiveSession{})
	repo := NewSessionRepository(db)

	old := time.Now().Add(-time.Hour)
	session := &model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: old, CreatedAt: old}
	require.NoError(t, repo.Create(session))

	require.NoError(t, repo.UpdateLastActive(session.ID))

	got, err := repo.GetByID(session.ID)
	require.NoError(t, err)
	assert.True(t, got.LastActiveAt.After(old))
}

func TestSessionRepository_RevokeByDeviceID(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ActiveSession{})
	repo := NewSessionRepository(db)

	now := time.Now()
	s1 := &model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", DeviceID: "device-abc", LastActiveAt: now, CreatedAt: now}
	s2 := &model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", DeviceID: "device-xyz", LastActiveAt: now, CreatedAt: now}
	require.NoError(t, repo.Create(s1))
	require.NoError(t, repo.Create(s2))

	require.NoError(t, repo.RevokeByDeviceID("device-abc"))

	sessions, err := repo.ListByUser(1)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, "device-xyz", sessions[0].DeviceID)
}

func TestSessionRepository_CountActiveByUser(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ActiveSession{})
	repo := NewSessionRepository(db)

	now := time.Now()
	require.NoError(t, repo.Create(&model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}))
	require.NoError(t, repo.Create(&model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}))

	count, err := repo.CountActiveByUser(1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestSessionRepository_RevokeAllForUser(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ActiveSession{})
	repo := NewSessionRepository(db)

	now := time.Now()
	require.NoError(t, repo.Create(&model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}))
	require.NoError(t, repo.Create(&model.ActiveSession{UserID: 1, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}))
	require.NoError(t, repo.Create(&model.ActiveSession{UserID: 2, UserRole: "client", SystemType: "client", LastActiveAt: now, CreatedAt: now}))

	require.NoError(t, repo.RevokeAllForUser(1))

	sessions, err := repo.ListByUser(1)
	require.NoError(t, err)
	assert.Len(t, sessions, 0)

	// User 2 should be unaffected
	sessions2, err := repo.ListByUser(2)
	require.NoError(t, err)
	assert.Len(t, sessions2, 1)
}
