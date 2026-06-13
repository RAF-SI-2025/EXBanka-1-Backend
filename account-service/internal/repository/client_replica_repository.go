package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/account-service/internal/model"
)

// ErrClientReplicaNotFound is returned when a client replica row is absent.
var ErrClientReplicaNotFound = errors.New("client replica not found")

// ClientReplicaRepository manages the local client read-model for account-service (SP-1).
type ClientReplicaRepository struct{ db *gorm.DB }

// NewClientReplicaRepository creates a new ClientReplicaRepository backed by db.
func NewClientReplicaRepository(db *gorm.DB) *ClientReplicaRepository {
	return &ClientReplicaRepository{db: db}
}

// Upsert applies an event-sourced client state, but ONLY if its Version is
// strictly greater than the stored row's Version (monotonic; tolerates
// out-of-order / duplicate Kafka delivery). A first insert always wins.
//
// CONTRACT: the caller MUST pass a full client snapshot (all profile fields
// populated). The version-guarded update uses Select to force-write the chosen
// columns — including zero values — so a partial or empty field will silently
// overwrite stored data with blanks. client-service satisfies this by
// publishing the complete client record in every client.created / client.updated
// event; any future producer MUST do the same.
func (r *ClientReplicaRepository) Upsert(ctx context.Context, in model.ClientReplica) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.ClientReplica
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing, in.ID).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			return tx.Create(&in).Error
		case err != nil:
			return err
		}
		if in.Version <= existing.Version {
			return nil // stale or duplicate; ignore
		}
		return tx.Model(&existing).Select("Email", "FirstName", "LastName", "JMBG", "Version").Updates(&in).Error
	})
}

// GetByID fetches a ClientReplica by its ID. Returns ErrClientReplicaNotFound if absent.
func (r *ClientReplicaRepository) GetByID(ctx context.Context, id uint64) (model.ClientReplica, error) {
	var c model.ClientReplica
	err := r.db.WithContext(ctx).First(&c, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.ClientReplica{}, ErrClientReplicaNotFound
	}
	return c, err
}
