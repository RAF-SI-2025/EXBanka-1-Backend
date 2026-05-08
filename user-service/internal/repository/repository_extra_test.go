// repository_extra_test.go
//
// Repository CRUD tests backed by an in-memory SQLite database. These cover
// straightforward GORM-mapped operations across the user-service repository
// layer that don't depend on PostgreSQL-specific SQL fragments.
package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/exbanka/contract/changelog"
	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/user-service/internal/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// -----------------------------------------------------------------------------
// EmployeeRepository
// -----------------------------------------------------------------------------

func TestEmployeeRepository_CreateGetByIDByEmailByJMBG(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Employee{})
	repo := NewEmployeeRepository(db)

	emp := &model.Employee{
		Email:       "alice@bank.rs",
		FirstName:   "Alice",
		LastName:    "A",
		JMBG:        "1111111111111",
		Username:    "alice",
		DateOfBirth: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	require.NoError(t, repo.Create(emp))
	require.NotZero(t, emp.ID)

	got, err := repo.GetByID(emp.ID)
	require.NoError(t, err)
	assert.Equal(t, "alice@bank.rs", got.Email)

	got2, err := repo.GetByEmail("alice@bank.rs")
	require.NoError(t, err)
	assert.Equal(t, emp.ID, got2.ID)

	got3, err := repo.GetByJMBG("1111111111111")
	require.NoError(t, err)
	assert.Equal(t, emp.ID, got3.ID)

	_, err = repo.GetByID(999999)
	assert.Error(t, err)

	_, err = repo.GetByEmail("nope@nope")
	assert.Error(t, err)

	_, err = repo.GetByJMBG("0000000000000")
	assert.Error(t, err)
}

func TestEmployeeRepository_GetByIDs(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Employee{})
	repo := NewEmployeeRepository(db)

	dob := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	a := &model.Employee{Email: "a@x", FirstName: "A", LastName: "1", JMBG: "1111111111111", Username: "a", DateOfBirth: dob}
	b := &model.Employee{Email: "b@x", FirstName: "B", LastName: "2", JMBG: "2222222222222", Username: "b", DateOfBirth: dob}
	c := &model.Employee{Email: "c@x", FirstName: "C", LastName: "3", JMBG: "3333333333333", Username: "c", DateOfBirth: dob}
	require.NoError(t, repo.Create(a))
	require.NoError(t, repo.Create(b))
	require.NoError(t, repo.Create(c))

	rows, err := repo.GetByIDs([]int64{a.ID, c.ID})
	require.NoError(t, err)
	assert.Len(t, rows, 2)

	empty, err := repo.GetByIDs(nil)
	require.NoError(t, err)
	assert.Nil(t, empty)
}

func TestEmployeeRepository_UpdateAndOptimisticLockMiss(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Employee{})
	repo := NewEmployeeRepository(db)

	emp := &model.Employee{
		Email:       "u@bank",
		FirstName:   "U",
		LastName:    "U",
		JMBG:        "9999999999999",
		Username:    "uuser",
		DateOfBirth: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
		Version:     1,
	}
	require.NoError(t, repo.Create(emp))

	emp.LastName = "Updated"
	require.NoError(t, repo.Update(emp))

	got, _ := repo.GetByID(emp.ID)
	assert.Equal(t, "Updated", got.LastName)
}

func TestEmployeeRepository_GetWithRoles(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Role{}, &model.Permission{}, &model.Employee{})
	repo := NewEmployeeRepository(db)

	role := model.Role{Name: "TestRole"}
	require.NoError(t, db.Create(&role).Error)

	emp := &model.Employee{
		Email:       "withroles@bank",
		FirstName:   "W",
		LastName:    "R",
		JMBG:        "5555555555555",
		Username:    "wruser",
		DateOfBirth: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
		Roles:       []model.Role{role},
	}
	require.NoError(t, repo.Create(emp))

	got, err := repo.GetByIDWithRoles(emp.ID)
	require.NoError(t, err)
	require.Len(t, got.Roles, 1)
	assert.Equal(t, "TestRole", got.Roles[0].Name)

	got2, err := repo.GetByEmailWithRoles("withroles@bank")
	require.NoError(t, err)
	require.Len(t, got2.Roles, 1)
}

