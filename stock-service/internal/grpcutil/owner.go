// Package grpcutil provides translation helpers between proto wire types
// and stock-service domain types.
//
// The legacy (user_id, system_type) ownership shim used by handlers prior to
// plan 2026-04-27-owner-type-schema.md is centralised here so service /
// repository code can take (model.OwnerType, *uint64) uniformly.
package grpcutil

import (
	"github.com/exbanka/stock-service/internal/model"
)

// OwnerFromLegacy is a thin re-export of model.OwnerFromLegacy so handlers
// can avoid an extra model import in conversion-only files.
func OwnerFromLegacy(userID uint64, systemType string) (model.OwnerType, *uint64) {
	return model.OwnerFromLegacy(userID, systemType)
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
