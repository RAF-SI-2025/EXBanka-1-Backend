package repository

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

func newClientReplicaDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.ClientReplica{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestClientReplicaRepo_UpsertAndGet(t *testing.T) {
	repo := NewClientReplicaRepository(newClientReplicaDB(t))
	ctx := context.Background()
	if err := repo.Upsert(ctx, model.ClientReplica{ID: 1, Email: "v1@b.com", FirstName: "A", LastName: "B", Version: 1}); err != nil {
		t.Fatalf("upsert1: %v", err)
	}
	got, err := repo.GetByID(ctx, 1)
	if err != nil || got.Email != "v1@b.com" {
		t.Fatalf("get1: %+v err=%v", got, err)
	}
	if err := repo.Upsert(ctx, model.ClientReplica{ID: 1, Email: "v2@b.com", Version: 2}); err != nil {
		t.Fatalf("upsert2: %v", err)
	}
	got, _ = repo.GetByID(ctx, 1)
	if got.Email != "v2@b.com" {
		t.Fatalf("expected v2, got %+v", got)
	}
	if err := repo.Upsert(ctx, model.ClientReplica{ID: 1, Email: "stale@b.com", Version: 1}); err != nil {
		t.Fatalf("upsert-stale: %v", err)
	}
	got, _ = repo.GetByID(ctx, 1)
	if got.Email != "v2@b.com" {
		t.Fatalf("stale event overwrote newer state: %+v", got)
	}
}

func TestClientReplicaRepo_GetMissing(t *testing.T) {
	repo := NewClientReplicaRepository(newClientReplicaDB(t))
	_, err := repo.GetByID(context.Background(), 999)
	if err == nil {
		t.Fatalf("expected error for missing replica")
	}
}

// TestClientReplicaRepo_EqualVersionIgnored verifies that an Upsert with the
// same Version as the stored row is a no-op (equal version must not overwrite).
func TestClientReplicaRepo_EqualVersionIgnored(t *testing.T) {
	repo := NewClientReplicaRepository(newClientReplicaDB(t))
	ctx := context.Background()

	if err := repo.Upsert(ctx, model.ClientReplica{ID: 1, Email: "v2@b.com", FirstName: "A", LastName: "B", Version: 2}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := repo.Upsert(ctx, model.ClientReplica{ID: 1, Email: "equal@b.com", Version: 2}); err != nil {
		t.Fatalf("equal-version upsert: %v", err)
	}
	got, err := repo.GetByID(ctx, 1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Email != "v2@b.com" {
		t.Fatalf("equal-version upsert overwrote stored row: got email %q, want %q", got.Email, "v2@b.com")
	}
}

// TestClientReplicaRepo_PersistsVersionOnUpdate verifies that after a
// higher-version Upsert, the stored Version reflects the new value.
// This guards against accidentally dropping "Version" from the Select allow-list,
// which would leave the row at its old version and break future ordering.
func TestClientReplicaRepo_PersistsVersionOnUpdate(t *testing.T) {
	repo := NewClientReplicaRepository(newClientReplicaDB(t))
	ctx := context.Background()

	if err := repo.Upsert(ctx, model.ClientReplica{ID: 1, Email: "v1@b.com", FirstName: "A", LastName: "B", Version: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := repo.Upsert(ctx, model.ClientReplica{ID: 1, Email: "v5@b.com", FirstName: "A", LastName: "B", Version: 5}); err != nil {
		t.Fatalf("upsert v5: %v", err)
	}
	got, err := repo.GetByID(ctx, 1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Email != "v5@b.com" {
		t.Fatalf("email not updated: got %q, want %q", got.Email, "v5@b.com")
	}
	if got.Version != int64(5) {
		t.Fatalf("version not persisted: got %d, want 5 — did someone drop \"Version\" from the Select allow-list?", got.Version)
	}
}
