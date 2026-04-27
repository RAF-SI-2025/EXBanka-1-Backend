package router

import (
	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/handler"
	"github.com/exbanka/api-gateway/internal/middleware"
	accountpb "github.com/exbanka/contract/accountpb"
	authpb "github.com/exbanka/contract/authpb"
	perms "github.com/exbanka/contract/permissions"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
)

// SetupV3Routes registers /api/v3 routes. v3 hosts new feature surfaces
// that don't exist on v1/v2 — currently the Celina-4 investment-funds
// endpoints (existing) and the Celina-5 inter-bank-aware POST /transfers
// (added in Spec 3). To call any v1/v2 route under /api/v3, use the v1 or
// v2 prefix directly; v3 does not (yet) wrap RegisterCoreRoutes.
func SetupV3Routes(
	r *gin.Engine,
	authClient authpb.AuthServiceClient,
	fundClient stockpb.InvestmentFundServiceClient,
	interBankClient transactionpb.InterBankServiceClient,
	accountClient accountpb.AccountServiceClient,
	txClient transactionpb.TransactionServiceClient,
	otcClient stockpb.OTCOptionsServiceClient,
) {
	v3 := r.Group("/api/v3")

	fundHandler := handler.NewInvestmentFundHandler(fundClient)
	interBankPublic := handler.NewInterBankPublicHandler(interBankClient, txClient, accountClient)
	otcHandler := handler.NewOTCOptionsHandler(otcClient)

	// /me/* — caller's resources (clients + employees).
	mev3 := v3.Group("/me")
	mev3.Use(middleware.AnyAuthMiddleware(authClient))
	{
		mev3.GET("/investment-funds", fundHandler.ListMyPositions)

		// Inter-bank-aware transfer creation. Detects inter-bank by the
		// 3-digit prefix of receiverAccount and routes accordingly. Falls
		// through to the regular intra-bank handler for own-bank prefixes.
		mev3.POST("/transfers", interBankPublic.CreateTransfer)
		mev3.GET("/transfers/:id", interBankPublic.GetTransferByID)
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

	// Manage (create / update) → funds.manage.catalog.
	fundsManage := v3.Group("/investment-funds")
	fundsManage.Use(middleware.AuthMiddleware(authClient))
	fundsManage.Use(middleware.RequirePermission(perms.Funds.Manage.Catalog))
	{
		fundsManage.POST("", fundHandler.CreateFund)
		fundsManage.PUT("/:id", fundHandler.UpdateFund)
	}

	// Bank-position read + actuary performance → funds.read.all (the catalog
	// supervisor-class read perm; no separate bank-position perm exists).
	fundsBank := v3.Group("/")
	fundsBank.Use(middleware.AuthMiddleware(authClient))
	fundsBank.Use(middleware.RequirePermission(perms.Funds.Read.All))
	{
		fundsBank.GET("/investment-funds/positions", fundHandler.ListBankPositions)
		fundsBank.GET("/actuaries/performance", fundHandler.ActuaryPerformance)
	}

	// ── OTC option trading (Spec 2) ─
	// Caller's offers/contracts → AnyAuth.
	mev3.GET("/otc/offers", otcHandler.ListMyOffers)
	mev3.GET("/otc/contracts", otcHandler.ListMyContracts)

	// Read endpoints (offer/contract detail) → any participant via AnyAuth.
	otcRead := v3.Group("/otc")
	otcRead.Use(middleware.AnyAuthMiddleware(authClient))
	{
		otcRead.GET("/offers/:id", otcHandler.GetOffer)
		otcRead.GET("/contracts/:id", otcHandler.GetContract)
	}

	// Trading actions require both securities.trade AND otc.trade.
	otcTrade := v3.Group("/otc")
	otcTrade.Use(middleware.AnyAuthMiddleware(authClient))
	otcTrade.Use(middleware.RequireAllPermissions(perms.Securities.Trade.Any, perms.Otc.Trade.Accept))
	{
		otcTrade.POST("/offers", otcHandler.CreateOffer)
		otcTrade.POST("/offers/:id/counter", otcHandler.CounterOffer)
		otcTrade.POST("/offers/:id/accept", otcHandler.AcceptOffer)
		otcTrade.POST("/offers/:id/reject", otcHandler.RejectOffer)
		otcTrade.POST("/contracts/:id/exercise", otcHandler.ExerciseContract)
	}
}
