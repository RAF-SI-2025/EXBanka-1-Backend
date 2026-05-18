package saga_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared"
	sharedsaga "github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/exbanka/transaction-service/internal/saga"
)

// --- stubs ---

type stubAcct struct {
	accountpb.AccountServiceClient
	updateErr        error
	calls            int
	lastIdempotencyK string
}

func (s *stubAcct) UpdateBalance(ctx context.Context, in *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	s.calls++
	s.lastIdempotencyK = in.IdempotencyKey
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	return &accountpb.AccountResponse{}, nil
}

type stubDLP struct {
	calls      int
	publishErr error
}

func (s *stubDLP) PublishSagaDeadLetter(ctx context.Context, msg kafkamsg.SagaDeadLetterMessage) error {
	s.calls++
	return s.publishErr
}

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.SagaLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// --- Classifier ---

func TestClassifier_Classify_NilClient(t *testing.T) {
	c := saga.NewClassifier(nil, shared.RetryConfig{}, nil, nil)
	got := c.Classify(context.Background(), sharedsaga.StuckStep{})
	if got != sharedsaga.ActionLeave {
		t.Fatalf("nil client: want Leave, got %v", got)
	}
}

func TestClassifier_Classify_NoAccount(t *testing.T) {
	c := saga.NewClassifier(&stubAcct{}, shared.RetryConfig{}, nil, nil)
	got := c.Classify(context.Background(), sharedsaga.StuckStep{Payload: map[string]any{}})
	if got != sharedsaga.ActionLeave {
		t.Fatalf("no acct: want Leave, got %v", got)
	}
}

func TestClassifier_Classify_NoAmount(t *testing.T) {
	c := saga.NewClassifier(&stubAcct{}, shared.RetryConfig{}, nil, nil)
	got := c.Classify(context.Background(), sharedsaga.StuckStep{
		Payload: map[string]any{"account_number": "111-1"},
	})
	if got != sharedsaga.ActionLeave {
		t.Fatalf("no amount: want Leave, got %v", got)
	}
}

func TestClassifier_Classify_Retry(t *testing.T) {
	c := saga.NewClassifier(&stubAcct{}, shared.RetryConfig{}, nil, nil)
	got := c.Classify(context.Background(), sharedsaga.StuckStep{
		Payload: map[string]any{"account_number": "111-1", "amount": decimal.NewFromInt(1)},
	})
	if got != sharedsaga.ActionRetry {
		t.Fatalf("want Retry, got %v", got)
	}
}

func TestClassifier_Retry_NilClient(t *testing.T) {
	c := saga.NewClassifier(nil, shared.RetryConfig{MaxAttempts: 1}, nil, nil)
	if err := c.Retry(context.Background(), sharedsaga.StuckStep{}); err == nil {
		t.Fatal("want err")
	}
}

func TestClassifier_Retry_EmptyAccount(t *testing.T) {
	c := saga.NewClassifier(&stubAcct{}, shared.RetryConfig{MaxAttempts: 1}, nil, nil)
	step := sharedsaga.StuckStep{Payload: map[string]any{"account_number": ""}}
	if err := c.Retry(context.Background(), step); err == nil {
		t.Fatal("want err")
	}
}

func TestClassifier_Retry_NoAmount(t *testing.T) {
	c := saga.NewClassifier(&stubAcct{}, shared.RetryConfig{MaxAttempts: 1}, nil, nil)
	step := sharedsaga.StuckStep{Payload: map[string]any{"account_number": "111-1"}}
	if err := c.Retry(context.Background(), step); err == nil {
		t.Fatal("want err")
	}
}

func TestClassifier_Retry_BadAmountType(t *testing.T) {
	c := saga.NewClassifier(&stubAcct{}, shared.RetryConfig{MaxAttempts: 1}, nil, nil)
	step := sharedsaga.StuckStep{Payload: map[string]any{
		"account_number": "111-1", "amount": "not-a-decimal",
	}}
	if err := c.Retry(context.Background(), step); err == nil {
		t.Fatal("want err")
	}
}

