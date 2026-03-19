package router

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	accountpb "github.com/exbanka/contract/accountpb"
	authpb "github.com/exbanka/contract/authpb"
	cardpb "github.com/exbanka/contract/cardpb"
	clientpb "github.com/exbanka/contract/clientpb"
	creditpb "github.com/exbanka/contract/creditpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	userpb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/api-gateway/internal/handler"
	"github.com/exbanka/api-gateway/internal/middleware"
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
	empHandler := handler.NewEmployeeHandler(userClient)
	roleHandler := handler.NewRoleHandler(userClient)
	limitHandler := handler.NewLimitHandler(empLimitClient, clientLimitClient)
	clientHandler := handler.NewClientHandler(clientClient)
	accountHandler := handler.NewAccountHandler(accountClient)
	cardHandler := handler.NewCardHandler(cardClient)
	txHandler := handler.NewTransactionHandler(txClient)
	exchangeHandler := handler.NewExchangeHandler(txClient)
	creditHandler := handler.NewCreditHandler(creditClient)

	api := r.Group("/api")
	{
		// Public auth routes
		auth := api.Group("/auth")
		{
			auth.POST("/login", authHandler.Login)
			auth.POST("/client-login", authHandler.ClientLogin)
			auth.POST("/refresh", authHandler.RefreshToken)
			auth.POST("/logout", authHandler.Logout)
			auth.POST("/password/reset-request", authHandler.RequestPasswordReset)
			auth.POST("/password/reset", authHandler.ResetPassword)
			auth.POST("/activate", authHandler.ActivateAccount)
		}

		// Public exchange rate routes
		api.GET("/exchange-rates", exchangeHandler.ListExchangeRates)
		api.GET("/exchange-rates/:from/:to", exchangeHandler.GetExchangeRate)

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
			clientsEmployee := protected.Group("/clients")
			clientsEmployee.Use(middleware.RequirePermission("clients.read"))
			{
				clientsEmployee.POST("", clientHandler.CreateClient)
				clientsEmployee.GET("", clientHandler.ListClients)
				clientsEmployee.GET("/:id", clientHandler.GetClient)
				clientsEmployee.PUT("/:id", clientHandler.UpdateClient)
				clientsEmployee.POST("/set-password", clientHandler.SetPassword)
			}

			// Account management (EmployeeBasic)
			accountsEmployee := protected.Group("/accounts")
			accountsEmployee.Use(middleware.RequirePermission("accounts.read"))
			{
				accountsEmployee.POST("", accountHandler.CreateAccount)
				accountsEmployee.GET("", accountHandler.ListAllAccounts)
				accountsEmployee.GET("/:id", accountHandler.GetAccount)
				accountsEmployee.GET("/client/:client_id", accountHandler.ListAccountsByClient)
				accountsEmployee.PUT("/:id/name", accountHandler.UpdateAccountName)
				accountsEmployee.PUT("/:id/limits", accountHandler.UpdateAccountLimits)
				accountsEmployee.PUT("/:id/status", accountHandler.UpdateAccountStatus)
			}

			// Currencies (any authenticated)
			protected.GET("/currencies", accountHandler.ListCurrencies)

			// Companies (EmployeeBasic)
			companiesEmployee := protected.Group("/companies")
			companiesEmployee.Use(middleware.RequirePermission("accounts.read"))
			{
				companiesEmployee.POST("", accountHandler.CreateCompany)
			}

			// Cards management (EmployeeBasic)
			cardsEmployee := protected.Group("/cards")
			cardsEmployee.Use(middleware.RequirePermission("cards.read"))
			{
				cardsEmployee.POST("", cardHandler.CreateCard)
				cardsEmployee.PUT("/:id/block", cardHandler.BlockCard)
				cardsEmployee.PUT("/:id/unblock", cardHandler.UnblockCard)
				cardsEmployee.PUT("/:id/deactivate", cardHandler.DeactivateCard)
				cardsEmployee.POST("/authorized-person", cardHandler.CreateAuthorizedPerson)
			}

			// Loans (EmployeeBasic) - employee-only operations
			loansEmployee := protected.Group("/loans")
			loansEmployee.Use(middleware.RequirePermission("loans.read"))
			{
				loansEmployee.GET("/requests/:id", creditHandler.GetLoanRequest)
				loansEmployee.GET("/requests", creditHandler.ListLoanRequests)
				loansEmployee.PUT("/requests/:id/approve", creditHandler.ApproveLoanRequest)
				loansEmployee.PUT("/requests/:id/reject", creditHandler.RejectLoanRequest)
				loansEmployee.GET("", creditHandler.ListAllLoans)
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

			// Transfers (client write)
			clientProtected.POST("/transfers", txHandler.CreateTransfer)

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
		}

		// Routes accessible by both employee and client tokens
		anyAuth := api.Group("/")
		anyAuth.Use(middleware.AnyAuthMiddleware(authClient))
		{
			// Account by number
			anyAuth.GET("/accounts/by-number/:account_number", accountHandler.GetAccountByNumber)

			// Cards (read)
			anyAuth.GET("/cards/:id", cardHandler.GetCard)
			anyAuth.GET("/cards/account/:account_number", cardHandler.ListCardsByAccount)
			anyAuth.GET("/cards/client/:client_id", cardHandler.ListCardsByClient)

			// Payments (read)
			anyAuth.GET("/payments/:id", txHandler.GetPayment)
			anyAuth.GET("/payments/account/:account_number", txHandler.ListPaymentsByAccount)

			// Transfers (read)
			anyAuth.GET("/transfers/:id", txHandler.GetTransfer)
			anyAuth.GET("/transfers/client/:client_id", txHandler.ListTransfersByClient)

			// Loans (read)
			anyAuth.GET("/loans/:id", creditHandler.GetLoan)
			anyAuth.GET("/loans/client/:client_id", creditHandler.ListLoansByClient)
			anyAuth.GET("/loans/:id/installments", creditHandler.GetInstallmentsByLoan)
		}
	}

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	return r
}
