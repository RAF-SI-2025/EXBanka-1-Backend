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
				accountsEmployee.GET("/by-number/:account_number", accountHandler.GetAccountByNumber)
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
				cardsEmployee.GET("/:id", cardHandler.GetCard)
				cardsEmployee.GET("/account/:account_number", cardHandler.ListCardsByAccount)
				cardsEmployee.GET("/client/:client_id", cardHandler.ListCardsByClient)
				cardsEmployee.PUT("/:id/block", cardHandler.BlockCard)
				cardsEmployee.PUT("/:id/unblock", cardHandler.UnblockCard)
				cardsEmployee.PUT("/:id/deactivate", cardHandler.DeactivateCard)
				cardsEmployee.POST("/authorized-person", cardHandler.CreateAuthorizedPerson)
			}

			// Payments - employee view
			paymentsEmployee := protected.Group("/payments")
			paymentsEmployee.Use(middleware.RequirePermission("accounts.read"))
			{
				paymentsEmployee.GET("/:id", txHandler.GetPayment)
				paymentsEmployee.GET("/account/:account_number", txHandler.ListPaymentsByAccount)
			}

			// Transfers - employee view
			transfersEmployee := protected.Group("/transfers")
			transfersEmployee.Use(middleware.RequirePermission("accounts.read"))
			{
				transfersEmployee.GET("/:id", txHandler.GetTransfer)
				transfersEmployee.GET("/client/:client_id", txHandler.ListTransfersByClient)
			}

			// Loans (EmployeeBasic)
			loansEmployee := protected.Group("/loans")
			loansEmployee.Use(middleware.RequirePermission("loans.read"))
			{
				loansEmployee.GET("/requests/:id", creditHandler.GetLoanRequest)
				loansEmployee.GET("/requests", creditHandler.ListLoanRequests)
				loansEmployee.PUT("/requests/:id/approve", creditHandler.ApproveLoanRequest)
				loansEmployee.PUT("/requests/:id/reject", creditHandler.RejectLoanRequest)
				loansEmployee.GET("/:id", creditHandler.GetLoan)
				loansEmployee.GET("/client/:client_id", creditHandler.ListLoansByClient)
				loansEmployee.GET("", creditHandler.ListAllLoans)
				loansEmployee.GET("/:id/installments", creditHandler.GetInstallmentsByLoan)
			}
		}

		// Client-protected routes
		clientProtected := api.Group("/")
		clientProtected.Use(middleware.ClientAuthMiddleware(authClient))
		{
			// Client self-service
			clientProtected.GET("/clients/me", clientHandler.GetCurrentClient)

			// Payments (client)
			clientProtected.POST("/payments", txHandler.CreatePayment)
			clientProtected.GET("/payments/:id", txHandler.GetPayment)
			clientProtected.GET("/payments/account/:account_number", txHandler.ListPaymentsByAccount)

			// Transfers (client)
			clientProtected.POST("/transfers", txHandler.CreateTransfer)
			clientProtected.GET("/transfers/:id", txHandler.GetTransfer)
			clientProtected.GET("/transfers/client/:client_id", txHandler.ListTransfersByClient)

			// Payment recipients (client)
			clientProtected.POST("/payment-recipients", txHandler.CreatePaymentRecipient)
			clientProtected.GET("/payment-recipients/:client_id", txHandler.ListPaymentRecipients)
			clientProtected.PUT("/payment-recipients/:id", txHandler.UpdatePaymentRecipient)
			clientProtected.DELETE("/payment-recipients/:id", txHandler.DeletePaymentRecipient)

			// Verification codes (client)
			clientProtected.POST("/verification", txHandler.CreateVerificationCode)
			clientProtected.POST("/verification/validate", txHandler.ValidateVerificationCode)

			// Account by number (client)
			clientProtected.GET("/accounts/by-number/:account_number", accountHandler.GetAccountByNumber)

			// Cards (client)
			clientProtected.GET("/cards/:id", cardHandler.GetCard)
			clientProtected.GET("/cards/account/:account_number", cardHandler.ListCardsByAccount)
			clientProtected.GET("/cards/client/:client_id", cardHandler.ListCardsByClient)

			// Loans (client)
			clientProtected.POST("/loans/requests", creditHandler.CreateLoanRequest)
			clientProtected.GET("/loans/:id", creditHandler.GetLoan)
			clientProtected.GET("/loans/client/:client_id", creditHandler.ListLoansByClient)
			clientProtected.GET("/loans/:id/installments", creditHandler.GetInstallmentsByLoan)
		}
	}

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	return r
}