func TestClassifier_Retry_Success(t *testing.T) {
	stub := &stubAcct{}
	c := saga.NewClassifier(stub, shared.RetryConfig{MaxAttempts: 2, BaseDelay: time.Millisecond}, nil, nil)
	step := sharedsaga.StuckStep{
		SagaID:   "saga-abc",
		StepName: "debit_sender",
		Payload: map[string]any{
			"account_number": "111-1", "amount": decimal.NewFromInt(100),
		},
	}
	if err := c.Retry(context.Background(), step); err != nil {
		t.Fatalf("retry success: %v", err)
	}
	if stub.calls < 1 {
		t.Fatal("UpdateBalance not called")
	}
	if stub.lastIdempotencyK == "" {
		t.Fatal("Retry must set IdempotencyKey; account-service rejects empty key with InvalidArgument")
	}
}

func TestClassifier_Retry_Compensation_AppendsSuffix(t *testing.T) {
	stub := &stubAcct{}
	c := saga.NewClassifier(stub, shared.RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond}, nil, nil)
	step := sharedsaga.StuckStep{
		SagaID:       "saga-def",
		StepName:     "credit_recipient",
		Compensation: true,
		Payload: map[string]any{
			"account_number": "222-2", "amount": decimal.NewFromInt(50),
		},
	}
	if err := c.Retry(context.Background(), step); err != nil {
		t.Fatalf("retry: %v", err)
	}
	want := sharedsaga.IdempotencyKey("saga-def", sharedsaga.StepKind("credit_recipient")) + ":compensate"
	if stub.lastIdempotencyK != want {
		t.Fatalf("compensation key: want %q, got %q", want, stub.lastIdempotencyK)
	}
}

func TestClassifier_Retry_AccountErr(t *testing.T) {
	stub := &stubAcct{updateErr: errors.New("nope")}
	c := saga.NewClassifier(stub, shared.RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond}, nil, nil)
	step := sharedsaga.StuckStep{Payload: map[string]any{
		"account_number": "111-1", "amount": decimal.NewFromInt(100),
	}}
	if err := c.Retry(context.Background(), step); err == nil {
		t.Fatal("want err")
	}
}

func TestClassifier_PublishDeadLetter_NilPublisher(t *testing.T) {
	c := saga.NewClassifier(nil, shared.RetryConfig{}, nil, nil)
	c.PublishDeadLetter(context.Background(), sharedsaga.StuckStep{}, "x")
}

func TestClassifier_PublishDeadLetter_Publishes(t *testing.T) {
	dl := &stubDLP{}
	c := saga.NewClassifier(nil, shared.RetryConfig{}, dl, nil)
	c.PublishDeadLetter(context.Background(), sharedsaga.StuckStep{
		Handle: sharedsaga.StepHandle{ID: 7},
		SagaID: "s", StepName: "debit",
		Payload: map[string]any{
			"account_number":   "111-1",
			"amount":           decimal.NewFromInt(100),
			"transaction_id":   uint64(42),
			"transaction_type": "transfer",
		},
		RetryCount: 3,
	}, "max retries")
	if dl.calls != 1 {
		t.Fatalf("expected 1 publish, got %d", dl.calls)
	}
}

func TestClassifier_PublishDeadLetter_PublisherErr(t *testing.T) {
	dl := &stubDLP{publishErr: errors.New("kafka down")}
	c := saga.NewClassifier(nil, shared.RetryConfig{}, dl, nil)
	// Should not panic — just log.
	c.PublishDeadLetter(context.Background(), sharedsaga.StuckStep{}, "x")
}

// --- Recorder ---

