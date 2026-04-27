package middleware

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// ResolvedIdentity is the per-request, fully-resolved actor and owner.
//
// Handlers consume this struct via c.MustGet("identity") — they do not
// compute identity themselves. This is the foundational lighthouse for
// the owner_type schema refactor (plan: 2026-04-27-owner-type-schema).
//
// Fields:
//   - PrincipalType: who is logged in ("client" | "employee").
//   - PrincipalID:   the principal's primary-key id.
//   - OwnerType:     who owns the resource being acted on ("client" | "bank").
//   - OwnerID:       the owner's primary-key id; nil iff OwnerType == "bank".
//   - ActingEmployeeID: &PrincipalID iff PrincipalType == "employee", else nil.
//     This carries the audit trail for employee-on-behalf-of-client actions.
type ResolvedIdentity struct {
	PrincipalType    string
	PrincipalID      uint64
	OwnerType        string
	OwnerID          *uint64
	ActingEmployeeID *uint64
}

// IdentityRule selects how ResolveIdentity computes OwnerType and OwnerID
// from the principal and the request URL.
type IdentityRule int

const (
	// OwnerIsPrincipal: owner == principal. Used for /me/profile, /me/cards
	// — routes where the resource owner is always the logged-in user.
	OwnerIsPrincipal IdentityRule = iota

	// OwnerIsBankIfEmployee: principal=employee → owner=bank; else owner=principal.
	// Used for /me/orders, /me/portfolios — trading routes that the bank also
	// "owns" (so an employee acting for the bank gets owner=bank/nil).
	OwnerIsBankIfEmployee

	// OwnerFromURLParam: owner=client, owner_id read from a path parameter
	// (default name "client_id"; override via the first variadic arg).
	// Used for /clients/:client_id/* admin-acts-on-client routes.
	OwnerFromURLParam
)

// ResolveIdentity is a per-route middleware that builds a ResolvedIdentity
// from the JWT principal (set upstream by AuthMiddleware / AnyAuthMiddleware)
// and stores it in the gin context under the key "identity".
//
// args:
//   - For OwnerFromURLParam, args[0] is the URL param name to read
//     (defaults to "client_id" when omitted).
//   - Ignored for the other rules.
func ResolveIdentity(rule IdentityRule, args ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// AuthMiddleware / AnyAuthMiddleware set "principal_type" and
		// "principal_id" upstream. The legacy keys "system_type" /
		// "user_id" were dropped in Spec C Task 3 and are no longer
		// populated by any middleware.
		principalType := c.GetString("principal_type")
		principalID := c.GetUint64("principal_id")

		if principalType == "" || principalID == 0 {
			abortWithError(c, http.StatusUnauthorized, "unauthorized", "principal not set")
			return
		}

		id := &ResolvedIdentity{
			PrincipalType: principalType,
			PrincipalID:   principalID,
		}
		if principalType == "employee" {
			emp := principalID
			id.ActingEmployeeID = &emp
		}

		switch rule {
		case OwnerIsPrincipal:
			id.OwnerType = principalType
			pid := principalID
			id.OwnerID = &pid

		case OwnerIsBankIfEmployee:
			if principalType == "employee" {
				id.OwnerType = "bank"
				id.OwnerID = nil
			} else {
				id.OwnerType = "client"
				pid := principalID
				id.OwnerID = &pid
			}

		case OwnerFromURLParam:
			paramName := "client_id"
			if len(args) > 0 && args[0] != "" {
				paramName = args[0]
			}
			raw := c.Param(paramName)
			cid, err := strconv.ParseUint(raw, 10, 64)
			if err != nil || cid == 0 {
				abortWithError(c, http.StatusBadRequest, "invalid_client_id",
					"client_id must be a positive integer")
				return
			}
			id.OwnerType = "client"
			id.OwnerID = &cid

		default:
			abortWithError(c, http.StatusInternalServerError, "internal_error",
				"unknown IdentityRule")
			return
		}

		c.Set("identity", id)
		c.Next()
	}
}
