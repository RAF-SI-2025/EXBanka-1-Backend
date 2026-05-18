// Package service: typed sentinel errors for user-service operations.
//
// Each sentinel embeds a gRPC code via svcerr.SentinelError, so wrapping it
// with fmt.Errorf("...: %w", err, sentinel) preserves the code through
// status.FromError. Handlers therefore become passthrough — no
// string-matching is required to map service errors back to gRPC status.
package service

import (
	"google.golang.org/grpc/codes"

	"github.com/exbanka/contract/shared/svcerr"
)

var (
	// ErrEmployeeNotFound — the employee does not exist.
	ErrEmployeeNotFound = svcerr.New(codes.NotFound, "employee not found")

	// ErrEmployeeAlreadyExists — duplicate employee (email / username / JMBG).
	ErrEmployeeAlreadyExists = svcerr.New(codes.AlreadyExists, "employee already exists")

	// ErrInvalidJMBG — JMBG is not exactly 13 digits.
	ErrInvalidJMBG = svcerr.New(codes.InvalidArgument, "invalid JMBG")

	// ErrInvalidPassword — password does not satisfy the policy.
	ErrInvalidPassword = svcerr.New(codes.InvalidArgument, "password does not meet policy")

	// ErrPermissionNotInCatalog — caller specified a permission code that is
	// not present in the catalog.
	ErrPermissionNotInCatalog = svcerr.New(codes.InvalidArgument, "permission not in catalog")

	// ErrRoleNotFound — caller referenced a role name that does not exist.
	ErrRoleNotFound = svcerr.New(codes.NotFound, "role not found")

	// ErrLimitExceedsTemplate — requested limit exceeds the template max.
	ErrLimitExceedsTemplate = svcerr.New(codes.FailedPrecondition, "limit exceeds template max")

	// ErrTemplateNotFound — limit template lookup failed.
	ErrTemplateNotFound = svcerr.New(codes.NotFound, "limit template not found")

	// ErrBlueprintNotFound — limit blueprint lookup failed.
	ErrBlueprintNotFound = svcerr.New(codes.NotFound, "blueprint not found")

	// ErrActuaryNotFound — actuary record lookup failed.
	ErrActuaryNotFound = svcerr.New(codes.NotFound, "actuary not found")

	// ErrHierarchyDenied — caller does not outrank the target employee.
	ErrHierarchyDenied = svcerr.New(codes.PermissionDenied, "hierarchy denies modification of higher rank")

	// ErrInvalidActuaryLimit — actuary limit value is invalid (e.g., negative).
	ErrInvalidActuaryLimit = svcerr.New(codes.InvalidArgument, "invalid actuary limit")

	// ErrNotActuary — employee does not hold an actuary-eligible role.
	ErrNotActuary = svcerr.New(codes.FailedPrecondition, "employee is not an actuary")

	// ErrInvalidBlueprint — blueprint payload or type is invalid.
	ErrInvalidBlueprint = svcerr.New(codes.InvalidArgument, "invalid blueprint")
)