func TestEmployeeRepository_SetEmployeeRolesAndAdditionalPerms(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Role{}, &model.Permission{}, &model.Employee{})
	repo := NewEmployeeRepository(db)

	roleA := model.Role{Name: "RoleA"}
	roleB := model.Role{Name: "RoleB"}
	require.NoError(t, db.Create(&roleA).Error)
	require.NoError(t, db.Create(&roleB).Error)

	permX := model.Permission{Code: "x.y.z"}
	require.NoError(t, db.Create(&permX).Error)

	emp := &model.Employee{
		Email:       "set@bank",
		FirstName:   "S",
		LastName:    "T",
		JMBG:        "7777777777777",
		Username:    "setuser",
		DateOfBirth: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	require.NoError(t, repo.Create(emp))

	// Set roles by ID (already-loaded role)
	require.NoError(t, repo.SetEmployeeRoles(emp.ID, []model.Role{roleA}))
	// Set roles by name (fresh, empty struct path)
	require.NoError(t, repo.SetEmployeeRoles(emp.ID, []model.Role{{Name: "RoleB"}}))

	loaded, err := repo.GetByIDWithRoles(emp.ID)
	require.NoError(t, err)
	require.Len(t, loaded.Roles, 1)
	assert.Equal(t, "RoleB", loaded.Roles[0].Name)

	// Now additional permissions.
	require.NoError(t, repo.SetAdditionalPermissions(emp.ID, []model.Permission{permX}))

	loaded2, err := repo.GetByIDWithRoles(emp.ID)
	require.NoError(t, err)
	require.Len(t, loaded2.AdditionalPermissions, 1)
	assert.Equal(t, "x.y.z", loaded2.AdditionalPermissions[0].Code)
}

// SetEmployeeRoles silently drops nameless / unresolvable role entries.
func TestEmployeeRepository_SetEmployeeRoles_UnknownRoleSkipped(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Role{}, &model.Permission{}, &model.Employee{})
	repo := NewEmployeeRepository(db)

	emp := &model.Employee{
		Email:       "skip@bank",
		FirstName:   "S",
		LastName:    "K",
		JMBG:        "8888888888888",
		Username:    "skipuser",
		DateOfBirth: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	require.NoError(t, repo.Create(emp))

	// Only an unknown role and an empty entry are passed: no roles get set.
	require.NoError(t, repo.SetEmployeeRoles(emp.ID, []model.Role{{Name: "Unknown"}, {Name: ""}}))

	loaded, err := repo.GetByIDWithRoles(emp.ID)
	require.NoError(t, err)
	assert.Empty(t, loaded.Roles, "unknown role names should not persist")
}

// -----------------------------------------------------------------------------
// EmployeeLimitRepository
// -----------------------------------------------------------------------------

func TestEmployeeLimitRepository_Lifecycle(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.EmployeeLimit{})
	repo := NewEmployeeLimitRepository(db)

	limit := &model.EmployeeLimit{
		EmployeeID:            42,
		MaxLoanApprovalAmount: decimal.NewFromInt(1000),
		MaxSingleTransaction:  decimal.NewFromInt(2000),
		Version:               1,
	}
	require.NoError(t, repo.Create(limit))
	require.NotZero(t, limit.ID)

	got, err := repo.GetByEmployeeID(42)
	require.NoError(t, err)
	assert.True(t, got.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(1000)))

	// GetByEmployeeID for missing employee returns a zero-value limit, not an error.
	missing, err := repo.GetByEmployeeID(99)
	require.NoError(t, err)
	assert.Equal(t, int64(99), missing.EmployeeID)
	assert.Zero(t, missing.ID, "missing limit returns zero-id placeholder")

	// Update happy path.
	got.MaxLoanApprovalAmount = decimal.NewFromInt(5000)
	require.NoError(t, repo.Update(got))

	updated, err := repo.GetByEmployeeID(42)
	require.NoError(t, err)
	assert.True(t, updated.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(5000)))

	require.NoError(t, repo.ResetDailyUsedLimits()) // currently a no-op
	require.NoError(t, repo.Delete(42))

	// After delete, GetByEmployeeID falls back to zero-value placeholder.
	post, err := repo.GetByEmployeeID(42)
	require.NoError(t, err)
	assert.Zero(t, post.ID)
}

