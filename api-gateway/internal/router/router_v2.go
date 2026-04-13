package router

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/handler"
	"github.com/exbanka/api-gateway/internal/middleware"
	authpb "github.com/exbanka/contract/authpb"
	stockpb "github.com/exbanka/contract/stockpb"
)

// SetupV2Routes registers v2-only routes and installs a NoRoute fallback
// that transparently rewrites unknown /api/v2/* paths to /api/v1/*.
//
// Call AFTER SetupV1Routes — NoRoute is engine-global and only one can be
// installed, so this also handles the final "route truly not found" case.
func SetupV2Routes(
	r *gin.Engine,
	authClient authpb.AuthServiceClient,
	securityClient stockpb.SecurityGRPCServiceClient,
	orderClient stockpb.OrderGRPCServiceClient,
	portfolioClient stockpb.PortfolioGRPCServiceClient,
) {
	optionsV2 := handler.NewOptionsV2Handler(securityClient, orderClient, portfolioClient)

	v2 := r.Group("/api/v2")
	{
		opts := v2.Group("/options")
		opts.Use(middleware.AnyAuthMiddleware(authClient))
		opts.Use(middleware.RequirePermission("securities.trade"))
		{
			opts.POST("/:option_id/orders", optionsV2.CreateOrder)
			opts.POST("/:option_id/exercise", optionsV2.Exercise)
		}
	}

	// Fallback: unknown /api/v2/* → rewrite to /api/v1/* and re-dispatch.
	// This also handles all other 404s (non-v2 paths).
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/v2/") {
			c.Request.URL.Path = "/api/v1/" + strings.TrimPrefix(c.Request.URL.Path, "/api/v2/")
			r.HandleContext(c)
			return
		}
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{"code": "not_found", "message": "route not found"},
		})
	})
}
