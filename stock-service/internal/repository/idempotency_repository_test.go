package repository

import (
	"errors"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	pb "github.com/exbanka/contract/stockpb"
)

// newIdempotencyTestDB opens a fresh in-memory SQLite database with the
// IdempotencyRecord table migrated. Stock-service uses gorm.io/driver/sqlite
// (cgo) to match the rest of its repository tests.
func newIdempotencyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.IdempotencyRecord{}); err != nil {
		t.Fatalf("migrate idempotency_records: %v", err)
	}
	return db
}

// newSetTestingResp returns a fresh empty SetTestingModeResponse — the proto
// type used as the cached response shell. It is a small message in the
// stock-service contract with a single bool field, easy to assert on.
func newSetTestingResp() *pb.SetTestingModeResponse { return &pb.SetTestingModeResponse{} }

func TestIdempotency_FirstCallExecutesAndCaches(t *testing.T) {
	db := newIdempotencyTestDB(t)
	repo := NewIdempotencyRepository(db)

	called := 0
	resp := &pb.SetTestingModeResponse{TestingMode: true}
	got, err := Run(repo, db, "key-1", newSetTestingResp,
		func() (*pb.SetTestingModeResponse, error) {
			called++
			return resp, nil
		})
	if err != nil {
		t.Fatalf("first call returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("first call: fn invoked %d times, want 1", called)
	}
	if !got.GetTestingMode() {
		t.Errorf("first call: testing_mode %v, want true", got.GetTestingMode())
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

	resp := &pb.SetTestingModeResponse{TestingMode: true}
	if _, err := Run(repo, db, "key-2", newSetTestingResp,
		func() (*pb.SetTestingModeResponse, error) { return resp, nil }); err != nil {
		t.Fatalf("priming call failed: %v", err)
	}

	called := 0
	got, err := Run(repo, db, "key-2", newSetTestingResp,
		func() (*pb.SetTestingModeResponse, error) {
			called++
			return nil, nil
		})
	if err != nil {
		t.Fatalf("second call returned error: %v", err)
	}
	if called != 0 {
		t.Errorf("expected 0 calls on cache hit, got %d", called)
	}
	if !got.GetTestingMode() {
		t.Errorf("cache miss: testing_mode %v, want true", got.GetTestingMode())
	}
}

func TestIdempotency_EmptyKeyRejected(t *testing.T) {
	db := newIdempotencyTestDB(t)
	repo := NewIdempotencyRepository(db)

	_, err := Run(repo, db, "", newSetTestingResp,
		func() (*pb.SetTestingModeResponse, error) { return &pb.SetTestingModeResponse{}, nil })
	if err == nil {
		t.Error("expected error for empty key, got nil")
	}
}

func TestIdempotency_FnErrorRollsBackClaim(t *testing.T) {
	db := newIdempotencyTestDB(t)
	repo := NewIdempotencyRepository(db)

	wantErr := errors.New("boom")
	_, err := Run(repo, db, "key-err", newSetTestingResp,
		func() (*pb.SetTestingModeResponse, error) { return nil, wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected business error to propagate, got %v", err)
	}

	called := 0
	_, err = Run(repo, db, "key-err", newSetTestingResp,
		func() (*pb.SetTestingModeResponse, error) {
			called++
			return &pb.SetTestingModeResponse{TestingMode: true}, nil
		})
	if err != nil {
		t.Fatalf("retry after error failed: %v", err)
	}
	if called != 1 {
		t.Errorf("expected retry to execute fn once, got %d calls", called)
	}
}
