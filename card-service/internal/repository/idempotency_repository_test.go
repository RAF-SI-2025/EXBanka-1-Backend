package repository

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/card-service/internal/model"
	pb "github.com/exbanka/contract/cardpb"
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

// newCardResp returns a fresh empty CardResponse — the proto type used
// throughout these tests as the cached response shell.
func newCardResp() *pb.CardResponse { return &pb.CardResponse{} }

func TestIdempotency_FirstCallExecutesAndCaches(t *testing.T) {
	db := newIdempotencyTestDB(t)
	repo := NewIdempotencyRepository(db)

	called := 0
	resp := &pb.CardResponse{CardNumber: "4111-XXXX-XXXX-1111"}
	got, err := Run(repo, db, "key-1", newCardResp,
		func() (*pb.CardResponse, error) {
			called++
			return resp, nil
		})
	if err != nil {
		t.Fatalf("first call returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("first call: fn invoked %d times, want 1", called)
	}
	if got.GetCardNumber() != "4111-XXXX-XXXX-1111" {
		t.Errorf("first call: card_number %q, want 4111-XXXX-XXXX-1111", got.GetCardNumber())
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

	resp := &pb.CardResponse{CardNumber: "5555-XXXX-XXXX-4444", CardBrand: "mastercard"}
	if _, err := Run(repo, db, "key-2", newCardResp,
		func() (*pb.CardResponse, error) { return resp, nil }); err != nil {
		t.Fatalf("priming call failed: %v", err)
	}

	called := 0
	got, err := Run(repo, db, "key-2", newCardResp,
		func() (*pb.CardResponse, error) {
			called++
			return nil, nil
		})
	if err != nil {
		t.Fatalf("second call returned error: %v", err)
	}
	if called != 0 {
		t.Errorf("expected 0 calls on cache hit, got %d", called)
	}
	if got.GetCardNumber() != "5555-XXXX-XXXX-4444" {
		t.Errorf("cache miss: card_number %q, want 5555-XXXX-XXXX-4444", got.GetCardNumber())
	}
	if got.GetCardBrand() != "mastercard" {
		t.Errorf("cache miss: card_brand %q, want mastercard", got.GetCardBrand())
	}
}

func TestIdempotency_EmptyKeyRejected(t *testing.T) {
	db := newIdempotencyTestDB(t)
	repo := NewIdempotencyRepository(db)

	_, err := Run(repo, db, "", newCardResp,
		func() (*pb.CardResponse, error) { return &pb.CardResponse{}, nil })
	if err == nil {
		t.Error("expected error for empty key, got nil")
	}
}

func TestIdempotency_FnErrorRollsBackClaim(t *testing.T) {
	db := newIdempotencyTestDB(t)
	repo := NewIdempotencyRepository(db)

	wantErr := errors.New("boom")
	_, err := Run(repo, db, "key-err", newCardResp,
		func() (*pb.CardResponse, error) { return nil, wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected business error to propagate, got %v", err)
	}

	called := 0
	_, err = Run(repo, db, "key-err", newCardResp,
		func() (*pb.CardResponse, error) {
			called++
			return &pb.CardResponse{CardNumber: "retry"}, nil
		})
	if err != nil {
		t.Fatalf("retry after error failed: %v", err)
	}
	if called != 1 {
		t.Errorf("expected retry to execute fn once, got %d calls", called)
	}
}
