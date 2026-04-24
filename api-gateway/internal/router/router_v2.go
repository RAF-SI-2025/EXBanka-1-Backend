package router

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/handler"
	"github.com/exbanka/api-gateway/internal/middleware"
	accountpb "github.com/exbanka/contract/accountpb"
	authpb "github.com/exbanka/contract/authpb"
	cardpb "github.com/exbanka/contract/cardpb"
	clientpb "github.com/exbanka/contract/clientpb"
	creditpb "github.com/exbanka/contract/creditpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	notificationpb "github.com/exbanka/contract/notificationpb"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	userpb "github.com/exbanka/contract/userpb"
	verificationpb "github.com/exbanka/contract/verificationpb"
)

// SetupV2Routes registers all /api/v2/ routes on the given engine.
//
// v2 re-registers every route that exists at v1 (via RegisterCoreRoutes) under
// the /api/v2 prefix, and adds v2-specific additions on top. v2 is a strict
// superset of v1 — every v1 path has an identical v2 equivalent.
//
// The previous NoRoute fallback that rewrote /api/v2/* → /api/v1/* has been
// removed: now that v2 registers the full core surface natively, the rewrite is
// no longer necessary. A genuinely unknown path at either version returns 404.
func SetupV2Routes(
	r *gin.Engine,
	authClient authpb.AuthServiceClient,
	userClient userpb.UserServiceClient,
	clientClient clientpb.ClientServiceClient,
	accountClient accountpb.AccountServiceClient,
	cardClient cardpb.CardServiceClient,
	txClient transactionpb.TransactionServiceClient,
	creditClient creditpb.CreditServiceClient,
	empLimitClient userpb.EmployeeLimitServiceClient,
	clientLimitClient clientpb.ClientLimitServiceClient,
	virtualCardClient cardpb.VirtualCardServiceClient,
	bankAccountClient accountpb.BankAccountServiceClient,
	feeClient transactionpb.FeeServiceClient,
	cardRequestClient cardpb.CardRequestServiceClient,
	exchangeClient exchangepb.ExchangeServiceClient,
	stockExchangeClient stockpb.StockExchangeGRPCServiceClient,
	securityClient stockpb.SecurityGRPCServiceClient,
	orderClient stockpb.OrderGRPCServiceClient,
	portfolioClient stockpb.PortfolioGRPCServiceClient,
	otcClient stockpb.OTCGRPCServiceClient,
	taxClient stockpb.TaxGRPCServiceClient,
	actuaryClient userpb.ActuaryServiceClient,
	blueprintClient userpb.BlueprintServiceClient,
	verificationClient verificationpb.VerificationGRPCServiceClient,
	notificationClient notificationpb.NotificationServiceClient,
	sourceAdminClient stockpb.SourceAdminServiceClient,
) {
	v2 := r.Group("/api/v2")

	// Core routes — identical surface to v1.
	RegisterCoreRoutes(
		v2,
		authClient, userClient, clientClient, accountClient, cardClient,
		txClient, creditClient, empLimitClient, clientLimitClient,
		virtualCardClient, bankAccountClient, feeClient, cardRequestClient,
		exchangeClient, stockExchangeClient, securityClient, orderClient,
		portfolioClient, otcClient, taxClient, actuaryClient, blueprintClient,
		verificationClient, notificationClient, sourceAdminClient,
	)

	// ── v2-specific additions ───────────────────────────────────────────
	optionsV2 := handler.NewOptionsV2Handler(securityClient, orderClient, portfolioClient)
	{
		opts := v2.Group("/options")
		opts.Use(middleware.AnyAuthMiddleware(authClient))
		opts.Use(middleware.RequirePermission("securities.trade"))
		{
			opts.POST("/:option_id/orders", optionsV2.CreateOrder)
			opts.POST("/:option_id/exercise", optionsV2.Exercise)
		}
	}

	// Catch-all 404 for any path not served by v1, v2, v3, or the swagger UI.
	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{"code": "not_found", "message": "route not found"},
		})
	})
}
