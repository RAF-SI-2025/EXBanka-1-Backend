package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/contract/testutil"
)

func TestAccountRepository_CreateAndGetByID(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{})
	repo := NewAccountRepository(db)

	a := &model.Account{
		Email:         "u@test.com",
		PasswordHash:  "hash",
		Status:        model.AccountStatusPending,
		PrincipalType: model.PrincipalTypeEmployee,
		PrincipalID:   1,
	}
	require.NoError(t, repo.Create(a))
	assert.NotZero(t, a.ID)

	var got model.Account
	require.NoError(t, repo.GetByID(a.ID, &got))
	assert.Equal(t, "u@test.com", got.Email)
}

func TestAccountRepository_GetByEmail(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{})
	repo := NewAccountRepository(db)
	require.NoError(t, repo.Create(&model.Account{
		Email: "x@test.com", Status: model.AccountStatusActive,
		PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 7,
	}))

	got, err := repo.GetByEmail("x@test.com")
	require.NoError(t, err)
	assert.Equal(t, int64(7), got.PrincipalID)

	_, err = repo.GetByEmail("missing@test.com")
	assert.Error(t, err)
}

func TestAccountRepository_GetByPrincipal(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{})
	repo := NewAccountRepository(db)
	require.NoError(t, repo.Create(&model.Account{
		Email: "p@test.com", Status: model.AccountStatusActive,
		PrincipalType: model.PrincipalTypeClient, PrincipalID: 99,
	}))

	got, err := repo.GetByPrincipal(model.PrincipalTypeClient, 99)
	require.NoError(t, err)
	assert.Equal(t, "p@test.com", got.Email)

	_, err = repo.GetByPrincipal(model.PrincipalTypeEmployee, 99)
	assert.Error(t, err, "wrong principal_type should miss")
}

func TestAccountRepository_GetByPrincipals(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{})
	repo := NewAccountRepository(db)

	for i, pid := range []int64{1, 2, 3} {
		require.NoError(t, repo.Create(&model.Account{
			Email: "u" + string(rune('a'+i)) + "@test.com",
			Status: func() string {
				if i%2 == 0 {
					return model.AccountStatusActive
				}
				return model.AccountStatusDisabled
			}(),
			PrincipalType: model.PrincipalTypeEmployee,
			PrincipalID:   pid,
		}))
	}

	// Empty slice short-circuits.
	m, err := repo.GetByPrincipals(model.PrincipalTypeEmployee, []int64{})
	require.NoError(t, err)
	assert.Empty(t, m)

	// Found subset.
	m, err = repo.GetByPrincipals(model.PrincipalTypeEmployee, []int64{1, 3, 99})
	require.NoError(t, err)
	assert.Len(t, m, 2)
	assert.Equal(t, model.AccountStatusActive, m[1].Status)
	assert.Equal(t, model.AccountStatusActive, m[3].Status)
	_, ok := m[99]
	assert.False(t, ok)
}

func TestAccountRepository_SetPassword(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{})
	repo := NewAccountRepository(db)
	a := &model.Account{
		Email: "u@test.com", PasswordHash: "old",
		Status:        model.AccountStatusActive,
		PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 1,
	}
	require.NoError(t, repo.Create(a))

	require.NoError(t, repo.SetPassword(a.ID, "newhash"))

	var got model.Account
	require.NoError(t, repo.GetByID(a.ID, &got))
	assert.Equal(t, "newhash", got.PasswordHash)
}

func TestAccountRepository_SetPasswordAndActivate_Success(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{})
	repo := NewAccountRepository(db)
	a := &model.Account{
		Email: "u@test.com", PasswordHash: "",
		Status:        model.AccountStatusPending,
		PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 1,
	}
	require.NoError(t, repo.Create(a))

	require.NoError(t, repo.SetPasswordAndActivate(a.ID, "newhash"))

	var got model.Account
	require.NoError(t, repo.GetByID(a.ID, &got))
	assert.Equal(t, "newhash", got.PasswordHash)
	assert.Equal(t, model.AccountStatusActive, got.Status)
}

func TestAccountRepository_SetPasswordAndActivate_AlreadyActive(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{})
	repo := NewAccountRepository(db)
	a := &model.Account{
		Email: "u@test.com", PasswordHash: "h",
		Status:        model.AccountStatusActive,
		PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 1,
	}
	require.NoError(t, repo.Create(a))

	err := repo.SetPasswordAndActivate(a.ID, "newhash")
	require.Error(t, err, "non-pending account must not be re-activated")
}

func TestAccountRepository_SetStatus(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{})
	repo := NewAccountRepository(db)
	a := &model.Account{
		Email: "u@test.com", Status: model.AccountStatusActive,
		PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 1,
	}
	require.NoError(t, repo.Create(a))

	require.NoError(t, repo.SetStatus(a.ID, model.AccountStatusDisabled))

	var got model.Account
	require.NoError(t, repo.GetByID(a.ID, &got))
	assert.Equal(t, model.AccountStatusDisabled, got.Status)
}

func TestAccountRepository_SetStatusByPrincipal_Success(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{})
	repo := NewAccountRepository(db)
	require.NoError(t, repo.Create(&model.Account{
		Email: "u@test.com", Status: model.AccountStatusActive,
		PrincipalType: model.PrincipalTypeEmployee, PrincipalID: 5,
	}))

	require.NoError(t, repo.SetStatusByPrincipal(model.PrincipalTypeEmployee, 5, model.AccountStatusDisabled))

	got, err := repo.GetByPrincipal(model.PrincipalTypeEmployee, 5)
	require.NoError(t, err)
	assert.Equal(t, model.AccountStatusDisabled, got.Status)
}

func TestAccountRepository_SetStatusByPrincipal_NotFound(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{})
	repo := NewAccountRepository(db)
	err := repo.SetStatusByPrincipal(model.PrincipalTypeEmployee, 9999, model.AccountStatusDisabled)
	assert.Error(t, err)
}