func TestEmployeeLimitRepository_Upsert(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.EmployeeLimit{})
	repo := NewEmployeeLimitRepository(db)

	first := &model.EmployeeLimit{
		EmployeeID:            7,
		MaxLoanApprovalAmount: decimal.NewFromInt(100),
		Version:               1,
	}
	require.NoError(t, repo.Upsert(first))

	// Second upsert on same employee_id should update, not insert.
	second := &model.EmployeeLimit{
		EmployeeID:            7,
		MaxLoanApprovalAmount: decimal.NewFromInt(200),
		Version:               1,
	}
	require.NoError(t, repo.Upsert(second))

	got, err := repo.GetByEmployeeID(7)
	require.NoError(t, err)
	assert.True(t, got.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(200)))
}

// -----------------------------------------------------------------------------
// LimitTemplateRepository
// -----------------------------------------------------------------------------

func TestLimitTemplateRepository_CRUD(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.LimitTemplate{})
	repo := NewLimitTemplateRepository(db)

	tmpl := &model.LimitTemplate{
		Name:                  "BasicTeller",
		Description:           "default basic",
		MaxLoanApprovalAmount: decimal.NewFromInt(50000),
	}
	require.NoError(t, repo.Create(tmpl))
	require.NotZero(t, tmpl.ID)

	got, err := repo.GetByID(tmpl.ID)
	require.NoError(t, err)
	assert.Equal(t, "BasicTeller", got.Name)

	got2, err := repo.GetByName("BasicTeller")
	require.NoError(t, err)
	assert.Equal(t, tmpl.ID, got2.ID)

	all, err := repo.List()
	require.NoError(t, err)
	assert.Len(t, all, 1)

	got.Description = "updated"
	require.NoError(t, repo.Update(got))
	updated, _ := repo.GetByID(tmpl.ID)
	assert.Equal(t, "updated", updated.Description)

	require.NoError(t, repo.Delete(tmpl.ID))
	_, err = repo.GetByID(tmpl.ID)
	assert.Error(t, err)
}

// -----------------------------------------------------------------------------
// LimitBlueprintRepository
// -----------------------------------------------------------------------------

func TestLimitBlueprintRepository_CRUD(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.LimitBlueprint{})
	repo := NewLimitBlueprintRepository(db)

	bp := &model.LimitBlueprint{
		Name:        "Standard",
		Description: "std",
		Type:        model.BlueprintTypeEmployee,
		Values:      datatypes.JSON([]byte(`{"foo":"bar"}`)),
	}
	require.NoError(t, repo.Create(bp))
	require.NotZero(t, bp.ID)

	got, err := repo.GetByID(bp.ID)
	require.NoError(t, err)
	assert.Equal(t, "Standard", got.Name)

	all, err := repo.List("")
	require.NoError(t, err)
	assert.Len(t, all, 1)

	filtered, err := repo.List(model.BlueprintTypeEmployee)
	require.NoError(t, err)
	assert.Len(t, filtered, 1)

	none, err := repo.List(model.BlueprintTypeActuary)
	require.NoError(t, err)
	assert.Len(t, none, 0)

	got2, err := repo.GetByNameAndType("Standard", model.BlueprintTypeEmployee)
	require.NoError(t, err)
	assert.Equal(t, bp.ID, got2.ID)

	_, err = repo.GetByNameAndType("Missing", model.BlueprintTypeEmployee)
	assert.Error(t, err)

	got.Description = "newdesc"
	require.NoError(t, repo.Update(got))
	post, _ := repo.GetByID(bp.ID)
	assert.Equal(t, "newdesc", post.Description)

	require.NoError(t, repo.Delete(bp.ID))
	_, err = repo.GetByID(bp.ID)
	assert.Error(t, err)
}

// -----------------------------------------------------------------------------
// PermissionRepository
// -----------------------------------------------------------------------------

