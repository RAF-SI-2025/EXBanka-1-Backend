package consumer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/client-service/internal/model"
	kafkamsg "github.com/exbanka/contract/kafka"
)

type fakeLimitReplicaRepo struct {
	last      model.EmployeeLimitReplica
	calls     int
	err       error
	failCount int
}

func (f *fakeLimitReplicaRepo) Upsert(_ context.Context, in model.EmployeeLimitReplica) error {
	f.calls++
	if f.calls <= f.failCount {
		return f.err
	}
	f.last = in
	return nil
}

func TestHandleLimitEvent_UpsertsReplica(t *testing.T) {
	repo := &fakeLimitReplicaRepo{}
	c := &EmployeeLimitReplicaConsumer{repo: repo}
	payload, _ := json.Marshal(kafkamsg.EmployeeLimitsUpdatedMessage{
		EmployeeID:            42,
		Action:                "set",
		MaxLoanApprovalAmount: "50000.0000",
		MaxSingleTransaction:  "10000.0000",
		MaxDailyTransaction:   "20000.0000",
		MaxClientDailyLimit:   "5000.0000",
		MaxClientMonthlyLimit: "100000.0000",
		Version:               3,
	})
	if err := c.handle(context.Background(), payload); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if repo.calls != 1 || repo.last.EmployeeID != 42 || repo.last.Version != 3 {
		t.Fatalf("bad upsert: %+v calls=%d", repo.last, repo.calls)
	}
	if !repo.last.MaxLoanApprovalAmount.Equal(decimal.RequireFromString("50000.0000")) {
		t.Fatalf("bad MaxLoanApprovalAmount: %s", repo.last.MaxLoanApprovalAmount)
	}
	if !repo.last.MaxSingleTransaction.Equal(decimal.RequireFromString("10000.0000")) {
		t.Fatalf("bad MaxSingleTransaction: %s", repo.last.MaxSingleTransaction)
	}
	if !repo.last.MaxDailyTransaction.Equal(decimal.RequireFromString("20000.0000")) {
		t.Fatalf("bad MaxDailyTransaction: %s", repo.last.MaxDailyTransaction)
	}
	if !repo.last.MaxClientDailyLimit.Equal(decimal.RequireFromString("5000.0000")) {
		t.Fatalf("bad MaxClientDailyLimit: %s", repo.last.MaxClientDailyLimit)
	}
	if !repo.last.MaxClientMonthlyLimit.Equal(decimal.RequireFromString("100000.0000")) {
		t.Fatalf("bad MaxClientMonthlyLimit: %s", repo.last.MaxClientMonthlyLimit)
	}
}

func TestParseDecimalOrZero_MalformedAndEmpty(t *testing.T) {
	if !parseDecimalOrZero("abc").IsZero() {
		t.Fatal("malformed non-empty string must return zero")
	}
	if !parseDecimalOrZero("").IsZero() {
		t.Fatal("empty string must return zero")
	}
	if !parseDecimalOrZero("123.45").Equal(decimal.RequireFromString("123.45")) {
		t.Fatal("valid decimal string must parse correctly")
	}
}

func TestHandleLimitEvent_EmptyDecimalsBecomeZero(t *testing.T) {
	repo := &fakeLimitReplicaRepo{}
	c := &EmployeeLimitReplicaConsumer{repo: repo}
	payload, _ := json.Marshal(kafkamsg.EmployeeLimitsUpdatedMessage{EmployeeID: 7, Action: "set", Version: 1}) // all values empty
	if err := c.handle(context.Background(), payload); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !repo.last.MaxLoanApprovalAmount.IsZero() || !repo.last.MaxSingleTransaction.IsZero() {
		t.Fatalf("empty decimals must be zero: %+v", repo.last)
	}
}

func TestHandleLimitEvent_BadJSON(t *testing.T) {
	repo := &fakeLimitReplicaRepo{}
	c := &EmployeeLimitReplicaConsumer{repo: repo}
	if err := c.handle(context.Background(), []byte("{bad")); err == nil {
		t.Fatalf("expected error on malformed json")
	}
	if repo.calls != 0 {
		t.Fatalf("repo must not be called on bad json, got %d", repo.calls)
	}
}

func TestHandleWithRetry_RetriesTransientError(t *testing.T) {
	repo := &fakeLimitReplicaRepo{err: context.DeadlineExceeded, failCount: 2}
	c := &EmployeeLimitReplicaConsumer{repo: repo, backoff: []time.Duration{0, 0}}
	payload, _ := json.Marshal(kafkamsg.EmployeeLimitsUpdatedMessage{EmployeeID: 1, Version: 1})
	if err := c.handleWithRetry(context.Background(), payload); err != nil {
		t.Fatalf("expected eventual success: %v", err)
	}
	if repo.calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", repo.calls)
	}
}

func TestHandleWithRetry_MalformedNotRetried(t *testing.T) {
	repo := &fakeLimitReplicaRepo{}
	c := &EmployeeLimitReplicaConsumer{repo: repo, backoff: []time.Duration{0, 0}}
	if err := c.handleWithRetry(context.Background(), []byte("{bad")); err == nil {
		t.Fatalf("expected error")
	}
	if repo.calls != 0 {
		t.Fatalf("malformed must not retry/call repo, got %d", repo.calls)
	}
}
