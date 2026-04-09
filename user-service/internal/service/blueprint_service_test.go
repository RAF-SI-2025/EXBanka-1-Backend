package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/exbanka/user-service/internal/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// --- Mock LimitBlueprintRepo ---

type mockBlueprintRepo struct {
	blueprints map[uint64]*model.LimitBlueprint
	byNameType map[string]*model.LimitBlueprint // key: "name|type"
	nextID     uint64
}

func newMockBlueprintRepo() *mockBlueprintRepo {
	return &mockBlueprintRepo{
		blueprints: make(map[uint64]*model.LimitBlueprint),
		byNameType: make(map[string]*model.LimitBlueprint),
		nextID:     1,
	}
}

func (m *mockBlueprintRepo) Create(bp *model.LimitBlueprint) error {
	key := bp.Name + "|" + bp.Type
	if _, exists := m.byNameType[key]; exists {
		return gorm.ErrDuplicatedKey
	}
	bp.ID = m.nextID
	m.nextID++
	copy := *bp
	m.blueprints[copy.ID] = &copy
	m.byNameType[key] = m.blueprints[copy.ID]
	return nil
}

func (m *mockBlueprintRepo) GetByID(id uint64) (*model.LimitBlueprint, error) {
	if bp, ok := m.blueprints[id]; ok {
		return bp, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockBlueprintRepo) List(bpType string) ([]model.LimitBlueprint, error) {
	var result []model.LimitBlueprint
	for _, bp := range m.blueprints {
		if bpType == "" || bp.Type == bpType {
			result = append(result, *bp)
		}
	}
	return result, nil
}

func (m *mockBlueprintRepo) GetByNameAndType(name, bpType string) (*model.LimitBlueprint, error) {
	key := name + "|" + bpType
	if bp, ok := m.byNameType[key]; ok {
		return bp, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockBlueprintRepo) Update(bp *model.LimitBlueprint) error {
	if _, ok := m.blueprints[bp.ID]; !ok {
		return gorm.ErrRecordNotFound
	}
	copy := *bp
	m.blueprints[copy.ID] = &copy
	key := copy.Name + "|" + copy.Type
	m.byNameType[key] = m.blueprints[copy.ID]
	return nil
}

func (m *mockBlueprintRepo) Delete(id uint64) error {
	bp, ok := m.blueprints[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	key := bp.Name + "|" + bp.Type
	delete(m.blueprints, id)
	delete(m.byNameType, key)
	return nil
}

// --- Mock ClientLimitClient ---

type mockClientLimitClient struct {
	calls []clientLimitCall
	err   error
}

type clientLimitCall struct {
	ClientID      int64
	DailyLimit    string
	MonthlyLimit  string
	TransferLimit string
	SetByEmployee int64
}

func (m *mockClientLimitClient) SetClientLimits(ctx context.Context, clientID int64, dailyLimit, monthlyLimit, transferLimit string, setByEmployee int64) error {
	m.calls = append(m.calls, clientLimitCall{
		ClientID:      clientID,
		DailyLimit:    dailyLimit,
		MonthlyLimit:  monthlyLimit,
		TransferLimit: transferLimit,
		SetByEmployee: setByEmployee,
	})
	return m.err
}

// --- Mock ActuaryRepo with Upsert ---

type mockActuaryRepoWithUpsert struct {
	mockActuaryRepo
	upserted []*model.ActuaryLimit
}

func newMockActuaryRepoWithUpsert() *mockActuaryRepoWithUpsert {
	return &mockActuaryRepoWithUpsert{
		mockActuaryRepo: *newMockActuaryRepo(),
	}
}

func (m *mockActuaryRepoWithUpsert) Upsert(limit *model.ActuaryLimit) error {
	copy := *limit
	m.upserted = append(m.upserted, &copy)
	m.byEmpID[limit.EmployeeID] = &copy
	return nil
}

// --- Helper to build JSON ---

func mustMarshal(v interface{}) datatypes.JSON {
	data, _ := json.Marshal(v)
	return datatypes.JSON(data)
}

// --- Tests ---

func TestCreateBlueprint_Employee(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name:        "BasicTeller",
		Description: "Default teller limits",
		Type:        model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: "50000",
			MaxSingleTransaction:  "100000",
			MaxDailyTransaction:   "500000",
			MaxClientDailyLimit:   "250000",
			MaxClientMonthlyLimit: "2500000",
		}),
	}
	result, err := svc.CreateBlueprint(context.Background(), bp)
	require.NoError(t, err)
	assert.Equal(t, "BasicTeller", result.Name)
	assert.Equal(t, model.BlueprintTypeEmployee, result.Type)
	assert.NotZero(t, result.ID)
}

