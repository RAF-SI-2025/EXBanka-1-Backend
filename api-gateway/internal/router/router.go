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
	transactionpb "github.com/exbanka/contract/transactionpb"
	userpb "github.com/exbanka/contract/userpb"
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
	bootstrapSecret string,
) *gin.Engine {
	r := gin.Default()
	r.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
	}))

	bootstrapHandler := handler.NewBootstrapHandler(userClient, authClient, bootstrapSecret)
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

	api := r.Group("/api")
	{
		api.POST("/bootstrap", bootstrapHandler.Bootstrap)

		// Public auth routes
		auth := api.Group("/auth")
		{
			auth.POST("/login", authHandler.Login)
			auth.POST("/refresh", authHandler.RefreshToken)
			auth.POST("/logout", authHandler.Logout)
			auth.POST("/password/reset-request", authHandler.RequestPasswordReset)
			auth.POST("/password/reset", authHandler.ResetPassword)
			auth.POST("/activate", authHandler.ActivateAccount)
		}

		// Public exchange rate routes (backward-compat + new paths)
		api.GET("/exchange-rates", exchangeHandler.ListExchangeRates)
		api.GET("/exchange-rates/:from/:to", exchangeHandler.GetExchangeRate)
		api.GET("/exchange/rates", exchangeHandler.ListExchangeRates)
		api.GET("/exchange/rates/:from/:to", exchangeHandler.GetExchangeRate)
		api.POST("/exchange/calculate", exchangeHandler.CalculateExchange)

		// Employee-protected routes
		protected := api.Group("/")
		protected.Use(middleware.AuthMiddleware(authClient))
		{
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

			// Bank account management (admin only)
			bankAccountsAdmin := protected.Group("/bank-accounts")
			bankAccountsAdmin.Use(middleware.RequirePermission("bank-accounts.manage"))
			{
				bankAccountsAdmin.GET("", accountHandler.ListBankAccounts)
				bankAccountsAdmin.POST("", accountHandler.CreateBankAccount)
				bankAccountsAdmin.DELETE("/:id", accountHandler.DeleteBankAccount)
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

			updateEmployees := protected.Group("/employees")
			updateEmployees.Use(middleware.RequirePermission("employees.update"))
			{
				updateEmployees.PUT("/:id", empHandler.UpdateEmployee)
			}

			// Role and permission management (employees.permissions)
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

			// Employee limit management (limits.manage)
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

			// Client management (EmployeeBasic)
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

			// Account management (EmployeeBasic)
			accountsRead := protected.Group("/accounts")
			accountsRead.Use(middleware.RequirePermission("accounts.read"))
			{
				accountsRead.GET("", accountHandler.ListAllAccounts)
				accountsRead.GET("/:id", accountHandler.GetAccount)
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

			// Currencies (any authenticated)
			protected.GET("/currencies", accountHandler.ListCurrencies)

			// Companies (EmployeeBasic)
			companiesEmployee := protected.Group("/companies")
			companiesEmployee.Use(middleware.RequirePermission("accounts.create"))
			{
				companiesEmployee.POST("", accountHandler.CreateCompany)
			}

			// Cards management (EmployeeBasic)
			cardsCreate := protected.Group("/cards")
			cardsCreate.Use(middleware.RequirePermission("cards.create"))
			{
				cardsCreate.POST("", cardHandler.CreateCard)
				cardsCreate.POST("/authorized-person", cardHandler.CreateAuthorizedPerson)
			}
			cardsUpdate := protected.Group("/cards")
			cardsUpdate.Use(middleware.RequirePermission("cards.update"))
			{
				cardsUpdate.PUT("/:id/block", cardHandler.BlockCard)
				cardsUpdate.PUT("/:id/unblock", cardHandler.UnblockCard)
				cardsUpdate.PUT("/:id/deactivate", cardHandler.DeactivateCard)
			}

			// Loans (EmployeeBasic) - employee-only operations
			loansRead := protected.Group("/loans")
			loansRead.Use(middleware.RequirePermission("credits.read"))
			{
				loansRead.GET("/requests/:id", creditHandler.GetLoanRequest)
				loansRead.GET("/requests", creditHandler.ListLoanRequests)
				loansRead.GET("", creditHandler.ListAllLoans)
			}
			loansApprove := protected.Group("/loans")
			loansApprove.Use(middleware.RequirePermission("credits.approve"))
			{
				loansApprove.PUT("/requests/:id/approve", creditHandler.ApproveLoanRequest)
				loansApprove.PUT("/requests/:id/reject", creditHandler.RejectLoanRequest)
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

			// Card request management (cards.approve)
			cardsApprove := protected.Group("/cards/requests")
			cardsApprove.Use(middleware.RequirePermission("cards.approve"))
			{
				cardsApprove.GET("", cardHandler.ListCardRequests)
				cardsApprove.GET("/:id", cardHandler.GetCardRequest)
				cardsApprove.PUT("/:id/approve", cardHandler.ApproveCardRequest)
				cardsApprove.PUT("/:id/reject", cardHandler.RejectCardRequest)
			}
		}

		// Client-only routes (write operations and self-service)
		clientProtected := api.Group("/")
		clientProtected.Use(middleware.ClientAuthMiddleware(authClient))
		{
			// Client self-service
			clientProtected.GET("/clients/me", clientHandler.GetCurrentClient)

			// Payments (client write)
			clientProtected.POST("/payments", txHandler.CreatePayment)
			clientProtected.POST("/payments/:id/execute", txHandler.ExecutePayment)

			// Transfers (client write)
			clientProtected.POST("/transfers", txHandler.CreateTransfer)
			clientProtected.POST("/transfers/:id/execute", txHandler.ExecuteTransfer)

			// Payment recipients (client)
			clientProtected.POST("/payment-recipients", txHandler.CreatePaymentRecipient)
			clientProtected.GET("/payment-recipients/:client_id", txHandler.ListPaymentRecipients)
			clientProtected.PUT("/payment-recipients/:id", txHandler.UpdatePaymentRecipient)
			clientProtected.DELETE("/payment-recipients/:id", txHandler.DeletePaymentRecipient)

			// Verification codes (client)
			clientProtected.POST("/verification", txHandler.CreateVerificationCode)
			clientProtected.POST("/verification/validate", txHandler.ValidateVerificationCode)

			// Loans (client write)
			clientProtected.POST("/loans/requests", creditHandler.CreateLoanRequest)

			// Cards (client)
			//clientProtected.PUT("/cards/:id/block", cardHandler.ClientBlockCard)

			// Virtual cards (client)
			clientProtected.POST("/cards/virtual", cardHandler.CreateVirtualCard)
			clientProtected.POST("/cards/:id/pin", cardHandler.SetCardPin)
			clientProtected.POST("/cards/:id/verify-pin", cardHandler.VerifyCardPin)
			clientProtected.POST("/cards/:id/temporary-block", cardHandler.TemporaryBlockCard)

			// Card requests (client)
			clientProtected.POST("/cards/requests", cardHandler.CreateCardRequest)
			clientProtected.GET("/cards/requests/me", cardHandler.ListMyCardRequests)
		}

		// Routes accessible by both employee and client tokens
		// Clients can only access their own resources (enforced in handlers via enforceClientSelf)
		anyAuth := api.Group("/")
		anyAuth.Use(middleware.AnyAuthMiddleware(authClient))
		{
			// Accounts (read)
			anyAuth.GET("/accounts/by-number/:account_number", accountHandler.GetAccountByNumber)
			anyAuth.GET("/accounts/client/:client_id", accountHandler.ListAccountsByClient)

			// Cards (read)
			anyAuth.GET("/cards/:id", cardHandler.GetCard)
			anyAuth.GET("/cards/account/:account_number", cardHandler.ListCardsByAccount)
			anyAuth.GET("/cards/client/:client_id", cardHandler.ListCardsByClient)

			// Payments (read)
			anyAuth.GET("/payments/:id", txHandler.GetPayment)
			anyAuth.GET("/payments/account/:account_number", txHandler.ListPaymentsByAccount)
			anyAuth.GET("/payments/client/:client_id", txHandler.ListPaymentsByClient)

			// Transfers (read)
			anyAuth.GET("/transfers/:id", txHandler.GetTransfer)
			anyAuth.GET("/transfers/client/:client_id", txHandler.ListTransfersByClient)

			// Loans (read)
			anyAuth.GET("/loans/:id", creditHandler.GetLoan)
			anyAuth.GET("/loans/client/:client_id", creditHandler.ListLoansByClient)
			anyAuth.GET("/loans/:id/installments", creditHandler.GetInstallmentsByLoan)
			anyAuth.GET("/loans/requests/client/:client_id", creditHandler.ListLoanRequestsByClient)
		}
	}

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	return r
}
