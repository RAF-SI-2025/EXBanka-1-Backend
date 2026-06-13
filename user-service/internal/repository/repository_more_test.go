// repository_more_test.go
//
// Additional repository tests that lift the previously-uncovered list/query
// surface: EmployeeRepository.List, ChangelogRepository.ListAll,
// ActuaryRepository.ListActuaries and the empty-result branch of
// RoleRepository.ListEmployeeIDsByRole. All are backed by in-memory SQLite.
// Postgres-only ILIKE filter branches are intentionally not exercised here
// (SQLite has no ILIKE keyword); the unfiltered query bodies are.
package repository

import (
	"testing"
	"time"

	"github.com/exbanka/contract/changelog"
	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/user-service/internal/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// -----------------------------------------------------------------------------
// EmployeeRepository.List — unfiltered pagination + default clamping
// -----------------------------------------------------------------------------

func TestEmployeeRepository_List_PaginationAndDefaults(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Employee{})
	repo := NewEmployeeRepository(db)

	dob := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		emp := &model.Employee{
			Email:       string(rune('a'+i)) + "@bank.rs",
			FirstName:   "First",
			LastName:    "Last",
			JMBG:        "111111111111" + string(rune('0'+i)),
			Username:    "user" + string(rune('0'+i)),
			DateOfBirth: dob,
		}
		require.NoError(t, repo.Create(emp))
	}

	// First page of 2 — total reflects the whole set, page is capped to size.
	rows, total, err := repo.List("", "", "", 1, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, rows, 2)

	// Second page.
	rows, total, err = repo.List("", "", "", 2, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, rows, 2)

	// page < 1 and pageSize < 1 normalize to page 1 / pageSize 20 → all five rows.
	rows, total, err = repo.List("", "", "", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, rows, 5)
}

// -----------------------------------------------------------------------------
// ChangelogRepository.ListAll — every optional filter branch
// -----------------------------------------------------------------------------

func TestChangelogRepository_ListAll_Filters(t *testing.T) {
	db := newChangelogTestDB(t)
	repo := NewChangelogRepository(db)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	entries := []changelog.Entry{
		{EntityType: "employee", EntityID: 1, Action: "create", ChangedBy: 10, ChangedAt: base},
		{EntityType: "employee", EntityID: 2, Action: "update", ChangedBy: 20, ChangedAt: base.Add(time.Hour)},
		{EntityType: "limit", EntityID: 1, Action: "update", ChangedBy: 10, ChangedAt: base.Add(2 * time.Hour)},
	}
	for _, e := range entries {
		require.NoError(t, repo.Create(e))
	}

	// No filters — all three, newest first.
	rows, total, err := repo.ListAll(ChangelogFilters{}, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, rows, 3)
	assert.Equal(t, "limit", rows[0].EntityType, "newest (changed_at DESC) first")

	// Filter by actor.
	rows, total, err = repo.ListAll(ChangelogFilters{ActorID: 10}, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, rows, 2)

	// Filter by action.
	rows, total, err = repo.ListAll(ChangelogFilters{Action: "update"}, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, rows, 2)

	// Time-bounded window. Boundaries use a wide (±48h) margin so the assertions
	// stay robust against SQLite storing datetimes in local time vs the
	// time.Unix-derived bounds (a string-comparison artifact that does not occur
	// on Postgres timestamptz). A wide window exercises both Since and Until
	// branches while still proving they filter.
	rows, total, err = repo.ListAll(ChangelogFilters{
		Since: base.Add(-48 * time.Hour).Unix(),
		Until: base.Add(48 * time.Hour).Unix(),
	}, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total, "wide window includes every row")
	assert.Len(t, rows, 3)

	// Since after every row → Since branch excludes all.
	_, total, err = repo.ListAll(ChangelogFilters{Since: base.Add(48 * time.Hour).Unix()}, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)

	// Until before every row → Until branch excludes all.
	_, total, err = repo.ListAll(ChangelogFilters{Until: base.Add(-48 * time.Hour).Unix()}, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)

	// Combined actor + action filter that matches nothing.
	rows, total, err = repo.ListAll(ChangelogFilters{ActorID: 20, Action: "create"}, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, rows)
}

// -----------------------------------------------------------------------------
// ActuaryRepository.ListActuaries — unfiltered join body
// -----------------------------------------------------------------------------

func TestActuaryRepository_ListActuaries_NoFilters(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Role{}, &model.Permission{}, &model.Employee{}, &model.ActuaryLimit{})
	repo := NewActuaryRepository(db)

	agent := model.Role{Name: "EmployeeAgent"}
	basic := model.Role{Name: "EmployeeBasic"}
	require.NoError(t, db.Create(&agent).Error)
	require.NoError(t, db.Create(&basic).Error)

	dob := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	// Agent — should appear, with a backing actuary_limit row.
	empAgent := &model.Employee{
		Email: "agent@bank.rs", FirstName: "Ag", LastName: "Ent", JMBG: "1111111111111",
		Username: "agent", DateOfBirth: dob, Position: "trader", Roles: []model.Role{agent},
	}
	require.NoError(t, repo.db.Create(empAgent).Error)
	require.NoError(t, repo.Upsert(&model.ActuaryLimit{EmployeeID: empAgent.ID, Limit: decimal.NewFromInt(1000)}))

	// Basic — must NOT appear (role filter excludes it).
	empBasic := &model.Employee{
		Email: "basic@bank.rs", FirstName: "Ba", LastName: "Sic", JMBG: "2222222222222",
		Username: "basic", DateOfBirth: dob, Roles: []model.Role{basic},
	}
	require.NoError(t, repo.db.Create(empBasic).Error)

	rows, total, err := repo.ListActuaries("", "", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total, "only EmployeeAgent qualifies as an actuary")
	require.Len(t, rows, 1)
	assert.Equal(t, empAgent.ID, rows[0].EmployeeID)
	assert.InDelta(t, 1000, rows[0].Limit, 0.001)

	// Default page/pageSize clamping path (page<1, pageSize<1).
	rows, total, err = repo.ListActuaries("", "", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, rows, 1)
}

// -----------------------------------------------------------------------------
// RoleRepository.ListEmployeeIDsByRole — populated and empty branches
// -----------------------------------------------------------------------------

func TestRoleRepository_ListEmployeeIDsByRole_PopulatedAndEmpty(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Role{}, &model.Permission{}, &model.Employee{})
	roleRepo := NewRoleRepository(db)
	empRepo := NewEmployeeRepository(db)

	role := &model.Role{Name: "EmployeeAgent"}
	require.NoError(t, roleRepo.Create(role))
	emptyRole := &model.Role{Name: "EmployeeSupervisor"}
	require.NoError(t, roleRepo.Create(emptyRole))

	dob := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		emp := &model.Employee{
			Email: string(rune('a'+i)) + "@x.rs", FirstName: "F", LastName: "L",
			JMBG: "333333333333" + string(rune('0'+i)), Username: "e" + string(rune('0'+i)),
			DateOfBirth: dob, Roles: []model.Role{*role},
		}
		require.NoError(t, empRepo.Create(emp))
	}

	ids, err := roleRepo.ListEmployeeIDsByRole(role.ID)
	require.NoError(t, err)
	assert.Len(t, ids, 2)

	// Role with no employees → non-nil empty slice (the nil-guard branch).
	empty, err := roleRepo.ListEmployeeIDsByRole(emptyRole.ID)
	require.NoError(t, err)
	assert.NotNil(t, empty)
	assert.Empty(t, empty)
}
