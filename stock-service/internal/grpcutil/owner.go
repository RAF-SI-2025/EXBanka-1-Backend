// Package grpcutil provides translation helpers between proto wire types
// and stock-service domain types.
//
// Owner translation is the main concern: every gRPC handler in stock-service
// receives ownership as either the legacy (user_id string system_type) pair
// (still in use pending Task 7 of plan
// docs/superpowers/plans/2026-04-27-owner-type-schema.md) or as the new
// (OwnerType, owner_id) pair once the proto schema flips. These helpers
// centralise the legacy → model conversion so service / repository code can
// uniformly take (model.OwnerType, *uint64).
package grpcutil

import (
	"github.com/exbanka/stock-service/internal/model"
)

// OwnerFromLegacy is a thin re-export of model.OwnerFromLegacy so handlers
// can avoid an extra model import in conversion-only files.
func OwnerFromLegacy(userID uint64, systemType string) (model.OwnerType, *uint64) {
	return model.OwnerFromLegacy(userID, systemType)
}

// OwnerToLegacyUserID exposes model.OwnerToLegacyUserID via the grpcutil
// surface for symmetry.
func OwnerToLegacyUserID(t model.OwnerType, id *uint64) uint64 {
	return model.OwnerToLegacyUserID(t, id)
}

// OwnerToLegacySystemType exposes model.OwnerToLegacySystemType via the
// grpcutil surface for symmetry.
func OwnerToLegacySystemType(t model.OwnerType) string {
	return model.OwnerToLegacySystemType(t)
}

// ActingEmployeeIDFromUint64 wraps a uint64 (proto3 default zero is "no
// employee") into a *uint64 (nil = "no employee").
func ActingEmployeeIDFromUint64(v uint64) *uint64 {
	if v == 0 {
		return nil
	}
	out := v
	return &out
}

// ActingEmployeeIDToUint64 unwraps a *uint64 acting employee id to a uint64
// (nil → 0).
func ActingEmployeeIDToUint64(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}
