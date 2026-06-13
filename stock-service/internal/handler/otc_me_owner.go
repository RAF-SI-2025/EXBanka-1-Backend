package handler

import (
	"strconv"

	"github.com/exbanka/stock-service/internal/model"
)

// otcMeOwner reports whether the acting identity (owner_type/owner_id) owns
// an OTC offer identified by kind + its SI-TX seller id. A remote listing is
// hosted by a peer and is never owned by us. For local: an employee acting as
// the bank owns bank listings (seller "bank"); a client owns "client-<id>".
func otcMeOwner(actingOwnerType string, actingOwnerID uint64, kind, sellerID string) bool {
	if kind != "local" {
		return false
	}
	if actingOwnerType == "bank" {
		return sellerID == "bank"
	}
	if actingOwnerType == "client" && actingOwnerID != 0 {
		return sellerID == "client-"+strconv.FormatUint(actingOwnerID, 10)
	}
	return false
}

// sellerIDForOwner builds the SI-TX seller id ("bank" | "client-<id>") for a
// local offer's initiator, matching how the option cache composes it
// (otccache.composeSellerID).
func sellerIDForOwner(ownerType model.OwnerType, ownerID *uint64) string {
	if ownerType == model.OwnerBank {
		return "bank"
	}
	if ownerID != nil {
		return "client-" + strconv.FormatUint(*ownerID, 10)
	}
	return ""
}
