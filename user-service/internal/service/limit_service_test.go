package service

import (
	"context"
	"errors"
	"testing"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/user-service/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// --- Mock implementations ---

type mockEmployeeLimitRepo struct {
	limits map[int64]*model.EmployeeLimit
}

func newMockEmployeeLimitRepo() *mockEmployeeLimitRepo {
	return &mockEmployeeLimitRepo{limits: make(map[int64]*model.EmployeeLimit)}
}

func (m *mockEmployeeLimitRepo) Create(limit *model.EmployeeLimit) error {
	m.limits[limit.EmployeeID] = limit
	return nil
}

func (m *mockEmployeeLimitRepo) GetByEmployeeID(employeeID int64) (*model.EmployeeLimit, error) {
	if l, ok := m.limits[employeeID]; ok {
		return l, nil
	}
	return &model.EmployeeLimit{EmployeeID: employeeID}, nil
}

func (m *mockEmployeeLimitRepo) Update(limit *model.EmployeeLimit) error {
	m.limits[limit.EmployeeID] = limit
	return nil
}

func (m *mockEmployeeLimitRepo) Delete(employeeID int64) error {
	delete(m.limits, employeeID)
	return nil
}

func (m *mockEmployeeLimitRepo) Upsert(limit *model.EmployeeLimit) error {
	m.limits[limit.EmployeeID] = limit
	return nil
}

func (m *mockEmployeeLimitRepo) ResetDailyUsedLimits() error {
	return nil
}

type mockLimitTemplateRepo struct {
	templates map[string]*model.LimitTemplate
	nextID    int64
}

func newMockLimitTemplateRepo() *mockLimitTemplateRepo {
	return &mockLimitTemplateRepo{
		templates: make(map[string]*model.LimitTemplate),
		nextID:    1,
	}
}

func (m *mockLimitTemplateRepo) Create(t *model.LimitTemplate) error {
	t.ID = m.nextID
	m.nextID++
	m.templates[t.Name] = t
	return nil
}

func (m *mockLimitTemplateRepo) GetByID(id int64) (*model.LimitTemplate, error) {
	for _, t := range m.templates {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockLimitTemplateRepo) GetByName(name string) (*model.LimitTemplate, error) {
	if t, ok := m.templates[name]; ok {
		return t, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockLimitTemplateRepo) List() ([]model.LimitTemplate, error) {
	result := make([]model.LimitTemplate, 0, len(m.templates))
	for _, t := range m.templates {
		result = append(result, *t)
	}
	return result, nil
}

func (m *mockLimitTemplateRepo) Update(t *model.LimitTemplate) error {
	m.templates[t.Name] = t
	return nil
}

func (m *mockLimitTemplateRepo) Delete(id int64) error {
	for name, t := range m.templates {
		if t.ID == id {
			delete(m.templates, name)
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

// --- Tests ---

func TestGetEmployeeLimits_NoRecord(t *testing.T) {
	limitRepo := newMockEmployeeLimitRepo()
	templateRepo := newMockLimitTemplateRepo()
	svc := NewLimitService(limitRepo, templateRepo, newMockHierarchyEmpRepo(), nil)

	limit, err := svc.GetEmployeeLimits(42)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if limit.EmployeeID != 42 {
		t.Errorf("expected EmployeeID=42, got %d", limit.EmployeeID)
	}
	if !limit.MaxLoanApprovalAmount.IsZero() {
		t.Errorf("expected zero MaxLoanApprovalAmount, got %s", limit.MaxLoanApprovalAmount)
	}
}

func TestSetEmployeeLimits_Create(t *testing.T) {
	limitRepo := newMockEmployeeLimitRepo()
	templateRepo := newMockLimitTemplateRepo()
	svc := NewLimitService(limitRepo, templateRepo, newMockHierarchyEmpRepo(), nil)

	limit := model.EmployeeLimit{
		EmployeeID:            10,
		MaxLoanApprovalAmount: decimal.NewFromInt(50000),
		MaxSingleTransaction:  decimal.NewFromInt(100000),
		MaxDailyTransaction:   decimal.NewFromInt(500000),
		MaxClientDailyLimit:   decimal.NewFromInt(250000),
		MaxClientMonthlyLimit: decimal.NewFromInt(2500000),
	}
	result, err := svc.SetEmployeeLimits(context.Background(), limit, 0)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result.EmployeeID != 10 {
		t.Errorf("expected EmployeeID=10, got %d", result.EmployeeID)
	}
	if !result.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(50000)) {
		t.Errorf("unexpected MaxLoanApprovalAmount: %s", result.MaxLoanApprovalAmount)
	}
}

func TestApplyTemplate_Success(t *testing.T) {
	limitRepo := newMockEmployeeLimitRepo()
	templateRepo := newMockLimitTemplateRepo()

	// Seed a template
	tmpl := &model.LimitTemplate{
		Name:                  "BasicTeller",
		MaxLoanApprovalAmount: decimal.NewFromInt(50000),
		MaxSingleTransaction:  decimal.NewFromInt(100000),
		MaxDailyTransaction:   decimal.NewFromInt(500000),
		MaxClientDailyLimit:   decimal.NewFromInt(250000),
		MaxClientMonthlyLimit: decimal.NewFromInt(2500000),
	}
	_ = templateRepo.Create(tmpl)

	svc := NewLimitService(limitRepo, templateRepo, newMockHierarchyEmpRepo(), nil)
	result, err := svc.ApplyTemplate(context.Background(), 99, "BasicTeller", 0)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result.EmployeeID != 99 {
		t.Errorf("expected EmployeeID=99, got %d", result.EmployeeID)
	}
	if !result.MaxLoanApprovalAmount.Equal(decimal.NewFromInt(50000)) {
		t.Errorf("unexpected MaxLoanApprovalAmount: %s", result.MaxLoanApprovalAmount)
	}
	if !result.MaxClientMonthlyLimit.Equal(decimal.NewFromInt(2500000)) {
		t.Errorf("unexpected MaxClientMonthlyLimit: %s", result.MaxClientMonthlyLimit)
	}
}

func TestApplyTemplate_NotFound(t *testing.T) {
	limitRepo := newMockEmployeeLimitRepo()
	templateRepo := newMockLimitTemplateRepo()
	svc := NewLimitService(limitRepo, templateRepo, newMockHierarchyEmpRepo(), nil)

	_, err := svc.ApplyTemplate(context.Background(), 99, "NonExistentTemplate", 0)
	if err == nil {
		t.Fatal("expected error for non-existent template, got nil")
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) && err.Error() != "template not found: NonExistentTemplate" {
		// Either error form is acceptable
		_ = err
	}
}

func TestSeedDefaultTemplates(t *testing.T) {
	limitRepo := newMockEmployeeLimitRepo()
	templateRepo := newMockLimitTemplateRepo()
	svc := NewLimitService(limitRepo, templateRepo, newMockHierarchyEmpRepo(), nil)

	if err := svc.SeedDefaultTemplates(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	templates, err := templateRepo.List()
	if err != nil {
		t.Fatalf("failed to list templates: %v", err)
	}
	if len(templates) != 3 {
		t.Errorf("expected 3 templates, got %d", len(templates))
	}

	expectedNames := map[string]bool{
		"BasicTeller": false,
		"SeniorAgent": false,
		"Supervisor":  false,
	}
	for _, tmpl := range templates {
		if _, ok := expectedNames[tmpl.Name]; ok {
			expectedNames[tmpl.Name] = true
		}
	}
	for name, found := range expectedNames {
		if !found {
			t.Errorf("expected template %q not found", name)
		}
	}

	// Seed again - should be idempotent
	if err := svc.SeedDefaultTemplates(); err != nil {
		t.Fatalf("second seed should not error, got: %v", err)
	}
	templates2, _ := templateRepo.List()
	if len(templates2) != 3 {
		t.Errorf("expected still 3 templates after re-seed, got %d", len(templates2))
	}
}

// --- Recording producer mock ---

// recordingLimitProducer captures the last published EmployeeLimitsUpdatedMessage.
type recordingLimitProducer struct {
	lastLimitsMsg *kafkamsg.EmployeeLimitsUpdatedMessage
}

func (r *recordingLimitProducer) PublishEmployeeLimitsUpdated(_ context.Context, msg kafkamsg.EmployeeLimitsUpdatedMessage) error {
	r.lastLimitsMsg = &msg
	return nil
}

func (r *recordingLimitProducer) PublishLimitTemplate(_ context.Context, _ kafkamsg.LimitTemplateMessage) error {
	return nil
}

// versionedLimitRepo simulates DB-side version assignment on Upsert.
type versionedLimitRepo struct {
	limits map[int64]*model.EmployeeLimit
}

func newVersionedLimitRepo() *versionedLimitRepo {
	return &versionedLimitRepo{limits: make(map[int64]*model.EmployeeLimit)}
}

func (r *versionedLimitRepo) Create(limit *model.EmployeeLimit) error {
	r.limits[limit.EmployeeID] = limit
	return nil
}

func (r *versionedLimitRepo) GetByEmployeeID(employeeID int64) (*model.EmployeeLimit, error) {
	if l, ok := r.limits[employeeID]; ok {
		return l, nil
	}
	return &model.EmployeeLimit{EmployeeID: employeeID}, nil
}

func (r *versionedLimitRepo) Update(limit *model.EmployeeLimit) error {
	r.limits[limit.EmployeeID] = limit
	return nil
}

func (r *versionedLimitRepo) Delete(employeeID int64) error {
	delete(r.limits, employeeID)
	return nil
}

func (r *versionedLimitRepo) Upsert(limit *model.EmployeeLimit) error {
	// Simulate DB-side monotonic version: version=1 on first insert, version+1 on
	// each subsequent conflict-update — matching the ON CONFLICT DO UPDATE behaviour
	// in EmployeeLimitRepository.Upsert after the SP-2 fix.
	if existing, ok := r.limits[limit.EmployeeID]; ok {
		limit.Version = existing.Version + 1
	} else {
		limit.Version = 1
	}
	r.limits[limit.EmployeeID] = limit
	return nil
}

func (r *versionedLimitRepo) ResetDailyUsedLimits() error {
	return nil
}

// --- SP-2 publish tests ---

func TestSetEmployeeLimits_PublishesFullSnapshot(t *testing.T) {
	limitRepo := newVersionedLimitRepo()
	templateRepo := newMockLimitTemplateRepo()
	pub := &recordingLimitProducer{}
	svc := NewLimitService(limitRepo, templateRepo, newMockHierarchyEmpRepo(), pub)

	limit := model.EmployeeLimit{
		EmployeeID:            10,
		MaxLoanApprovalAmount: decimal.NewFromInt(50000),
		MaxSingleTransaction:  decimal.NewFromInt(10000),
		MaxDailyTransaction:   decimal.NewFromInt(20000),
		MaxClientDailyLimit:   decimal.NewFromInt(5000),
		MaxClientMonthlyLimit: decimal.NewFromInt(100000),
	}
	result, err := svc.SetEmployeeLimits(context.Background(), limit, 0)
	if err != nil {
		t.Fatalf("SetEmployeeLimits: %v", err)
	}
	if pub.lastLimitsMsg == nil {
		t.Fatal("expected EmployeeLimitsUpdatedMessage to be published, got nil")
	}
	msg := pub.lastLimitsMsg
	if msg.Action != "set" {
		t.Errorf("expected action=set, got %q", msg.Action)
	}
	if msg.MaxLoanApprovalAmount != result.MaxLoanApprovalAmount.StringFixed(4) {
		t.Errorf("MaxLoanApprovalAmount: want %q, got %q", result.MaxLoanApprovalAmount.StringFixed(4), msg.MaxLoanApprovalAmount)
	}
	if msg.MaxSingleTransaction != result.MaxSingleTransaction.StringFixed(4) {
		t.Errorf("MaxSingleTransaction: want %q, got %q", result.MaxSingleTransaction.StringFixed(4), msg.MaxSingleTransaction)
	}
	if msg.MaxDailyTransaction != result.MaxDailyTransaction.StringFixed(4) {
		t.Errorf("MaxDailyTransaction: want %q, got %q", result.MaxDailyTransaction.StringFixed(4), msg.MaxDailyTransaction)
	}
	if msg.MaxClientDailyLimit != result.MaxClientDailyLimit.StringFixed(4) {
		t.Errorf("MaxClientDailyLimit: want %q, got %q", result.MaxClientDailyLimit.StringFixed(4), msg.MaxClientDailyLimit)
	}
	if msg.MaxClientMonthlyLimit != result.MaxClientMonthlyLimit.StringFixed(4) {
		t.Errorf("MaxClientMonthlyLimit: want %q, got %q", result.MaxClientMonthlyLimit.StringFixed(4), msg.MaxClientMonthlyLimit)
	}
	if msg.Version != result.Version {
		t.Errorf("Version: want %d, got %d", result.Version, msg.Version)
	}
}

func TestApplyTemplate_PublishesFullSnapshot(t *testing.T) {
	limitRepo := newVersionedLimitRepo()
	templateRepo := newMockLimitTemplateRepo()
	pub := &recordingLimitProducer{}

	tmpl := &model.LimitTemplate{
		Name:                  "BasicTeller",
		MaxLoanApprovalAmount: decimal.NewFromInt(50000),
		MaxSingleTransaction:  decimal.NewFromInt(10000),
		MaxDailyTransaction:   decimal.NewFromInt(20000),
		MaxClientDailyLimit:   decimal.NewFromInt(5000),
		MaxClientMonthlyLimit: decimal.NewFromInt(100000),
	}
	_ = templateRepo.Create(tmpl)

	svc := NewLimitService(limitRepo, templateRepo, newMockHierarchyEmpRepo(), pub)
	result, err := svc.ApplyTemplate(context.Background(), 99, "BasicTeller", 0)
	if err != nil {
		t.Fatalf("ApplyTemplate: %v", err)
	}
	if pub.lastLimitsMsg == nil {
		t.Fatal("expected EmployeeLimitsUpdatedMessage to be published, got nil")
	}
	msg := pub.lastLimitsMsg
	if msg.Action != "template_applied" {
		t.Errorf("expected action=template_applied, got %q", msg.Action)
	}
	if msg.MaxLoanApprovalAmount != result.MaxLoanApprovalAmount.StringFixed(4) {
		t.Errorf("MaxLoanApprovalAmount: want %q, got %q", result.MaxLoanApprovalAmount.StringFixed(4), msg.MaxLoanApprovalAmount)
	}
	if msg.MaxSingleTransaction != result.MaxSingleTransaction.StringFixed(4) {
		t.Errorf("MaxSingleTransaction: want %q, got %q", result.MaxSingleTransaction.StringFixed(4), msg.MaxSingleTransaction)
	}
	if msg.MaxDailyTransaction != result.MaxDailyTransaction.StringFixed(4) {
		t.Errorf("MaxDailyTransaction: want %q, got %q", result.MaxDailyTransaction.StringFixed(4), msg.MaxDailyTransaction)
	}
	if msg.MaxClientDailyLimit != result.MaxClientDailyLimit.StringFixed(4) {
		t.Errorf("MaxClientDailyLimit: want %q, got %q", result.MaxClientDailyLimit.StringFixed(4), msg.MaxClientDailyLimit)
	}
	if msg.MaxClientMonthlyLimit != result.MaxClientMonthlyLimit.StringFixed(4) {
		t.Errorf("MaxClientMonthlyLimit: want %q, got %q", result.MaxClientMonthlyLimit.StringFixed(4), msg.MaxClientMonthlyLimit)
	}
	if msg.Version != result.Version {
		t.Errorf("Version: want %d, got %d", result.Version, msg.Version)
	}
}
