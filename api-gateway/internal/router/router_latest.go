// api-gateway/internal/router/router_latest.go
package router

import (
	"github.com/gin-gonic/gin"
)

// SetupLatestRoutes registers /api/latest/* as an alias for the highest
// versioned API (/api/v1). When a v2 is added, change the rewrite target.
//
// IMPORTANT: Must be called AFTER SetupV1Routes so that /api/v1/ routes exist.
func SetupLatestRoutes(r *gin.Engine) {
	r.Any("/api/latest/*path", func(c *gin.Context) {
		path := c.Param("path")
		c.Request.URL.Path = "/api/v1" + path
		r.HandleContext(c)
	})
}
