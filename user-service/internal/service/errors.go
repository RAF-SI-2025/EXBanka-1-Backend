// user-service/internal/service/errors.go
package service

import "errors"

// Sentinel errors surfaced by the role/permission management API.
//
// These are simple value sentinels so callers can use errors.Is(...) to
// detect specific failure modes and translate them into the appropriate
// gRPC status code at the handler boundary.
var (
	// ErrPermissionNotInCatalog is returned when an admin attempts to grant
	// a permission code that is not part of the codegened catalog.
	ErrPermissionNotInCatalog = errors.New("permission not in catalog")

	// ErrRoleNotFound is returned when a role lookup by name fails.
	ErrRoleNotFound = errors.New("role not found")
)
