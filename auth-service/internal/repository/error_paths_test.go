package repository

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/contract/testutil"
)

// These tests exercise the DB-error return branches of the repositories by
// dropping the backing table before the call, forcing the underlying query to
// fail. They assert the repository surfaces (rather than swallows) the error.

func TestAccountRepository_GetByPrincipals_DBError(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{})
	repo := NewAccountRepository(db)
	require.NoError(t, db.Migrator().DropTable(&model.Account{}))

	_, err := repo.GetByPrincipals(model.PrincipalTypeEmployee, []int64{1, 2})
	require.Error(t, err)
}

func TestAccountRepository_SetPasswordAndActivate_DBError(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{})
	repo := NewAccountRepository(db)
	require.NoError(t, db.Migrator().DropTable(&model.Account{}))

	err := repo.SetPasswordAndActivate(1, "hash")
	require.Error(t, err)
}

func TestAccountRepository_SetStatusByPrincipal_DBError(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{})
	repo := NewAccountRepository(db)
	require.NoError(t, db.Migrator().DropTable(&model.Account{}))

	err := repo.SetStatusByPrincipal(model.PrincipalTypeEmployee, 1, model.AccountStatusActive)
	require.Error(t, err)
}

func TestSessionRepository_GetByID_DBError(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ActiveSession{})
	repo := NewSessionRepository(db)
	require.NoError(t, db.Migrator().DropTable(&model.ActiveSession{}))

	_, err := repo.GetByID(1)
	require.Error(t, err)
}

func TestMobileDeviceRepository_Update_DBError(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.MobileDevice{})
	repo := NewMobileDeviceRepository(db)
	require.NoError(t, db.Migrator().DropTable(&model.MobileDevice{}))

	err := repo.Update(&model.MobileDevice{ID: 1, DeviceID: "x"})
	require.Error(t, err)
}

func TestLoginAttemptRepository_GetActiveLock_DBError(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.AccountLock{})
	repo := NewLoginAttemptRepository(db)
	require.NoError(t, db.Migrator().DropTable(&model.AccountLock{}))

	_, err := repo.GetActiveLock("u@test.com")
	require.Error(t, err)
}
