// api-gateway/internal/router/router_v1.go
package router

import (
	"net/http"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/exbanka/api-gateway/internal/handler"
	apimetrics "github.com/exbanka/api-gateway/internal/metrics"
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
// @Router       /api/v2/accounts/{id}/changelog [get]
func v1GetAccountChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get employee changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Employee ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v2/employees/{id}/changelog [get]
func v1GetEmployeeChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get client changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Client ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v2/clients/{id}/changelog [get]
func v1GetClientChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get card changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Card ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v2/cards/{id}/changelog [get]
func v1GetCardChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get loan changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Loan ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v2/loans/{id}/changelog [get]
func v1GetLoanChangelog(c *gin.Context) { notImplemented(c) }

// NewRouter creates the Gin engine with CORS, metrics, and Swagger.
// Version-specific route functions (SetupV1Routes, SetupV2Routes, SetupV3Routes, …)
// attach their routes to this engine.
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

// RegisterCoreRoutes wires every route that exists at this API version on the
// given *gin.RouterGroup. The caller owns the prefix (/api/v1 or /api/v2); this
// function registers only relative paths. Handlers are stateless — instantiating
// them per-caller is zero-cost.
//
// This function is the single source of truth for the core REST surface. v1 and
// v2 both call it so they expose the same set of routes.
func RegisterCoreRoutes(
	group *gin.RouterGroup,
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
	// ── Create handlers ─────────────────────────────────────────────────
	authHandler := handler.NewAuthHandler(authClient)
	empHandler := handler.NewEmployeeHandler(userClient, authClient)
	roleHandler := handler.NewRoleHandler(userClient)
	limitHandler := handler.NewLimitHandler(empLimitClient, clientLimitClient)
	clientHandler := handler.NewClientHandler(clientClient, authClient)
	accountHandler := handler.NewAccountHandler(accountClient, bankAccountClient, cardClient, txClient)
	cardHandler := handler.NewCardHandler(cardClient, virtualCardClient, cardRequestClient, accountClient)
	txHandler := handler.NewTransactionHandler(txClient, feeClient, accountClient, exchangeClient)
	exchangeHandler := handler.NewExchangeHandler(exchangeClient)
	creditHandler := handler.NewCreditHandler(creditClient)
	meHandler := handler.NewMeHandler(clientClient, userClient, authClient)
	sessionHandler := handler.NewSessionHandler(authClient)
	stockExchangeHandler := handler.NewStockExchangeHandler(stockExchangeClient)
	securitiesHandler := handler.NewSecuritiesHandler(securityClient)
	stockOrderHandler := handler.NewStockOrderHandler(orderClient, accountClient)
	portfolioHandler := handler.NewPortfolioHandler(portfolioClient, otcClient, accountClient)
	actuaryHandler := handler.NewActuaryHandler(actuaryClient)
	blueprintHandler := handler.NewBlueprintHandler(blueprintClient)
	taxHandler := handler.NewTaxHandler(taxClient)
	stockSourceHandler := handler.NewStockSourceHandler(sourceAdminClient)

	// ── Public auth routes (no middleware) ───────────────────────
	auth := group.Group("/auth")
	{
		auth.POST("/login", authHandler.Login)
		auth.POST("/refresh", authHandler.RefreshToken)
		auth.POST("/logout", authHandler.Logout)
		auth.POST("/password/reset-request", authHandler.RequestPasswordReset)
		auth.POST("/password/reset", authHandler.ResetPassword)
		auth.POST("/activate", authHandler.ActivateAccount)
		auth.POST("/resend-activation", authHandler.ResendActivationEmail)
	}

	// ── Public exchange rate routes (no middleware) ──────────────
	group.GET("/exchange/rates", exchangeHandler.ListExchangeRates)
	group.GET("/exchange/rates/:from/:to", exchangeHandler.GetExchangeRate)
	group.POST("/exchange/calculate", exchangeHandler.CalculateExchange)

	// ── /me/* (AnyAuthMiddleware) ────────────────────────────────
	me := group.Group("/me")
	me.Use(middleware.AnyAuthMiddleware(authClient))
	{
		me.GET("", middleware.RequireClientToken(), meHandler.GetMe)

		// Accounts
		me.GET("/accounts", accountHandler.ListMyAccounts)
		me.GET("/accounts/:id", accountHandler.GetMyAccount)
		me.GET("/accounts/:id/activity", accountHandler.GetMyAccountActivity)

		// Cards
		me.GET("/cards", cardHandler.ListMyCards)
		me.GET("/cards/:id", cardHandler.GetMyCard)
		me.POST("/cards/:id/pin", middleware.RequireClientToken(), cardHandler.SetCardPin)
		me.POST("/cards/:id/verify-pin", middleware.RequireClientToken(), cardHandler.VerifyCardPin)
		me.POST("/cards/:id/temporary-block", middleware.RequireClientToken(), cardHandler.TemporaryBlockCard)
		me.POST("/cards/virtual", cardHandler.CreateVirtualCard)
		me.POST("/cards/requests", middleware.RequireClientToken(), cardHandler.CreateCardRequest)
		me.GET("/cards/requests", middleware.RequireClientToken(), cardHandler.ListMyCardRequests)

		// Payments
		me.POST("/payments", txHandler.CreatePayment)
		me.GET("/payments", txHandler.ListMyPayments)
		me.GET("/payments/:id", txHandler.GetMyPayment)
		me.POST("/payments/:id/execute", txHandler.ExecutePayment)

		// Transfers
		me.POST("/transfers", txHandler.CreateTransfer)
		me.POST("/transfers/preview", txHandler.PreviewTransfer)
		me.GET("/transfers", txHandler.ListMyTransfers)
		me.GET("/transfers/:id", txHandler.GetMyTransfer)
		me.POST("/transfers/:id/execute", txHandler.ExecuteTransfer)

		// Payment recipients
		me.POST("/payment-recipients", txHandler.CreateMyPaymentRecipient)
		me.GET("/payment-recipients", txHandler.ListMyPaymentRecipients)
		me.PUT("/payment-recipients/:id", txHandler.UpdatePaymentRecipient)
		me.DELETE("/payment-recipients/:id", txHandler.DeletePaymentRecipient)

		// Loans
		me.POST("/loan-requests", creditHandler.CreateLoanRequest)
		me.GET("/loan-requests", creditHandler.ListMyLoanRequests)
		me.GET("/loans", creditHandler.ListMyLoans)
		me.GET("/loans/:id", creditHandler.GetMyLoan)
		me.GET("/loans/:id/installments", creditHandler.GetMyInstallments)

		// Stock orders
		me.POST("/orders", stockOrderHandler.CreateOrder)
		me.GET("/orders", stockOrderHandler.ListMyOrders)
		me.GET("/orders/:id", stockOrderHandler.GetMyOrder)
		me.POST("/orders/:id/cancel", stockOrderHandler.CancelOrder)

		// Portfolio
		me.GET("/portfolio", portfolioHandler.ListHoldings)
		me.GET("/portfolio/summary", portfolioHandler.GetPortfolioSummary)
		me.POST("/portfolio/:id/make-public", portfolioHandler.MakePublic)
		me.POST("/portfolio/:id/exercise", portfolioHandler.ExerciseOption)
		// Part B: per-holding transaction history.
		me.GET("/holdings/:id/transactions", portfolioHandler.ListHoldingTransactions)

		// Tax
		me.GET("/tax", taxHandler.ListMyTaxRecords)

		// Sessions
		me.GET("/sessions", sessionHandler.ListMySessions)
		me.POST("/sessions/revoke", sessionHandler.RevokeSession)
		me.POST("/sessions/revoke-others", sessionHandler.RevokeAllSessions)
		me.GET("/login-history", sessionHandler.GetMyLoginHistory)

		// Notifications (general, persistent, read/unread)
		notifHandler := handler.NewNotificationHandler(notificationClient)
		me.GET("/notifications", notifHandler.ListNotifications)
		me.GET("/notifications/unread-count", notifHandler.GetUnreadCount)
		me.POST("/notifications/read-all", notifHandler.MarkAllRead)
		me.POST("/notifications/:id/read", notifHandler.MarkRead)
	}

	// ── Stock exchanges (AnyAuth — market data is browsable) ────
	stockExchanges := group.Group("/stock-exchanges")
	stockExchanges.Use(middleware.AnyAuthMiddleware(authClient))
	{
		stockExchanges.GET("", stockExchangeHandler.ListExchanges)
		stockExchanges.GET("/:id", stockExchangeHandler.GetExchange)
	}

	// ── Securities (AnyAuth — market data is browsable) ─────────
	securities := group.Group("/securities")
	securities.Use(middleware.AnyAuthMiddleware(authClient))
	{
		securities.GET("/stocks", securitiesHandler.ListStocks)
		securities.GET("/stocks/:id", securitiesHandler.GetStock)
		securities.GET("/stocks/:id/history", securitiesHandler.GetStockHistory)
		securities.GET("/futures", securitiesHandler.ListFutures)
		securities.GET("/futures/:id", securitiesHandler.GetFutures)
		securities.GET("/futures/:id/history", securitiesHandler.GetFuturesHistory)
		securities.GET("/forex", securitiesHandler.ListForexPairs)
		securities.GET("/forex/:id", securitiesHandler.GetForexPair)
		securities.GET("/forex/:id/history", securitiesHandler.GetForexPairHistory)
		securities.GET("/options", securitiesHandler.ListOptions)
		securities.GET("/options/:id", securitiesHandler.GetOption)
		// Candles (InfluxDB time-series)
		securities.GET("/candles", securitiesHandler.GetCandles)
	}

	// ── OTC (AnyAuth for browsing, securities.trade for buying) ─
	otc := group.Group("/otc/offers")
	otc.Use(middleware.AnyAuthMiddleware(authClient))
	{
		otc.GET("", portfolioHandler.ListOTCOffers)
	}
	otcTrade := group.Group("/otc/offers")
	otcTrade.Use(middleware.AuthMiddleware(authClient))
	otcTrade.Use(middleware.RequireAnyPermission("otc.trade.accept", "securities.trade"))
	{
		otcTrade.POST("/:id/buy", portfolioHandler.BuyOTCOffer)
	}

	// ── Mobile auth (public) ────────────────────────────────────
	mobileAuth := group.Group("/mobile/auth")
	{
		mobileAuthHandler := handler.NewMobileAuthHandler(authClient)
		mobileAuth.POST("/request-activation", mobileAuthHandler.RequestActivation)
		mobileAuth.POST("/activate", mobileAuthHandler.ActivateDevice)
		mobileAuth.POST("/refresh", mobileAuthHandler.RefreshMobileToken)
	}

	// ── Mobile device management (MobileAuthMiddleware) ─────────
	mobileDevice := group.Group("/mobile/device")
	mobileDevice.Use(middleware.MobileAuthMiddleware(authClient))
	{
		mobileAuthHandler := handler.NewMobileAuthHandler(authClient)
		mobileDevice.GET("", mobileAuthHandler.GetDeviceInfo)
		mobileDevice.POST("/deactivate", mobileAuthHandler.DeactivateDevice)
		mobileDevice.POST("/transfer", mobileAuthHandler.TransferDevice)
	}

	// ── Mobile device settings (MobileAuth + DeviceSignature) ────
	mobileDeviceSettings := group.Group("/mobile/device")
	mobileDeviceSettings.Use(middleware.MobileAuthMiddleware(authClient))
	mobileDeviceSettings.Use(middleware.RequireDeviceSignature(authClient))
	{
		mobileAuthSettingsHandler := handler.NewMobileAuthHandler(authClient)
		mobileDeviceSettings.POST("/biometrics", mobileAuthSettingsHandler.SetBiometrics)
		mobileDeviceSettings.GET("/biometrics", mobileAuthSettingsHandler.GetBiometrics)
	}

	// ── Mobile verifications (MobileAuth + DeviceSignature) ─────
	mobileVerify := group.Group("/mobile/verifications")
	mobileVerify.Use(middleware.MobileAuthMiddleware(authClient))
	mobileVerify.Use(middleware.RequireDeviceSignature(authClient))
	{
		verifyHandler := handler.NewVerificationHandler(verificationClient, notificationClient)
		mobileVerify.GET("/pending", verifyHandler.GetPendingVerifications)
		mobileVerify.POST("/:id/submit", verifyHandler.SubmitMobileVerification)
		mobileVerify.POST("/:id/ack", verifyHandler.AckVerification)
		mobileVerify.POST("/:id/biometric", verifyHandler.BiometricVerify)
	}

	// ── QR verification (MobileAuth + DeviceSignature) ──────────
	qrVerify := group.Group("/verify")
	qrVerify.Use(middleware.MobileAuthMiddleware(authClient))
	qrVerify.Use(middleware.RequireDeviceSignature(authClient))
	{
		verifyHandler := handler.NewVerificationHandler(verificationClient, notificationClient)
		qrVerify.POST("/:challenge_id", verifyHandler.VerifyQR)
	}

	// ── Browser-facing verifications (AnyAuthMiddleware) ────────
	verifications := group.Group("/verifications")
	verifications.Use(middleware.AnyAuthMiddleware(authClient))
	{
		verifyHandler := handler.NewVerificationHandler(verificationClient, notificationClient)
		verifications.POST("", verifyHandler.CreateVerification)
		verifications.GET("/:id/status", verifyHandler.GetVerificationStatus)
		verifications.POST("/:id/code", verifyHandler.SubmitVerificationCode)
	}

	// ── Employee/admin routes (AuthMiddleware + RequirePermission) ─
	protected := group.Group("/")
	protected.Use(middleware.AuthMiddleware(authClient))
	{
		// Employees
		employees := protected.Group("/employees")
		employees.Use(middleware.RequireAnyPermission("employees.read.all", "employees.read"))
		{
			employees.GET("", empHandler.ListEmployees)
			employees.GET("/:id", empHandler.GetEmployee)
		}
		adminEmployees := protected.Group("/employees")
		adminEmployees.Use(middleware.RequirePermission("employees.create"))
		{
			adminEmployees.POST("", empHandler.CreateEmployee)
		}
		updateEmployees := protected.Group("/employees")
		updateEmployees.Use(middleware.RequireAnyPermission("employees.update.profile", "employees.update"))
		{
			updateEmployees.PUT("/:id", empHandler.UpdateEmployee)
		}

		// Role catalog (read)
		roleRead := protected.Group("/")
		roleRead.Use(middleware.RequireAnyPermission("roles.read", "permissions.read", "employees.permissions"))
		{
			roleRead.GET("/roles", roleHandler.ListRoles)
			roleRead.GET("/roles/:id", roleHandler.GetRole)
			roleRead.GET("/permissions", roleHandler.ListPermissions)
		}
		// Role mutation
		roleCreate := protected.Group("/")
		roleCreate.Use(middleware.RequireAnyPermission("roles.create", "employees.permissions"))
		{
			roleCreate.POST("/roles", roleHandler.CreateRole)
		}
		roleUpdate := protected.Group("/")
		roleUpdate.Use(middleware.RequireAnyPermission("roles.update", "employees.permissions"))
		{
			roleUpdate.PUT("/roles/:id/permissions", roleHandler.UpdateRolePermissions)
		}
		// Per-employee role/permission assignment
		empPermAssign := protected.Group("/")
		empPermAssign.Use(middleware.RequireAnyPermission("employees.roles.assign", "employees.permissions.assign", "employees.permissions"))
		{
			empPermAssign.PUT("/employees/:id/roles", roleHandler.SetEmployeeRoles)
			empPermAssign.PUT("/employees/:id/permissions", roleHandler.SetEmployeeAdditionalPermissions)
		}

		// Employee limit read
		empLimitsRead := protected.Group("/")
		empLimitsRead.Use(middleware.RequireAnyPermission("limits.employee.read", "limits.manage"))
		{
			empLimitsRead.GET("/employees/:id/limits", limitHandler.GetEmployeeLimits)
		}
		// Employee limit update
		empLimitsUpdate := protected.Group("/")
		empLimitsUpdate.Use(middleware.RequireAnyPermission("limits.employee.update", "limits.manage"))
		{
			empLimitsUpdate.PUT("/employees/:id/limits", limitHandler.SetEmployeeLimits)
			empLimitsUpdate.POST("/employees/:id/limits/template", limitHandler.ApplyLimitTemplate)
		}
		// Limit-template management
		limitTemplatesRead := protected.Group("/limits/templates")
		limitTemplatesRead.Use(middleware.RequireAnyPermission("limit-templates.read", "limits.manage"))
		{
			limitTemplatesRead.GET("", limitHandler.ListLimitTemplates)
		}
		limitTemplatesCreate := protected.Group("/limits/templates")
		limitTemplatesCreate.Use(middleware.RequireAnyPermission("limit-templates.create", "limits.manage"))
		{
			limitTemplatesCreate.POST("", limitHandler.CreateLimitTemplate)
		}
		// Client limits
		clientLimitsRead := protected.Group("/")
		clientLimitsRead.Use(middleware.RequireAnyPermission("limits.client.read", "limits.manage"))
		{
			clientLimitsRead.GET("/clients/:id/limits", limitHandler.GetClientLimits)
		}
		clientLimitsUpdate := protected.Group("/")
		clientLimitsUpdate.Use(middleware.RequireAnyPermission("limits.client.update", "limits.manage"))
		{
			clientLimitsUpdate.PUT("/clients/:id/limits", limitHandler.SetClientLimits)
		}

		// Limit blueprint management — split per CRUD verb
		blueprintsRead := protected.Group("/blueprints")
		blueprintsRead.Use(middleware.RequireAnyPermission("limit-templates.read", "limits.manage"))
		{
			blueprintsRead.GET("", blueprintHandler.ListBlueprints)
			blueprintsRead.GET("/:id", blueprintHandler.GetBlueprint)
		}
		blueprintsCreate := protected.Group("/blueprints")
		blueprintsCreate.Use(middleware.RequireAnyPermission("limit-templates.create", "limits.manage"))
		{
			blueprintsCreate.POST("", blueprintHandler.CreateBlueprint)
		}
		blueprintsUpdate := protected.Group("/blueprints")
		blueprintsUpdate.Use(middleware.RequireAnyPermission("limit-templates.update", "limits.manage"))
		{
			blueprintsUpdate.PUT("/:id", blueprintHandler.UpdateBlueprint)
			blueprintsUpdate.POST("/:id/apply", blueprintHandler.ApplyBlueprint)
		}
		blueprintsDelete := protected.Group("/blueprints")
		blueprintsDelete.Use(middleware.RequireAnyPermission("limit-templates.delete", "limits.manage"))
		{
			blueprintsDelete.DELETE("/:id", blueprintHandler.DeleteBlueprint)
		}

		// Client management
		clientsRead := protected.Group("/clients")
		clientsRead.Use(middleware.RequireAnyPermission("clients.read.all", "clients.read"))
		{
			clientsRead.GET("", clientHandler.ListClients)
			clientsRead.GET("/:id", clientHandler.GetClient)
		}
		clientsCreate := protected.Group("/clients")
		clientsCreate.Use(middleware.RequirePermission("clients.create"))
		{
			clientsCreate.POST("", clientHandler.CreateClient)
		}
		clientsUpdate := protected.Group("/clients")
		clientsUpdate.Use(middleware.RequireAnyPermission("clients.update.profile", "clients.update"))
		{
			clientsUpdate.PUT("/:id", clientHandler.UpdateClient)
		}

		// Currencies (any authenticated employee)
		protected.GET("/currencies", accountHandler.ListCurrencies)

		// Account management
		accountsRead := protected.Group("/accounts")
		accountsRead.Use(middleware.RequireAnyPermission("accounts.read.all", "accounts.read"))
		{
			accountsRead.GET("", accountHandler.ListAllAccounts)
			accountsRead.GET("/:id", accountHandler.GetAccount)
			accountsRead.GET("/by-number/:account_number", accountHandler.GetAccountByNumber)
		}
		accountsCreate := protected.Group("/accounts")
		accountsCreate.Use(middleware.RequireAnyPermission("accounts.create.current", "accounts.create.foreign", "accounts.create"))
		{
			accountsCreate.POST("", accountHandler.CreateAccount)
		}
		accountsName := protected.Group("/accounts")
		accountsName.Use(middleware.RequireAnyPermission("accounts.update.name", "accounts.update"))
		{
			accountsName.PUT("/:id/name", accountHandler.UpdateAccountName)
		}
		accountsLimits := protected.Group("/accounts")
		accountsLimits.Use(middleware.RequireAnyPermission("accounts.update.limits", "accounts.update"))
		{
			accountsLimits.PUT("/:id/limits", accountHandler.UpdateAccountLimits)
		}
		accountsStatus := protected.Group("/accounts")
		accountsStatus.Use(middleware.RequireAnyPermission("accounts.update.status", "accounts.update"))
		{
			accountsStatus.PUT("/:id/status", accountHandler.UpdateAccountStatus)
		}

		// Companies
		companiesEmployee := protected.Group("/companies")
		companiesEmployee.Use(middleware.RequireAnyPermission("accounts.create.current", "accounts.create.foreign", "accounts.create"))
		{
			companiesEmployee.POST("", accountHandler.CreateCompany)
		}

		// Bank account management — split per CRUD verb
		bankAccountsRead := protected.Group("/bank-accounts")
		bankAccountsRead.Use(middleware.RequireAnyPermission("bank-accounts.read", "bank-accounts.manage"))
		{
			bankAccountsRead.GET("", accountHandler.ListBankAccounts)
		}
		bankAccountsCreate := protected.Group("/bank-accounts")
		bankAccountsCreate.Use(middleware.RequireAnyPermission("bank-accounts.create", "bank-accounts.manage"))
		{
			bankAccountsCreate.POST("", accountHandler.CreateBankAccount)
		}
		bankAccountsDeactivate := protected.Group("/bank-accounts")
		bankAccountsDeactivate.Use(middleware.RequireAnyPermission("bank-accounts.deactivate", "bank-accounts.manage"))
		{
			bankAccountsDeactivate.DELETE("/:id", accountHandler.DeleteBankAccount)
		}

		// Cards management
		cardsRead := protected.Group("/cards")
		cardsRead.Use(middleware.RequireAnyPermission("cards.read.all", "cards.read"))
		{
			cardsRead.GET("", cardHandler.ListCards)
			cardsRead.GET("/:id", cardHandler.GetCard)
		}
		cardsCreate := protected.Group("/cards")
		cardsCreate.Use(middleware.RequireAnyPermission("cards.create.physical", "cards.create.virtual", "cards.create"))
		{
			cardsCreate.POST("", cardHandler.CreateCard)
			cardsCreate.POST("/authorized-person", cardHandler.CreateAuthorizedPerson)
		}
		cardsBlock := protected.Group("/cards")
		cardsBlock.Use(middleware.RequireAnyPermission("cards.block", "cards.update"))
		{
			cardsBlock.POST("/:id/block", cardHandler.BlockCard)
		}
		cardsUnblock := protected.Group("/cards")
		cardsUnblock.Use(middleware.RequireAnyPermission("cards.unblock", "cards.update"))
		{
			cardsUnblock.POST("/:id/unblock", cardHandler.UnblockCard)
		}
		cardsDeactivate := protected.Group("/cards")
		cardsDeactivate.Use(middleware.RequireAnyPermission("cards.deactivate", "cards.update"))
		{
			cardsDeactivate.POST("/:id/deactivate", cardHandler.DeactivateCard)
		}

		// Card requests — read/approve/reject split
		cardsRequestsRead := protected.Group("/cards/requests")
		cardsRequestsRead.Use(middleware.RequireAnyPermission("cards.approve", "cards.read.all"))
		{
			cardsRequestsRead.GET("", cardHandler.ListCardRequests)
			cardsRequestsRead.GET("/:id", cardHandler.GetCardRequest)
		}
		cardsRequestsApprove := protected.Group("/cards/requests")
		cardsRequestsApprove.Use(middleware.RequireAnyPermission("cards.approve.physical", "cards.approve.virtual", "cards.approve"))
		{
			cardsRequestsApprove.POST("/:id/approve", cardHandler.ApproveCardRequest)
		}
		cardsRequestsReject := protected.Group("/cards/requests")
		cardsRequestsReject.Use(middleware.RequireAnyPermission("cards.reject", "cards.approve"))
		{
			cardsRequestsReject.POST("/:id/reject", cardHandler.RejectCardRequest)
		}

		// Payments (employee read)
		paymentsRead := protected.Group("/payments")
		paymentsRead.Use(middleware.RequireAnyPermission("payments.read.all", "payments.read"))
		{
			paymentsRead.GET("", txHandler.ListPayments)
			paymentsRead.GET("/:id", txHandler.GetPayment)
		}

		// Transfers (employee read)
		transfersRead := protected.Group("/transfers")
		transfersRead.Use(middleware.RequireAnyPermission("transfers.read.all", "payments.read"))
		{
			transfersRead.GET("", txHandler.ListTransfers)
			transfersRead.GET("/:id", txHandler.GetTransfer)
		}

		// Transfer fee management — split per CRUD verb
		feesRead := protected.Group("/fees")
		feesRead.Use(middleware.RequireAnyPermission("fees.read", "fees.manage"))
		{
			feesRead.GET("", txHandler.ListFees)
		}
		feesCreate := protected.Group("/fees")
		feesCreate.Use(middleware.RequireAnyPermission("fees.create", "fees.manage"))
		{
			feesCreate.POST("", txHandler.CreateFee)
		}
		feesUpdate := protected.Group("/fees")
		feesUpdate.Use(middleware.RequireAnyPermission("fees.update", "fees.manage"))
		{
			feesUpdate.PUT("/:id", txHandler.UpdateFee)
		}
		feesDelete := protected.Group("/fees")
		feesDelete.Use(middleware.RequireAnyPermission("fees.delete", "fees.manage"))
		{
			feesDelete.DELETE("/:id", txHandler.DeleteFee)
		}

		// Loans (employee read)
		loansRead := protected.Group("/loans")
		loansRead.Use(middleware.RequireAnyPermission("credits.read.all", "credits.read"))
		{
			loansRead.GET("", creditHandler.ListAllLoans)
			loansRead.GET("/:id", creditHandler.GetLoan)
			loansRead.GET("/:id/installments", creditHandler.GetInstallmentsByLoan)
		}

		// Loan requests
		loanRequestsRead := protected.Group("/loan-requests")
		loanRequestsRead.Use(middleware.RequireAnyPermission("credits.read.all", "credits.read"))
		{
			loanRequestsRead.GET("", creditHandler.ListLoanRequests)
			loanRequestsRead.GET("/:id", creditHandler.GetLoanRequest)
		}
		loanRequestsApprove := protected.Group("/loan-requests")
		loanRequestsApprove.Use(middleware.RequireAnyPermission(
			"credits.approve.cash", "credits.approve.housing", "credits.approve.auto",
			"credits.approve.refinancing", "credits.approve.student", "credits.approve"))
		{
			loanRequestsApprove.POST("/:id/approve", creditHandler.ApproveLoanRequest)
		}
		loanRequestsReject := protected.Group("/loan-requests")
		loanRequestsReject.Use(middleware.RequireAnyPermission("credits.reject", "credits.approve"))
		{
			loanRequestsReject.POST("/:id/reject", creditHandler.RejectLoanRequest)
		}

		// Interest rate tier management — split read/update
		rateTiersRead := protected.Group("/interest-rate-tiers")
		rateTiersRead.Use(middleware.RequireAnyPermission("interest-rates.tiers.read", "interest-rates.manage"))
		{
			rateTiersRead.GET("", creditHandler.ListInterestRateTiers)
		}
		rateTiersUpdate := protected.Group("/interest-rate-tiers")
		rateTiersUpdate.Use(middleware.RequireAnyPermission("interest-rates.tiers.update", "interest-rates.manage"))
		{
			rateTiersUpdate.POST("", creditHandler.CreateInterestRateTier)
			rateTiersUpdate.PUT("/:id", creditHandler.UpdateInterestRateTier)
			rateTiersUpdate.DELETE("/:id", creditHandler.DeleteInterestRateTier)
			rateTiersUpdate.POST("/:id/apply", creditHandler.ApplyVariableRateUpdate)
		}

		// Bank margin management — split read/update
		bankMarginsRead := protected.Group("/bank-margins")
		bankMarginsRead.Use(middleware.RequireAnyPermission("interest-rates.bank-margins.read", "interest-rates.manage"))
		{
			bankMarginsRead.GET("", creditHandler.ListBankMargins)
		}
		bankMarginsUpdate := protected.Group("/bank-margins")
		bankMarginsUpdate.Use(middleware.RequireAnyPermission("interest-rates.bank-margins.update", "interest-rates.manage"))
		{
			bankMarginsUpdate.PUT("/:id", creditHandler.UpdateBankMargin)
		}

		// Stock exchange management
		stockExchangeRead := protected.Group("/stock-exchanges")
		stockExchangeRead.Use(middleware.RequireAnyPermission("exchanges.read", "exchanges.manage"))
		{
			stockExchangeRead.GET("/testing-mode", stockExchangeHandler.GetTestingMode)
		}
		stockExchangeToggle := protected.Group("/stock-exchanges")
		stockExchangeToggle.Use(middleware.RequireAnyPermission("exchanges.testing-mode.toggle", "exchanges.manage"))
		{
			stockExchangeToggle.POST("/testing-mode", stockExchangeHandler.SetTestingMode)
		}

		// Admin stock-source management
		adminStockSource := protected.Group("/admin/stock-source")
		adminStockSource.Use(middleware.RequireAnyPermission("securities.manage.market-simulator", "securities.manage"))
		{
			adminStockSource.POST("", stockSourceHandler.SwitchSource)
			adminStockSource.GET("", stockSourceHandler.GetSourceStatus)
		}

		// Orders — on-behalf-of-client (and bank by default — see CreateOrderOnBehalf handler)
		ordersOnBehalf := protected.Group("/orders")
		ordersOnBehalf.Use(middleware.RequireAnyPermission(
			"orders.place.on-behalf-client", "orders.place.for-bank", "orders.place-on-behalf"))
		{
			ordersOnBehalf.POST("", stockOrderHandler.CreateOrderOnBehalf)
		}

		// OTC — employee on-behalf buying
		otcOnBehalf := protected.Group("/otc/admin/offers")
		otcOnBehalf.Use(middleware.RequireAnyPermission(
			"otc.trade.accept", "orders.place.on-behalf-client", "orders.place-on-behalf"))
		{
			otcOnBehalf.POST("/:id/buy", portfolioHandler.BuyOTCOfferOnBehalf)
		}

		// Order management (supervisor) — read split from approve/reject
		ordersRead := protected.Group("/orders")
		ordersRead.Use(middleware.RequireAnyPermission(
			"orders.read.all", "orders.read.bank", "orders.approve"))
		{
			ordersRead.GET("", stockOrderHandler.ListOrders)
		}
		ordersApprove := protected.Group("/orders")
		ordersApprove.Use(middleware.RequireAnyPermission(
			"orders.approve.market", "orders.approve.limit", "orders.approve.stop", "orders.approve"))
		{
			ordersApprove.POST("/:id/approve", stockOrderHandler.ApproveOrder)
		}
		ordersReject := protected.Group("/orders")
		ordersReject.Use(middleware.RequireAnyPermission("orders.reject", "orders.approve"))
		{
			ordersReject.POST("/:id/decline", stockOrderHandler.DeclineOrder)
		}

		// Actuary (agent) management — split read/assign/unassign
		actuariesRead := protected.Group("/actuaries")
		actuariesRead.Use(middleware.RequireAnyPermission("agents.read", "agents.manage"))
		{
			actuariesRead.GET("", actuaryHandler.ListActuaries)
		}
		actuariesAssign := protected.Group("/actuaries")
		actuariesAssign.Use(middleware.RequireAnyPermission("agents.assign", "agents.manage"))
		{
			actuariesAssign.PUT("/:id/limit", actuaryHandler.SetActuaryLimit)
			actuariesAssign.PUT("/:id/approval", actuaryHandler.SetNeedApproval)
		}
		actuariesUnassign := protected.Group("/actuaries")
		actuariesUnassign.Use(middleware.RequireAnyPermission("agents.unassign", "agents.manage"))
		{
			actuariesUnassign.POST("/:id/reset-limit", actuaryHandler.ResetActuaryLimit)
		}

		// Tax management — split read/collect
		taxRead := protected.Group("/tax")
		taxRead.Use(middleware.RequireAnyPermission("tax.read", "tax.manage"))
		{
			taxRead.GET("", taxHandler.ListTaxRecords)
		}
		taxCollect := protected.Group("/tax")
		taxCollect.Use(middleware.RequireAnyPermission("tax.collect", "tax.manage"))
		{
			taxCollect.POST("/collect", taxHandler.CollectTax)
		}

		// ── Changelog endpoints — granular per-resource changelog perms ──
		changelogAccounts := protected.Group("/accounts")
		changelogAccounts.Use(middleware.RequireAnyPermission("changelog.read.accounts", "accounts.read.all", "accounts.read"))
		{
			changelogAccounts.GET("/:id/changelog", v1GetAccountChangelog)
		}
		changelogEmployees := protected.Group("/employees")
		changelogEmployees.Use(middleware.RequireAnyPermission("changelog.read.employees", "employees.read.all", "employees.read"))
		{
			changelogEmployees.GET("/:id/changelog", v1GetEmployeeChangelog)
		}
		changelogClients := protected.Group("/clients")
		changelogClients.Use(middleware.RequireAnyPermission("changelog.read.clients", "clients.read.all", "clients.read"))
		{
			changelogClients.GET("/:id/changelog", v1GetClientChangelog)
		}
		changelogCards := protected.Group("/cards")
		changelogCards.Use(middleware.RequireAnyPermission("changelog.read.cards", "cards.read.all", "cards.read"))
		{
			changelogCards.GET("/:id/changelog", v1GetCardChangelog)
		}
		changelogLoans := protected.Group("/loans")
		changelogLoans.Use(middleware.RequireAnyPermission("changelog.read.loans", "credits.read.all", "credits.read"))
		{
			changelogLoans.GET("/:id/changelog", v1GetLoanChangelog)
		}
	}
}

// SetupV1Routes registers all /api/v1/ routes on the given engine. It delegates
// to RegisterCoreRoutes with the /api/v1 prefix — v1 is now a thin wrapper over
// the version-agnostic route set.
func SetupV1Routes(
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
	v1 := r.Group("/api/v1")
	RegisterCoreRoutes(
		v1,
		authClient, userClient, clientClient, accountClient, cardClient,
		txClient, creditClient, empLimitClient, clientLimitClient,
		virtualCardClient, bankAccountClient, feeClient, cardRequestClient,
		exchangeClient, stockExchangeClient, securityClient, orderClient,
		portfolioClient, otcClient, taxClient, actuaryClient, blueprintClient,
		verificationClient, notificationClient, sourceAdminClient,
	)
}