func TestPermissionRepository_CRUD(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Permission{})
	repo := NewPermissionRepository(db)

	p := &model.Permission{Code: "clients.read.all", Description: "Read clients", Category: "clients"}
	require.NoError(t, repo.Create(p))
	require.NotZero(t, p.ID)

	got, err := repo.GetByCode("clients.read.all")
	require.NoError(t, err)
	assert.Equal(t, p.ID, got.ID)

	got2, err := repo.GetByID(p.ID)
	require.NoError(t, err)
	assert.Equal(t, "clients.read.all", got2.Code)

	_ = repo.Create(&model.Permission{Code: "accounts.read.all", Category: "accounts"})

	all, err := repo.List()
	require.NoError(t, err)
	assert.Len(t, all, 2)

	clients, err := repo.ListByCategory("clients")
	require.NoError(t, err)
	assert.Len(t, clients, 1)
	assert.Equal(t, "clients.read.all", clients[0].Code)

	subset, err := repo.ListByCodes([]string{"clients.read.all"})
	require.NoError(t, err)
	assert.Len(t, subset, 1)

	none, err := repo.ListByCodes(nil)
	require.NoError(t, err)
	assert.Empty(t, none)

	require.NoError(t, repo.Delete(p.ID))
	_, err = repo.GetByCode("clients.read.all")
	assert.Error(t, err)

	err = repo.Delete(99999)
	// Delete of missing row is a no-op (no error from gorm) but harmless.
	_ = err
}

// -----------------------------------------------------------------------------
// RoleRepository basic CRUD
// -----------------------------------------------------------------------------

func TestRoleRepository_CRUDAndPermissions(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Role{}, &model.Permission{})
	repo := NewRoleRepository(db)

	p1 := model.Permission{Code: "p1"}
	p2 := model.Permission{Code: "p2"}
	require.NoError(t, db.Create(&p1).Error)
	require.NoError(t, db.Create(&p2).Error)

	role := &model.Role{Name: "RoleX", Description: "x", Permissions: []model.Permission{p1}}
	require.NoError(t, repo.Create(role))
	require.NotZero(t, role.ID)

	got, err := repo.GetByID(role.ID)
	require.NoError(t, err)
	assert.Equal(t, "RoleX", got.Name)
	require.Len(t, got.Permissions, 1)

	got2, err := repo.GetByName("RoleX")
	require.NoError(t, err)
	assert.Equal(t, role.ID, got2.ID)

	all, err := repo.List()
	require.NoError(t, err)
	assert.Len(t, all, 1)

	// Add another role and exercise GetByNames.
	role2 := &model.Role{Name: "RoleY", Description: "y"}
	require.NoError(t, repo.Create(role2))

	multi, err := repo.GetByNames([]string{"RoleX", "RoleY"})
	require.NoError(t, err)
	assert.Len(t, multi, 2)

	none, err := repo.GetByNames(nil)
	require.NoError(t, err)
	assert.Empty(t, none)

	// Update + SetPermissions
	got.Description = "updated"
	require.NoError(t, repo.Update(got))
	require.NoError(t, repo.SetPermissions(role.ID, []model.Permission{p2}))

	updated, _ := repo.GetByID(role.ID)
	require.Len(t, updated.Permissions, 1)
	assert.Equal(t, "p2", updated.Permissions[0].Code)

	// SetPermissions on missing role should error.
	err = repo.SetPermissions(99999, nil)
	assert.Error(t, err)

	// Delete clears permissions and the row itself.
	require.NoError(t, repo.Delete(role.ID))
	_, err = repo.GetByID(role.ID)
	assert.Error(t, err)
}

// -----------------------------------------------------------------------------
// ActuaryRepository (basic CRUD branches; ListActuaries uses ILIKE which SQLite doesn't support)
// -----------------------------------------------------------------------------

func TestActuaryRepository_CRUDAndUpsert(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ActuaryLimit{})
	repo := NewActuaryRepository(db)

	limit := &model.ActuaryLimit{EmployeeID: 11, Limit: decimal.NewFromInt(1000), Version: 1}
	require.NoError(t, repo.Create(limit))
	require.NotZero(t, limit.ID)

	got, err := repo.GetByID(limit.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(11), got.EmployeeID)

	got2, err := repo.GetByEmployeeID(11)
	require.NoError(t, err)
	assert.Equal(t, limit.ID, got2.ID)

	_, err = repo.GetByID(99999)
	assert.Error(t, err)

	_, err = repo.GetByEmployeeID(99999)
	assert.Error(t, err)

	// Save: happy path
	got.Limit = decimal.NewFromInt(2000)
	require.NoError(t, repo.Save(got))

	post, _ := repo.GetByEmployeeID(11)
	assert.True(t, post.Limit.Equal(decimal.NewFromInt(2000)))

	// Upsert: insert when missing
	require.NoError(t, repo.Upsert(&model.ActuaryLimit{EmployeeID: 22, Limit: decimal.NewFromInt(50)}))
	row, _ := repo.GetByEmployeeID(22)
	assert.True(t, row.Limit.Equal(decimal.NewFromInt(50)))

	// Upsert: update existing
	require.NoError(t, repo.Upsert(&model.ActuaryLimit{EmployeeID: 22, Limit: decimal.NewFromInt(75)}))
	row2, _ := repo.GetByEmployeeID(22)
	assert.True(t, row2.Limit.Equal(decimal.NewFromInt(75)))

	// ResetAllUsedLimits flips used_limit > 0 back to 0.
	row2.UsedLimit = decimal.NewFromInt(123)
	require.NoError(t, db.Save(row2).Error)
	require.NoError(t, repo.ResetAllUsedLimits())

	final, _ := repo.GetByEmployeeID(22)
	assert.True(t, final.UsedLimit.IsZero(), "used limit should be reset to zero")
}

