package repository_test

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

func newCommonDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Payment{},
		&model.Transfer{},
		&model.PaymentRecipient{},
		&model.TransferFee{},
		&model.SagaLog{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestPaymentRepository_CRUD(t *testing.T) {
	db := newCommonDB(t)
	r := repository.NewPaymentRepository(db)

	p := &model.Payment{
		IdempotencyKey:    "k1",
		ClientID:          1,
		FromAccountNumber: "111-1",
		ToAccountNumber:   "111-2",
		InitialAmount:     decimal.NewFromInt(100),
		FinalAmount:       decimal.NewFromInt(99),
		Status:            "pending",
		Timestamp:         time.Now(),
	}
	if err := r.Create(p); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := r.GetByID(p.ID)
	if err != nil || got.IdempotencyKey != "k1" {
		t.Fatalf("get: %v %+v", err, got)
	}
	idem, err := r.GetByIdempotencyKey("k1")
	if err != nil || idem.ID != p.ID {
		t.Fatalf("idempotency: %v %+v", err, idem)
	}
	if _, err := r.GetByIdempotencyKey("missing"); err == nil {
		t.Fatal("want err for missing")
	}
	if err := r.UpdateStatus(p.ID, "completed"); err != nil {
		t.Fatalf("update status: %v", err)
	}
	if err := r.UpdateStatusWithReason(p.ID, "failed", "reason"); err != nil {
		t.Fatalf("update status w/ reason: %v", err)
	}
	if err := r.UpdateStatus(99999, "x"); err == nil {
		t.Fatal("want err for missing id update")
	}
	if err := r.UpdateStatusWithReason(99999, "x", "y"); err == nil {
		t.Fatal("want err for missing id update reason")
	}

	// ListByAccount
	list, total, err := r.ListByAccount("111-1", "", "", "", 0, 0, 0, 0)
	if err != nil || total < 1 || len(list) < 1 {
		t.Fatalf("list: %v %d %d", err, total, len(list))
	}
	// With filters
	now := time.Now().UTC().Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	_, _, _ = r.ListByAccount("111-1", past, now, "failed", 1, 1000, 1, 5)
	// Bad date filters fall through silently
	_, _, _ = r.ListByAccount("111-1", "garbage", "garbage", "", 0, 0, 1, 5)

	// ListByAccountNumbers
	list2, total2, err := r.ListByAccountNumbers([]string{"111-1"}, 0, 0)
	if err != nil || total2 < 1 || len(list2) < 1 {
		t.Fatalf("list nums: %v %d %d", err, total2, len(list2))
	}
	if list3, total3, err := r.ListByAccountNumbers(nil, 1, 5); err != nil || total3 != 0 || list3 != nil {
		t.Fatalf("list empty: %v %d %v", err, total3, list3)
	}
}

func TestTransferRepository_CRUD(t *testing.T) {
	db := newCommonDB(t)
	r := repository.NewTransferRepository(db)

	tr := &model.Transfer{
		IdempotencyKey:    "tk1",
		ClientID:          1,
		FromAccountNumber: "a1",
		ToAccountNumber:   "a2",
		InitialAmount:     decimal.NewFromInt(50),
		FinalAmount:       decimal.NewFromInt(49),
		Status:            "pending",
		Timestamp:         time.Now(),
	}
	if err := r.Create(tr); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := r.GetByID(tr.ID)
	if err != nil || got.IdempotencyKey != "tk1" {
		t.Fatalf("get: %v %+v", err, got)
	}
	if _, err := r.GetByID(99999); err == nil {
		t.Fatal("want missing err")
	}
	if _, err := r.GetByIdempotencyKey("tk1"); err != nil {
		t.Fatalf("idem: %v", err)
	}
	if _, err := r.GetByIdempotencyKey("missing"); err == nil {
		t.Fatal("want missing idem err")
	}
	if err := r.UpdateStatus(tr.ID, "completed"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := r.UpdateStatusWithReason(tr.ID, "failed", "x"); err != nil {
		t.Fatalf("update reason: %v", err)
	}
	if err := r.UpdateStatus(99999, "x"); err == nil {
		t.Fatal("want missing update err")
	}
	if err := r.UpdateStatusWithReason(99999, "x", "y"); err == nil {
		t.Fatal("want missing update reason err")
	}
	list, total, err := r.ListByAccountNumbers([]string{"a1"}, 0, 0)
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("list: %v %d %d", err, total, len(list))
	}
	if list2, total2, err := r.ListByAccountNumbers(nil, 1, 5); err != nil || total2 != 0 || list2 != nil {
		t.Fatalf("list empty: %v %d %v", err, total2, list2)
	}
}

func TestPaymentRecipientRepository_CRUD(t *testing.T) {
	db := newCommonDB(t)
	r := repository.NewPaymentRecipientRepository(db)

	pr := &model.PaymentRecipient{ClientID: 1, RecipientName: "alice", AccountNumber: "111-1"}
	if err := r.Create(pr); err != nil {
		t.Fatalf("create: %v", err)
	}
	if pr.ID == 0 {
		t.Fatal("missing id")
	}
	list, err := r.ListByClient(1)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	if got, err := r.GetByID(pr.ID); err != nil || got.RecipientName != "alice" {
		t.Fatalf("get: %v %+v", err, got)
	}
	if _, err := r.GetByID(99999); err == nil {
		t.Fatal("want missing err")
	}
	pr.RecipientName = "alice2"
	if err := r.Update(pr); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := r.Delete(pr.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list2, _ := r.ListByClient(1)
	if len(list2) != 0 {
		t.Fatalf("after delete: %d", len(list2))
	}
}

func TestTransferFeeRepository_CRUDAndApplicable(t *testing.T) {
	db := newCommonDB(t)
	r := repository.NewTransferFeeRepository(db)

	feeA := &model.TransferFee{
		Name: "pct", FeeType: "percentage", FeeValue: decimal.NewFromFloat(0.001),
		MinAmount: decimal.NewFromInt(1000), TransactionType: "all",
		Active: true, CurrencyCode: "",
	}
	feeB := &model.TransferFee{
		Name: "fix", FeeType: "fixed", FeeValue: decimal.NewFromInt(50),
		TransactionType: "transfer", CurrencyCode: "RSD", Active: true,
	}
	feeC := &model.TransferFee{
		Name: "off", FeeType: "fixed", FeeValue: decimal.NewFromInt(99),
		TransactionType: "all", Active: false,
	}
	for _, f := range []*model.TransferFee{feeA, feeB, feeC} {
		if err := r.Create(f); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	if got, err := r.GetByID(feeA.ID); err != nil || got.Name != "pct" {
		t.Fatalf("get: %v %+v", err, got)
	}
	if _, err := r.GetByID(99999); err == nil {
		t.Fatal("want missing err")
	}
	if list, err := r.List(); err != nil || len(list) != 3 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	feeB.Name = "fix2"
	if err := r.Update(feeB); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := r.Deactivate(feeA.ID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	// Applicable: amount 5000, transfer, RSD → only feeB matches now (feeA deactivated, feeC inactive)
	app, err := r.GetApplicableFees(decimal.NewFromInt(5000), "transfer", "RSD")
	if err != nil {
		t.Fatalf("applicable: %v", err)
	}
	if len(app) == 0 {
		t.Fatal("expected at least one applicable fee")
	}
	// Below min amount: feeA's deactivated rule should be excluded; feeB (no min) still applies.
	_, err = r.GetApplicableFees(decimal.NewFromInt(100), "transfer", "RSD")
	if err != nil {
		t.Fatalf("applicable low: %v", err)
	}
}

func TestSagaLogRepository_AllPaths(t *testing.T) {
	db := newCommonDB(t)
	r := repository.NewSagaLogRepository(db)

	step := &model.SagaLog{
		SagaID:          "saga-1",
		TransactionID:   1,
		TransactionType: "transfer",
		StepNumber:      1,
		StepName:        "debit",
		Status:          "pending",
		AccountNumber:   "111-1",
		Amount:          decimal.NewFromInt(-100),
		CreatedAt:       time.Now().Add(-2 * time.Hour),
	}
	if err := r.RecordStep(step); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := r.CompleteStep(step.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := r.FailStep(step.ID, "boom"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if err := r.IncrementRetryCount(step.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	// Make a compensating step so FindPendingCompensations and ListStuckOlderThan return it
	comp := &model.SagaLog{
		SagaID: "saga-1", TransactionID: 1, TransactionType: "transfer",
		StepNumber: 2, StepName: "credit-comp", Status: "compensating",
		AccountNumber: "111-1", Amount: decimal.NewFromInt(100),
		IsCompensation: true, CreatedAt: time.Now().Add(-2 * time.Hour),
	}
	if err := r.RecordStep(comp); err != nil {
		t.Fatalf("record comp: %v", err)
	}
	if pend, err := r.FindPendingCompensations(); err != nil || len(pend) != 1 {
		t.Fatalf("pending: %v %d", err, len(pend))
	}
	if err := r.MarkDeadLetter(step.ID, "give up"); err != nil {
		t.Fatalf("dead letter: %v", err)
	}
	if err := r.SetErrorMessage(comp.ID, "still bad"); err != nil {
		t.Fatalf("set err: %v", err)
	}
	if logs, err := r.GetBySagaID("saga-1"); err != nil || len(logs) != 2 {
		t.Fatalf("by saga: %v %d", err, len(logs))
	}
	// Mark one completed forward and check IsForwardCompleted
	completed := &model.SagaLog{
		SagaID: "saga-2", TransactionID: 2, TransactionType: "payment",
		StepNumber: 1, StepName: "debit", Status: "completed",
		AccountNumber: "111-1", Amount: decimal.NewFromInt(-1),
		IsCompensation: false, CreatedAt: time.Now(),
	}
	if err := r.RecordStep(completed); err != nil {
		t.Fatalf("record completed: %v", err)
	}
	ok, err := r.IsForwardCompleted("saga-2", "debit")
	if err != nil || !ok {
		t.Fatalf("forward completed: %v %v", err, ok)
	}
	ok, err = r.IsForwardCompleted("saga-2", "missing")
	if err != nil || ok {
		t.Fatalf("forward not completed: %v %v", err, ok)
	}
	stuck, err := r.ListStuckOlderThan(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("stuck: %v", err)
	}
	_ = stuck
}

func TestPaymentTransferBeforeUpdateHooks(t *testing.T) {
	db := newCommonDB(t)
	// Direct invocation of the hook covers the model.go branches.
	p := &model.Payment{Version: 5}
	if err := p.BeforeUpdate(db); err != nil {
		t.Fatalf("payment hook: %v", err)
	}
	if p.Version != 6 {
		t.Fatalf("payment version not bumped: %d", p.Version)
	}
	tr := &model.Transfer{Version: 5}
	if err := tr.BeforeUpdate(db); err != nil {
		t.Fatalf("transfer hook: %v", err)
	}
	if tr.Version != 6 {
		t.Fatalf("transfer version not bumped: %d", tr.Version)
	}
}
