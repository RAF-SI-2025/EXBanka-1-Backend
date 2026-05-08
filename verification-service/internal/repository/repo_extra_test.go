package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/exbanka/verification-service/internal/model"
)

func TestGetByIDForUpdate_AndSaveInTx(t *testing.T) {
	repo, db := setupTestRepo(t)

	vc := newChallenge(1, "dev-1", "pending", time.Now().Add(time.Minute))
	require.NoError(t, repo.Create(vc))

	tx := db.Begin()
	defer tx.Rollback()

	got, err := repo.GetByIDForUpdate(tx, vc.ID)
	require.NoError(t, err)
	require.Equal(t, vc.ID, got.ID)

	got.Status = "verified"
	require.NoError(t, repo.SaveInTx(tx, got))
	require.NoError(t, tx.Commit().Error)

	final, err := repo.GetByID(vc.ID)
	require.NoError(t, err)
	require.Equal(t, "verified", final.Status)
}

func TestGetByIDForUpdate_Missing(t *testing.T) {
	repo, db := setupTestRepo(t)
	tx := db.Begin()
	defer tx.Rollback()
	_, err := repo.GetByIDForUpdate(tx, 99999)
	require.Error(t, err)
}

func TestVerificationChallenge_BeforeUpdate_HookBumpsVersion(t *testing.T) {
	_, db := setupTestRepo(t)
	vc := &model.VerificationChallenge{Version: 7}
	require.NoError(t, vc.BeforeUpdate(db))
	require.Equal(t, int64(8), vc.Version)
}