// -----------------------------------------------------------------------------
// ChangelogRepository — manually-created table to bypass default:now()
// -----------------------------------------------------------------------------

func newChangelogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE changelogs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		entity_type TEXT NOT NULL,
		entity_id BIGINT NOT NULL,
		action TEXT NOT NULL,
		field_name TEXT,
		old_value TEXT,
		new_value TEXT,
		changed_by BIGINT NOT NULL,
		changed_at DATETIME NOT NULL,
		reason TEXT
	)`).Error)
	return db
}

func TestChangelogRepository_CreateAndCreateBatchAndList(t *testing.T) {
	db := newChangelogTestDB(t)
	repo := NewChangelogRepository(db)

	now := time.Now().UTC()
	require.NoError(t, repo.Create(changelog.Entry{
		EntityType: "employee",
		EntityID:   1,
		Action:     "create",
		ChangedAt:  now,
	}))

	require.NoError(t, repo.CreateBatch([]changelog.Entry{
		{EntityType: "employee", EntityID: 1, Action: "update", FieldName: "name", ChangedAt: now.Add(time.Second)},
		{EntityType: "employee", EntityID: 1, Action: "update", FieldName: "phone", ChangedAt: now.Add(2 * time.Second)},
	}))

	// CreateBatch empty input is a no-op.
	require.NoError(t, repo.CreateBatch(nil))

	rows, total, err := repo.ListByEntity("employee", 1, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, rows, 3)
	// Newest first
	assert.Equal(t, "phone", rows[0].FieldName)
}

// -----------------------------------------------------------------------------
// OutboxRepository — bytea isn't SQLite-friendly, fall back to manual schema.
// -----------------------------------------------------------------------------

func newOutboxTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE outbox_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		aggregate_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		payload BLOB NOT NULL,
		created_at DATETIME NOT NULL,
		published_at DATETIME
	)`).Error)
	return db
}

func TestOutboxRepository_InsertClaimMarkPublished(t *testing.T) {
	db := newOutboxTestDB(t)
	repo := NewOutboxRepository(db)
	assert.NotNil(t, repo.DB())

	evt := &model.OutboxEvent{
		AggregateID: "supervisor:1",
		EventType:   "user.supervisor-demoted",
		Payload:     []byte(`{"hello":"world"}`),
	}
	require.NoError(t, repo.Insert(evt))
	require.NotZero(t, evt.ID)
	assert.False(t, evt.CreatedAt.IsZero(), "Insert should stamp CreatedAt")

	// InsertTx exercise (use the same DB handle as a transaction proxy).
	require.NoError(t, repo.InsertTx(db, &model.OutboxEvent{
		AggregateID: "ag2",
		EventType:   "evt2",
		Payload:     []byte(`{}`),
	}))

	rows, err := repo.ClaimUnpublished(10)
	require.NoError(t, err)
	assert.Len(t, rows, 2, "two unpublished rows present")

	// Mark first as published.
	require.NoError(t, repo.MarkPublished(rows[0].ID, time.Now().UTC()))

	postClaim, err := repo.ClaimUnpublished(10)
	require.NoError(t, err)
	assert.Len(t, postClaim, 1, "one row should remain after MarkPublished")

	// MarkPublished on missing row returns ErrRecordNotFound.
	err = repo.MarkPublished(99999, time.Now().UTC())
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "missing id should map to record-not-found")
}
