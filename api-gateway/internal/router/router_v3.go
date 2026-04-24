package router

import "github.com/gin-gonic/gin"

// SetupV3Routes is a placeholder for a future API version. It registers the
// /api/v3 group with no routes yet. When v3-specific behavior is needed, add
// routes here. Core v2-equivalent routes can optionally be included by calling
// RegisterCoreRoutes(v3, …deps).
//
// Registering an empty group is cheap; it reserves the prefix in the router so
// that any subsequent addition is purely additive.
func SetupV3Routes(r *gin.Engine) {
	_ = r.Group("/api/v3")
}
