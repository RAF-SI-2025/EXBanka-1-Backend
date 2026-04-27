// Package model: ownership types for the owner_type/owner_id schema.
//
// Plan: docs/superpowers/plans/2026-04-27-owner-type-schema.md (Spec C, Task 4).
// Replaces the (user_id, system_type) composite ownership pattern + the
// BankSentinelUserID = 1_000_000_000 magic number with two explicit columns
// per resource row: (owner_type, owner_id). owner_id is NULL iff
// owner_type = "bank".
package model

import "errors"

// OwnerType is the discriminator for resource ownership in stock-service.
// Used on every model that previously carried a (user_id, system_type) pair.
type OwnerType string

const (
	OwnerClient OwnerType = "client"
	OwnerBank   OwnerType = "bank"
)

// Valid reports whether o is one of the recognised owner types.
func (o OwnerType) Valid() bool {
	return o == OwnerClient || o == OwnerBank
}

// String returns the underlying string form.
func (o OwnerType) String() string {
	return string(o)
}

// ValidateOwner enforces the (owner_type, owner_id) pair invariant:
//   - owner_type must be one of {client, bank}
//   - bank owners MUST have owner_id == nil
//   - client owners MUST have owner_id != nil (and non-zero by convention)
//
// Used in BeforeSave GORM hooks on every owner-bearing model.
func ValidateOwner(t OwnerType, id *uint64) error {
	if !t.Valid() {
		return errors.New("invalid owner_type")
	}
	if t == OwnerBank && id != nil {
		return errors.New("bank owner must have nil owner_id")
	}
	if t == OwnerClient && id == nil {
		return errors.New("client owner must have non-nil owner_id")
	}
	return nil
}

// OwnerIDOrZero returns 0 for nil owner ids. Useful for legacy callers / logs
// that need a uint64. Do not use for query predicates — use the *uint64
// directly so SQL emits IS NULL.
func OwnerIDOrZero(id *uint64) uint64 {
	if id == nil {
		return 0
	}
	return *id
}

// OwnerFromLegacy converts the legacy (user_id, system_type) ownership pair —
// still carried by gRPC requests / api-gateway shims pending Task 7 of plan
// docs/superpowers/plans/2026-04-27-owner-type-schema.md — into the new
// (OwnerType, *uint64) representation used by repositories and models.
//
// Convention: system_type "bank" (legacy) maps to OwnerBank with a nil owner_id;
// any other value (typically "client" or "employee") maps to OwnerClient with
// owner_id = userID. Employees acting on behalf of a client must already have
// rewritten userID/systemType to the client's identity at the handler layer
// (resolveOrderOwner pattern); this helper does not perform that rewrite.
func OwnerFromLegacy(userID uint64, systemType string) (OwnerType, *uint64) {
	if systemType == string(OwnerBank) {
		return OwnerBank, nil
	}
	uid := userID
	return OwnerClient, &uid
}

// OwnerToLegacyUserID returns the uint64 representation of an owner for legacy
// proto fields (Order.user_id, etc.). Bank owners surface as 0 — the api-
// gateway / consumer side is responsible for treating 0 as "bank" until Task 7
// rolls out the OwnerType enum on the wire.
func OwnerToLegacyUserID(t OwnerType, id *uint64) uint64 {
	if id == nil {
		return 0
	}
	return *id
}

// OwnerToLegacySystemType returns the legacy system_type string for an owner.
// Used by handlers to populate proto fields whose schema still names the
// column system_type.
func OwnerToLegacySystemType(t OwnerType) string {
	return string(t)
}
