// api-gateway/internal/router/router_v3.go
package router

import (
	"net/http"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	apimetrics "github.com/exbanka/api-gateway/internal/metrics"
	"github.com/exbanka/api-gateway/internal/middleware"
	perms "github.com/exbanka/contract/permissions"
)

// NewRouter creates the Gin engine with CORS, metrics, and Swagger.
// SetupV3 (and any future SetupV4) attach their routes to this engine.
//
// Per-version pattern: each version is an explicit, self-contained router
// file. There is no transparent fallback. See router_versioning.md.
func NewRouter() *gin.Engine {
	r := gin.Default()
	r.Use(apimetrics.GinMiddleware())
	r.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
	}))
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	return r
}

// SetupV3 registers every /api/v3 route on the given engine. v3 is the
// only live API version and contains the full surface inherited from
// the deleted v1+v2 routers (see plan E, 2026-04-27).
//
// Identity middleware (middleware.ResolveIdentity) is wired per route
// group — each group declares whether owner==principal, owner==bank
// for employees, or owner==URL-param-derived. Permissions use the
// typed perms.X.Y.Z constants from contract/permissions.
//
// To add a v4 router later: create router_v4.go with SetupV4(r, h),
// register the changed routes explicitly, and call SetupV4(r, h) from
// cmd/main.go alongside SetupV3(r, h). v3 keeps working untouched.
func SetupV3(r *gin.Engine, h *Handlers) {
	v3 := r.Group("/api/v3")

	// Forward X-Saga-* fault-injection headers to downstream saga executors.
	// No-op in production builds (saga.FaultsEnabled == false); active only in
	// the fault-enabled test image used by the SG-* saga integration suite.
	v3.Use(middleware.FaultHeaderForwarder())

	// ── Public auth routes (no middleware) ───────────────────────
	auth := v3.Group("/auth")
	{
		auth.POST("/login", h.Auth.Login)
		auth.POST("/refresh", h.Auth.RefreshToken)
		auth.POST("/logout", h.Auth.Logout)
		auth.POST("/password/reset-request", h.Auth.RequestPasswordReset)
		auth.POST("/password/reset", h.Auth.ResetPassword)
		auth.POST("/activate", h.Auth.ActivateAccount)
		auth.POST("/resend-activation", h.Auth.ResendActivationEmail)
	}

	// ── Public exchange rate routes (no middleware) ──────────────
	v3.GET("/exchange/rates", h.Exchange.ListExchangeRates)
	v3.GET("/exchange/rates/:from/:to", h.Exchange.GetExchangeRate)
	v3.POST("/exchange/calculate", h.Exchange.CalculateExchange)

	// ── /me/* (AnyAuthMiddleware) ────────────────────────────────
	me := v3.Group("/me")
	me.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
	{
		me.GET("", middleware.RequireClientToken(), h.Me.GetMe)

		// Accounts
		me.GET("/accounts", h.Account.ListMyAccounts)
		me.GET("/accounts/:id", h.Account.GetMyAccount)
		me.GET("/accounts/:id/activity", h.Account.GetMyAccountActivity)

		// Cards
		me.GET("/cards", h.Card.ListMyCards)
		me.GET("/cards/:id", h.Card.GetMyCard)
		me.POST("/cards/:id/pin", middleware.RequireClientToken(), h.Card.SetCardPin)
		me.POST("/cards/:id/verify-pin", middleware.RequireClientToken(), h.Card.VerifyCardPin)
		me.POST("/cards/:id/temporary-block", middleware.RequireClientToken(), h.Card.TemporaryBlockCard)
		me.POST("/cards/virtual", h.Card.CreateVirtualCard)
		me.POST("/cards/requests", middleware.RequireClientToken(), h.Card.CreateCardRequest)
		me.GET("/cards/requests", middleware.RequireClientToken(), h.Card.ListMyCardRequests)

		// Payments. Cross-bank money sends (to another person at another bank)
		// are payments: PeerTxDispatcherHandler detects a foreign 3-digit
		// prefix and dispatches to PeerTxService.InitiateOutboundTx, which
		// rejects an unregistered peer bank (404) before any debit. Intra-bank
		// payments run the standard flow.
		me.POST("/payments", h.PeerTxDispatcher.CreatePayment)
		me.POST("/payments/preview", h.Tx.PreviewPayment)
		me.GET("/payments", h.Tx.ListMyPayments)
		me.GET("/payments/:id", h.PeerTxDispatcher.GetPaymentByID)
		me.GET("/payments/:id/status", h.PeerTxDispatcher.GetPaymentStatusByID)
		me.POST("/payments/:id/execute", h.Tx.ExecutePayment)

		// Transfers are intra-bank, same-client only (e.g. between your own
		// RSD and EUR accounts, with FX). Cross-bank sends use /payments above.
		me.POST("/transfers", h.Tx.CreateTransfer)
		me.POST("/transfers/preview", h.Tx.PreviewTransfer)
		me.GET("/transfers", h.Tx.ListMyTransfers)
		me.GET("/transfers/:id", h.Tx.GetMyTransfer)
		me.GET("/transfers/:id/status", h.Tx.GetMyTransferStatus)
		me.POST("/transfers/:id/execute", h.Tx.ExecuteTransfer)

		// Payment recipients
		me.POST("/payment-recipients", h.Tx.CreateMyPaymentRecipient)
		me.GET("/payment-recipients", h.Tx.ListMyPaymentRecipients)
		me.PUT("/payment-recipients/:id", h.Tx.UpdatePaymentRecipient)
		me.DELETE("/payment-recipients/:id", h.Tx.DeletePaymentRecipient)

		// Loans
		me.POST("/loan-requests", h.Credit.CreateLoanRequest)
		me.GET("/loan-requests", h.Credit.ListMyLoanRequests)
		me.GET("/loans", h.Credit.ListMyLoans)
		me.GET("/loans/:id", h.Credit.GetMyLoan)
		me.GET("/loans/:id/installments", h.Credit.GetMyInstallments)

		// Stock orders
		// ResolveIdentity(OwnerIsBankIfEmployee): client principals own
		// their own portfolio data; employees act for the bank
		// (OwnerType="bank", OwnerID=nil) but their JWT id is carried
		// as ActingEmployeeID for per-actuary limits. (Spec C.)
		bankIfEmp := middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee)
		me.POST("/orders", bankIfEmp, h.StockOrder.CreateOrder)
		me.GET("/orders", bankIfEmp, h.StockOrder.ListMyOrders)
		me.GET("/orders/:id", bankIfEmp, h.StockOrder.GetMyOrder)
		me.POST("/orders/:id/cancel", bankIfEmp, h.StockOrder.CancelOrder)

		// Portfolio
		// GET /me/portfolio returns the unified portfolio (securities + fund positions)
		// for the caller. Replaced legacy ListHoldings with the unified handler so
		// the response now includes fund positions and P/L totals. The legacy
		// /me/portfolio/summary endpoint keeps the old summary view.
		me.GET("/portfolio", bankIfEmp, h.UnifiedPortfolio.GetMy)
		me.GET("/portfolio/summary", bankIfEmp, h.Portfolio.GetPortfolioSummary)
		// (Phase 8) /me/portfolio/:id/make-public deleted — use
		// POST /api/v3/me/otc/stocks with direction=sell.
		me.POST("/portfolio/:id/exercise", bankIfEmp, h.Portfolio.ExerciseOption)
		me.GET("/holdings/:id/transactions", bankIfEmp, h.Portfolio.ListHoldingTransactions)

		// Tax
		me.GET("/tax", bankIfEmp, h.Tax.ListMyTaxRecords)

		// Watchlist (personal list of tracked listings)
		me.GET("/watchlist", bankIfEmp, h.Watchlist.ListMy)
		me.POST("/watchlist", bankIfEmp, h.Watchlist.AddItem)
		me.DELETE("/watchlist/:listing_id", bankIfEmp, h.Watchlist.RemoveItem)

		// Price alerts (per-owner thresholds on listing price / daily-change %)
		me.GET("/price-alerts", bankIfEmp, h.PriceAlert.ListMy)
		me.POST("/price-alerts", bankIfEmp, h.PriceAlert.Create)
		me.GET("/price-alerts/:id", bankIfEmp, h.PriceAlert.Get)
		me.PUT("/price-alerts/:id", bankIfEmp, h.PriceAlert.Update)
		me.DELETE("/price-alerts/:id", bankIfEmp, h.PriceAlert.Delete)

		// Recurring securities orders (weekly/monthly Market-order templates).
		// CRUD + lifecycle (pause/resume/cancel). Cron tick fires per template;
		// production wiring of the order-placer is deferred — see plan B6.
		me.GET("/recurring-orders", bankIfEmp, h.RecurringOrder.ListMy)
		me.POST("/recurring-orders", bankIfEmp, h.RecurringOrder.Create)
		me.GET("/recurring-orders/:id", bankIfEmp, h.RecurringOrder.Get)
		me.POST("/recurring-orders/:id/pause", bankIfEmp, h.RecurringOrder.Pause)
		me.POST("/recurring-orders/:id/resume", bankIfEmp, h.RecurringOrder.Resume)
		me.POST("/recurring-orders/:id/cancel", bankIfEmp, h.RecurringOrder.Cancel)

		// Recurring fund investments (monthly DCA into a single fund)
		me.GET("/recurring-funds", h.RecurringFund.ListMy)
		me.POST("/recurring-funds", h.RecurringFund.Create)
		me.GET("/recurring-funds/:id", h.RecurringFund.Get)
		me.POST("/recurring-funds/:id/pause", h.RecurringFund.Pause)
		me.POST("/recurring-funds/:id/resume", h.RecurringFund.Resume)
		me.DELETE("/recurring-funds/:id", h.RecurringFund.Cancel)

		// Sessions
		me.GET("/sessions", h.Session.ListMySessions)
		me.DELETE("/sessions/:id", h.Session.RevokeSession)
		me.POST("/sessions/revoke-others", h.Session.RevokeAllSessions)
		me.GET("/login-history", h.Session.GetMyLoginHistory)

		// Notifications (general, persistent, read/unread)
		me.GET("/notifications", h.Notification.ListNotifications)
		me.GET("/notifications/unread-count", h.Notification.GetUnreadCount)
		me.POST("/notifications/read-all", h.Notification.MarkAllRead)
		me.POST("/notifications/:id/read", h.Notification.MarkRead)

		// Investment funds (Celina-4): caller's positions.
		me.GET("/investment-funds", bankIfEmp, h.Fund.ListMyPositions)
		// E4: caller's dividend payout history.
		me.GET("/dividends",
			middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee),
			h.Dividend.ListMyDividends)

		// OTC option trading (Spec 2): caller's offers/contracts.
		// (Phase 8) /me/otc/offers renamed to /me/otc/options.
		me.GET("/otc/options", bankIfEmp, h.Portfolio.ListMyOTCOptions)
		me.GET("/otc/options/posted", bankIfEmp, h.OTCOptions.ListMyPostedOffers)
		me.POST("/otc/options", bankIfEmp, h.OTCOptions.CreateOffer)
		me.GET("/otc/history", bankIfEmp, h.OTCOptions.ListNegotiationHistory)
		me.POST("/otc/ratings", bankIfEmp, h.OTCOptions.SubmitRating)
		me.GET("/otc/ratings/received", bankIfEmp, h.OTCOptions.ListMyReceivedRatings)
		me.GET("/otc/contracts", bankIfEmp, h.OTCOptions.ListMyContracts)
		// Cross-bank OTC trade status: resolve the SI-TX transaction id from a
		// cross-bank trade's poll_url via PeerTxService.GetTxStatus.
		me.GET("/otc/transactions/:txid/status", h.PeerTxDispatcher.GetCrossBankTxStatus)

		// --- Phase 2: parallel-negotiation-chain marketplace ---
		// Listing-poster sees all chains via the OPEN
		// /otc/options/:id/negotiations route (not under /me) so it's
		// reachable without owning the listing — gateway-side ownership
		// would block the poster from seeing their own listing's bids.
		// Per-chain actions stay under /me/ since they always act on
		// the caller's own chain (bid, counter, accept, reject, cancel).
		me.GET("/otc/options/negotiations", bankIfEmp, h.OTCOptions.ListMyNegotiations)
		me.GET("/otc/options/negotiations/:nid/revisions", bankIfEmp, h.OTCOptions.ListMyNegotiationRevisions)
		me.POST("/otc/options/:id/negotiations/:nid/counter", bankIfEmp, h.OTCOptions.CounterMyNegotiation)
		me.POST("/otc/options/:id/negotiations/:nid/accept", bankIfEmp, h.OTCOptions.AcceptMyNegotiation)
		me.POST("/otc/options/:id/negotiations/:nid/reject", bankIfEmp, h.OTCOptions.RejectMyNegotiation)
		me.DELETE("/otc/options/:id/negotiations/:nid", bankIfEmp, h.OTCOptions.CancelMyNegotiation)
		me.DELETE("/otc/options/:id", bankIfEmp, h.OTCOptions.CancelMyListing)

		// --- Phase 3: OTC stocks marketplace (sell + buy direction) ---
		me.GET("/otc/stocks", bankIfEmp, h.OTCStock.ListMyOTCStocks)
		me.POST("/otc/stocks", bankIfEmp, h.OTCStock.CreateOTCStockOffer)
		me.DELETE("/otc/stocks/:id", bankIfEmp, h.OTCStock.CancelOTCStockOffer)

		// Cross-bank OTC option exercise (Celina-5 SI-TX). Buyer-only
		// (only the buyer's bank holds direction=CREDIT contracts);
		// stock-service rejects non-buyer-side calls.
		me.POST("/otc/contracts/peer/:id/exercise", bankIfEmp, h.OTCOptions.ExercisePeerContract)

		// Cross-bank OTC negotiation initiation (Celina 5). Lets a
		// buyer at this bank kick off a negotiation against a peer's
		// listing — composes the SI-TX OtcOffer with the caller's
		// JWT identity as buyerId and HTTP-POSTs to the seller bank's
		// /api/v3/negotiations.
		me.POST("/peer-otc/negotiations", h.PeerOTCInitiate.CreatePeerNegotiation)
		// Discovery + client-facing negotiation controls (Phase 4 SI-TX
		// follow-up). Both the buyer and the seller's own bank surface
		// their own peer_otc_negotiations rows through these routes.
		me.GET("/peer-otc/negotiations", h.PeerOTCInitiate.ListMyPeerNegotiations)
		me.PUT("/peer-otc/negotiations/:rid/:id", h.PeerOTCInitiate.CounterPeerNegotiation)
		me.POST("/peer-otc/negotiations/:rid/:id/accept", h.PeerOTCInitiate.AcceptPeerNegotiation)
		me.DELETE("/peer-otc/negotiations/:rid/:id", h.PeerOTCInitiate.CancelPeerNegotiation)
	}

	// ── SI-TX canonical prefix ───────────────────────────────────────
	// All cross-bank wire-protocol routes live exclusively under
	// /cross-bank-protocol/... — there is no legacy alias. Cohort banks
	// MUST register this bank's base_url as .../api/v3/cross-bank-protocol
	// to interoperate. Legacy paths (/api/v3/interbank, /api/v3/public-stock,
	// etc.) were removed on 2026-05-29 per user direction.
	crossBank := v3.Group("/cross-bank-protocol")
	crossBank.Use(h.PeerAuthMW)
	{
		// SI-TX wire entry (NEW_TX / COMMIT_TX / ROLLBACK_TX / VOTE)
		crossBank.POST("/interbank", h.PeerTx.PostInterbank)
		// CHECK_STATUS: peer banks query state of a cross-bank TX (Celina-5 §"Mehanizam za Retry")
		crossBank.GET("/interbank/:transaction_id/status", h.PeerTxStatus.GetTxStatus)
		// OTC stock + option discovery (Phase 4 + 6)
		crossBank.GET("/public-stock", h.PeerOTC.GetPublicStocks)
		crossBank.GET("/public-option-offers", h.PeerOTC.GetPublicOptionOffers)
		// Cross-bank OTC negotiations (Phase 4)
		crossBank.POST("/negotiations", h.PeerOTC.CreateNegotiation)
		crossBank.PUT("/negotiations/:rid/:id", h.PeerOTC.UpdateNegotiation)
		crossBank.GET("/negotiations/:rid/:id", h.PeerOTC.GetNegotiation)
		crossBank.DELETE("/negotiations/:rid/:id", h.PeerOTC.DeleteNegotiation)
		crossBank.GET("/negotiations/:rid/:id/accept", h.PeerOTC.AcceptNegotiation)
		// Counterparty user identity lookup
		crossBank.GET("/user/:rid/:id", h.PeerUser.GetUser)
	}

	// ── Stock exchanges (AnyAuth — market data is browsable) ────
	stockExchanges := v3.Group("/stock-exchanges")
	stockExchanges.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
	{
		stockExchanges.GET("", h.StockExchange.ListExchanges)
		stockExchanges.GET("/:id", h.StockExchange.GetExchange)
	}

	// ── Securities (AnyAuth — market data is browsable) ─────────
	securities := v3.Group("/securities")
	securities.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
	{
		securities.GET("/stocks", h.Securities.ListStocks)
		securities.GET("/stocks/:id", h.Securities.GetStock)
		securities.GET("/stocks/:id/history", h.Securities.GetStockHistory)
		securities.GET("/futures", h.Securities.ListFutures)
		securities.GET("/futures/:id", h.Securities.GetFutures)
		securities.GET("/futures/:id/history", h.Securities.GetFuturesHistory)
		securities.GET("/forex", h.Securities.ListForexPairs)
		securities.GET("/forex/:id", h.Securities.GetForexPair)
		securities.GET("/forex/:id/history", h.Securities.GetForexPairHistory)
		securities.GET("/options", h.Securities.ListOptions)
		securities.GET("/options/:id", h.Securities.GetOption)
		securities.GET("/candles", h.Securities.GetCandles)
	}

	// ── OTC stocks marketplace (Phase 8 clean cut — replaces the
	// legacy /api/v3/otc/offers group). Browsing is AnyAuth; buying
	// requires the securities/otc trade permission.
	otcStocksRead := v3.Group("/otc/stocks")
	otcStocksRead.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
	{
		otcStocksRead.GET("", h.Portfolio.ListOTCOffers)
	}
	otcStocksTrade := v3.Group("/otc/stocks")
	otcStocksTrade.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
	otcStocksTrade.Use(middleware.RequirePermissionOrClient(middleware.PermAny, perms.Otc.Trade.Accept, perms.Securities.Trade.Any))
	otcStocksTrade.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
	{
		otcStocksTrade.POST("/:id/buy", h.Portfolio.BuyOTCOffer)
		// Phase 3B: fill a buy-direction offer with the caller's shares.
		// Seller-can-deliver check + cash-already-reserved guarantee live
		// inside OTCStockService.FillBuyOffer.
		otcStocksTrade.POST("/:id/sell", h.OTCStock.SellOTCStockOffer)
	}

	// ── OTC option trading (Spec 2) — read endpoints ─────────────
	otcRead := v3.Group("/otc")
	otcRead.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
	otcRead.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
	{
		// (Phase 8) /otc/offers/:id renamed to /otc/options/:id.
		otcRead.GET("/options/:id", h.OTCOptions.GetOffer)
		otcRead.GET("/contracts/:id", h.OTCOptions.GetContract)
		// Public trader profile — aggregate rating + recent comments.
		// Visible to all authenticated callers; useful for OTC discovery.
		otcRead.GET("/traders/:owner_type/:owner_id/rating", h.OTCOptions.GetTraderProfile)
		// Phase 2 marketplace: every chain against a parent listing.
		// Visible to all authenticated callers — used by the listing's
		// poster to see incoming bids, and by bidders to see what
		// counter-bids competitors have placed.
		otcRead.GET("/options/:id/negotiations", h.OTCOptions.ListNegotiationsOnListing)
		// Phase 6 marketplace: unified local + cross-bank discovery
		// of open option listings.
		otcRead.GET("/options", h.Portfolio.ListOTCOptions)
	}
	// Trading actions require both securities.trade AND otc.trade.
	otcOptionsTrade := v3.Group("/otc")
	otcOptionsTrade.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
	otcOptionsTrade.Use(middleware.RequirePermissionOrClient(middleware.PermAll, perms.Securities.Trade.Any, perms.Otc.Trade.Accept))
	otcOptionsTrade.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
	{
		// (Phase 8 clean cut) The legacy single-chain options ops
		// — POST /otc/offers + counter/accept/reject — are deleted.
		// CreateOffer is reachable as POST /api/v3/me/otc/options
		// (defined above in the /me group). Per-chain mutations live
		// at /api/v3/me/otc/options/:id/negotiations/:nid/{counter,
		// accept,reject} so frontends can never accidentally mix the
		// two negotiation models.
		otcOptionsTrade.POST("/contracts/:id/exercise", h.OTCOptions.ExerciseContract)
		// Phase 2 marketplace: open a new negotiation chain by bidding
		// on a listing. Many bidders can each open one chain.
		otcOptionsTrade.POST("/options/:id/bid", h.OTCOptions.OpenNegotiationChain)
	}

	// ── Mobile auth (public) ────────────────────────────────────
	mobileAuth := v3.Group("/mobile/auth")
	{
		mobileAuth.POST("/request-activation", h.MobileAuth.RequestActivation)
		mobileAuth.POST("/activate", h.MobileAuth.ActivateDevice)
		mobileAuth.POST("/refresh", h.MobileAuth.RefreshMobileToken)
	}

	// ── Mobile device management (MobileAuthMiddleware) ─────────
	mobileDevice := v3.Group("/mobile/device")
	mobileDevice.Use(middleware.MobileAuthMiddleware(h.Auth.Client()))
	{
		mobileDevice.GET("", h.MobileAuth.GetDeviceInfo)
		mobileDevice.POST("/deactivate", h.MobileAuth.DeactivateDevice)
		mobileDevice.POST("/transfer", h.MobileAuth.TransferDevice)
	}

	// ── Mobile device settings (MobileAuth + DeviceSignature) ────
	mobileDeviceSettings := v3.Group("/mobile/device")
	mobileDeviceSettings.Use(middleware.MobileAuthMiddleware(h.Auth.Client()))
	mobileDeviceSettings.Use(middleware.RequireDeviceSignature(h.Auth.Client()))
	{
		mobileDeviceSettings.POST("/biometrics", h.MobileAuth.SetBiometrics)
		mobileDeviceSettings.GET("/biometrics", h.MobileAuth.GetBiometrics)
	}

	// ── Mobile verifications (MobileAuth + DeviceSignature) ─────
	mobileVerify := v3.Group("/mobile/verifications")
	mobileVerify.Use(middleware.MobileAuthMiddleware(h.Auth.Client()))
	mobileVerify.Use(middleware.RequireDeviceSignature(h.Auth.Client()))
	{
		mobileVerify.GET("/pending", h.Verification.GetPendingVerifications)
		mobileVerify.POST("/:id/submit", h.Verification.SubmitMobileVerification)
		mobileVerify.POST("/:id/ack", h.Verification.AckVerification)
		mobileVerify.POST("/:id/biometric", h.Verification.BiometricVerify)
	}

	// ── QR verification (MobileAuth + DeviceSignature) ──────────
	qrVerify := v3.Group("/verify")
	qrVerify.Use(middleware.MobileAuthMiddleware(h.Auth.Client()))
	qrVerify.Use(middleware.RequireDeviceSignature(h.Auth.Client()))
	{
		qrVerify.POST("/:challenge_id", h.Verification.VerifyQR)
	}

	// ── Browser-facing verifications (AnyAuthMiddleware) ────────
	verifications := v3.Group("/verifications")
	verifications.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
	{
		verifications.POST("", h.Verification.CreateVerification)
		verifications.GET("/:id/status", h.Verification.GetVerificationStatus)
		verifications.POST("/:id/code", h.Verification.SubmitVerificationCode)
	}

	// ── Employee/admin routes (AuthMiddleware + RequirePermission) ─
	protected := v3.Group("/")
	protected.Use(middleware.AuthMiddleware(h.Auth.Client()))
	{
		// Employees
		employees := protected.Group("/employees")
		employees.Use(middleware.RequirePermission(perms.Employees.Read.All))
		{
			employees.GET("", h.Employee.ListEmployees)
			employees.GET("/:id", h.Employee.GetEmployee)
		}
		adminEmployees := protected.Group("/employees")
		adminEmployees.Use(middleware.RequirePermission(perms.Employees.Create.Any))
		{
			adminEmployees.POST("", h.Employee.CreateEmployee)
		}
		updateEmployees := protected.Group("/employees")
		updateEmployees.Use(middleware.RequirePermission(perms.Employees.Update.Any))
		{
			updateEmployees.PUT("/:id", h.Employee.UpdateEmployee)
		}

		// Role catalog (read)
		roleRead := protected.Group("/")
		roleRead.Use(middleware.RequirePermission(perms.Roles.Read.All))
		{
			roleRead.GET("/roles", h.Role.ListRoles)
			roleRead.GET("/roles/:id", h.Role.GetRole)
			roleRead.GET("/permissions", h.Role.ListPermissions)
		}
		// Role mutation
		roleCreate := protected.Group("/")
		roleCreate.Use(middleware.RequirePermission(perms.Roles.Update.Any))
		{
			roleCreate.POST("/roles", h.Role.CreateRole)
		}
		roleUpdate := protected.Group("/")
		roleUpdate.Use(middleware.RequireAnyPermission(perms.Roles.Permissions.Assign, perms.Roles.Permissions.Revoke, perms.Roles.Update.Any))
		{
			roleUpdate.PUT("/roles/:id/permissions", h.Role.UpdateRolePermissions)
		}
		// Granular role-permission management (one perm at a time, by role
		// ID). Each verb uses its dedicated catalog perm.
		rolePermAssign := protected.Group("/")
		rolePermAssign.Use(middleware.RequirePermission(perms.Roles.Permissions.Assign))
		{
			rolePermAssign.POST("/roles/:id/permissions", h.Role.AssignPermissionToRole)
		}
		rolePermRevoke := protected.Group("/")
		rolePermRevoke.Use(middleware.RequirePermission(perms.Roles.Permissions.Revoke))
		{
			rolePermRevoke.DELETE("/roles/:id/permissions/:permission", h.Role.RevokePermissionFromRole)
		}
		// Per-employee role/permission assignment
		empPermAssign := protected.Group("/")
		empPermAssign.Use(middleware.RequireAnyPermission(perms.Employees.Roles.Assign, perms.Employees.Permissions.Assign))
		{
			empPermAssign.PUT("/employees/:id/roles", h.Role.SetEmployeeRoles)
			empPermAssign.PUT("/employees/:id/permissions", h.Role.SetEmployeeAdditionalPermissions)
		}

		// Employee limit read
		empLimitsRead := protected.Group("/")
		empLimitsRead.Use(middleware.RequirePermission(perms.Limits.Employee.Read))
		{
			empLimitsRead.GET("/employees/:id/limits", h.Limit.GetEmployeeLimits)
		}
		// Employee limit update
		empLimitsUpdate := protected.Group("/")
		empLimitsUpdate.Use(middleware.RequirePermission(perms.Limits.Employee.Update))
		{
			empLimitsUpdate.PUT("/employees/:id/limits", h.Limit.SetEmployeeLimits)
			empLimitsUpdate.POST("/employees/:id/limits/template", h.Limit.ApplyLimitTemplate)
		}
		// Limit-template management
		limitTemplatesRead := protected.Group("/limits/templates")
		limitTemplatesRead.Use(middleware.RequireAnyPermission(perms.LimitTemplates.Create.Any, perms.LimitTemplates.Update.Any))
		{
			limitTemplatesRead.GET("", h.Limit.ListLimitTemplates)
		}
		limitTemplatesCreate := protected.Group("/limits/templates")
		limitTemplatesCreate.Use(middleware.RequirePermission(perms.LimitTemplates.Create.Any))
		{
			limitTemplatesCreate.POST("", h.Limit.CreateLimitTemplate)
		}
		// Client limits — gated by clients.update.limits (the catalog's
		// authoritative client-limit perm; no separate "limits.client.*" exists).
		clientLimitsRead := protected.Group("/")
		clientLimitsRead.Use(middleware.RequirePermission(perms.Clients.Update.Limits))
		{
			clientLimitsRead.GET("/clients/:id/limits", h.Limit.GetClientLimits)
		}
		clientLimitsUpdate := protected.Group("/")
		clientLimitsUpdate.Use(middleware.RequirePermission(perms.Clients.Update.Limits))
		{
			clientLimitsUpdate.PUT("/clients/:id/limits", h.Limit.SetClientLimits)
		}

		// Limit blueprint management — split per CRUD verb
		blueprintsRead := protected.Group("/blueprints")
		blueprintsRead.Use(middleware.RequireAnyPermission(perms.LimitTemplates.Create.Any, perms.LimitTemplates.Update.Any))
		{
			blueprintsRead.GET("", h.Blueprint.ListBlueprints)
			blueprintsRead.GET("/:id", h.Blueprint.GetBlueprint)
		}
		blueprintsCreate := protected.Group("/blueprints")
		blueprintsCreate.Use(middleware.RequirePermission(perms.LimitTemplates.Create.Any))
		{
			blueprintsCreate.POST("", h.Blueprint.CreateBlueprint)
		}
		blueprintsUpdate := protected.Group("/blueprints")
		blueprintsUpdate.Use(middleware.RequirePermission(perms.LimitTemplates.Update.Any))
		{
			blueprintsUpdate.PUT("/:id", h.Blueprint.UpdateBlueprint)
			blueprintsUpdate.POST("/:id/apply", h.Blueprint.ApplyBlueprint)
		}
		blueprintsDelete := protected.Group("/blueprints")
		blueprintsDelete.Use(middleware.RequirePermission(perms.LimitTemplates.Update.Any))
		{
			blueprintsDelete.DELETE("/:id", h.Blueprint.DeleteBlueprint)
		}

		// Client management
		clientsRead := protected.Group("/clients")
		clientsRead.Use(middleware.RequireAnyPermission(perms.Clients.Read.All, perms.Clients.Read.Assigned, perms.Clients.Read.Own))
		{
			clientsRead.GET("", h.Client.ListClients)
			clientsRead.GET("/:id", h.Client.GetClient)
		}
		// Client-scoped sub-collections — each gated by the perm of the
		// downstream resource (accounts/payments/transfers, loans, cards).
		// The Gin tree uses :id (matches /clients/:id elsewhere).
		clientsAccountsRead := protected.Group("/clients")
		clientsAccountsRead.Use(middleware.RequireAnyPermission(perms.Accounts.Read.All, perms.Accounts.Read.Own))
		{
			clientsAccountsRead.GET("/:id/accounts", h.Account.ListAccountsByClientPath)
			clientsAccountsRead.GET("/:id/payments", h.Tx.ListPaymentsByClientPath)
			clientsAccountsRead.GET("/:id/transfers", h.Tx.ListTransfersByClientPath)
		}
		clientsLoansRead := protected.Group("/clients")
		clientsLoansRead.Use(middleware.RequireAnyPermission(perms.Credits.Read.All, perms.Credits.Read.Own))
		{
			clientsLoansRead.GET("/:id/loans", h.Credit.ListLoansByClientPath)
		}
		clientsCardsRead := protected.Group("/clients")
		clientsCardsRead.Use(middleware.RequireAnyPermission(perms.Cards.Read.All, perms.Cards.Read.Own))
		{
			clientsCardsRead.GET("/:id/cards", h.Card.ListCardsByClientPath)
		}
		clientsCreate := protected.Group("/clients")
		clientsCreate.Use(middleware.RequirePermission(perms.Clients.Create.Any))
		{
			clientsCreate.POST("", h.Client.CreateClient)
		}
		clientsUpdate := protected.Group("/clients")
		clientsUpdate.Use(middleware.RequireAnyPermission(perms.Clients.Update.Profile, perms.Clients.Update.Contact))
		{
			clientsUpdate.PUT("/:id", h.Client.UpdateClient)
		}

		// Currencies (any authenticated employee)
		protected.GET("/currencies", h.Account.ListCurrencies)

		// Account management
		accountsRead := protected.Group("/accounts")
		accountsRead.Use(middleware.RequireAnyPermission(perms.Accounts.Read.All, perms.Accounts.Read.Own))
		{
			accountsRead.GET("", h.Account.ListAllAccounts)
			accountsRead.GET("/:id", h.Account.GetAccount)
			// Account-scoped sub-collections — resolve account ID → account_number
			// internally before calling the downstream RPCs.
			accountsRead.GET("/:id/payments", h.Tx.ListPaymentsByAccountPath)
			accountsRead.GET("/:id/cards", h.Card.ListCardsByAccountPath)
		}
		accountsCreate := protected.Group("/accounts")
		accountsCreate.Use(middleware.RequireAnyPermission(perms.Accounts.Create.Current, perms.Accounts.Create.Foreign))
		{
			accountsCreate.POST("", h.Account.CreateAccount)
		}
		accountsName := protected.Group("/accounts")
		accountsName.Use(middleware.RequirePermission(perms.Accounts.Update.Name))
		{
			accountsName.PUT("/:id/name", h.Account.UpdateAccountName)
		}
		accountsLimits := protected.Group("/accounts")
		accountsLimits.Use(middleware.RequirePermission(perms.Accounts.Update.Limits))
		{
			accountsLimits.PUT("/:id/limits", h.Account.UpdateAccountLimits)
		}
		// Activate/deactivate action pair. Both cluster under deactivate.any:
		// changing account status is a deactivation-class privilege.
		accountsStatus := protected.Group("/accounts")
		accountsStatus.Use(middleware.RequirePermission(perms.Accounts.Deactivate.Any))
		{
			accountsStatus.POST("/:id/activate", h.Account.ActivateAccount)
			accountsStatus.POST("/:id/deactivate", h.Account.DeactivateAccount)
		}

		// Companies
		companiesEmployee := protected.Group("/companies")
		companiesEmployee.Use(middleware.RequireAnyPermission(perms.Accounts.Create.Current, perms.Accounts.Create.Foreign))
		{
			companiesEmployee.POST("", h.Account.CreateCompany)
		}

		// Bank account management — single umbrella perm.
		bankAccountsRead := protected.Group("/bank-accounts")
		bankAccountsRead.Use(middleware.RequirePermission(perms.BankAccounts.Manage.Any))
		{
			bankAccountsRead.GET("", h.Account.ListBankAccounts)
			bankAccountsRead.GET("/:id/activity", h.Account.GetBankAccountActivity)
		}
		bankAccountsCreate := protected.Group("/bank-accounts")
		bankAccountsCreate.Use(middleware.RequirePermission(perms.BankAccounts.Manage.Any))
		{
			bankAccountsCreate.POST("", h.Account.CreateBankAccount)
		}
		bankAccountsDeactivate := protected.Group("/bank-accounts")
		bankAccountsDeactivate.Use(middleware.RequirePermission(perms.BankAccounts.Manage.Any))
		{
			bankAccountsDeactivate.DELETE("/:id", h.Account.DeleteBankAccount)
		}

		// Notification templates — admin-managed copy + variable discovery.
		notifTemplates := protected.Group("/notification-templates")
		notifTemplates.Use(middleware.RequirePermission(perms.Notifications.Templates.Manage))
		{
			notifTemplates.GET("", h.Notification.ListNotificationTemplates)
			notifTemplates.GET("/:channel/:type", h.Notification.GetNotificationTemplate)
			notifTemplates.PUT("/:channel/:type", h.Notification.SetNotificationTemplate)
			notifTemplates.DELETE("/:channel/:type", h.Notification.ResetNotificationTemplate)
		}

		// ── SI-TX peer-banks admin (Phase 2 Task 14) ───────────────────
		peerBanksAdmin := protected.Group("/peer-banks")
		peerBanksAdmin.Use(middleware.RequirePermission(perms.PeerBanks.Manage.Any))
		{
			peerBanksAdmin.GET("", h.PeerBankAdmin.List)
			peerBanksAdmin.GET("/:id", h.PeerBankAdmin.Get)
			peerBanksAdmin.POST("", h.PeerBankAdmin.Create)
			peerBanksAdmin.PUT("/:id", h.PeerBankAdmin.Update)
			peerBanksAdmin.DELETE("/:id", h.PeerBankAdmin.Delete)
		}

		// Cards management — list endpoints moved to path-scoped variants:
		//   GET /clients/:id/cards   (by client)
		//   GET /accounts/:id/cards  (by account)
		// (No bare ListAllCards RPC exists in card-service.)
		cardsRead := protected.Group("/cards")
		cardsRead.Use(middleware.RequireAnyPermission(perms.Cards.Read.All, perms.Cards.Read.Own))
		{
			cardsRead.GET("/:id", h.Card.GetCard)
		}
		cardsCreate := protected.Group("/cards")
		cardsCreate.Use(middleware.RequireAnyPermission(perms.Cards.Create.Physical, perms.Cards.Create.Virtual))
		{
			cardsCreate.POST("", h.Card.CreateCard)
			cardsCreate.POST("/authorized-persons", h.Card.CreateAuthorizedPerson)
		}
		cardsBlock := protected.Group("/cards")
		cardsBlock.Use(middleware.RequirePermission(perms.Cards.Block.Any))
		{
			cardsBlock.POST("/:id/block", h.Card.BlockCard)
		}
		cardsUnblock := protected.Group("/cards")
		cardsUnblock.Use(middleware.RequirePermission(perms.Cards.Unblock.Any))
		{
			cardsUnblock.POST("/:id/unblock", h.Card.UnblockCard)
		}
		// Deactivate clusters with block.any (terminating a card is a
		// supervisor-class block-equivalent action); no catalog perm exists
		// specifically for deactivate.
		cardsDeactivate := protected.Group("/cards")
		cardsDeactivate.Use(middleware.RequirePermission(perms.Cards.Block.Any))
		{
			cardsDeactivate.POST("/:id/deactivate", h.Card.DeactivateCard)
		}

		// Card requests — read/approve/reject split
		cardsRequestsRead := protected.Group("/cards/requests")
		cardsRequestsRead.Use(middleware.RequireAnyPermission(perms.Cards.Approve.Physical, perms.Cards.Approve.Virtual, perms.Cards.Read.All))
		{
			cardsRequestsRead.GET("", h.Card.ListCardRequests)
			cardsRequestsRead.GET("/:id", h.Card.GetCardRequest)
		}
		cardsRequestsApprove := protected.Group("/cards/requests")
		cardsRequestsApprove.Use(middleware.RequireAnyPermission(perms.Cards.Approve.Physical, perms.Cards.Approve.Virtual))
		{
			cardsRequestsApprove.POST("/:id/approve", h.Card.ApproveCardRequest)
		}
		cardsRequestsReject := protected.Group("/cards/requests")
		cardsRequestsReject.Use(middleware.RequireAnyPermission(perms.Cards.Approve.Physical, perms.Cards.Approve.Virtual))
		{
			cardsRequestsReject.POST("/:id/reject", h.Card.RejectCardRequest)
		}

		// Payments (employee read) — list endpoints moved to path-scoped variants:
		//   GET /clients/:id/payments   (by client)
		//   GET /accounts/:id/payments  (by account, with rich filters)
		paymentsRead := protected.Group("/payments")
		paymentsRead.Use(middleware.RequirePermission(perms.Accounts.Read.All))
		{
			paymentsRead.GET("/:id", h.Tx.GetPayment)
		}

		// Transfers (employee read) — list endpoint moved to path-scoped variant:
		//   GET /clients/:id/transfers  (by client)
		transfersRead := protected.Group("/transfers")
		transfersRead.Use(middleware.RequirePermission(perms.Accounts.Read.All))
		{
			transfersRead.GET("/:id", h.Tx.GetTransfer)
		}

		// Transfer fee management — split per CRUD verb.
		feesRead := protected.Group("/fees")
		feesRead.Use(middleware.RequireAnyPermission(perms.Fees.Create.Any, perms.Fees.Update.Any))
		{
			feesRead.GET("", h.Tx.ListFees)
		}
		feesCreate := protected.Group("/fees")
		feesCreate.Use(middleware.RequirePermission(perms.Fees.Create.Any))
		{
			feesCreate.POST("", h.Tx.CreateFee)
		}
		feesUpdate := protected.Group("/fees")
		feesUpdate.Use(middleware.RequirePermission(perms.Fees.Update.Any))
		{
			feesUpdate.PUT("/:id", h.Tx.UpdateFee)
		}
		feesDelete := protected.Group("/fees")
		feesDelete.Use(middleware.RequirePermission(perms.Fees.Update.Any))
		{
			feesDelete.DELETE("/:id", h.Tx.DeleteFee)
		}

		// Loans (employee read)
		loansRead := protected.Group("/loans")
		loansRead.Use(middleware.RequireAnyPermission(perms.Credits.Read.All, perms.Credits.Read.Own))
		{
			loansRead.GET("", h.Credit.ListAllLoans)
			loansRead.GET("/:id", h.Credit.GetLoan)
			loansRead.GET("/:id/installments", h.Credit.GetInstallmentsByLoan)
		}

		// Loan requests — read/approve/reject split
		loanRequestsRead := protected.Group("/loan-requests")
		loanRequestsRead.Use(middleware.RequireAnyPermission(perms.Credits.Read.All, perms.Credits.Read.Own))
		{
			loanRequestsRead.GET("", h.Credit.ListLoanRequests)
			loanRequestsRead.GET("/:id", h.Credit.GetLoanRequest)
		}
		loanRequestsApprove := protected.Group("/loan-requests")
		loanRequestsApprove.Use(middleware.RequireAnyPermission(
			perms.Credits.Approve.Cash, perms.Credits.Approve.Housing))
		{
			loanRequestsApprove.POST("/:id/approve", h.Credit.ApproveLoanRequest)
		}
		loanRequestsReject := protected.Group("/loan-requests")
		loanRequestsReject.Use(middleware.RequireAnyPermission(perms.Credits.Approve.Cash, perms.Credits.Approve.Housing))
		{
			loanRequestsReject.POST("/:id/reject", h.Credit.RejectLoanRequest)
		}

		// Interest rate tier management
		rateTiersRead := protected.Group("/interest-rate-tiers")
		rateTiersRead.Use(middleware.RequirePermission(perms.Credits.Disburse.Any))
		{
			rateTiersRead.GET("", h.Credit.ListInterestRateTiers)
		}
		rateTiersUpdate := protected.Group("/interest-rate-tiers")
		rateTiersUpdate.Use(middleware.RequirePermission(perms.Credits.Disburse.Any))
		{
			rateTiersUpdate.POST("", h.Credit.CreateInterestRateTier)
			rateTiersUpdate.PUT("/:id", h.Credit.UpdateInterestRateTier)
			rateTiersUpdate.DELETE("/:id", h.Credit.DeleteInterestRateTier)
			rateTiersUpdate.POST("/:id/apply", h.Credit.ApplyVariableRateUpdate)
		}

		// Bank margin management
		bankMarginsRead := protected.Group("/bank-margins")
		bankMarginsRead.Use(middleware.RequirePermission(perms.Credits.Disburse.Any))
		{
			bankMarginsRead.GET("", h.Credit.ListBankMargins)
		}
		bankMarginsUpdate := protected.Group("/bank-margins")
		bankMarginsUpdate.Use(middleware.RequirePermission(perms.Credits.Disburse.Any))
		{
			bankMarginsUpdate.PUT("/:id", h.Credit.UpdateBankMargin)
		}

		// Stock exchange management
		stockExchangeRead := protected.Group("/stock-exchanges")
		stockExchangeRead.Use(middleware.RequirePermission(perms.Securities.Manage.Catalog))
		{
			stockExchangeRead.GET("/testing-mode", h.StockExchange.GetTestingMode)
		}
		stockExchangeToggle := protected.Group("/stock-exchanges")
		stockExchangeToggle.Use(middleware.RequirePermission(perms.Securities.Manage.Catalog))
		{
			stockExchangeToggle.POST("/testing-mode", h.StockExchange.SetTestingMode)
		}

		// Stock-source management. Plural collection; the active source lives
		// under /active so a future "list all known sources" GET can sit at
		// the bare collection without ambiguity.
		stockSources := protected.Group("/stock-sources")
		stockSources.Use(middleware.RequirePermission(perms.Securities.Manage.Catalog))
		{
			stockSources.POST("", h.StockSource.SwitchSource)
			stockSources.GET("/active", h.StockSource.GetSourceStatus)
		}

		// Orders — on-behalf-of-client (and bank by default).
		ordersOnBehalf := protected.Group("/orders")
		ordersOnBehalf.Use(middleware.RequireAnyPermission(
			perms.Orders.Place.OnBehalfClient, perms.Orders.Place.OnBehalfBank))
		ordersOnBehalf.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
		{
			ordersOnBehalf.POST("", h.StockOrder.CreateOrderOnBehalf)
		}

		// OTC stocks — employee on-behalf buying (Phase 8 rename:
		// /otc/offers/:id/buy-on-behalf moved here).
		otcStocksOnBehalf := protected.Group("/otc/stocks")
		otcStocksOnBehalf.Use(middleware.RequireAnyPermission(
			perms.Otc.Trade.Accept, perms.Otc.Trade.OnBehalf))
		otcStocksOnBehalf.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
		{
			otcStocksOnBehalf.POST("/:id/buy-on-behalf", h.Portfolio.BuyOTCOfferOnBehalf)
		}

		// Order management (supervisor) — read split from approve/reject.
		ordersRead := protected.Group("/orders")
		ordersRead.Use(middleware.RequirePermission(perms.Orders.Read.All))
		{
			ordersRead.GET("", h.StockOrder.ListOrders)
		}
		ordersApprove := protected.Group("/orders")
		ordersApprove.Use(middleware.RequirePermission(perms.Orders.Cancel.All))
		ordersApprove.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
		{
			ordersApprove.POST("/:id/approve", h.StockOrder.ApproveOrder)
		}
		ordersReject := protected.Group("/orders")
		ordersReject.Use(middleware.RequirePermission(perms.Orders.Cancel.All))
		ordersReject.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
		{
			ordersReject.POST("/:id/reject", h.StockOrder.RejectOrder)
		}

		// Actuary (agent) management
		actuariesRead := protected.Group("/actuaries")
		actuariesRead.Use(middleware.RequirePermission(perms.Actuaries.Read.All))
		{
			actuariesRead.GET("", h.Actuary.ListActuaries)
			// Performance read sits with the rest of /actuaries — supervisors
			// have actuaries.read.all so they reach this group.
			actuariesRead.GET("/performance", h.Fund.ActuaryPerformance)
		}
		actuariesAssign := protected.Group("/actuaries")
		actuariesAssign.Use(middleware.RequirePermission(perms.Actuaries.Manage.Any))
		{
			actuariesAssign.PUT("/:id/limit", h.Actuary.SetActuaryLimit)
			// Approval action pair — bodyless POST, idempotent.
			actuariesAssign.POST("/:id/require-approval", h.Actuary.RequireApproval)
			actuariesAssign.POST("/:id/skip-approval", h.Actuary.SkipApproval)
		}
		actuariesUnassign := protected.Group("/actuaries")
		actuariesUnassign.Use(middleware.RequirePermission(perms.Actuaries.Manage.Any))
		{
			actuariesUnassign.POST("/:id/reset-limit", h.Actuary.ResetActuaryLimit)
		}

		// Tax management
		taxRead := protected.Group("/tax")
		taxRead.Use(middleware.RequirePermission(perms.Securities.Read.HoldingsAll))
		{
			taxRead.GET("", h.Tax.ListTaxRecords)
		}
		taxCollect := protected.Group("/tax")
		taxCollect.Use(middleware.RequirePermission(perms.Securities.Manage.Catalog))
		{
			taxCollect.POST("/collect", h.Tax.CollectTax)
		}

		// ── Changelog endpoints ─────────────────────────────────────
		changelogAccounts := protected.Group("/accounts")
		changelogAccounts.Use(middleware.RequirePermission(perms.Accounts.Read.All))
		{
			changelogAccounts.GET("/:id/changelog", h.Changelog.GetAccountChangelog)
		}
		changelogEmployees := protected.Group("/employees")
		changelogEmployees.Use(middleware.RequirePermission(perms.Employees.Read.All))
		{
			changelogEmployees.GET("/:id/changelog", h.Changelog.GetEmployeeChangelog)
		}
		changelogClients := protected.Group("/clients")
		changelogClients.Use(middleware.RequirePermission(perms.Clients.Read.All))
		{
			changelogClients.GET("/:id/changelog", h.Changelog.GetClientChangelog)
		}
		changelogCards := protected.Group("/cards")
		changelogCards.Use(middleware.RequirePermission(perms.Cards.Read.All))
		{
			changelogCards.GET("/:id/changelog", h.Changelog.GetCardChangelog)
		}
		changelogLoans := protected.Group("/loans")
		changelogLoans.Use(middleware.RequirePermission(perms.Credits.Read.All))
		{
			changelogLoans.GET("/:id/changelog", h.Changelog.GetLoanChangelog)
		}

		// ── Unified portfolio routes (B6 — 2026-05-28) ────────────────
		// Static paths (/bank, /client/:id, /investment-fund/:id) are
		// registered BEFORE the wildcard /portfolio/:portfolio_id so
		// Gin's static-segment-wins rule resolves correctly.
		//
		// All routes use AuthMiddleware (employee only) because:
		//   - clients use GET /me/portfolio (above)
		//   - employees need portfolio.view_client or portfolio.view_fund
		//     to view portfolios other than the bank's own
		//
		// ResolveIdentity(OwnerIsBankIfEmployee) is wired per route
		// group below so identity is available to enforcePortfolioAccess.
		portfolioBank := protected.Group("/portfolio")
		portfolioBank.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
		{
			portfolioBank.GET("/bank", h.UnifiedPortfolio.GetBank)
			portfolioBank.GET("/client/:client_id", h.UnifiedPortfolio.GetByClientID)
			portfolioBank.GET("/investment-fund/:fund_id", h.UnifiedPortfolio.GetByFundID)
			portfolioBank.GET("/:portfolio_id", h.UnifiedPortfolio.GetByPortfolioID)
		}

		// ── Watchlist by portfolio_id (B7 — 2026-05-28) ─────────────
		// The /me/watchlist routes above remain untouched.
		// This route allows employees (with the appropriate portfolio perm)
		// to view any owner's watchlist via the portfolio-id model.
		watchlistByPortfolio := protected.Group("/watchlist")
		watchlistByPortfolio.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
		{
			watchlistByPortfolio.GET("/:portfolio_id", h.Watchlist.GetByPortfolioID)
		}

		// ── Investment funds (Celina-4) ────────────────────────────
		// Manage (create/update) requires funds.manage.catalog.
		fundsManage := protected.Group("/investment-funds")
		fundsManage.Use(middleware.RequirePermission(perms.Funds.Manage.Catalog))
		{
			fundsManage.POST("", h.Fund.CreateFund)
			fundsManage.PUT("/:id", h.Fund.UpdateFund)
		}

		// Bank-position read. The /actuaries/performance route lives under
		// the actuariesRead group above — same URL, sits with its sibling
		// actuary endpoints rather than under the funds umbrella.
		fundsBank := protected.Group("/")
		fundsBank.Use(middleware.RequirePermission(perms.Funds.Read.All))
		{
			fundsBank.GET("/investment-funds/positions", h.Fund.ListBankPositions)
		}

		// ── Options trading (by option_id) ──────────────────────────
		opts := protected.Group("/options")
		opts.Use(middleware.RequireAnyPermission(perms.Otc.Trade.Accept, perms.Otc.Trade.Exercise, perms.Securities.Trade.Any))
		opts.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
		{
			opts.POST("/:option_id/orders", h.OptionsV2.CreateOrder)
			opts.POST("/:option_id/exercise", h.OptionsV2.Exercise)
		}

		// ── Admin cron viewer (C9/C10 — 2026-05-28) ─────────────────────
		// Three separate sub-groups let us apply distinct permissions to
		// read vs trigger vs manage (pause/resume) routes.
		adminCronRead := protected.Group("/admin/crons")
		adminCronRead.Use(middleware.RequirePermission(perms.Admin.Crons.View))
		{
			adminCronRead.GET("", h.AdminCron.List)
			adminCronRead.GET("/:service/:name", h.AdminCron.Get)
		}
		adminCronTrigger := protected.Group("/admin/crons")
		adminCronTrigger.Use(middleware.RequirePermission(perms.Admin.Crons.Trigger))
		{
			adminCronTrigger.POST("/:service/:name/trigger", h.AdminCron.Trigger)
		}
		adminCronManage := protected.Group("/admin/crons")
		adminCronManage.Use(middleware.RequirePermission(perms.Admin.Crons.Manage))
		{
			adminCronManage.POST("/:service/:name/pause", h.AdminCron.Pause)
			adminCronManage.POST("/:service/:name/resume", h.AdminCron.Resume)
		}

		// ── Admin audit log viewer (D4 — 2026-05-28) ──────────────────────
		// All six routes require admin.audit.view. Returns full changelog
		// tables (paginated, filterable) without scoping to a single entity.
		auditAdmin := protected.Group("/admin/audit")
		auditAdmin.Use(middleware.RequirePermission(perms.Admin.Audit.View))
		{
			auditAdmin.GET("/clients-changelog", h.AdminAudit.ListClientsChangelog)
			auditAdmin.GET("/accounts-changelog", h.AdminAudit.ListAccountsChangelog)
			auditAdmin.GET("/cards-changelog", h.AdminAudit.ListCardsChangelog)
			auditAdmin.GET("/loans-changelog", h.AdminAudit.ListLoansChangelog)
			auditAdmin.GET("/employees-changelog", h.AdminAudit.ListEmployeesChangelog)
			auditAdmin.GET("/cron-actions", h.AdminAudit.ListCronActions)
			auditAdmin.GET("/saga-logs", h.AdminAudit.ListSagaLogs)
		}
	}

	// ── Investment fund browsing + invest/redeem (AnyAuth) ──────
	// Browsing (List/Get) doesn't read identity; invest/redeem do.
	fundsAny := v3.Group("/investment-funds")
	fundsAny.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
	{
		fundsAny.GET("", h.Fund.ListFunds)
		fundsAny.GET("/:id", h.Fund.GetFund)
		fundsAny.POST("/:id/invest",
			middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee),
			h.Fund.Invest)
		fundsAny.POST("/:id/redeem",
			middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee),
			h.Fund.Redeem)
		// E4: fund dividend history (AnyAuth — fund manager + portfolio.view_fund)
		fundsAny.GET("/:id/dividends", h.Dividend.ListFundDividends)
	}

	// ── Dividend admin routes (E4 — 2026-05-28) ───────────────────
	dividendAdmin := protected.Group("/admin/dividends")
	dividendAdmin.Use(middleware.RequirePermission(perms.Securities.Manage.Catalog))
	{
		dividendAdmin.POST("", h.Dividend.DeclareDividend)
		dividendAdmin.POST("/:id/payout", h.Dividend.PayoutDividend)
	}

	// Catch-all 404 for any path not served by v3 or the swagger UI.
	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{"code": "not_found", "message": "route not found"},
		})
	})
}