func TestCreateBlueprint_InvalidType(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name:   "Bad",
		Type:   "invalid",
		Values: mustMarshal(map[string]string{}),
	}
	_, err := svc.CreateBlueprint(context.Background(), bp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid blueprint type")
}

func TestCreateBlueprint_InvalidValues(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name:   "Bad",
		Type:   model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{}), // all empty
	}
	_, err := svc.CreateBlueprint(context.Background(), bp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is required")
}

func TestListBlueprints_FilterByType(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	// Create one of each type
	empBp := model.LimitBlueprint{
		Name: "EmpBP", Type: model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: "1", MaxSingleTransaction: "1",
			MaxDailyTransaction: "1", MaxClientDailyLimit: "1", MaxClientMonthlyLimit: "1",
		}),
	}
	actBp := model.LimitBlueprint{
		Name: "ActBP", Type: model.BlueprintTypeActuary,
		Values: mustMarshal(model.ActuaryBlueprintValues{Limit: "1000", NeedApproval: true}),
	}
	_, _ = svc.CreateBlueprint(context.Background(), empBp)
	_, _ = svc.CreateBlueprint(context.Background(), actBp)

	// List all
	all, err := svc.ListBlueprints("")
	require.NoError(t, err)
	assert.Len(t, all, 2)

	// Filter employee
	empOnly, err := svc.ListBlueprints(model.BlueprintTypeEmployee)
	require.NoError(t, err)
	assert.Len(t, empOnly, 1)
	assert.Equal(t, "EmpBP", empOnly[0].Name)
}

func TestApplyBlueprint_Employee(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	limitRepo := newMockEmployeeLimitRepo()
	svc := NewBlueprintService(bpRepo, limitRepo, nil, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name: "TestEmp", Type: model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: "50000", MaxSingleTransaction: "100000",
			MaxDailyTransaction: "500000", MaxClientDailyLimit: "250000",
			MaxClientMonthlyLimit: "2500000",
		}),
	}
	created, _ := svc.CreateBlueprint(context.Background(), bp)

	err := svc.ApplyBlueprint(context.Background(), created.ID, 42, 1)
	require.NoError(t, err)

	// Verify the limit was upserted
	limit, _ := limitRepo.GetByEmployeeID(42)
	assert.True(t, limit.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(50000)))
	assert.True(t, limit.MaxClientMonthlyLimit.Equal(decimal.NewFromInt(2500000)))
}

func TestApplyBlueprint_Actuary(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	actuaryRepo := newMockActuaryRepoWithUpsert()
	svc := NewBlueprintService(bpRepo, nil, actuaryRepo, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name: "TestAct", Type: model.BlueprintTypeActuary,
		Values: mustMarshal(model.ActuaryBlueprintValues{Limit: "1000000", NeedApproval: true}),
	}
	created, _ := svc.CreateBlueprint(context.Background(), bp)

	err := svc.ApplyBlueprint(context.Background(), created.ID, 55, 1)
	require.NoError(t, err)
	require.Len(t, actuaryRepo.upserted, 1)
	assert.Equal(t, int64(55), actuaryRepo.upserted[0].EmployeeID)
	assert.True(t, actuaryRepo.upserted[0].Limit.Equal(decimal.NewFromInt(1000000)))
	assert.True(t, actuaryRepo.upserted[0].NeedApproval)
}

func TestApplyBlueprint_Client(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	clientClient := &mockClientLimitClient{}
	svc := NewBlueprintService(bpRepo, nil, nil, clientClient, nil, nil)

	bp := model.LimitBlueprint{
		Name: "TestClient", Type: model.BlueprintTypeClient,
		Values: mustMarshal(model.ClientBlueprintValues{
			DailyLimit: "100000", MonthlyLimit: "1000000", TransferLimit: "500000",
		}),
	}
	created, _ := svc.CreateBlueprint(context.Background(), bp)

	err := svc.ApplyBlueprint(context.Background(), created.ID, 99, 7)
	require.NoError(t, err)
	require.Len(t, clientClient.calls, 1)
	assert.Equal(t, int64(99), clientClient.calls[0].ClientID)
	assert.Equal(t, "100000", clientClient.calls[0].DailyLimit)
	assert.Equal(t, int64(7), clientClient.calls[0].SetByEmployee)
}

