package router

import (
	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/handler"
	"github.com/exbanka/api-gateway/internal/middleware"
	authpb "github.com/exbanka/contract/authpb"
	stockpb "github.com/exbanka/contract/stockpb"
)

// SetupV3Routes registers /api/v3 routes. v3 hosts new feature surfaces that
// don't exist on v1/v2 — currently the Celina-4 investment-funds endpoints.
// To call any v1/v2 route under /api/v3, use the v1 or v2 prefix directly;
// v3 does not (yet) wrap RegisterCoreRoutes, so its routes are purely
// additive and version-independent.
func SetupV3Routes(
	r *gin.Engine,
	authClient authpb.AuthServiceClient,
	fundClient stockpb.InvestmentFundServiceClient,
) {
	v3 := r.Group("/api/v3")

	fundHandler := handler.NewInvestmentFundHandler(fundClient)

	// /me/investment-funds — caller's positions.
	mev3 := v3.Group("/me")
	mev3.Use(middleware.AnyAuthMiddleware(authClient))
	{
		mev3.GET("/investment-funds", fundHandler.ListMyPositions)
	}

	// Browsing + invest/redeem → AnyAuth (clients + employees).
	fundsAny := v3.Group("/investment-funds")
	fundsAny.Use(middleware.AnyAuthMiddleware(authClient))
	{
		fundsAny.GET("", fundHandler.ListFunds)
		fundsAny.GET("/:id", fundHandler.GetFund)
		fundsAny.POST("/:id/invest", fundHandler.Invest)
		fundsAny.POST("/:id/redeem", fundHandler.Redeem)
	}

	// Manage (create / update) → funds.manage permission.
	fundsManage := v3.Group("/investment-funds")
	fundsManage.Use(middleware.AuthMiddleware(authClient))
	fundsManage.Use(middleware.RequirePermission("funds.manage"))
	{
		fundsManage.POST("", fundHandler.CreateFund)
		fundsManage.PUT("/:id", fundHandler.UpdateFund)
	}

	// Bank-position read + actuary performance → funds.bank-position-read.
	fundsBank := v3.Group("/")
	fundsBank.Use(middleware.AuthMiddleware(authClient))
	fundsBank.Use(middleware.RequirePermission("funds.bank-position-read"))
	{
		fundsBank.GET("/investment-funds/positions", fundHandler.ListBankPositions)
		fundsBank.GET("/actuaries/performance", fundHandler.ActuaryPerformance)
	}
}
