package handler

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/exbanka/card-service/internal/service"
	"github.com/exbanka/contract/identity"
)

// ownsCard reports whether the gRPC caller (from identity metadata) may access a
// resource owned by ownerID. OWN-1: client → own only; employee-on-behalf →
// bound client; employee/admin + trusted service → allowed (the gateway already
// permission-gated employees). A false result is mapped by callers to a NotFound
// sentinel (don't leak existence across tenants).
func ownsCard(ctx context.Context, ownerID uint64) bool {
	return identity.FromIncoming(ctx).OwnsResource(int64(ownerID))
}

// requireCardOwnerByID fetches the card and returns ErrCardNotFound unless the
// caller owns it. Used by mutating RPCs (pin/block) that take only a card id.
func requireCardOwnerByID(ctx context.Context, svc cardServiceFacade, id uint64) error {
	card, err := svc.GetCard(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return service.ErrCardNotFound
		}
		return err
	}
	if card == nil || !ownsCard(ctx, card.OwnerID) {
		return service.ErrCardNotFound
	}
	return nil
}