func TestApplyBlueprint_NotFound(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	err := svc.ApplyBlueprint(context.Background(), 999, 1, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDeleteBlueprint(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name: "ToDelete", Type: model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: "1", MaxSingleTransaction: "1",
			MaxDailyTransaction: "1", MaxClientDailyLimit: "1", MaxClientMonthlyLimit: "1",
		}),
	}
	created, _ := svc.CreateBlueprint(context.Background(), bp)

	err := svc.DeleteBlueprint(context.Background(), created.ID)
	require.NoError(t, err)

	_, err = svc.GetBlueprint(created.ID)
	assert.Error(t, err)
}

func TestSeedFromTemplates(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	templates := []model.LimitTemplate{
		{
			Name:                  "BasicTeller",
			Description:           "Default teller limits",
			MaxLoanApprovalAmount: decimal.NewFromInt(50000),
			MaxSingleTransaction:  decimal.NewFromInt(100000),
			MaxDailyTransaction:   decimal.NewFromInt(500000),
			MaxClientDailyLimit:   decimal.NewFromInt(250000),
			MaxClientMonthlyLimit: decimal.NewFromInt(2500000),
		},
	}

	err := svc.SeedFromTemplates(templates)
	require.NoError(t, err)

	bps, _ := svc.ListBlueprints(model.BlueprintTypeEmployee)
	assert.Len(t, bps, 1)
	assert.Equal(t, "BasicTeller", bps[0].Name)

	// Idempotent
	err = svc.SeedFromTemplates(templates)
	require.NoError(t, err)

	bps2, _ := svc.ListBlueprints(model.BlueprintTypeEmployee)
	assert.Len(t, bps2, 1, "should not duplicate on re-seed")
}

func TestApplyBlueprint_Employee_SetsLimits(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	limitRepo := newMockEmployeeLimitRepo()
	svc := NewBlueprintService(bpRepo, limitRepo, nil, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name: "SetLimitsEmp", Type: model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: "10000",
			MaxSingleTransaction:  "20000",
			MaxDailyTransaction:   "30000",
			MaxClientDailyLimit:   "40000",
			MaxClientMonthlyLimit: "50000",
		}),
	}
	created, err := svc.CreateBlueprint(context.Background(), bp)
	require.NoError(t, err)

	err = svc.ApplyBlueprint(context.Background(), created.ID, 11, 1)
	require.NoError(t, err)

	limit, err := limitRepo.GetByEmployeeID(11)
	require.NoError(t, err)
	assert.True(t, limit.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(10000)))
	assert.True(t, limit.MaxSingleTransaction.Equal(decimal.NewFromInt(20000)))
	assert.True(t, limit.MaxDailyTransaction.Equal(decimal.NewFromInt(30000)))
	assert.True(t, limit.MaxClientDailyLimit.Equal(decimal.NewFromInt(40000)))
	assert.True(t, limit.MaxClientMonthlyLimit.Equal(decimal.NewFromInt(50000)))
}

func TestApplyBlueprint_Actuary_SetsLimits(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	actuaryRepo := newMockActuaryRepoWithUpsert()
	svc := NewBlueprintService(bpRepo, nil, actuaryRepo, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name: "SetLimitsAct", Type: model.BlueprintTypeActuary,
		Values: mustMarshal(model.ActuaryBlueprintValues{Limit: "75000", NeedApproval: false}),
	}
	created, err := svc.CreateBlueprint(context.Background(), bp)
	require.NoError(t, err)

	err = svc.ApplyBlueprint(context.Background(), created.ID, 22, 1)
	require.NoError(t, err)

	require.Len(t, actuaryRepo.upserted, 1)
	assert.Equal(t, int64(22), actuaryRepo.upserted[0].EmployeeID)
	assert.True(t, actuaryRepo.upserted[0].Limit.Equal(decimal.NewFromInt(75000)))
	assert.False(t, actuaryRepo.upserted[0].NeedApproval)
}

func TestApplyBlueprint_Client_CallsGRPC(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	clientClient := &mockClientLimitClient{}
	svc := NewBlueprintService(bpRepo, nil, nil, clientClient, nil, nil)

	bp := model.LimitBlueprint{
		Name: "ClientGRPCBP", Type: model.BlueprintTypeClient,
		Values: mustMarshal(model.ClientBlueprintValues{
			DailyLimit: "200000", MonthlyLimit: "2000000", TransferLimit: "600000",
		}),
	}
	created, err := svc.CreateBlueprint(context.Background(), bp)
	require.NoError(t, err)

	err = svc.ApplyBlueprint(context.Background(), created.ID, 33, 5)
	require.NoError(t, err)

	require.Len(t, clientClient.calls, 1)
	call := clientClient.calls[0]
	assert.Equal(t, int64(33), call.ClientID)
	assert.Equal(t, "200000", call.DailyLimit)
	assert.Equal(t, "2000000", call.MonthlyLimit)
	assert.Equal(t, "600000", call.TransferLimit)
	assert.Equal(t, int64(5), call.SetByEmployee)
}

