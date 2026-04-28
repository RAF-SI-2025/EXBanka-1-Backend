package repository

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/model"
)

// newIdempotencyTestDB opens a fresh in-memory SQLite database with the
// IdempotencyRecord table migrated. A unique DSN per test name keeps
// concurrent t.Parallel() runs from sharing state.
func newIdempotencyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.IdempotencyRecord{}))
	return db
}

// newPaymentResp returns a fresh empty PaymentResponse — the proto type used
// throughout these tests as the cached response shell.
func newPaymentResp() *pb.PaymentResponse { return &pb.PaymentResponse{} }

func TestIdempotency_FirstCallExecutesAndCaches(t *testing.T) {
	db := newIdempotencyTestDB(t)
	repo := NewIdempotencyRepository(db)

	called := 0
	resp := &pb.PaymentResponse{FinalAmount: "100"}
	got, err := Run(repo, db, "key-1", newPaymentResp,
		func() (*pb.PaymentResponse, error) {
			called++
			return resp, nil
		})
	if err != nil {
		t.Fatalf("first call returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("first call: fn invoked %d times, want 1", called)
	}
	if got.GetFinalAmount() != "100" {
		t.Errorf("first call: response final_amount %q, want 100", got.GetFinalAmount())
	}

	var rec model.IdempotencyRecord
	if err := db.First(&rec, "key = ?", "key-1").Error; err != nil {
		t.Fatalf("expected record persisted: %v", err)
	}
	if len(rec.ResponseBlob) == 0 {
		t.Error("expected non-empty response blob after first call")
	}
}

func TestIdempotency_SecondCallReturnsCacheNotExecutes(t *testing.T) {
	db := newIdempotencyTestDB(t)
	repo := NewIdempotencyRepository(db)

	resp := &pb.PaymentResponse{FinalAmount: "200", FromAccountNumber: "ABC"}
	if _, err := Run(repo, db, "key-2", newPaymentResp,
		func() (*pb.PaymentResponse, error) { return resp, nil }); err != nil {
		t.Fatalf("priming call failed: %v", err)
	}

	called := 0
	got, err := Run(repo, db, "key-2", newPaymentResp,
		func() (*pb.PaymentResponse, error) {
			called++
			return nil, nil
		})
	if err != nil {
		t.Fatalf("second call returned error: %v", err)
	}
	if called != 0 {
		t.Errorf("expected 0 calls on cache hit, got %d", called)
	}
	if got.GetFinalAmount() != "200" {
		t.Errorf("cache miss: final_amount %q, want 200", got.GetFinalAmount())
	}
	if got.GetFromAccountNumber() != "ABC" {
		t.Errorf("cache miss: from_account_number %q, want ABC", got.GetFromAccountNumber())
	}
}

func TestIdempotency_EmptyKeyRejected(t *testing.T) {
	db := newIdempotencyTestDB(t)
	repo := NewIdempotencyRepository(db)

	_, err := Run(repo, db, "", newPaymentResp,
		func() (*pb.PaymentResponse, error) { return &pb.PaymentResponse{}, nil })
	if err == nil {
		t.Error("expected error for empty key, got nil")
	}
}

func TestIdempotency_FnErrorRollsBackClaim(t *testing.T) {
	db := newIdempotencyTestDB(t)
	repo := NewIdempotencyRepository(db)

	wantErr := errors.New("boom")
	_, err := Run(repo, db, "key-err", newPaymentResp,
		func() (*pb.PaymentResponse, error) { return nil, wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected business error to propagate, got %v", err)
	}

	called := 0
	_, err = Run(repo, db, "key-err", newPaymentResp,
		func() (*pb.PaymentResponse, error) {
			called++
			return &pb.PaymentResponse{FinalAmount: "300"}, nil
		})
	if err != nil {
		t.Fatalf("retry after error failed: %v", err)
	}
	if called != 1 {
		t.Errorf("expected retry to execute fn once, got %d calls", called)
	}
}
