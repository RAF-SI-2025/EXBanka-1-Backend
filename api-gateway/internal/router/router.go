package router

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

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

func Setup(
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
	verificationClient verificationpb.VerificationGRPCServiceClient,
	notificationClient notificationpb.NotificationServiceClient,
	wsHandler *handler.WebSocketHandler,
) *gin.Engine {
	r := gin.Default()
	r.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
	}))

	authHandler := handler.NewAuthHandler(authClient)
	empHandler := handler.NewEmployeeHandler(userClient, authClient)
	roleHandler := handler.NewRoleHandler(userClient)
	limitHandler := handler.NewLimitHandler(empLimitClient, clientLimitClient)
	clientHandler := handler.NewClientHandler(clientClient, authClient)
	accountHandler := handler.NewAccountHandler(accountClient, bankAccountClient, cardClient, txClient)
	cardHandler := handler.NewCardHandler(cardClient, virtualCardClient, cardRequestClient)
	txHandler := handler.NewTransactionHandler(txClient, feeClient, accountClient)
	exchangeHandler := handler.NewExchangeHandler(exchangeClient)
	creditHandler := handler.NewCreditHandler(creditClient)
	meHandler := handler.NewMeHandler(clientClient, userClient, authClient)
	stockExchangeHandler := handler.NewStockExchangeHandler(stockExchangeClient)
	securitiesHandler := handler.NewSecuritiesHandler(securityClient)
	stockOrderHandler := handler.NewStockOrderHandler(orderClient)
	portfolioHandler := handler.NewPortfolioHandler(portfolioClient, otcClient)
	actuaryHandler := handler.NewActuaryHandler(actuaryClient)
	taxHandler := handler.NewTaxHandler(taxClient)

	api := r.Group("/api")
	{
		// Public routes
		auth := api.Group("/auth")
		{
			auth.POST("/login", authHandler.Login)
			auth.POST("/refresh", authHandler.RefreshToken)
			auth.POST("/logout", authHandler.Logout)
			auth.POST("/password/reset-request", authHandler.RequestPasswordReset)
			auth.POST("/password/reset", authHandler.ResetPassword)
			auth.POST("/activate", authHandler.ActivateAccount)
		}

		// Public exchange rate routes
		api.GET("/exchange/rates", exchangeHandler.ListExchangeRates)
		api.GET("/exchange/rates/:from/:to", exchangeHandler.GetExchangeRate)
		api.POST("/exchange/calculate", exchangeHandler.CalculateExchange)

		// /api/me/* — authenticated user's own resources (client or employee).
		// Ownership derived from JWT user_id, never from URL params.
		me := api.Group("/me")
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
		}

		// Stock exchanges — accessible to any authenticated user
		stockExchanges := api.Group("/stock-exchanges")
		stockExchanges.Use(middleware.AnyAuthMiddleware(authClient))
		{
			stockExchanges.GET("", stockExchangeHandler.ListExchanges)
			stockExchanges.GET("/:id", stockExchangeHandler.GetExchange)
		}

		// Securities — accessible to any authenticated user
		securities := api.Group("/securities")
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
		}

		// OTC — accessible to any authenticated user
		otc := api.Group("/otc/offers")
		otc.Use(middleware.AnyAuthMiddleware(authClient))
		{
			otc.GET("", portfolioHandler.ListOTCOffers)
			otc.POST("/:id/buy", portfolioHandler.BuyOTCOffer)
		}

		// --- Mobile Auth (public, no auth required) ---
		mobileAuth := api.Group("/mobile/auth")
		{
			mobileAuthHandler := handler.NewMobileAuthHandler(authClient)
			mobileAuth.POST("/request-activation", mobileAuthHandler.RequestActivation)
			mobileAuth.POST("/activate", mobileAuthHandler.ActivateDevice)
			mobileAuth.POST("/refresh", mobileAuthHandler.RefreshMobileToken)
		}

		// --- Mobile Device Management (MobileAuthMiddleware) ---
		mobileDevice := api.Group("/mobile/device")
		mobileDevice.Use(middleware.MobileAuthMiddleware(authClient))
		{
			mobileAuthHandler := handler.NewMobileAuthHandler(authClient)
			mobileDevice.GET("", mobileAuthHandler.GetDeviceInfo)
			mobileDevice.POST("/deactivate", mobileAuthHandler.DeactivateDevice)
			mobileDevice.POST("/transfer", mobileAuthHandler.TransferDevice)
		}

		// --- Mobile Verifications (MobileAuthMiddleware + RequireDeviceSignature) ---
		mobileVerify := api.Group("/mobile/verifications")
		mobileVerify.Use(middleware.MobileAuthMiddleware(authClient))
		mobileVerify.Use(middleware.RequireDeviceSignature(authClient))
		{
			verifyHandler := handler.NewVerificationHandler(verificationClient, notificationClient)
			mobileVerify.GET("/pending", verifyHandler.GetPendingVerifications)
			mobileVerify.POST("/:challenge_id/submit", verifyHandler.SubmitMobileVerification)
		}

		// --- QR Verification (MobileAuthMiddleware + RequireDeviceSignature) ---
		qrVerify := api.Group("/verify")
		qrVerify.Use(middleware.MobileAuthMiddleware(authClient))
		qrVerify.Use(middleware.RequireDeviceSignature(authClient))
		{
			verifyHandler := handler.NewVerificationHandler(verificationClient, notificationClient)
			qrVerify.POST("/:challenge_id", verifyHandler.VerifyQR)
		}

		// --- WebSocket for mobile push ---
		api.GET("/ws/mobile", wsHandler.HandleConnect)

		// --- Browser-facing Verifications (AnyAuthMiddleware) ---
		verifications := api.Group("/verifications")
		verifications.Use(middleware.AnyAuthMiddleware(authClient))
		{
			verifyHandler := handler.NewVerificationHandler(verificationClient, notificationClient)
			verifications.POST("", verifyHandler.CreateVerification)
			verifications.GET("/:id/status", verifyHandler.GetVerificationStatus)
			verifications.POST("/:id/code", verifyHandler.SubmitVerificationCode)
		}

		// Employee/admin routes (AuthMiddleware + RequirePermission)
		protected := api.Group("/")
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
				// GET /api/accounts — supports ?client_id=X for filtering by client
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
				// GET /api/cards — requires ?client_id=X or ?account_number=X
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
				// GET /api/payments — requires ?client_id=X or ?account_number=X
				paymentsRead.GET("", txHandler.ListPayments)
				paymentsRead.GET("/:id", txHandler.GetPayment)
			}

			// Transfers (employee read)
			transfersRead := protected.Group("/transfers")
			transfersRead.Use(middleware.RequirePermission("payments.read"))
			{
				// GET /api/transfers — requires ?client_id=X
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
				// GET /api/loans — supports ?client_id=X
				loansRead.GET("", creditHandler.ListAllLoans)
				loansRead.GET("/:id", creditHandler.GetLoan)
				loansRead.GET("/:id/installments", creditHandler.GetInstallmentsByLoan)
			}

			// Loan requests (promoted from /loans/requests to /loan-requests)
			loanRequestsRead := protected.Group("/loan-requests")
			loanRequestsRead.Use(middleware.RequirePermission("credits.read"))
			{
				// GET /api/loan-requests — supports ?client_id=X
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
		}
	}

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	return r
}