func TestApplyBlueprint_Employee_UpsertOverwrites(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	limitRepo := newMockEmployeeLimitRepo()
	svc := NewBlueprintService(bpRepo, limitRepo, nil, nil, nil, nil)

	// First blueprint
	bp1 := model.LimitBlueprint{
		Name: "First", Type: model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: "1000", MaxSingleTransaction: "2000",
			MaxDailyTransaction: "3000", MaxClientDailyLimit: "4000",
			MaxClientMonthlyLimit: "5000",
		}),
	}
	created1, err := svc.CreateBlueprint(context.Background(), bp1)
	require.NoError(t, err)

	// Second blueprint with higher values
	bp2 := model.LimitBlueprint{
		Name: "Second", Type: model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: "9000", MaxSingleTransaction: "8000",
			MaxDailyTransaction: "7000", MaxClientDailyLimit: "6000",
			MaxClientMonthlyLimit: "50000",
		}),
	}
	created2, err := svc.CreateBlueprint(context.Background(), bp2)
	require.NoError(t, err)

	// Apply first blueprint to employee 77
	err = svc.ApplyBlueprint(context.Background(), created1.ID, 77, 1)
	require.NoError(t, err)
	limit, _ := limitRepo.GetByEmployeeID(77)
	assert.True(t, limit.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(1000)))

	// Apply second blueprint to the same employee — values must be overwritten
	err = svc.ApplyBlueprint(context.Background(), created2.ID, 77, 1)
	require.NoError(t, err)
	limit, _ = limitRepo.GetByEmployeeID(77)
	assert.True(t, limit.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(9000)), "MaxLoanApprovalAmount should be overwritten")
	assert.True(t, limit.MaxSingleTransaction.Equal(decimal.NewFromInt(8000)), "MaxSingleTransaction should be overwritten")
	assert.True(t, limit.MaxClientMonthlyLimit.Equal(decimal.NewFromInt(50000)), "MaxClientMonthlyLimit should be overwritten")
}

func TestCreateBlueprint_InvalidValues_MissingField(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	// Employee blueprint missing max_single_transaction
	bp := model.LimitBlueprint{
		Name: "MissingField",
		Type: model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: "50000",
			// MaxSingleTransaction intentionally omitted
			MaxDailyTransaction:   "500000",
			MaxClientDailyLimit:   "250000",
			MaxClientMonthlyLimit: "2500000",
		}),
	}
	_, err := svc.CreateBlueprint(context.Background(), bp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max_single_transaction")
	assert.Contains(t, err.Error(), "required")
}

func TestCreateBlueprint_InvalidValues_NonDecimal(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	// Employee blueprint with non-decimal value "abc"
	bp := model.LimitBlueprint{
		Name: "NonDecimal",
		Type: model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: "abc",
			MaxSingleTransaction:  "100000",
			MaxDailyTransaction:   "500000",
			MaxClientDailyLimit:   "250000",
			MaxClientMonthlyLimit: "2500000",
		}),
	}
	_, err := svc.CreateBlueprint(context.Background(), bp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max_loan_approval_amount")
}

func TestUpdateBlueprint(t *testing.T) {
	bpRepo := newMockBlueprintRepo()
	svc := NewBlueprintService(bpRepo, nil, nil, nil, nil, nil)

	bp := model.LimitBlueprint{
		Name: "OldName", Type: model.BlueprintTypeActuary,
		Values: mustMarshal(model.ActuaryBlueprintValues{Limit: "1000", NeedApproval: false}),
	}
	created, _ := svc.CreateBlueprint(context.Background(), bp)

	newValues := json.RawMessage(mustMarshal(model.ActuaryBlueprintValues{Limit: "5000", NeedApproval: true}))
	updated, err := svc.UpdateBlueprint(context.Background(), created.ID, "NewName", "new desc", newValues)
	require.NoError(t, err)
	assert.Equal(t, "NewName", updated.Name)
	assert.Equal(t, "new desc", updated.Description)

	// Verify values changed
	var vals model.ActuaryBlueprintValues
	_ = json.Unmarshal(updated.Values, &vals)
	assert.Equal(t, "5000", vals.Limit)
	assert.True(t, vals.NeedApproval)
}