func TestRecorder_RecordForward_MarkCompletedFailed(t *testing.T) {
	db := newDB(t)
	repo := repository.NewSagaLogRepository(db)
	rec := saga.NewRecorder(repo)

	st := sharedsaga.NewState()
	st.Set("transaction_id", uint64(1))
	st.Set("transaction_type", "transfer")
	st.Set("step:debit:account_number", "111-1")
	st.Set("step:debit:amount", decimal.NewFromInt(-100))

	h, err := rec.RecordForward(context.Background(), "saga-1", "debit", 1, st)
	if err != nil || h.ID == 0 {
		t.Fatalf("forward: %v %+v", err, h)
	}
	if err := rec.MarkCompleted(context.Background(), h); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// Same row again won't re-pending, but RecordForward of a different step works
	h2, err := rec.RecordForward(context.Background(), "saga-1", "credit", 2, st)
	if err != nil {
		t.Fatalf("forward2: %v", err)
	}
	if err := rec.MarkFailed(context.Background(), h2, "boom"); err != nil {
		t.Fatalf("fail: %v", err)
	}

	ok, err := rec.IsCompleted(context.Background(), "saga-1", "debit")
	if err != nil || !ok {
		t.Fatalf("is completed: %v %v", err, ok)
	}
	ok, _ = rec.IsCompleted(context.Background(), "saga-1", "missing")
	if ok {
		t.Fatal("missing should be false")
	}
}

func TestRecorder_RecordCompensation_MarkCompensated(t *testing.T) {
	db := newDB(t)
	repo := repository.NewSagaLogRepository(db)
	rec := saga.NewRecorder(repo)

	st := sharedsaga.NewState()
	st.Set("transaction_id", uint64(2))
	st.Set("transaction_type", "payment")
	st.Set("step:debit:account_number", "111-1")
	st.Set("step:debit:amount", decimal.NewFromInt(-50))

	forward := sharedsaga.StepHandle{ID: 99}
	h, err := rec.RecordCompensation(context.Background(), "saga-2", "debit", 1, forward, st)
	if err != nil || h.ID == 0 {
		t.Fatalf("comp: %v %+v", err, h)
	}
	if err := rec.MarkCompensated(context.Background(), h); err != nil {
		t.Fatalf("comp success: %v", err)
	}

	// Compensation without forward (forward.ID == 0) takes the no-link branch.
	h2, err := rec.RecordCompensation(context.Background(), "saga-2", "debit", 2, sharedsaga.StepHandle{}, st)
	if err != nil {
		t.Fatalf("comp 2: %v", err)
	}
	if err := rec.MarkCompensationFailed(context.Background(), h2, "still bad"); err != nil {
		t.Fatalf("comp fail: %v", err)
	}
}

func TestRecorder_ListStuck_IncrementRetry_DeadLetter(t *testing.T) {
	db := newDB(t)
	repo := repository.NewSagaLogRepository(db)
	rec := saga.NewRecorder(repo)

	// Insert a stuck row with old created_at directly via repo so ListStuck picks it up.
	stuck := &model.SagaLog{
		SagaID: "s", TransactionID: 1, TransactionType: "transfer",
		StepNumber: 1, StepName: "debit_compensation",
		Status: "compensating", IsCompensation: true,
		AccountNumber: "111-1", Amount: decimal.NewFromInt(100),
		CreatedAt: time.Now().Add(-2 * time.Hour),
	}
	if err := repo.RecordStep(stuck); err != nil {
		t.Fatalf("record: %v", err)
	}

	rows, err := rec.ListStuck(context.Background(), time.Hour)
	if err != nil || len(rows) != 1 {
		t.Fatalf("list stuck: %v %d", err, len(rows))
	}
	// stripCompensationSuffix should strip the suffix
	if rows[0].StepName != "debit" {
		t.Fatalf("step name strip: %q", rows[0].StepName)
	}
	if !rows[0].Compensation {
		t.Fatal("Compensation flag")
	}

	if err := rec.IncrementRetry(context.Background(), rows[0].Handle); err != nil {
		t.Fatalf("inc: %v", err)
	}
	if err := rec.MarkDeadLetter(context.Background(), rows[0].Handle, "give up"); err != nil {
		t.Fatalf("dl: %v", err)
	}
}
