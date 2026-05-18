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

func newMobileActivationFixture(t *testing.T) *MobileActivationRepository {
	t.Helper()
	db := testutil.SetupTestDB(t, &model.MobileActivationCode{})
	return NewMobileActivationRepository(db)
}

func TestMobileActivationRepository_CreateAndGetLatest(t *testing.T) {
	repo := newMobileActivationFixture(t)
	require.NoError(t, repo.Create(&model.MobileActivationCode{
		Email: "u@test.com", Code: "111111",
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}))
	require.NoError(t, repo.Create(&model.MobileActivationCode{
		Email: "u@test.com", Code: "222222",
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}))

	got, err := repo.GetLatestByEmail("u@test.com")
	require.NoError(t, err)
	assert.Equal(t, "222222", got.Code, "GetLatestByEmail must return newest first")
}

func TestMobileActivationRepository_GetLatestByEmail_NotFound(t *testing.T) {
	repo := newMobileActivationFixture(t)
	_, err := repo.GetLatestByEmail("ghost@test.com")
	assert.Error(t, err)
}

func TestMobileActivationRepository_IncrementAttempts(t *testing.T) {
	repo := newMobileActivationFixture(t)
	c := &model.MobileActivationCode{
		Email: "u@test.com", Code: "111111",
		ExpiresAt: time.Now().Add(15 * time.Minute),
		Attempts:  0,
	}
	require.NoError(t, repo.Create(c))

	require.NoError(t, repo.IncrementAttempts(c.ID))
	require.NoError(t, repo.IncrementAttempts(c.ID))

	got, err := repo.GetLatestByEmail("u@test.com")
	require.NoError(t, err)
	assert.Equal(t, 2, got.Attempts)
}

func TestMobileActivationRepository_MarkUsed(t *testing.T) {
	repo := newMobileActivationFixture(t)
	c := &model.MobileActivationCode{
		Email: "u@test.com", Code: "111111",
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	require.NoError(t, repo.Create(c))

	require.NoError(t, repo.MarkUsed(c.ID))

	_, err := repo.GetLatestByEmail("u@test.com")
	assert.Error(t, err, "MarkUsed should hide it from GetLatestByEmail")
}

func TestMobileActivationRepository_TxHelpers(t *testing.T) {
	repo := newMobileActivationFixture(t)
	c := &model.MobileActivationCode{
		Email: "u@test.com", Code: "999999",
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	require.NoError(t, repo.Create(c))

	err := repo.DB().Transaction(func(tx *gorm.DB) error {
		got, err := repo.GetLatestByEmailForUpdate(tx, "u@test.com")
		if err != nil {
			return err
		}
		assert.Equal(t, c.ID, got.ID)
		if err := repo.IncrementAttemptsInTx(tx, c.ID); err != nil {
			return err
		}
		return repo.MarkUsedInTx(tx, c.ID)
	})
	require.NoError(t, err)

	_, err = repo.GetLatestByEmail("u@test.com")
	assert.Error(t, err, "code should be marked used after Tx")
}
