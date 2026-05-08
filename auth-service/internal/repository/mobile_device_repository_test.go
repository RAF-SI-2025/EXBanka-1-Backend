package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/contract/testutil"
)

func newMobileDeviceFixture(t *testing.T) *MobileDeviceRepository {
	t.Helper()
	db := testutil.SetupTestDB(t, &model.MobileDevice{})
	return NewMobileDeviceRepository(db)
}

func makeDevice(userID int64, deviceID, status string) *model.MobileDevice {
	now := time.Now()
	return &model.MobileDevice{
		UserID:       userID,
		SystemType:   "client",
		DeviceID:     deviceID,
		DeviceSecret: "secret",
		DeviceName:   "phone",
		Status:       status,
		LastSeenAt:   now,
		Version:      1,
	}
}

func TestMobileDeviceRepository_CreateAndGetByID(t *testing.T) {
	repo := newMobileDeviceFixture(t)
	d := makeDevice(1, "dev-uuid-1", "active")
	require.NoError(t, repo.Create(d))
	assert.NotZero(t, d.ID)

	got, err := repo.GetByID(d.ID)
	require.NoError(t, err)
	assert.Equal(t, "dev-uuid-1", got.DeviceID)
}

func TestMobileDeviceRepository_GetByID_NotFound(t *testing.T) {
	repo := newMobileDeviceFixture(t)
	_, err := repo.GetByID(9999)
	assert.Error(t, err)
}

func TestMobileDeviceRepository_GetByDeviceID(t *testing.T) {
	repo := newMobileDeviceFixture(t)
	require.NoError(t, repo.Create(makeDevice(1, "dev-A", "active")))

	got, err := repo.GetByDeviceID("dev-A")
	require.NoError(t, err)
	assert.Equal(t, int64(1), got.UserID)

	_, err = repo.GetByDeviceID("nope")
	assert.Error(t, err)
}

func TestMobileDeviceRepository_GetActiveByUserID(t *testing.T) {
	repo := newMobileDeviceFixture(t)
	require.NoError(t, repo.Create(makeDevice(1, "old", "deactivated")))
	require.NoError(t, repo.Create(makeDevice(1, "current", "active")))

	got, err := repo.GetActiveByUserID(1)
	require.NoError(t, err)
	assert.Equal(t, "current", got.DeviceID)

	_, err = repo.GetActiveByUserID(999)
	assert.Error(t, err)
}

func TestMobileDeviceRepository_Update_VersionIncrements(t *testing.T) {
	repo := newMobileDeviceFixture(t)
	d := makeDevice(1, "dev-1", "active")
	require.NoError(t, repo.Create(d))

	d.DeviceName = "renamed"
	require.NoError(t, repo.Update(d))

	got, err := repo.GetByDeviceID("dev-1")
	require.NoError(t, err)
	assert.Equal(t, "renamed", got.DeviceName)
	assert.Greater(t, got.Version, int64(1))
}

func TestMobileDeviceRepository_Update_BumpsVersionEachCall(t *testing.T) {
	repo := newMobileDeviceFixture(t)
	d := makeDevice(1, "dev-1", "active")
	require.NoError(t, repo.Create(d))

	d.DeviceName = "first"
	require.NoError(t, repo.Update(d))
	require.NoError(t, repo.Update(d))

	got, err := repo.GetByDeviceID("dev-1")
	require.NoError(t, err)
	// Version increments at least twice (once per Update call).
	assert.GreaterOrEqual(t, got.Version, int64(3))
}

func TestMobileDeviceRepository_DeactivateAllForUser(t *testing.T) {
	repo := newMobileDeviceFixture(t)
	require.NoError(t, repo.Create(makeDevice(1, "a", "active")))
	require.NoError(t, repo.Create(makeDevice(1, "b", "active")))
	require.NoError(t, repo.Create(makeDevice(2, "c", "active")))

	require.NoError(t, repo.DeactivateAllForUser(1))

	_, err := repo.GetActiveByUserID(1)
	assert.Error(t, err, "user 1 should have no active devices")

	got, err := repo.GetActiveByUserID(2)
	require.NoError(t, err)
	assert.Equal(t, "c", got.DeviceID)
}

func TestMobileDeviceRepository_UpdateLastSeen(t *testing.T) {
	repo := newMobileDeviceFixture(t)
	old := time.Now().Add(-time.Hour)
	d := makeDevice(1, "dev-1", "active")
	d.LastSeenAt = old
	require.NoError(t, repo.Create(d))

	require.NoError(t, repo.UpdateLastSeen("dev-1"))

	got, err := repo.GetByDeviceID("dev-1")
	require.NoError(t, err)
	assert.True(t, got.LastSeenAt.After(old))
}

func TestMobileDeviceRepository_DeactivateAllForUserInTx_AndCreateInTx(t *testing.T) {
	repo := newMobileDeviceFixture(t)
	require.NoError(t, repo.Create(makeDevice(1, "old", "active")))

	err := repo.DB().Transaction(func(tx *gorm.DB) error {
		if err := repo.DeactivateAllForUserInTx(tx, 1); err != nil {
			return err
		}
		return repo.CreateInTx(tx, makeDevice(1, "new", "active"))
	})
	require.NoError(t, err)

	got, err := repo.GetActiveByUserID(1)
	require.NoError(t, err)
	assert.Equal(t, "new", got.DeviceID)
}
