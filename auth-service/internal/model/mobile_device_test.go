package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/contract/testutil"
)

// TestMobileDevice_BeforeUpdate_AppendsVersionWhereAndIncrements ensures the
// optimistic-lock GORM hook is called on save: the in-memory Version must be
// incremented and the persisted row must reflect the new version.
func TestMobileDevice_BeforeUpdate_AppendsVersionWhereAndIncrements(t *testing.T) {
	db := testutil.SetupTestDB(t, &MobileDevice{})

	now := time.Now()
	d := &MobileDevice{
		UserID: 1, SystemType: "client",
		DeviceID: "dev-x", DeviceSecret: "s", DeviceName: "n",
		Status: "active", LastSeenAt: now, Version: 1,
	}
	require.NoError(t, db.Create(d).Error)

	d.DeviceName = "renamed"
	require.NoError(t, db.Save(d).Error)
	// Hook should have post-incremented in-memory version.
	assert.Equal(t, int64(2), d.Version)

	// Persist matches the in-memory version.
	var got MobileDevice
	require.NoError(t, db.First(&got, d.ID).Error)
	assert.Equal(t, int64(2), got.Version)
	assert.Equal(t, "renamed", got.DeviceName)
}
