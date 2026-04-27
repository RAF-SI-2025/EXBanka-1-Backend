package repository

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/credit-service/internal/model"
	pb "github.com/exbanka/contract/creditpb"
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

// newLoanResp returns a fresh empty LoanResponse — the proto type used
// throughout these tests as the cached response shell.
func newLoanResp() *pb.LoanResponse { return &pb.LoanResponse{} }

func TestIdempotency_FirstCallExecutesAndCaches(t *testing.T) {
	db := newIdempotencyTestDB(t)
	repo := NewIdempotencyRepository(db)

	called := 0
	resp := &pb.LoanResponse{LoanNumber: "LN-001", Amount: "100000"}
	got, err := Run(repo, db, "key-1", newLoanResp,
		func() (*pb.LoanResponse, error) {
			called++
			return resp, nil
		})
	if err != nil {
		t.Fatalf("first call returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("first call: fn invoked %d times, want 1", called)
	}
	if got.GetLoanNumber() != "LN-001" {
		t.Errorf("first call: loan_number %q, want LN-001", got.GetLoanNumber())
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

	resp := &pb.LoanResponse{LoanNumber: "LN-002", Amount: "200000"}
	if _, err := Run(repo, db, "key-2", newLoanResp,
		func() (*pb.LoanResponse, error) { return resp, nil }); err != nil {
		t.Fatalf("priming call failed: %v", err)
	}

	called := 0
	got, err := Run(repo, db, "key-2", newLoanResp,
		func() (*pb.LoanResponse, error) {
			called++
			return nil, nil
		})
	if err != nil {
		t.Fatalf("second call returned error: %v", err)
	}
	if called != 0 {
		t.Errorf("expected 0 calls on cache hit, got %d", called)
	}
	if got.GetLoanNumber() != "LN-002" {
		t.Errorf("cache miss: loan_number %q, want LN-002", got.GetLoanNumber())
	}
	if got.GetAmount() != "200000" {
		t.Errorf("cache miss: amount %q, want 200000", got.GetAmount())
	}
}

func TestIdempotency_EmptyKeyRejected(t *testing.T) {
	db := newIdempotencyTestDB(t)
	repo := NewIdempotencyRepository(db)

	_, err := Run(repo, db, "", newLoanResp,
		func() (*pb.LoanResponse, error) { return &pb.LoanResponse{}, nil })
	if err == nil {
		t.Error("expected error for empty key, got nil")
	}
}

func TestIdempotency_FnErrorRollsBackClaim(t *testing.T) {
	db := newIdempotencyTestDB(t)
	repo := NewIdempotencyRepository(db)

	wantErr := errors.New("boom")
	_, err := Run(repo, db, "key-err", newLoanResp,
		func() (*pb.LoanResponse, error) { return nil, wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected business error to propagate, got %v", err)
	}

	called := 0
	_, err = Run(repo, db, "key-err", newLoanResp,
		func() (*pb.LoanResponse, error) {
			called++
			return &pb.LoanResponse{LoanNumber: "LN-retry"}, nil
		})
	if err != nil {
		t.Fatalf("retry after error failed: %v", err)
	}
	if called != 1 {
		t.Errorf("expected retry to execute fn once, got %d calls", called)
	}
}
