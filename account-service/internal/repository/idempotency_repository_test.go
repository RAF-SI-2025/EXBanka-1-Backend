package repository

import (
	"errors"
	"testing"

	"github.com/exbanka/account-service/internal/model"
	pb "github.com/exbanka/contract/accountpb"
)

// newAccountResp returns a fresh empty AccountResponse — the proto type used
// throughout these tests as the cached response shell. Any proto.Message
// would do; AccountResponse is convenient because it's local to the service
// and has scalar fields easy to assert on.
func newAccountResp() *pb.AccountResponse { return &pb.AccountResponse{} }

func TestIdempotency_FirstCallExecutesAndCaches(t *testing.T) {
	db := newTestDB(t)
	repo := NewIdempotencyRepository(db)

	called := 0
	resp := &pb.AccountResponse{Balance: "100"}
	got, err := Run(repo, db, "key-1", newAccountResp,
		func() (*pb.AccountResponse, error) {
			called++
			return resp, nil
		})
	if err != nil {
		t.Fatalf("first call returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("first call: fn invoked %d times, want 1", called)
	}
	if got.GetBalance() != "100" {
		t.Errorf("first call: response balance %q, want 100", got.GetBalance())
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
	db := newTestDB(t)
	repo := NewIdempotencyRepository(db)

	resp := &pb.AccountResponse{Balance: "200", AccountNumber: "123"}
	if _, err := Run(repo, db, "key-2", newAccountResp,
		func() (*pb.AccountResponse, error) { return resp, nil }); err != nil {
		t.Fatalf("priming call failed: %v", err)
	}

	called := 0
	got, err := Run(repo, db, "key-2", newAccountResp,
		func() (*pb.AccountResponse, error) {
			called++
			return nil, nil
		})
	if err != nil {
		t.Fatalf("second call returned error: %v", err)
	}
	if called != 0 {
		t.Errorf("expected 0 calls on cache hit, got %d", called)
	}
	if got.GetBalance() != "200" {
		t.Errorf("cache miss: balance %q, want 200", got.GetBalance())
	}
	if got.GetAccountNumber() != "123" {
		t.Errorf("cache miss: account_number %q, want 123", got.GetAccountNumber())
	}
}

func TestIdempotency_EmptyKeyRejected(t *testing.T) {
	db := newTestDB(t)
	repo := NewIdempotencyRepository(db)

	_, err := Run(repo, db, "", newAccountResp,
		func() (*pb.AccountResponse, error) { return &pb.AccountResponse{}, nil })
	if err == nil {
		t.Error("expected error for empty key, got nil")
	}
}

func TestIdempotency_FnErrorRollsBackClaim(t *testing.T) {
	db := newTestDB(t)
	repo := NewIdempotencyRepository(db)

	wantErr := errors.New("boom")
	_, err := Run(repo, db, "key-err", newAccountResp,
		func() (*pb.AccountResponse, error) { return nil, wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected business error to propagate, got %v", err)
	}

	// Claim must be released so a retry can re-execute.
	called := 0
	_, err = Run(repo, db, "key-err", newAccountResp,
		func() (*pb.AccountResponse, error) {
			called++
			return &pb.AccountResponse{Balance: "300"}, nil
		})
	if err != nil {
		t.Fatalf("retry after error failed: %v", err)
	}
	if called != 1 {
		t.Errorf("expected retry to execute fn once, got %d calls", called)
	}
}
