package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/contract/testutil"
)

func TestTOTPRepository_CRUD(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.TOTPSecret{})
	repo := NewTOTPRepository(db)

	require.NoError(t, repo.Create(&model.TOTPSecret{
		UserID: 7, Secret: "BASE32SECRET", Enabled: false,
	}))

	got, err := repo.GetByUserID(7)
	require.NoError(t, err)
	assert.Equal(t, "BASE32SECRET", got.Secret)
	assert.False(t, got.Enabled)

	require.NoError(t, repo.Enable(7))
	got, err = repo.GetByUserID(7)
	require.NoError(t, err)
	assert.True(t, got.Enabled)

	require.NoError(t, repo.Delete(7))
	_, err = repo.GetByUserID(7)
	assert.Error(t, err)
}

func TestTOTPRepository_GetByUserID_NotFound(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.TOTPSecret{})
	repo := NewTOTPRepository(db)
	_, err := repo.GetByUserID(9999)
	assert.Error(t, err)
}
