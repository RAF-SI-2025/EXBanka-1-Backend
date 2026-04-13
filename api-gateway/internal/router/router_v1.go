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
// @Router       /api/v1/accounts/{id}/changelog [get]
func v1GetAccountChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get employee changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Employee ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v1/employees/{id}/changelog [get]
func v1GetEmployeeChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get client changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Client ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v1/clients/{id}/changelog [get]
func v1GetClientChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get card changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Card ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v1/cards/{id}/changelog [get]
func v1GetCardChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get loan changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Loan ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v1/loans/{id}/changelog [get]
func v1GetLoanChangelog(c *gin.Context) { notImplemented(c) }

// SetupV1Routes registers all /api/v1/ routes on the given engine.
// It mirrors every route from Setup() (the /api/ router) and adds new v1-only
// placeholder endpoints. The handler instances are freshly created from the
// same gRPC clients — they are stateless, so duplication is zero-cost.
//
// IMPORTANT: This function must be called AFTER Setup() has already configured
// the engine (CORS, swagger, etc.) and registered /api/ routes.
// NewRouter creates the Gin engine with CORS, metrics, and Swagger.
// Version-specific route functions (SetupV1Routes, future SetupV2Routes, etc.)
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
	// ── Create handlers ─────────────────────────────────────────────────
	authHandler := handler.NewAuthHandler(authClient)
	empHandler := handler.NewEmployeeHandler(userClient, authClient)
	roleHandler := handler.NewRoleHandler(userClient)
	limitHandler := handler.NewLimitHandler(empLimitClient, clientLimitClient)
	clientHandler := handler.NewClientHandler(clientClient, authClient)
	accountHandler := handler.NewAccountHandler(accountClient, bankAccountClient, cardClient, txClient)
	cardHandler := handler.NewCardHandler(cardClient, virtualCardClient, cardRequestClient)
	txHandler := handler.NewTransactionHandler(txClient, feeClient, accountClient, exchangeClient)
	exchangeHandler := handler.NewExchangeHandler(exchangeClient)
	creditHandler := handler.NewCreditHandler(creditClient)
	meHandler := handler.NewMeHandler(clientClient, userClient, authClient)
	sessionHandler := handler.NewSessionHandler(authClient)
	stockExchangeHandler := handler.NewStockExchangeHandler(stockExchangeClient)
	securitiesHandler := handler.NewSecuritiesHandler(securityClient)
	stockOrderHandler := handler.NewStockOrderHandler(orderClient)
	portfolioHandler := handler.NewPortfolioHandler(portfolioClient, otcClient)
	actuaryHandler := handler.NewActuaryHandler(actuaryClient)
	blueprintHandler := handler.NewBlueprintHandler(blueprintClient)
	taxHandler := handler.NewTaxHandler(taxClient)
	stockSourceHandler := handler.NewStockSourceHandler(sourceAdminClient)

	// ── /api/v1 root group ──────────────────────────────────────────────
	v1 := r.Group("/api/v1")
	{
		// ── Public auth routes (no middleware) ───────────────────────
		auth := v1.Group("/auth")
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
		v1.GET("/exchange/rates", exchangeHandler.ListExchangeRates)
		v1.GET("/exchange/rates/:from/:to", exchangeHandler.GetExchangeRate)
		v1.POST("/exchange/calculate", exchangeHandler.CalculateExchange)

		// ── /api/v1/me/* (AnyAuthMiddleware) ────────────────────────
		me := v1.Group("/me")
		me.Use(middleware.AnyAuthMiddleware(authClient))
		{
			me.GET("", middleware.RequireClientToken(), meHandler.GetMe)

			// Accounts
			me.GET("/accounts", accountHandler.ListMyAccounts)
			me.GET("/accounts/:id", accountHandler.GetMyAccount)

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
		stockExchanges := v1.Group("/stock-exchanges")
		stockExchanges.Use(middleware.AnyAuthMiddleware(authClient))
		{
			stockExchanges.GET("", stockExchangeHandler.ListExchanges)
			stockExchanges.GET("/:id", stockExchangeHandler.GetExchange)
		}

		// ── Securities (AnyAuth — market data is browsable) ─────────
		securities := v1.Group("/securities")
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
		otc := v1.Group("/otc/offers")
		otc.Use(middleware.AnyAuthMiddleware(authClient))
		{
			otc.GET("", portfolioHandler.ListOTCOffers)
		}
		otcTrade := v1.Group("/otc/offers")
		otcTrade.Use(middleware.AuthMiddleware(authClient))
		otcTrade.Use(middleware.RequirePermission("securities.trade"))
		{
			otcTrade.POST("/:id/buy", portfolioHandler.BuyOTCOffer)
		}

		// ── Mobile auth (public) ────────────────────────────────────
		mobileAuth := v1.Group("/mobile/auth")
		{
			mobileAuthHandler := handler.NewMobileAuthHandler(authClient)
			mobileAuth.POST("/request-activation", mobileAuthHandler.RequestActivation)
			mobileAuth.POST("/activate", mobileAuthHandler.ActivateDevice)
			mobileAuth.POST("/refresh", mobileAuthHandler.RefreshMobileToken)
		}

		// ── Mobile device management (MobileAuthMiddleware) ─────────
		mobileDevice := v1.Group("/mobile/device")
		mobileDevice.Use(middleware.MobileAuthMiddleware(authClient))
		{
			mobileAuthHandler := handler.NewMobileAuthHandler(authClient)
			mobileDevice.GET("", mobileAuthHandler.GetDeviceInfo)
			mobileDevice.POST("/deactivate", mobileAuthHandler.DeactivateDevice)
			mobileDevice.POST("/transfer", mobileAuthHandler.TransferDevice)
		}

		// ── Mobile device settings (MobileAuth + DeviceSignature) ────
		mobileDeviceSettings := v1.Group("/mobile/device")
		mobileDeviceSettings.Use(middleware.MobileAuthMiddleware(authClient))
		mobileDeviceSettings.Use(middleware.RequireDeviceSignature(authClient))
		{
			mobileAuthSettingsHandler := handler.NewMobileAuthHandler(authClient)
			mobileDeviceSettings.POST("/biometrics", mobileAuthSettingsHandler.SetBiometrics)
			mobileDeviceSettings.GET("/biometrics", mobileAuthSettingsHandler.GetBiometrics)
		}

		// ── Mobile verifications (MobileAuth + DeviceSignature) ─────
		mobileVerify := v1.Group("/mobile/verifications")
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
		qrVerify := v1.Group("/verify")
		qrVerify.Use(middleware.MobileAuthMiddleware(authClient))
		qrVerify.Use(middleware.RequireDeviceSignature(authClient))
		{
			verifyHandler := handler.NewVerificationHandler(verificationClient, notificationClient)
			qrVerify.POST("/:challenge_id", verifyHandler.VerifyQR)
		}

		// ── Browser-facing verifications (AnyAuthMiddleware) ────────
		verifications := v1.Group("/verifications")
		verifications.Use(middleware.AnyAuthMiddleware(authClient))
		{
			verifyHandler := handler.NewVerificationHandler(verificationClient, notificationClient)
			verifications.POST("", verifyHandler.CreateVerification)
			verifications.GET("/:id/status", verifyHandler.GetVerificationStatus)
			verifications.POST("/:id/code", verifyHandler.SubmitVerificationCode)
		}

		// ── Employee/admin routes (AuthMiddleware + RequirePermission) ─
		protected := v1.Group("/")
		protected.Use(middleware.AuthMiddleware(authClient))
		{
			// Employees
			employees := protected.Group("/employees")
			employees.Use(middleware.RequirePermission("employees.read"))
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
			updateEmployees.Use(middleware.RequirePermission("employees.update"))
			{
				updateEmployees.PUT("/:id", empHandler.UpdateEmployee)
			}

			// Role and permission management
			permManagement := protected.Group("/")
			permManagement.Use(middleware.RequirePermission("employees.permissions"))
			{
				permManagement.GET("/roles", roleHandler.ListRoles)
				permManagement.GET("/roles/:id", roleHandler.GetRole)
				permManagement.POST("/roles", roleHandler.CreateRole)
				permManagement.PUT("/roles/:id/permissions", roleHandler.UpdateRolePermissions)
				permManagement.GET("/permissions", roleHandler.ListPermissions)
				permManagement.PUT("/employees/:id/roles", roleHandler.SetEmployeeRoles)
				permManagement.PUT("/employees/:id/permissions", roleHandler.SetEmployeeAdditionalPermissions)
			}

			// Employee limit management
			limitsEmployee := protected.Group("/")
			limitsEmployee.Use(middleware.RequirePermission("limits.manage"))
			{
				limitsEmployee.GET("/employees/:id/limits", limitHandler.GetEmployeeLimits)
				limitsEmployee.PUT("/employees/:id/limits", limitHandler.SetEmployeeLimits)
				limitsEmployee.POST("/employees/:id/limits/template", limitHandler.ApplyLimitTemplate)
				limitsEmployee.GET("/limits/templates", limitHandler.ListLimitTemplates)
				limitsEmployee.POST("/limits/templates", limitHandler.CreateLimitTemplate)
				limitsEmployee.GET("/clients/:id/limits", limitHandler.GetClientLimits)
				limitsEmployee.PUT("/clients/:id/limits", limitHandler.SetClientLimits)
			}

			// Limit blueprint management
			blueprintsAdmin := protected.Group("/blueprints")
			blueprintsAdmin.Use(middleware.RequirePermission("limits.manage"))
			{
				blueprintsAdmin.GET("", blueprintHandler.ListBlueprints)
				blueprintsAdmin.POST("", blueprintHandler.CreateBlueprint)
				blueprintsAdmin.GET("/:id", blueprintHandler.GetBlueprint)
				blueprintsAdmin.PUT("/:id", blueprintHandler.UpdateBlueprint)
				blueprintsAdmin.DELETE("/:id", blueprintHandler.DeleteBlueprint)
				blueprintsAdmin.POST("/:id/apply", blueprintHandler.ApplyBlueprint)
			}

			// Client management
			clientsRead := protected.Group("/clients")
			clientsRead.Use(middleware.RequirePermission("clients.read"))
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
			clientsUpdate.Use(middleware.RequirePermission("clients.update"))
			{
				clientsUpdate.PUT("/:id", clientHandler.UpdateClient)
			}

			// Currencies (any authenticated employee)
			protected.GET("/currencies", accountHandler.ListCurrencies)

			// Account management
			accountsRead := protected.Group("/accounts")
			accountsRead.Use(middleware.RequirePermission("accounts.read"))
			{
				accountsRead.GET("", accountHandler.ListAllAccounts)
				accountsRead.GET("/:id", accountHandler.GetAccount)
				accountsRead.GET("/by-number/:account_number", accountHandler.GetAccountByNumber)
			}
			accountsCreate := protected.Group("/accounts")
			accountsCreate.Use(middleware.RequirePermission("accounts.create"))
			{
				accountsCreate.POST("", accountHandler.CreateAccount)
			}
			accountsUpdate := protected.Group("/accounts")
			accountsUpdate.Use(middleware.RequirePermission("accounts.update"))
			{
				accountsUpdate.PUT("/:id/name", accountHandler.UpdateAccountName)
				accountsUpdate.PUT("/:id/limits", accountHandler.UpdateAccountLimits)
				accountsUpdate.PUT("/:id/status", accountHandler.UpdateAccountStatus)
			}

			// Companies
			companiesEmployee := protected.Group("/companies")
			companiesEmployee.Use(middleware.RequirePermission("accounts.create"))
			{
				companiesEmployee.POST("", accountHandler.CreateCompany)
			}

			// Bank account management (admin only)
			bankAccountsAdmin := protected.Group("/bank-accounts")
			bankAccountsAdmin.Use(middleware.RequirePermission("bank-accounts.manage"))
			{
				bankAccountsAdmin.GET("", accountHandler.ListBankAccounts)
				bankAccountsAdmin.POST("", accountHandler.CreateBankAccount)
				bankAccountsAdmin.DELETE("/:id", accountHandler.DeleteBankAccount)
			}

			// Cards management
			cardsRead := protected.Group("/cards")
			cardsRead.Use(middleware.RequirePermission("cards.read"))
			{
				cardsRead.GET("", cardHandler.ListCards)
				cardsRead.GET("/:id", cardHandler.GetCard)
			}
			cardsCreate := protected.Group("/cards")
			cardsCreate.Use(middleware.RequirePermission("cards.create"))
			{
				cardsCreate.POST("", cardHandler.CreateCard)
				cardsCreate.POST("/authorized-person", cardHandler.CreateAuthorizedPerson)
			}
			cardsUpdate := protected.Group("/cards")
			cardsUpdate.Use(middleware.RequirePermission("cards.update"))
			{
				cardsUpdate.POST("/:id/block", cardHandler.BlockCard)
				cardsUpdate.POST("/:id/unblock", cardHandler.UnblockCard)
				cardsUpdate.POST("/:id/deactivate", cardHandler.DeactivateCard)
			}

			// Card requests (cards.approve)
			cardsApprove := protected.Group("/cards/requests")
			cardsApprove.Use(middleware.RequirePermission("cards.approve"))
			{
				cardsApprove.GET("", cardHandler.ListCardRequests)
				cardsApprove.GET("/:id", cardHandler.GetCardRequest)
				cardsApprove.POST("/:id/approve", cardHandler.ApproveCardRequest)
				cardsApprove.POST("/:id/reject", cardHandler.RejectCardRequest)
			}

			// Payments (employee read)
			paymentsRead := protected.Group("/payments")
			paymentsRead.Use(middleware.RequirePermission("payments.read"))
			{
				paymentsRead.GET("", txHandler.ListPayments)
				paymentsRead.GET("/:id", txHandler.GetPayment)
			}

			// Transfers (employee read)
			transfersRead := protected.Group("/transfers")
			transfersRead.Use(middleware.RequirePermission("payments.read"))
			{
				transfersRead.GET("", txHandler.ListTransfers)
				transfersRead.GET("/:id", txHandler.GetTransfer)
			}

			// Transfer fee management (admin only)
			feesAdmin := protected.Group("/fees")
			feesAdmin.Use(middleware.RequirePermission("fees.manage"))
			{
				feesAdmin.GET("", txHandler.ListFees)
				feesAdmin.POST("", txHandler.CreateFee)
				feesAdmin.PUT("/:id", txHandler.UpdateFee)
				feesAdmin.DELETE("/:id", txHandler.DeleteFee)
			}

			// Loans (employee read)
			loansRead := protected.Group("/loans")
			loansRead.Use(middleware.RequirePermission("credits.read"))
			{
				loansRead.GET("", creditHandler.ListAllLoans)
				loansRead.GET("/:id", creditHandler.GetLoan)
				loansRead.GET("/:id/installments", creditHandler.GetInstallmentsByLoan)
			}

			// Loan requests
			loanRequestsRead := protected.Group("/loan-requests")
			loanRequestsRead.Use(middleware.RequirePermission("credits.read"))
			{
				loanRequestsRead.GET("", creditHandler.ListLoanRequests)
				loanRequestsRead.GET("/:id", creditHandler.GetLoanRequest)
			}
			loanRequestsApprove := protected.Group("/loan-requests")
			loanRequestsApprove.Use(middleware.RequirePermission("credits.approve"))
			{
				loanRequestsApprove.POST("/:id/approve", creditHandler.ApproveLoanRequest)
				loanRequestsApprove.POST("/:id/reject", creditHandler.RejectLoanRequest)
			}

			// Interest rate tier management (admin only)
			rateTiersAdmin := protected.Group("/interest-rate-tiers")
			rateTiersAdmin.Use(middleware.RequirePermission("interest-rates.manage"))
			{
				rateTiersAdmin.GET("", creditHandler.ListInterestRateTiers)
				rateTiersAdmin.POST("", creditHandler.CreateInterestRateTier)
				rateTiersAdmin.PUT("/:id", creditHandler.UpdateInterestRateTier)
				rateTiersAdmin.DELETE("/:id", creditHandler.DeleteInterestRateTier)
				rateTiersAdmin.POST("/:id/apply", creditHandler.ApplyVariableRateUpdate)
			}

			// Bank margin management (admin only)
			bankMarginsAdmin := protected.Group("/bank-margins")
			bankMarginsAdmin.Use(middleware.RequirePermission("interest-rates.manage"))
			{
				bankMarginsAdmin.GET("", creditHandler.ListBankMargins)
				bankMarginsAdmin.PUT("/:id", creditHandler.UpdateBankMargin)
			}

			// Stock exchange management (supervisor)
			stockExchangeAdmin := protected.Group("/stock-exchanges")
			stockExchangeAdmin.Use(middleware.RequirePermission("exchanges.manage"))
			{
				stockExchangeAdmin.POST("/testing-mode", stockExchangeHandler.SetTestingMode)
				stockExchangeAdmin.GET("/testing-mode", stockExchangeHandler.GetTestingMode)
			}

			// Admin stock-source management (securities.manage)
			adminStockSource := protected.Group("/admin/stock-source")
			adminStockSource.Use(middleware.RequirePermission("securities.manage"))
			{
				adminStockSource.POST("", stockSourceHandler.SwitchSource)
				adminStockSource.GET("", stockSourceHandler.GetSourceStatus)
			}

			// Order management (supervisor)
			ordersAdmin := protected.Group("/orders")
			ordersAdmin.Use(middleware.RequirePermission("orders.approve"))
			{
				ordersAdmin.GET("", stockOrderHandler.ListOrders)
				ordersAdmin.POST("/:id/approve", stockOrderHandler.ApproveOrder)
				ordersAdmin.POST("/:id/decline", stockOrderHandler.DeclineOrder)
			}

			// Actuary management (supervisor)
			actuariesAdmin := protected.Group("/actuaries")
			actuariesAdmin.Use(middleware.RequirePermission("agents.manage"))
			{
				actuariesAdmin.GET("", actuaryHandler.ListActuaries)
				actuariesAdmin.PUT("/:id/limit", actuaryHandler.SetActuaryLimit)
				actuariesAdmin.POST("/:id/reset-limit", actuaryHandler.ResetActuaryLimit)
				actuariesAdmin.PUT("/:id/approval", actuaryHandler.SetNeedApproval)
			}

			// Tax management (supervisor)
			taxAdmin := protected.Group("/tax")
			taxAdmin.Use(middleware.RequirePermission("tax.manage"))
			{
				taxAdmin.GET("", taxHandler.ListTaxRecords)
				taxAdmin.POST("/collect", taxHandler.CollectTax)
			}

			// ── v1-only: Changelog endpoints (Plan 2) ───────────────
			// These require the same permission as the parent resource's read.
			changelogAccounts := protected.Group("/accounts")
			changelogAccounts.Use(middleware.RequirePermission("accounts.read"))
			{
				changelogAccounts.GET("/:id/changelog", v1GetAccountChangelog)
			}
			changelogEmployees := protected.Group("/employees")
			changelogEmployees.Use(middleware.RequirePermission("employees.read"))
			{
				changelogEmployees.GET("/:id/changelog", v1GetEmployeeChangelog)
			}
			changelogClients := protected.Group("/clients")
			changelogClients.Use(middleware.RequirePermission("clients.read"))
			{
				changelogClients.GET("/:id/changelog", v1GetClientChangelog)
			}
			changelogCards := protected.Group("/cards")
			changelogCards.Use(middleware.RequirePermission("cards.read"))
			{
				changelogCards.GET("/:id/changelog", v1GetCardChangelog)
			}
			changelogLoans := protected.Group("/loans")
			changelogLoans.Use(middleware.RequirePermission("credits.read"))
			{
				changelogLoans.GET("/:id/changelog", v1GetLoanChangelog)
			}
		}
	}
}
