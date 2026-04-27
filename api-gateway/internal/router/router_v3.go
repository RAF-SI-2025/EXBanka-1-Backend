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

// notImplemented returns a 501 handler for endpoints planned but not yet backed by gRPC.
func notImplemented(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": gin.H{
			"code":    "not_implemented",
			"message": "this endpoint is coming in a future release",
		},
	})
}

// ── Named placeholder handlers with Swagger annotations ─────────────────────

// @Summary      Get account changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Account ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v3/accounts/{id}/changelog [get]
func v3GetAccountChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get employee changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Employee ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v3/employees/{id}/changelog [get]
func v3GetEmployeeChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get client changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Client ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v3/clients/{id}/changelog [get]
func v3GetClientChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get card changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Card ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v3/cards/{id}/changelog [get]
func v3GetCardChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get loan changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Loan ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v3/loans/{id}/changelog [get]
func v3GetLoanChangelog(c *gin.Context) { notImplemented(c) }

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

		// Payments
		me.POST("/payments", h.Tx.CreatePayment)
		me.GET("/payments", h.Tx.ListMyPayments)
		me.GET("/payments/:id", h.Tx.GetMyPayment)
		me.POST("/payments/:id/execute", h.Tx.ExecutePayment)

		// Transfers — inter-bank-aware. Detects inter-bank by 3-digit
		// receiverAccount prefix and routes accordingly. Falls through
		// to the regular intra-bank handler for own-bank prefixes.
		me.POST("/transfers", h.InterBankPub.CreateTransfer)
		me.POST("/transfers/preview", h.Tx.PreviewTransfer)
		me.GET("/transfers", h.Tx.ListMyTransfers)
		me.GET("/transfers/:id", h.InterBankPub.GetTransferByID)
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
		me.GET("/portfolio", bankIfEmp, h.Portfolio.ListHoldings)
		me.GET("/portfolio/summary", bankIfEmp, h.Portfolio.GetPortfolioSummary)
		me.POST("/portfolio/:id/make-public", bankIfEmp, h.Portfolio.MakePublic)
		me.POST("/portfolio/:id/exercise", bankIfEmp, h.Portfolio.ExerciseOption)
		me.GET("/holdings/:id/transactions", bankIfEmp, h.Portfolio.ListHoldingTransactions)

		// Tax
		me.GET("/tax", bankIfEmp, h.Tax.ListMyTaxRecords)

		// Sessions
		me.GET("/sessions", h.Session.ListMySessions)
		me.POST("/sessions/revoke", h.Session.RevokeSession)
		me.POST("/sessions/revoke-others", h.Session.RevokeAllSessions)
		me.GET("/login-history", h.Session.GetMyLoginHistory)

		// Notifications (general, persistent, read/unread)
		me.GET("/notifications", h.Notification.ListNotifications)
		me.GET("/notifications/unread-count", h.Notification.GetUnreadCount)
		me.POST("/notifications/read-all", h.Notification.MarkAllRead)
		me.POST("/notifications/:id/read", h.Notification.MarkRead)

		// Investment funds (Celina-4): caller's positions.
		me.GET("/investment-funds", bankIfEmp, h.Fund.ListMyPositions)

		// OTC option trading (Spec 2): caller's offers/contracts.
		me.GET("/otc/offers", bankIfEmp, h.OTCOptions.ListMyOffers)
		me.GET("/otc/contracts", bankIfEmp, h.OTCOptions.ListMyContracts)
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

	// ── OTC (AnyAuth for browsing, securities.trade for buying) ─
	otc := v3.Group("/otc/offers")
	otc.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
	{
		otc.GET("", h.Portfolio.ListOTCOffers)
	}
	otcTrade := v3.Group("/otc/offers")
	otcTrade.Use(middleware.AuthMiddleware(h.Auth.Client()))
	otcTrade.Use(middleware.RequireAnyPermission(perms.Otc.Trade.Accept, perms.Securities.Trade.Any))
	otcTrade.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
	{
		otcTrade.POST("/:id/buy", h.Portfolio.BuyOTCOffer)
	}

	// ── OTC option trading (Spec 2) — read endpoints ─────────────
	otcRead := v3.Group("/otc")
	otcRead.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
	otcRead.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
	{
		otcRead.GET("/offers/:id", h.OTCOptions.GetOffer)
		otcRead.GET("/contracts/:id", h.OTCOptions.GetContract)
	}
	// Trading actions require both securities.trade AND otc.trade.
	otcOptionsTrade := v3.Group("/otc")
	otcOptionsTrade.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
	otcOptionsTrade.Use(middleware.RequireAllPermissions(perms.Securities.Trade.Any, perms.Otc.Trade.Accept))
	otcOptionsTrade.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
	{
		otcOptionsTrade.POST("/offers", h.OTCOptions.CreateOffer)
		otcOptionsTrade.POST("/offers/:id/counter", h.OTCOptions.CounterOffer)
		otcOptionsTrade.POST("/offers/:id/accept", h.OTCOptions.AcceptOffer)
		otcOptionsTrade.POST("/offers/:id/reject", h.OTCOptions.RejectOffer)
		otcOptionsTrade.POST("/contracts/:id/exercise", h.OTCOptions.ExerciseContract)
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
		// name). Each verb uses its dedicated catalog perm.
		rolePermAssign := protected.Group("/")
		rolePermAssign.Use(middleware.RequirePermission(perms.Roles.Permissions.Assign))
		{
			rolePermAssign.POST("/roles/:role_name/permissions", h.Role.AssignPermissionToRole)
		}
		rolePermRevoke := protected.Group("/")
		rolePermRevoke.Use(middleware.RequirePermission(perms.Roles.Permissions.Revoke))
		{
			rolePermRevoke.DELETE("/roles/:role_name/permissions/:permission", h.Role.RevokePermissionFromRole)
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
			accountsRead.GET("/by-number/:account_number", h.Account.GetAccountByNumber)
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
		// Status updates cluster under deactivate.any: changing account status
		// (active ↔ inactive) is an deactivation-class privilege.
		accountsStatus := protected.Group("/accounts")
		accountsStatus.Use(middleware.RequirePermission(perms.Accounts.Deactivate.Any))
		{
			accountsStatus.PUT("/:id/status", h.Account.UpdateAccountStatus)
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

		// Cards management
		cardsRead := protected.Group("/cards")
		cardsRead.Use(middleware.RequireAnyPermission(perms.Cards.Read.All, perms.Cards.Read.Own))
		{
			cardsRead.GET("", h.Card.ListCards)
			cardsRead.GET("/:id", h.Card.GetCard)
		}
		cardsCreate := protected.Group("/cards")
		cardsCreate.Use(middleware.RequireAnyPermission(perms.Cards.Create.Physical, perms.Cards.Create.Virtual))
		{
			cardsCreate.POST("", h.Card.CreateCard)
			cardsCreate.POST("/authorized-person", h.Card.CreateAuthorizedPerson)
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

		// Payments (employee read)
		paymentsRead := protected.Group("/payments")
		paymentsRead.Use(middleware.RequirePermission(perms.Accounts.Read.All))
		{
			paymentsRead.GET("", h.Tx.ListPayments)
			paymentsRead.GET("/:id", h.Tx.GetPayment)
		}

		// Transfers (employee read)
		transfersRead := protected.Group("/transfers")
		transfersRead.Use(middleware.RequirePermission(perms.Accounts.Read.All))
		{
			transfersRead.GET("", h.Tx.ListTransfers)
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

		// Admin stock-source management
		adminStockSource := protected.Group("/admin/stock-source")
		adminStockSource.Use(middleware.RequirePermission(perms.Securities.Manage.Catalog))
		{
			adminStockSource.POST("", h.StockSource.SwitchSource)
			adminStockSource.GET("", h.StockSource.GetSourceStatus)
		}

		// Orders — on-behalf-of-client (and bank by default).
		ordersOnBehalf := protected.Group("/orders")
		ordersOnBehalf.Use(middleware.RequireAnyPermission(
			perms.Orders.Place.OnBehalfClient, perms.Orders.Place.OnBehalfBank))
		ordersOnBehalf.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
		{
			ordersOnBehalf.POST("", h.StockOrder.CreateOrderOnBehalf)
		}

		// OTC — employee on-behalf buying.
		otcOnBehalf := protected.Group("/otc/admin/offers")
		otcOnBehalf.Use(middleware.RequireAnyPermission(
			perms.Otc.Trade.Accept, perms.Orders.Place.OnBehalfClient))
		otcOnBehalf.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
		{
			otcOnBehalf.POST("/:id/buy", h.Portfolio.BuyOTCOfferOnBehalf)
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
			ordersReject.POST("/:id/decline", h.StockOrder.DeclineOrder)
		}

		// Actuary (agent) management
		actuariesRead := protected.Group("/actuaries")
		actuariesRead.Use(middleware.RequirePermission(perms.Employees.Read.All))
		{
			actuariesRead.GET("", h.Actuary.ListActuaries)
		}
		actuariesAssign := protected.Group("/actuaries")
		actuariesAssign.Use(middleware.RequirePermission(perms.Employees.Update.Any))
		{
			actuariesAssign.PUT("/:id/limit", h.Actuary.SetActuaryLimit)
			actuariesAssign.PUT("/:id/approval", h.Actuary.SetNeedApproval)
		}
		actuariesUnassign := protected.Group("/actuaries")
		actuariesUnassign.Use(middleware.RequirePermission(perms.Employees.Update.Any))
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
			changelogAccounts.GET("/:id/changelog", v3GetAccountChangelog)
		}
		changelogEmployees := protected.Group("/employees")
		changelogEmployees.Use(middleware.RequirePermission(perms.Employees.Read.All))
		{
			changelogEmployees.GET("/:id/changelog", v3GetEmployeeChangelog)
		}
		changelogClients := protected.Group("/clients")
		changelogClients.Use(middleware.RequirePermission(perms.Clients.Read.All))
		{
			changelogClients.GET("/:id/changelog", v3GetClientChangelog)
		}
		changelogCards := protected.Group("/cards")
		changelogCards.Use(middleware.RequirePermission(perms.Cards.Read.All))
		{
			changelogCards.GET("/:id/changelog", v3GetCardChangelog)
		}
		changelogLoans := protected.Group("/loans")
		changelogLoans.Use(middleware.RequirePermission(perms.Credits.Read.All))
		{
			changelogLoans.GET("/:id/changelog", v3GetLoanChangelog)
		}

		// ── Investment funds (Celina-4) ────────────────────────────
		// Manage (create/update) requires funds.manage.catalog.
		fundsManage := protected.Group("/investment-funds")
		fundsManage.Use(middleware.RequirePermission(perms.Funds.Manage.Catalog))
		{
			fundsManage.POST("", h.Fund.CreateFund)
			fundsManage.PUT("/:id", h.Fund.UpdateFund)
		}

		// Bank-position read + actuary performance.
		fundsBank := protected.Group("/")
		fundsBank.Use(middleware.RequirePermission(perms.Funds.Read.All))
		{
			fundsBank.GET("/investment-funds/positions", h.Fund.ListBankPositions)
			fundsBank.GET("/actuaries/performance", h.Fund.ActuaryPerformance)
		}

		// ── v2 options (legacy v2-only routes, now hosted on v3) ────
		opts := protected.Group("/options")
		opts.Use(middleware.RequireAnyPermission(perms.Otc.Trade.Accept, perms.Otc.Trade.Exercise, perms.Securities.Trade.Any))
		opts.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
		{
			opts.POST("/:option_id/orders", h.OptionsV2.CreateOrder)
			opts.POST("/:option_id/exercise", h.OptionsV2.Exercise)
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
	}

	// Catch-all 404 for any path not served by v3 or the swagger UI.
	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{"code": "not_found", "message": "route not found"},
		})
	})
}
